package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/config"
)

func TestStaticProvider(t *testing.T) {
	p := &StaticProvider{token: "my-static-token"}
	token, err := p.GetToken(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "my-static-token" {
		t.Errorf("expected %q, got %q", "my-static-token", token)
	}
}

func TestNewTokenProvider_Static(t *testing.T) {
	a := &config.HyperFleetAPIAuthConfig{Type: "static", Token: "abc123"}
	provider, err := NewTokenProvider(a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	token, err := provider.GetToken(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "abc123" {
		t.Errorf("expected %q, got %q", "abc123", token)
	}
}

func TestNewTokenProvider_Kubernetes(t *testing.T) {
	a := &config.HyperFleetAPIAuthConfig{Type: "kubernetes"}
	provider, err := NewTokenProvider(a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := provider.(*KubernetesProvider); !ok {
		t.Errorf("expected *KubernetesProvider, got %T", provider)
	}
}

func TestNewTokenProvider_Unknown(t *testing.T) {
	a := &config.HyperFleetAPIAuthConfig{Type: "oauth2"}
	_, err := NewTokenProvider(a)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected error to contain %q, got: %v", "unsupported", err)
	}
}

func TestNewTokenProvider_Nil(t *testing.T) {
	_, err := NewTokenProvider(nil)
	if err == nil {
		t.Fatal("expected error for nil config, got nil")
	}
}

func TestKubernetesProvider_MissingFile(t *testing.T) {
	p := &KubernetesProvider{}
	_, err := p.GetToken(context.Background())
	if err == nil {
		t.Fatal("expected error when mounted token file is absent, got nil")
	}
	if !strings.Contains(err.Error(), "failed to read mounted ServiceAccount token") {
		t.Errorf("unexpected error message: %v", err)
	}
}
