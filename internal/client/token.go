package client

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// TokenError is returned when a bearer token cannot be read from disk. It is
// never retriable — the file path is wrong or the file is unreadable, which
// requires operator intervention rather than a retry.
type TokenError struct {
	cause error
}

func (e *TokenError) Error() string { return fmt.Sprintf("bearer token unavailable: %v", e.cause) }
func (e *TokenError) Unwrap() error { return e.cause }

// IsTokenError reports whether any error in err's chain is a TokenError.
func IsTokenError(err error) bool {
	var t *TokenError
	return errors.As(err, &t)
}

// fileTokenSource reads a bearer token from disk on every call, or caches it
// for cacheTTL when cacheTTL > 0. A zero cacheTTL disables caching and causes
// the file to be re-read on every request. It is safe for concurrent use.
type fileTokenSource struct {
	path      string
	cached    string
	mu        sync.RWMutex
	cacheTTL  time.Duration
	expiresAt int64 // Unix nanoseconds; only meaningful when cacheTTL > 0
}

func newFileTokenSource(path string, cacheTTL time.Duration) *fileTokenSource {
	return &fileTokenSource{path: path, cacheTTL: cacheTTL}
}

// get returns the current token. When cacheTTL > 0 the result is served from
// memory until the TTL elapses; when cacheTTL == 0 the file is read every call.
func (s *fileTokenSource) get() (string, error) {
	if s.cacheTTL == 0 {
		return s.readFile()
	}

	now := time.Now().UnixNano()

	s.mu.RLock()
	if now < s.expiresAt {
		tok := s.cached
		s.mu.RUnlock()
		return tok, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-check under write lock — another goroutine may have refreshed.
	if now < s.expiresAt {
		return s.cached, nil
	}

	tok, err := s.readFile()
	if err != nil {
		return "", err
	}
	s.cached = tok
	s.expiresAt = time.Now().Add(s.cacheTTL).UnixNano()
	return tok, nil
}

func (s *fileTokenSource) readFile() (string, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return "", fmt.Errorf("reading token file %s: %w", s.path, err)
	}
	tok := strings.TrimSpace(string(raw))
	if tok == "" {
		return "", fmt.Errorf("token file %s is empty", s.path)
	}
	return tok, nil
}
