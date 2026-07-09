package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileTokenSource_Get(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")

	if err := os.WriteFile(path, []byte("  my-token\n"), 0600); err != nil {
		t.Fatal(err)
	}

	ts := newFileTokenSource(path, 0)

	tok, err := ts.get()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "my-token" {
		t.Fatalf("got %q, want %q", tok, "my-token")
	}
}

func TestFileTokenSource_NoCacheReadsFileEveryTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")

	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}

	ts := newFileTokenSource(path, 0) // cacheTTL == 0: no cache

	tok1, err := ts.get()
	if err != nil {
		t.Fatal(err)
	}

	if err = os.WriteFile(path, []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}

	tok2, err := ts.get()
	if err != nil {
		t.Fatal(err)
	}
	if tok1 == tok2 {
		t.Fatalf("expected file to be re-read (no cache), but got same token %q both times", tok1)
	}
}

func TestFileTokenSource_CachesToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")

	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}

	ts := newFileTokenSource(path, time.Minute)
	tok1, err := ts.get()
	if err != nil {
		t.Fatal(err)
	}

	// Overwrite the file — the cache should still return the first value.
	if err = os.WriteFile(path, []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}

	tok2, err := ts.get()
	if err != nil {
		t.Fatal(err)
	}
	if tok1 != tok2 {
		t.Fatalf("expected cached token %q, got %q", tok1, tok2)
	}
}

func TestFileTokenSource_RefreshesAfterTTL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")

	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}

	ts := newFileTokenSource(path, time.Minute)
	if _, err := ts.get(); err != nil {
		t.Fatal(err)
	}

	// Expire the cache manually.
	ts.expiresAt = time.Now().Add(-time.Second).UnixNano()

	if err := os.WriteFile(path, []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}

	tok, err := ts.get()
	if err != nil {
		t.Fatal(err)
	}
	if tok != "second" {
		t.Fatalf("expected refreshed token %q, got %q", "second", tok)
	}
}

func TestFileTokenSource_MissingFile(t *testing.T) {
	ts := newFileTokenSource("/nonexistent/path/token", 0)
	_, err := ts.get()
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestFileTokenSource_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")

	if err := os.WriteFile(path, []byte("   \n"), 0600); err != nil {
		t.Fatal(err)
	}

	ts := newFileTokenSource(path, 0)
	_, err := ts.get()
	if err == nil {
		t.Fatal("expected error for empty token file")
	}
}

func TestIsTokenError(t *testing.T) {
	cause := fmt.Errorf("underlying cause")
	tokenErr := &TokenError{cause: cause}

	tests := []struct {
		err  error
		name string
		want bool
	}{
		{err: tokenErr, name: "direct TokenError", want: true},
		{err: fmt.Errorf("outer: %w", tokenErr), name: "TokenError wrapped with fmt.Errorf", want: true},
		{err: &APIError{cause: tokenErr, Message: "msg"}, name: "TokenError buried in APIError cause", want: true},
		{err: fmt.Errorf("something else"), name: "plain error", want: false},
		{err: nil, name: "nil", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTokenError(tt.err); got != tt.want {
				t.Errorf("IsTokenError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestTokenError_Unwrap(t *testing.T) {
	cause := fmt.Errorf("underlying cause")
	tokenErr := &TokenError{cause: cause}

	if tokenErr.Unwrap() != cause {
		t.Errorf("Unwrap() = %v, want %v", tokenErr.Unwrap(), cause)
	}

	// errors.Is must reach through TokenError to find the sentinel cause.
	if !errors.Is(tokenErr, cause) {
		t.Error("errors.Is(tokenErr, cause) = false, want true")
	}

	// Same check when TokenError is itself wrapped by fmt.Errorf.
	wrapped := fmt.Errorf("outer: %w", tokenErr)
	if !errors.Is(wrapped, cause) {
		t.Error("errors.Is through wrapped TokenError = false, want true")
	}
}

func TestNewHyperFleetClient_BearerToken(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("test-jwt-token"), 0600); err != nil {
		t.Fatal(err)
	}

	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		response := map[string]interface{}{
			"kind":  "ClusterList",
			"page":  1,
			"size":  0,
			"total": 0,
			"items": []interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	c, err := NewHyperFleetClient(server.URL, 10*time.Second, "test-sentinel", "test", DefaultPageSize, tokenFile, 0)
	if err != nil {
		t.Fatalf("NewHyperFleetClient: %v", err)
	}

	if _, err := c.FetchResources(context.Background(), "clusters", nil); err != nil {
		t.Fatalf("FetchResources: %v", err)
	}

	want := "Bearer test-jwt-token"
	if receivedAuth != want {
		t.Errorf("Authorization = %q, want %q", receivedAuth, want)
	}
}

func TestNewHyperFleetClient_NoAuthHeader_WhenNoTokenPath(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		response := map[string]interface{}{
			"kind":  "ClusterList",
			"page":  1,
			"size":  0,
			"total": 0,
			"items": []interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	c, err := NewHyperFleetClient(server.URL, 10*time.Second, "test-sentinel", "test", DefaultPageSize, "", 0)
	if err != nil {
		t.Fatalf("NewHyperFleetClient: %v", err)
	}

	if _, err := c.FetchResources(context.Background(), "clusters", nil); err != nil {
		t.Fatalf("FetchResources: %v", err)
	}

	if receivedAuth != "" {
		t.Errorf("expected no Authorization header, got %q", receivedAuth)
	}
}
