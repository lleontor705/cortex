// Package sqlite: build-agnostic payload codec pins (REQ-VEC-FILTER-001).
//
// The int8 oracle below is hand-built byte by byte so a change to the header
// layout, the dequantization, or the legacy fallback fails against an
// independent expectation instead of a round-trip through the same code.
package sqlite

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/testutil"
)

func float32Bytes(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, x := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(x))
	}
	return buf
}

// buildInt8Payload assembles an int8 prefix record without touching the
// adapter encoder, keeping the test an independent byte-level oracle.
func buildInt8Payload(dims uint16, scale float32, codes []int8) []byte {
	data := make([]byte, QuantHeaderBytes+len(codes))
	copy(data[:4], QuantMagic[:])
	binary.LittleEndian.PutUint16(data[4:6], dims)
	binary.LittleEndian.PutUint32(data[6:10], math.Float32bits(scale))
	for i, c := range codes {
		data[QuantHeaderBytes+i] = byte(c)
	}
	return data
}

func TestDecodeEmbeddingPayload_HandBuiltInt8Oracle(t *testing.T) {
	payload := buildInt8Payload(8, 2, []int8{10, -5, 0, 127})

	dims, prefix, scale, ok := DecodeQuantHeader(payload)
	if !ok {
		t.Fatal("DecodeQuantHeader rejected a valid int8 payload")
	}
	if dims != 8 || prefix != 4 || scale != 2 {
		t.Errorf("header = (dims %d, prefix %d, scale %v), want (8, 4, 2)", dims, prefix, scale)
	}

	got, err := DecodeEmbeddingPayload(payload)
	if err != nil {
		t.Fatalf("DecodeEmbeddingPayload: %v", err)
	}
	want := []float32{20, -10, 0, 254, 0, 0, 0, 0}
	if len(got) != len(want) {
		t.Fatalf("decoded length = %d, want declared dims %d (zero-filled Matryoshka tail)", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("element %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestDecodeQuantHeader_RejectsMalformedPayloads(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"legacy float32 blob", float32Bytes([]float32{1, 2, 3})},
		{"truncated header", buildInt8Payload(8, 2, []int8{1})[:QuantHeaderBytes]},
		{"zero declared dims", buildInt8Payload(0, 2, []int8{1})},
		{"prefix exceeds dims", buildInt8Payload(2, 2, []int8{1, 2, 3, 4})},
		{"zero scale", buildInt8Payload(8, 0, []int8{1})},
		{"NaN scale", buildInt8Payload(8, float32(math.NaN()), []int8{1})},
		{"infinite scale", buildInt8Payload(8, float32(math.Inf(1)), []int8{1})},
		{"empty payload", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, ok := DecodeQuantHeader(tc.payload); ok {
				t.Errorf("DecodeQuantHeader accepted %s", tc.name)
			}
		})
	}
}

func TestDecodeEmbeddingPayload_LegacyFloat32Fallback(t *testing.T) {
	vec := []float32{1.5, -2.25, 0, 3.75}
	got, err := DecodeEmbeddingPayload(float32Bytes(vec))
	if err != nil {
		t.Fatalf("DecodeEmbeddingPayload legacy: %v", err)
	}
	if len(got) != len(vec) {
		t.Fatalf("decoded length = %d, want %d", len(got), len(vec))
	}
	for i := range vec {
		if got[i] != vec[i] {
			t.Errorf("element %d = %v, want %v", i, got[i], vec[i])
		}
	}

	// Trailing bytes beyond whole floats are ignored, as before the move.
	withTrailing, err := DecodeEmbeddingPayload(append(float32Bytes(vec), 0xAB))
	if err != nil {
		t.Fatalf("DecodeEmbeddingPayload trailing: %v", err)
	}
	if len(withTrailing) != len(vec) {
		t.Errorf("trailing-byte payload decoded to %d elements, want %d", len(withTrailing), len(vec))
	}

	for _, junk := range [][]byte{nil, {1, 2, 3}} {
		if _, err := DecodeEmbeddingPayload(junk); err == nil {
			t.Errorf("DecodeEmbeddingPayload(%v) = nil error, want empty-data error", junk)
		}
	}
}

// TestGetEmbedding_DecodesBothPayloadFormats pins the regression that motivated
// the shared codec: GetEmbedding must return the correct vector for BOTH the
// int8 Matryoshka payload the adapter writes and the legacy float32 blob.
// The default build can only verify the stub's disabled error; the
// cortex_vectors build runs both format assertions against a live table.
func TestGetEmbedding_DecodesBothPayloadFormats(t *testing.T) {
	db := testutil.NewTestDB(t)
	db.MustExec(`CREATE TABLE observation_vectors (
		observation_id INTEGER PRIMARY KEY,
		embedding      BLOB,
		embedding_model TEXT,
		dimensions     INTEGER,
		created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	store := NewVectorStore(db.DB())
	ctx := context.Background()

	if !store.IsAvailable() {
		if _, _, err := store.GetEmbedding(ctx, 1); !errors.Is(err, domain.ErrVectorSearchDisabled) {
			t.Fatalf("stub GetEmbedding error = %v, want ErrVectorSearchDisabled", err)
		}
		return
	}

	int8Payload := buildInt8Payload(8, 2, []int8{10, -5, 0, 127})
	legacyVec := []float32{0.5, -1, 2, 4}
	db.MustExec(`INSERT INTO observation_vectors (observation_id, embedding, embedding_model, dimensions)
		VALUES (1, ?, 'codec-model', 8)`, int8Payload)
	db.MustExec(`INSERT INTO observation_vectors (observation_id, embedding, embedding_model, dimensions)
		VALUES (2, ?, 'codec-model', 4)`, float32Bytes(legacyVec))

	gotInt8, model, err := store.GetEmbedding(ctx, 1)
	if err != nil {
		t.Fatalf("GetEmbedding int8 row: %v", err)
	}
	if model != "codec-model" {
		t.Errorf("model = %q, want %q", model, "codec-model")
	}
	wantInt8 := []float32{20, -10, 0, 254, 0, 0, 0, 0}
	if len(gotInt8) != len(wantInt8) {
		t.Fatalf("int8 row decoded to %d elements, want %d", len(gotInt8), len(wantInt8))
	}
	for i := range wantInt8 {
		if gotInt8[i] != wantInt8[i] {
			t.Errorf("int8 row element %d = %v, want %v", i, gotInt8[i], wantInt8[i])
		}
	}

	gotLegacy, _, err := store.GetEmbedding(ctx, 2)
	if err != nil {
		t.Fatalf("GetEmbedding legacy row: %v", err)
	}
	if len(gotLegacy) != len(legacyVec) {
		t.Fatalf("legacy row decoded to %d elements, want %d", len(gotLegacy), len(legacyVec))
	}
	for i := range legacyVec {
		if gotLegacy[i] != legacyVec[i] {
			t.Errorf("legacy row element %d = %v, want %v", i, gotLegacy[i], legacyVec[i])
		}
	}

	if _, _, err := store.GetEmbedding(ctx, 999); !domain.IsNotFoundError(err) {
		t.Errorf("missing row error = %v, want NotFoundError", err)
	}
}
