package auth

import (
	"context"
	"fmt"
	"os"

	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/config"
)

const mountedTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token" //nolint:gosec

// TokenProvider generates a bearer token for outbound API requests.
type TokenProvider interface {
	GetToken(ctx context.Context) (string, error)
}

// StaticProvider returns a pre-configured static token.
type StaticProvider struct{ token string }

func (s *StaticProvider) GetToken(_ context.Context) (string, error) { return s.token, nil }

// KubernetesProvider reads the pod's projected ServiceAccount token from the standard mount path.
type KubernetesProvider struct{}

func (p *KubernetesProvider) GetToken(_ context.Context) (string, error) {
	data, err := os.ReadFile(mountedTokenPath)
	if err != nil {
		return "", fmt.Errorf("failed to read mounted ServiceAccount token from %s: %w", mountedTokenPath, err)
	}
	return string(data), nil
}

// NewTokenProvider builds a TokenProvider from auth config.
// type=static: returns the literal token value (set via config or HYPERFLEET_API_AUTH_TOKEN env var).
// type=kubernetes: reads the pod's mounted ServiceAccount token file on each call.
func NewTokenProvider(auth *config.HyperFleetAPIAuthConfig) (TokenProvider, error) {
	if auth == nil {
		return nil, fmt.Errorf("auth config must not be nil")
	}
	switch auth.Type {
	case "static":
		return &StaticProvider{token: auth.Token}, nil
	case "kubernetes":
		return &KubernetesProvider{}, nil
	default:
		return nil, fmt.Errorf("unsupported authorization type: %q", auth.Type)
	}
}
