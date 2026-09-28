package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func tagsServer(status int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestIsRunning_NonOKStatus(t *testing.T) {
	srv := tagsServer(http.StatusInternalServerError, "")
	defer srv.Close()

	if NewManager(srv.URL).IsRunning(context.Background()) {
		t.Fatal("expected false for non-200 response")
	}
}

func TestIsRunning_InvalidBaseURL(t *testing.T) {
	if NewManager("://bad").IsRunning(context.Background()) {
		t.Fatal("expected false for invalid base URL")
	}
}

func TestHasModel_ErrorBranches(t *testing.T) {
	t.Run("non-200", func(t *testing.T) {
		srv := tagsServer(http.StatusServiceUnavailable, "")
		defer srv.Close()

		_, err := NewManager(srv.URL).HasModel(context.Background(), "m")
		if err == nil || !strings.Contains(err.Error(), "503") {
			t.Fatalf("got %v, want status error", err)
		}
	})

	t.Run("malformed json", func(t *testing.T) {
		srv := tagsServer(http.StatusOK, "{not json")
		defer srv.Close()

		_, err := NewManager(srv.URL).HasModel(context.Background(), "m")
		if err == nil || !strings.Contains(err.Error(), "failed to decode tags") {
			t.Fatalf("got %v, want decode error", err)
		}
	})

	t.Run("invalid base url", func(t *testing.T) {
		_, err := NewManager("://bad").HasModel(context.Background(), "m")
		if err == nil {
			t.Fatal("expected request construction error")
		}
	})
}

func TestHasModel_PrefixNormalization(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(tagsResponse{
			Models: []struct {
				Name string `json:"name"`
			}{{Name: "qwen3-embedding:8b-instruct"}},
		})
	}))
	defer srv.Close()

	has, err := NewManager(srv.URL).HasModel(context.Background(), "qwen3-embedding")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("expected bare model name to match tagged entry")
	}
}

func TestWaitReady_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := NewManager("http://127.0.0.1:19999").WaitReady(ctx, 5*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestWaitReady_BecomesReadyAfterPoll(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := NewManager(srv.URL).WaitReady(context.Background(), 5*time.Second); err != nil {
		t.Fatalf("WaitReady should succeed after retry: %v", err)
	}
}

func TestStart_BinaryNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	mgr := NewManager("http://127.0.0.1:19999")
	err := mgr.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ollama binary not found in PATH") {
		t.Fatalf("got %v, want missing binary error", err)
	}
	if mgr.StartedByUs() {
		t.Fatal("expected StartedByUs false after failed start")
	}
}

func TestPullModel_StartError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := NewManager("").PullModel(context.Background(), "some-model", nil)
	if err == nil || !strings.Contains(err.Error(), "failed to start ollama pull") {
		t.Fatalf("got %v, want start error", err)
	}
}

func TestEnsureRunning_AlreadyRunning(t *testing.T) {
	srv := tagsServer(http.StatusOK, "{}")
	defer srv.Close()

	if err := NewManager(srv.URL).EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning on live server: %v", err)
	}
}

func TestEnsureRunning_StartFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := NewManager("http://127.0.0.1:19999").EnsureRunning(context.Background())
	if err == nil {
		t.Fatal("expected error when ollama cannot be started")
	}
}
