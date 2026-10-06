// Package sqlite: build-agnostic embedding payload codec.
//
// The int8 Matryoshka prefix record is written by the sqlite_blob adapter
// (which compiles under BOTH build tags) and must be readable by the
// cortex_vectors-only store helpers, so the header contract lives here next
// to vector_constants.go instead of inside a build-tag-gated file.
package sqlite

import (
	"encoding/binary"
	"fmt"
	"math"
)

// QuantMagic marks a payload as an int8 prefix record. Its bytes are the
// little-endian encoding of a quiet NaN float32, so a legacy float32 blob
// cannot begin with them; DecodeQuantHeader additionally enforces structural
// invariants so a misread can never be scored.
var QuantMagic = [4]byte{0x00, 0x00, 0xC0, 0x7F}

// QuantHeaderBytes is magic (4) + declared dimension (2) + scale (4). The
// prefix length is implied by the payload length, so it costs no bytes.
const QuantHeaderBytes = 10

// DecodeQuantHeader validates the int8 payload header. Every structural
// invariant is checked, so a legacy float32 blob that happened to start with
// the magic bytes still falls through to the float32 path instead of being
// misread.
func DecodeQuantHeader(data []byte) (dims, prefix int, scale float64, ok bool) {
	if len(data) < QuantHeaderBytes+1 || data[0] != QuantMagic[0] || data[1] != QuantMagic[1] ||
		data[2] != QuantMagic[2] || data[3] != QuantMagic[3] {
		return 0, 0, 0, false
	}
	dims = int(binary.LittleEndian.Uint16(data[4:6]))
	scale = float64(math.Float32frombits(binary.LittleEndian.Uint32(data[6:10])))
	prefix = len(data) - QuantHeaderBytes
	if dims < 1 || prefix < 1 || prefix > dims {
		return 0, 0, 0, false
	}
	if math.IsNaN(scale) || math.IsInf(scale, 0) || scale <= 0 {
		return 0, 0, 0, false
	}
	return dims, prefix, scale, true
}

// DecodeEmbeddingPayload decodes either stored payload format back to a
// dims-length vector. An int8 prefix record dequantizes its stored prefix and
// zero-fills the truncated Matryoshka tail (the truncated dimensions carried
// under the energy floor); anything else falls through to the legacy
// little-endian float32 decode.
func DecodeEmbeddingPayload(data []byte) ([]float32, error) {
	dims, prefix, scale, ok := DecodeQuantHeader(data)
	if !ok {
		return deserializeEmbedding(data)
	}
	embedding := make([]float32, dims)
	for i := 0; i < prefix; i++ {
		embedding[i] = float32(int8(data[QuantHeaderBytes+i])) * float32(scale)
	}
	return embedding, nil
}

// deserializeEmbedding converts a legacy binary BLOB back to a float32 slice.
// Trailing bytes of a blob whose length is not a multiple of 4 are ignored:
// only len(data)/4 complete floats are consumed.
func deserializeEmbedding(data []byte) ([]float32, error) {
	dimension := len(data) / 4 // float32 is 4 bytes
	if dimension == 0 {
		return nil, fmt.Errorf("empty embedding data")
	}

	embedding := make([]float32, dimension)
	for i := range embedding {
		embedding[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return embedding, nil
}
