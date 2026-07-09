package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const (
	testClustersAPIPath     = "/api/hyperfleet/v1/clusters"
	testCreatedTime         = "2025-01-01T09:00:00Z"
	testUpdatedTime         = "2025-01-01T10:00:00Z"
	testLastUpdatedTime     = "2025-01-01T12:00:00Z"
	testUser                = "test-user@example.com"
	testConditionStatusTrue = "True"
	testReconciledFilter    = "status.conditions.Reconciled='False'"
	testKindCluster         = "Cluster"
	testKindNodePool        = "NodePool"
	testLabelRegion         = "region"
	testLabelEnv            = "env"
	testLabelShard          = "shard"
	testLabelValue          = "value"
	testRegionUSEast        = "us-east"

	keyHref               = "href"
	keyKind               = "kind"
	keyName               = "name"
	keyGeneration         = "generation"
	keyCreatedTime        = "created_time"
	keyUpdatedTime        = "updated_time"
	keyCreatedBy          = "created_by"
	keyUpdatedBy          = "updated_by"
	keySpec               = "spec"
	keyStatus             = "status"
	keyConditions         = "conditions"
	keyType               = "type"
	keyLastTransitionTime = "last_transition_time"
	keyLastUpdatedTime    = "last_updated_time"
	keyObservedGeneration = "observed_generation"
	keyPage               = "page"
	keySize               = "size"
	keyTotal              = "total"
	keyItems              = "items"
)

func createMockCondition(condType, status string, observedGen int) map[string]interface{} {
	return map[string]interface{}{
		keyType:               condType,
		keyStatus:             status,
		keyCreatedTime:        testCreatedTime,
		keyLastTransitionTime: testUpdatedTime,
		keyLastUpdatedTime:    testLastUpdatedTime,
		keyObservedGeneration: observedGen,
	}
}

func createMockResource(id, kind string) map[string]interface{} {
	return map[string]interface{}{
		"id":           id,
		keyHref:        "/api/hyperfleet/v1/resources/" + id,
		keyKind:        kind,
		keyName:        id,
		keyGeneration:  5,
		keyCreatedTime: testCreatedTime,
		keyUpdatedTime: testUpdatedTime,
		keyCreatedBy:   testUser,
		keyUpdatedBy:   testUser,
		keySpec:        map[string]interface{}{},
		keyStatus: map[string]interface{}{
			keyConditions: []map[string]interface{}{
				createMockCondition("Reconciled", testConditionStatusTrue, 5),
				createMockCondition("LastKnownReconciled", testConditionStatusTrue, 5),
			},
		},
	}
}

func createMockResourceWithOwner(
	id, name, kind string, generation int, ownerID, ownerKind string,
) map[string]interface{} {
	r := map[string]interface{}{
		"id":           id,
		keyHref:        "/api/hyperfleet/v1/resources/" + id,
		keyKind:        kind,
		keyName:        name,
		keyGeneration:  generation,
		keyCreatedTime: testCreatedTime,
		keyUpdatedTime: testUpdatedTime,
		keyCreatedBy:   testUser,
		keyUpdatedBy:   testUser,
		"owner_references": map[string]interface{}{
			"id":    ownerID,
			keyKind: ownerKind,
			keyHref: "/api/hyperfleet/v1/resources/" + ownerID,
		},
		keySpec: map[string]interface{}{},
		keyStatus: map[string]interface{}{
			keyConditions: []map[string]interface{}{
				createMockCondition("Reconciled", testConditionStatusTrue, generation),
				createMockCondition("LastKnownReconciled", testConditionStatusTrue, generation),
			},
		},
	}
	return r
}

func createMockResourceList(items []map[string]interface{}, page, total int) map[string]interface{} {
	return map[string]interface{}{
		keyPage:  page,
		keySize:  len(items),
		keyTotal: total,
		keyItems: items,
	}
}

func TestFetchResources_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		if r.URL.Path != testClustersAPIPath {
			t.Errorf("Expected path %s, got %s", testClustersAPIPath, r.URL.Path)
		}

		cluster := createMockResource("cluster-1", testKindCluster)
		cluster["labels"] = map[string]string{testLabelRegion: testRegionUSEast}
		response := createMockResourceList([]map[string]interface{}{cluster}, 1, 1)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	ctx := context.Background()
	resources, err := client.FetchResources(ctx, "clusters", nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("Expected 1 resource, got %d", len(resources))
	}
	if resources[0].ID != "cluster-1" {
		t.Errorf("Expected ID cluster-1, got %s", resources[0].ID)
	}
	if resources[0].Generation != 5 {
		t.Errorf("Expected generation 5, got %d", resources[0].Generation)
	}
	if len(resources[0].Status.Conditions) == 0 {
		t.Fatal("Expected at least one condition")
	}
	if resources[0].Status.Conditions[0].Status != "True" {
		t.Errorf("Expected Reconciled condition status 'True', got %s", resources[0].Status.Conditions[0].Status)
	}
}

func TestFetchResources_EmptyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := createMockResourceList([]map[string]interface{}{}, 1, 0)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	resources, err := client.FetchResources(context.Background(), "clusters", nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("Expected 0 resources, got %d", len(resources))
	}
}

func TestFetchResources_404NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		if _, err := w.Write([]byte(`{"error": "not found"}`)); err != nil {
			t.Errorf("Failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	_, err := client.FetchResources(context.Background(), "clusters", nil)

	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	t.Logf("Got expected 404 error: %v", err)
}

func TestFetchResources_500ServerError(t *testing.T) {
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := w.Write([]byte(`{"error": "internal server error"}`)); err != nil {
			t.Errorf("Failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := client.FetchResources(ctx, "clusters", nil)

	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if attemptCount < 2 {
		t.Errorf("Expected at least 2 attempts due to retries, got %d", attemptCount)
	}
	t.Logf("Server received %d requests (initial + retries)", attemptCount)
}

func TestFetchResources_503ServiceUnavailable_ThenSuccess(t *testing.T) {
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		if attemptCount < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := w.Write([]byte(`{"error": "service unavailable"}`)); err != nil {
				t.Errorf("Failed to write response: %v", err)
			}
			return
		}

		response := createMockResourceList([]map[string]interface{}{
			createMockResource("cluster-1", testKindCluster),
		}, 1, 1)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	resources, err := client.FetchResources(context.Background(), "clusters", nil)
	if err != nil {
		t.Fatalf("Expected no error after retry, got %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("Expected 1 resource, got %d", len(resources))
	}
	if attemptCount < 2 {
		t.Errorf("Expected at least 2 attempts, got %d", attemptCount)
	}
	t.Logf("Successfully recovered after %d attempts", attemptCount)
}

func TestFetchResources_429RateLimited(t *testing.T) {
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		if attemptCount < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			if _, err := w.Write([]byte(`{"error": "rate limited"}`)); err != nil {
				t.Errorf("Failed to write response: %v", err)
			}
			return
		}

		response := createMockResourceList([]map[string]interface{}{}, 1, 0)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	_, err := client.FetchResources(context.Background(), "clusters", nil)
	if err != nil {
		t.Fatalf("Expected no error after retry, got %v", err)
	}
	if attemptCount < 2 {
		t.Errorf("Expected at least 2 attempts due to 429, got %d", attemptCount)
	}
}

func TestFetchResources_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := client.FetchResources(ctx, "clusters", nil)

	if err == nil {
		t.Fatal("Expected timeout error, got nil")
	}
	t.Logf("Got expected timeout error: %v", err)
}

func TestFetchResources_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := client.FetchResources(ctx, "clusters", nil)

	if err == nil {
		t.Fatal("Expected context cancellation error, got nil")
	}
	t.Logf("Got expected context cancellation error: %v", err)
}

func TestFetchResources_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"items": [malformed json`)); err != nil {
			t.Errorf("Failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	_, err := client.FetchResources(context.Background(), "clusters", nil)

	if err == nil {
		t.Fatal("Expected error for malformed JSON, got nil")
	}
	t.Logf("Got expected error for malformed JSON: %v", err)
}

func TestFetchResources_NilContext(t *testing.T) {
	client := newTestClient(t, "http://localhost", 10*time.Second)

	// nolint:staticcheck // Testing nil context validation
	var nilCtx context.Context
	_, err := client.FetchResources(nilCtx, "clusters", nil)

	if err == nil {
		t.Fatal("Expected error for nil context, got nil")
	}
	if err.Error() != "context cannot be nil" {
		t.Errorf("Expected 'context cannot be nil' error, got: %v", err)
	}
}

func TestFetchResources_EmptyResourceType(t *testing.T) {
	client := newTestClient(t, "http://localhost", 10*time.Second)

	_, err := client.FetchResources(context.Background(), "", nil)

	if err == nil {
		t.Fatal("Expected error for empty resourceType, got nil")
	}
	if !strings.Contains(err.Error(), "resourceType cannot be empty") {
		t.Fatalf("Expected 'resourceType cannot be empty' error, got: %v", err)
	}
}

func TestFetchResources_InvalidResourceType(t *testing.T) {
	client := newTestClient(t, "http://localhost", 10*time.Second)

	tests := []struct {
		name         string
		resourceType string
	}{
		{"contains slash", "clusters/foo"},
		{"contains query", "clusters?x=1"},
		{"contains hash", "clusters#frag"},
		{"contains percent", "clusters%2Ffoo"},
		{"contains backslash", "clusters\\foo"},
		{"dot segment", "."},
		{"dot-dot segment", ".."},
		{"internal whitespace", "my resources"},
		{"internal tab", "my\tresources"},
		{"leading whitespace", " clusters"},
		{"trailing whitespace", "clusters "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.FetchResources(context.Background(), tt.resourceType, nil)
			if err == nil {
				t.Fatalf("Expected error for resourceType %q, got nil", tt.resourceType)
			}
		})
	}
}

func TestFetchResources_CustomResourceType(t *testing.T) {
	var receivedPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path

		response := createMockResourceList([]map[string]interface{}{
			createMockResource("wc-1", "WifConfig"),
		}, 1, 1)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	resources, err := client.FetchResources(context.Background(), "wifconfigs", nil)
	if err != nil {
		t.Fatalf("Expected no error for custom resource type, got %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("Expected 1 resource, got %d", len(resources))
	}
	if resources[0].Kind != "WifConfig" {
		t.Errorf("Expected kind WifConfig, got %s", resources[0].Kind)
	}
	if receivedPath != "/api/hyperfleet/v1/wifconfigs" {
		t.Errorf("Expected path /api/hyperfleet/v1/wifconfigs, got %s", receivedPath)
	}
}

func TestFetchResources_WithReferences(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resource := createMockResource("cluster-1", testKindCluster)
		resource["references"] = map[string]interface{}{
			"wif_config": []interface{}{
				map[string]interface{}{
					"id":    "wc-xyz",
					keyKind: "WifConfig",
					keyHref: "/api/hyperfleet/v1/resources/wc-xyz",
				},
			},
			"network": []interface{}{
				map[string]interface{}{
					"id":    "net-1",
					keyKind: "Network",
					keyHref: "/api/hyperfleet/v1/resources/net-1",
				},
				map[string]interface{}{
					"id":    "net-2",
					keyKind: "Network",
					keyHref: "/api/hyperfleet/v1/resources/net-2",
				},
			},
		}
		response := createMockResourceList([]map[string]interface{}{resource}, 1, 1)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	resources, err := client.FetchResources(context.Background(), "clusters", nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("Expected 1 resource, got %d", len(resources))
	}

	refs := resources[0].References
	if refs == nil {
		t.Fatal("Expected References to be set, got nil")
	}
	if len(refs["wif_config"]) != 1 {
		t.Fatalf("Expected 1 wif_config reference, got %d", len(refs["wif_config"]))
	}
	if refs["wif_config"][0].ID != "wc-xyz" {
		t.Errorf("Expected wif_config ref ID wc-xyz, got %s", refs["wif_config"][0].ID)
	}
	if len(refs["network"]) != 2 {
		t.Fatalf("Expected 2 network references, got %d", len(refs["network"]))
	}
}

func TestFetchResources_WithOwnerReferences(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		np := createMockResourceWithOwner("np-1", "workers", testKindNodePool, 3, "cluster-123", testKindCluster)
		response := createMockResourceList([]map[string]interface{}{np}, 1, 1)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	resources, err := client.FetchResources(context.Background(), "nodepools", nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("Expected 1 resource, got %d", len(resources))
	}
	if resources[0].OwnerReferences == nil {
		t.Fatal("Expected OwnerReferences to be set, got nil")
	}
	if resources[0].OwnerReferences.ID != "cluster-123" {
		t.Errorf("Expected OwnerReferences.ID cluster-123, got %s", resources[0].OwnerReferences.ID)
	}
	if resources[0].OwnerReferences.Kind != testKindCluster {
		t.Errorf("Expected OwnerReferences.Kind Cluster, got %s", resources[0].OwnerReferences.Kind)
	}
	if resources[0].Name != "workers" {
		t.Errorf("Expected Name workers, got %s", resources[0].Name)
	}
}

func TestFetchResources_NilStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cluster := createMockResource("cluster-2", testKindCluster)
		delete(cluster, keyStatus)
		response := createMockResourceList([]map[string]interface{}{cluster}, 1, 1)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	resources, err := client.FetchResources(context.Background(), "clusters", nil)
	if err != nil {
		t.Fatalf("Expected no error (graceful degradation), got %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("Expected 1 resource, got %d", len(resources))
	}
	if resources[0].ID != "cluster-2" {
		t.Errorf("Expected cluster-2, got %s", resources[0].ID)
	}
}

func TestFetchResources_MissingTokenFile(t *testing.T) {
	requestReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewHyperFleetClient(
		server.URL, 10*time.Second, "test-sentinel", "test", DefaultPageSize, "/nonexistent/token", 0,
	)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	_, err = client.FetchResources(context.Background(), "clusters", nil)
	if err == nil {
		t.Fatal("Expected error for missing token file, got nil")
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Retriable {
		t.Errorf("Expected non-retriable error, got retriable: %v", err)
	}
	if requestReceived {
		t.Error("Expected no request to reach the server, but one was sent")
	}
}

func TestIsHTTPStatusRetriable(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		retriable  bool
	}{
		{name: "200 OK - not retriable", statusCode: 200, retriable: false},
		{name: "201 Created - not retriable", statusCode: 201, retriable: false},
		{name: "400 Bad Request - not retriable", statusCode: 400, retriable: false},
		{name: "401 Unauthorized - not retriable", statusCode: 401, retriable: false},
		{name: "403 Forbidden - not retriable", statusCode: 403, retriable: false},
		{name: "404 Not Found - not retriable", statusCode: 404, retriable: false},
		{name: "408 Request Timeout - retriable", statusCode: 408, retriable: true},
		{name: "429 Too Many Requests - retriable", statusCode: 429, retriable: true},
		{name: "500 Internal Server Error - retriable", statusCode: 500, retriable: true},
		{name: "502 Bad Gateway - retriable", statusCode: 502, retriable: true},
		{name: "503 Service Unavailable - retriable", statusCode: 503, retriable: true},
		{name: "504 Gateway Timeout - retriable", statusCode: 504, retriable: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isHTTPStatusRetriable(tt.statusCode)
			if result != tt.retriable {
				t.Errorf("isHTTPStatusRetriable(%d) = %v, want %v",
					tt.statusCode, result, tt.retriable)
			}
		})
	}
}

func TestLabelSelectorToSearchString(t *testing.T) {
	tests := []struct {
		name     string
		selector map[string]string
		want     string
	}{
		{name: "empty selector", selector: map[string]string{}, want: ""},
		{name: "nil selector", selector: nil, want: ""},
		{
			name:     "single label",
			selector: map[string]string{testLabelRegion: testRegionUSEast},
			want:     "labels.region='us-east'",
		},
		{
			name:     "multiple labels (sorted)",
			selector: map[string]string{testLabelRegion: testRegionUSEast, testLabelEnv: "production"},
			want:     "labels.env='production' and labels.region='us-east'",
		},
		{
			name:     "three labels (sorted)",
			selector: map[string]string{"tier": "frontend", testLabelRegion: "us-west", testLabelEnv: "staging"},
			want:     "labels.env='staging' and labels.region='us-west' and labels.tier='frontend'",
		},
		{
			name:     "label with hyphen in key",
			selector: map[string]string{"my-label": testLabelValue},
			want:     "labels.my-label='value'",
		},
		{
			name:     "label with underscore in key",
			selector: map[string]string{"my_label": testLabelValue},
			want:     "labels.my_label='value'",
		},
		{
			name:     "label with hyphen in value",
			selector: map[string]string{testLabelRegion: "us-east-1"},
			want:     "labels.region='us-east-1'",
		},
		{
			name:     "label value with single quote (escaped)",
			selector: map[string]string{keyName: "test'value"},
			want:     "labels.name='test''value'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := labelSelectorToSearchString(tt.selector)
			if got != tt.want {
				t.Errorf("labelSelectorToSearchString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFetchResources_NodePools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		if r.URL.Path != "/api/hyperfleet/v1/nodepools" {
			t.Errorf("Expected path /api/hyperfleet/v1/nodepools, got %s", r.URL.Path)
		}

		np := createMockResourceWithOwner("nodepool-1", "workers", testKindNodePool, 3, "cluster-123", testKindCluster)
		response := createMockResourceList([]map[string]interface{}{np}, 1, 1)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)
	resources, err := client.FetchResources(context.Background(), "nodepools", nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("Expected 1 resource, got %d", len(resources))
	}
	if resources[0].ID != "nodepool-1" {
		t.Errorf("Expected ID nodepool-1, got %s", resources[0].ID)
	}
	if resources[0].Kind != testKindNodePool {
		t.Errorf("Expected kind NodePool, got %s", resources[0].Kind)
	}
	if resources[0].Generation != 3 {
		t.Errorf("Expected generation 3 for nodepool, got %d", resources[0].Generation)
	}
	if len(resources[0].Status.Conditions) == 0 {
		t.Fatal("Expected at least one condition for nodepool")
	}
	if resources[0].Status.Conditions[0].ObservedGeneration != 3 {
		t.Errorf("Expected observed generation 3, got %d", resources[0].Status.Conditions[0].ObservedGeneration)
	}
	if resources[0].OwnerReferences == nil {
		t.Fatal("Expected OwnerReferences to be set, got nil")
	}
	if resources[0].OwnerReferences.ID != "cluster-123" {
		t.Errorf("Expected OwnerReferences.ID cluster-123, got %s", resources[0].OwnerReferences.ID)
	}
	if resources[0].OwnerReferences.Kind != testKindCluster {
		t.Errorf("Expected OwnerReferences.Kind Cluster, got %s", resources[0].OwnerReferences.Kind)
	}
}

func TestNewHyperFleetClient_UserAgent(t *testing.T) {
	var receivedUA string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		response := createMockResourceList([]map[string]interface{}{}, 1, 0)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	c, err := NewHyperFleetClient(server.URL, 10*time.Second, "my-sentinel", "v1.2.3", DefaultPageSize, "", 0)
	if err != nil {
		t.Fatalf("NewHyperFleetClient: %v", err)
	}

	if _, err := c.FetchResources(context.Background(), "clusters", nil); err != nil {
		t.Fatalf("FetchResources: %v", err)
	}

	expected := "hyperfleet-sentinel/v1.2.3 (my-sentinel)"
	if receivedUA != expected {
		t.Errorf("User-Agent = %q, want %q", receivedUA, expected)
	}
}

func TestFetchResources_WithLabelSelector(t *testing.T) {
	var receivedSearchParam string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSearchParam = r.URL.Query().Get("search")

		cluster := createMockResource("cluster-1", testKindCluster)
		response := createMockResourceList([]map[string]interface{}{cluster}, 1, 1)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	labelSelector := map[string]string{
		testLabelRegion: testRegionUSEast,
		testLabelEnv:    "production",
	}

	resources, err := client.FetchResources(context.Background(), "clusters", labelSelector)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("Expected 1 resource, got %d", len(resources))
	}

	expectedSearch := "labels.env='production' and labels.region='us-east'"
	if receivedSearchParam != expectedSearch {
		t.Errorf("Expected search parameter %q, got %q", expectedSearch, receivedSearchParam)
	}
}

func TestVerifyConnectivity_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != testClustersAPIPath {
			t.Errorf("Expected path %s, got %s", testClustersAPIPath, r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET request, got %s", r.Method)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		response := map[string]string{keyStatus: "ok"}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	ctx := context.Background()
	err := client.VerifyConnectivity(ctx, "clusters")
	if err != nil {
		t.Errorf("Expected successful health check, got error: %v", err)
	}
}

func TestVerifyConnectivity_NonOKStatus(t *testing.T) {
	ctx := context.Background()
	testCases := []struct {
		name       string
		response   string
		statusCode int
	}{
		{name: "ServiceUnavailable", statusCode: http.StatusServiceUnavailable, response: `{"error": "service unavailable"}`},
		{
			name:       "InternalServerError",
			statusCode: http.StatusInternalServerError,
			response:   `{"error": "internal server error"}`,
		},
		{name: "NotFound", statusCode: http.StatusNotFound, response: `{"error": "endpoint not found"}`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.statusCode)
				if _, err := w.Write([]byte(tc.response)); err != nil {
					t.Errorf("Failed to write response: %v", err)
				}
			}))
			defer server.Close()

			client := newTestClient(t, server.URL, 10*time.Second)

			err := client.VerifyConnectivity(ctx, "clusters")

			if err == nil {
				t.Fatalf("Expected error for status %d, got nil", tc.statusCode)
			}
			if !strings.Contains(err.Error(), strconv.Itoa(tc.statusCode)) {
				t.Errorf("Expected error to mention status %d, got: %v", tc.statusCode, err)
			}
		})
	}
}

func TestVerifyConnectivity_MissingTokenFile(t *testing.T) {
	requestReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewHyperFleetClient(
		server.URL, 10*time.Second, "test-sentinel", "test", DefaultPageSize, "/nonexistent/token", 0,
	)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	err = client.VerifyConnectivity(context.Background(), "clusters")
	if err == nil {
		t.Fatal("Expected error for missing token file, got nil")
	}
	if !IsTokenError(err) {
		t.Errorf("Expected IsTokenError to be true, got false for: %v", err)
	}
	if requestReceived {
		t.Error("Expected no request to reach the server, but one was sent")
	}
}

func TestVerifyConnectivity_CustomResourceType(t *testing.T) {
	var receivedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{keyStatus: "ok"}); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	err := client.VerifyConnectivity(context.Background(), "wifconfigs")
	if err != nil {
		t.Errorf("Expected successful connectivity check for wifconfigs, got: %v", err)
	}
	if receivedPath != "/api/hyperfleet/v1/wifconfigs" {
		t.Errorf("Expected path /api/hyperfleet/v1/wifconfigs, got %s", receivedPath)
	}
}

func TestVerifyConnectivity_SendsAuthHeader(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{keyStatus: "ok"}); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	tokenFile := t.TempDir() + "/token"
	if err := os.WriteFile(tokenFile, []byte("test-token"), 0600); err != nil {
		t.Fatalf("Failed to write token file: %v", err)
	}

	client, err := NewHyperFleetClient(server.URL, 10*time.Second, "test-sentinel", "test", DefaultPageSize, tokenFile, 0)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	if err := client.VerifyConnectivity(context.Background(), "clusters"); err != nil {
		t.Fatalf("VerifyConnectivity returned unexpected error: %v", err)
	}
	if receivedAuth != "Bearer test-token" {
		t.Errorf("Expected Authorization header %q, got %q", "Bearer test-token", receivedAuth)
	}
}

func TestBuildSearchString(t *testing.T) {
	staleTimeFilter := "status.conditions.Reconciled.last_updated_time<='2025-01-01T00:00:00Z'"
	tests := []struct {
		labelSelector     map[string]string
		name              string
		want              string
		additionalFilters []string
	}{
		{name: "labels only", labelSelector: map[string]string{testLabelShard: "1"}, want: "labels.shard='1'"},
		{name: "filter only", additionalFilters: []string{testReconciledFilter}, want: testReconciledFilter},
		{
			name: "both labels and filter", labelSelector: map[string]string{testLabelShard: "1"},
			additionalFilters: []string{testReconciledFilter},
			want:              "labels.shard='1' and " + testReconciledFilter,
		},
		{
			name: "multiple filters", labelSelector: map[string]string{testLabelShard: "1"},
			additionalFilters: []string{"status.conditions.Reconciled='True'", staleTimeFilter},
			want:              "labels.shard='1' and status.conditions.Reconciled='True' and " + staleTimeFilter,
		},
		{name: "both empty", want: ""},
		{
			name:              "empty string filter ignored",
			additionalFilters: []string{"", testReconciledFilter, ""},
			want:              testReconciledFilter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSearchString(tt.labelSelector, tt.additionalFilters)
			if got != tt.want {
				t.Errorf("buildSearchString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func newTestClient(t *testing.T, url string, timeout time.Duration) *HyperFleetClient {
	t.Helper()
	client, err := NewHyperFleetClient(url, timeout, "test-sentinel", "test", DefaultPageSize, "", 0)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	return client
}

func TestNewHyperFleetClient_HTTPInstrumentation(t *testing.T) {
	previousProvider := otel.GetTracerProvider()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSampler(trace.AlwaysSample()),
		trace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(tp)
	defer func(tp *trace.TracerProvider, ctx context.Context) {
		err := tp.Shutdown(ctx)
		if err != nil {
			t.Errorf("Error shutting down tracer: %v", err)
		}
		otel.SetTracerProvider(previousProvider)
	}(tp, context.Background())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		if r.URL.Path != testClustersAPIPath {
			t.Errorf("Expected path %s, got %s", testClustersAPIPath, r.URL.Path)
		}

		cluster := createMockResource("test-cluster-1", testKindCluster)
		response := createMockResourceList([]map[string]interface{}{cluster}, 1, 1)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client, err := NewHyperFleetClient(server.URL, 10*time.Second, "test-sentinel", "test", DefaultPageSize, "", 0)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	if client == nil {
		t.Fatal("Expected client to be created")
	}

	ctx := context.Background()
	resources, err := client.FetchResources(ctx, "clusters", nil)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("Expected 1 resource, got %d", len(resources))
	}
	if resources[0].ID != "test-cluster-1" {
		t.Errorf("Expected ID test-cluster-1, got %s", resources[0].ID)
	}

	err = tp.ForceFlush(ctx)
	if err != nil {
		t.Fatalf("Error force flush: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("Expected HTTP spans to be created, got none")
	}

	var httpSpan *tracetest.SpanStub
	for i := range spans {
		if strings.Contains(spans[i].Name, "GET") {
			httpSpan = &spans[i]
			break
		}
	}

	if httpSpan == nil {
		spanNames := make([]string, len(spans))
		for i, span := range spans {
			spanNames[i] = span.Name
		}
		t.Fatalf("Expected HTTP GET span, got spans: %v", spanNames)
	}

	foundMethodAttr := false
	foundURLAttr := false
	for _, attr := range httpSpan.Attributes {
		switch string(attr.Key) {
		case "http.request.method", "http.method":
			if attr.Value.AsString() == "GET" {
				foundMethodAttr = true
			}
		case "url.full", "http.url":
			if strings.Contains(attr.Value.AsString(), testClustersAPIPath) {
				foundURLAttr = true
			}
		}
	}

	if !foundMethodAttr {
		t.Error("Expected HTTP method attribute in span")
	}
	if !foundURLAttr {
		t.Error("Expected URL attribute in span")
	}

	if httpSpan.Status.Code.String() == "ERROR" {
		t.Errorf("Expected successful HTTP span, got error status: %s", httpSpan.Status.Description)
	}
}

func TestNewHyperFleetClient_HTTPInstrumentation_ErrorCase(t *testing.T) {
	previousProvider := otel.GetTracerProvider()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSampler(trace.AlwaysSample()),
		trace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(tp)
	defer func(tp *trace.TracerProvider, ctx context.Context) {
		err := tp.Shutdown(ctx)
		if err != nil {
			t.Errorf("Error shutting down tracer: %v", err)
		}
		otel.SetTracerProvider(previousProvider)
	}(tp, context.Background())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, err := w.Write([]byte(`{"error": "internal server error"}`))
		if err != nil {
			t.Errorf("Failed to write response: %v", err)
			return
		}
	}))
	defer server.Close()

	client, err := NewHyperFleetClient(server.URL, 10*time.Second, "test-sentinel", "test", DefaultPageSize, "", 0)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	_, err = client.FetchResources(ctx, "clusters", nil)

	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	err = tp.ForceFlush(ctx)
	if err != nil {
		t.Fatalf("Error force flush: %v", err)
	}
	spans := exporter.GetSpans()

	if len(spans) == 0 {
		t.Fatal("Expected HTTP spans even on error")
	}
}

func TestFetchResources_WithAdditionalFilters(t *testing.T) {
	var receivedSearchParam string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSearchParam = r.URL.Query().Get("search")

		response := createMockResourceList([]map[string]interface{}{}, 1, 0)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Logf("Error encoding response: %v", err)
		}
	}))
	defer server.Close()

	client, _ := NewHyperFleetClient(server.URL, 10*time.Second, "test-sentinel", "test", DefaultPageSize, "", 0)
	labelSelector := map[string]string{testLabelShard: "1"}

	_, err := client.FetchResources(
		context.Background(), "clusters", labelSelector,
		testReconciledFilter,
	)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expectedSearch := "labels.shard='1' and " + testReconciledFilter
	if receivedSearchParam != expectedSearch {
		t.Errorf("Expected search parameter %q, got %q", expectedSearch, receivedSearchParam)
	}
}

func TestFetchResources_WithConditionFilterOnly(t *testing.T) {
	var receivedSearchParam string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSearchParam = r.URL.Query().Get("search")

		response := createMockResourceList([]map[string]interface{}{}, 1, 0)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Logf("Error encoding response: %v", err)
		}
	}))
	defer server.Close()

	client, _ := NewHyperFleetClient(server.URL, 10*time.Second, "test-sentinel", "test", DefaultPageSize, "", 0)

	_, err := client.FetchResources(
		context.Background(), "clusters", nil,
		"status.conditions.Reconciled='True'",
	)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expectedSearch := "status.conditions.Reconciled='True'"
	if receivedSearchParam != expectedSearch {
		t.Errorf("Expected search parameter %q, got %q", expectedSearch, receivedSearchParam)
	}
}

func TestFetchResources_Pagination(t *testing.T) {
	var requestedPages []int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageStr := r.URL.Query().Get(keyPage)
		page, _ := strconv.Atoi(pageStr)
		requestedPages = append(requestedPages, page)

		totalResources := 54
		pageSize := 20
		start := (page - 1) * pageSize
		end := start + pageSize
		if end > totalResources {
			end = totalResources
		}

		items := make([]map[string]interface{}, 0, end-start)
		for i := start; i < end; i++ {
			items = append(items, createMockResource(fmt.Sprintf("cluster-%d", i+1), testKindCluster))
		}

		response := createMockResourceList(items, page, totalResources)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)
	resources, err := client.FetchResources(context.Background(), "clusters", nil)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(resources) != 54 {
		t.Fatalf("Expected 54 resources, got %d", len(resources))
	}
	if len(requestedPages) != 3 {
		t.Fatalf("Expected 3 page requests, got %d: %v", len(requestedPages), requestedPages)
	}
	if resources[0].ID != "cluster-1" {
		t.Errorf("Expected first resource ID cluster-1, got %s", resources[0].ID)
	}
	if resources[53].ID != "cluster-54" {
		t.Errorf("Expected last resource ID cluster-54, got %s", resources[53].ID)
	}
}

func TestFetchResources_PaginationSinglePage(t *testing.T) {
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		items := []map[string]interface{}{
			createMockResource("cluster-1", testKindCluster),
			createMockResource("cluster-2", testKindCluster),
		}
		response := createMockResourceList(items, 1, 2)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)
	resources, err := client.FetchResources(context.Background(), "clusters", nil)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("Expected 2 resources, got %d", len(resources))
	}
	if requestCount != 1 {
		t.Errorf("Expected 1 request for single page, got %d", requestCount)
	}
}

func TestFetchResources_PaginationErrorOnSecondPage(t *testing.T) {
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount == 1 {
			items := make([]map[string]interface{}, 20)
			for i := range items {
				items[i] = createMockResource(fmt.Sprintf("cluster-%d", i+1), testKindCluster)
			}
			response := createMockResourceList(items, 1, 40)
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Errorf("Failed to encode response: %v", err)
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
		if _, err := w.Write([]byte(`{"error": "not found"}`)); err != nil {
			t.Errorf("Failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)
	resources, err := client.FetchResources(context.Background(), "clusters", nil)

	if err == nil {
		t.Fatal("Expected error on second page, got nil")
	}
	if len(resources) != 0 {
		t.Fatalf("Expected no resources on paginated failure, got %d", len(resources))
	}
}

func TestFetchResources_PaginationSendsSize(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		expectedPath string
	}{
		{name: "clusters", resourceType: "clusters", expectedPath: testClustersAPIPath},
		{name: "nodepools", resourceType: "nodepools", expectedPath: "/api/hyperfleet/v1/nodepools"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedSize string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedSize = r.URL.Query().Get(keySize)
				response := createMockResourceList([]map[string]interface{}{}, 1, 0)
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Errorf("Failed to encode response: %v", err)
				}
			}))
			defer server.Close()

			client := newTestClient(t, server.URL, 10*time.Second)
			_, err := client.FetchResources(context.Background(), tt.resourceType, nil)
			if err != nil {
				t.Fatalf("Expected no error, got %v", err)
			}

			expected := strconv.Itoa(int(DefaultPageSize))
			if receivedSize != expected {
				t.Errorf("Expected size=%s, got %q", expected, receivedSize)
			}
		})
	}
}
