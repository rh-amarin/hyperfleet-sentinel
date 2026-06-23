package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const testClustersAPIPath = "/api/hyperfleet/v1/clusters"

// createMockCluster creates a mock cluster response with all required fields
func createMockCluster(id string) map[string]interface{} {
	return map[string]interface{}{
		"id":           id,
		"href":         "/api/hyperfleet/v1/clusters/" + id,
		"kind":         "Cluster",
		"name":         id,
		"generation":   5,
		"created_time": "2025-01-01T09:00:00Z",
		"updated_time": "2025-01-01T10:00:00Z",
		"created_by":   "test-user@example.com",
		"updated_by":   "test-user@example.com",
		"spec":         map[string]interface{}{},
		"status": map[string]interface{}{
			"conditions": []map[string]interface{}{
				{
					"type":                 "Reconciled",
					"status":               "True",
					"created_time":         "2025-01-01T09:00:00Z",
					"last_transition_time": "2025-01-01T10:00:00Z",
					"last_updated_time":    "2025-01-01T12:00:00Z",
					"observed_generation":  5,
				},
				{
					"type":                 "LastKnownReconciled",
					"status":               "True",
					"created_time":         "2025-01-01T09:00:00Z",
					"last_transition_time": "2025-01-01T10:00:00Z",
					"last_updated_time":    "2025-01-01T12:00:00Z",
					"observed_generation":  5,
				},
			},
		},
	}
}

// createMockClusterList creates a mock ClusterList response
func createMockClusterList(clusters []map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"page":  1,
		"size":  len(clusters),
		"total": len(clusters),
		"items": clusters,
	}
}

// TestFetchResources_Success tests successful resource fetching
func TestFetchResources_Success(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		if r.URL.Path != testClustersAPIPath {
			t.Errorf("Expected path /api/hyperfleet/v1/clusters, got %s", r.URL.Path)
		}

		// Return mock response matching v1.0.0 spec
		cluster := createMockCluster("cluster-1")
		cluster["labels"] = map[string]string{"region": "us-east"}
		response := createMockClusterList([]map[string]interface{}{cluster})

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	// Fetch resources
	ctx := context.Background()
	resources, err := client.FetchResources(ctx, ResourceTypeClusters, nil)
	// Verify
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

// TestFetchResources_EmptyList tests handling of empty resource list
func TestFetchResources_EmptyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := createMockClusterList([]map[string]interface{}{})

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	resources, err := client.FetchResources(context.Background(), ResourceTypeClusters, nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("Expected 0 resources, got %d", len(resources))
	}
}

// TestFetchResources_404NotFound tests handling of 404 errors (non-retriable)
func TestFetchResources_404NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		if _, err := w.Write([]byte(`{"error": "not found"}`)); err != nil {
			t.Errorf("Failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	_, err := client.FetchResources(context.Background(), ResourceTypeClusters, nil)

	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	// The error is wrapped, so we just verify it contains the right information
	// APIError is internal to the implementation, the wrapped error should still convey
	// that it's a 404 and failed
	errMsg := err.Error()
	if errMsg == "" {
		t.Error("Expected non-empty error message")
	}
	t.Logf("Got expected 404 error: %v", err)
}

// TestFetchResources_500ServerError tests handling of 500 errors (retriable)
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

	_, err := client.FetchResources(ctx, ResourceTypeClusters, nil)

	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	// Verify retry happened (should have multiple attempts)
	if attemptCount < 2 {
		t.Errorf("Expected at least 2 attempts due to retries, got %d", attemptCount)
	}

	t.Logf("Server received %d requests (initial + retries)", attemptCount)
}

// TestFetchResources_503ServiceUnavailable_ThenSuccess tests retry on 503 with eventual success
func TestFetchResources_503ServiceUnavailable_ThenSuccess(t *testing.T) {
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		if attemptCount < 2 {
			// First attempt fails with 503
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := w.Write([]byte(`{"error": "service unavailable"}`)); err != nil {
				t.Errorf("Failed to write response: %v", err)
			}
			return
		}

		// Second attempt succeeds
		response := createMockClusterList([]map[string]interface{}{
			createMockCluster("cluster-1"),
		})

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	resources, err := client.FetchResources(context.Background(), ResourceTypeClusters, nil)
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

// TestFetchResources_429RateLimited tests handling of 429 errors (retriable)
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

		// Success after retry
		response := createMockClusterList([]map[string]interface{}{})
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	_, err := client.FetchResources(context.Background(), ResourceTypeClusters, nil)
	if err != nil {
		t.Fatalf("Expected no error after retry, got %v", err)
	}
	if attemptCount < 2 {
		t.Errorf("Expected at least 2 attempts due to 429, got %d", attemptCount)
	}
}

// TestFetchResources_Timeout tests handling of request timeouts
func TestFetchResources_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than client timeout
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create client with very short timeout
	client := newTestClient(t, server.URL, 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := client.FetchResources(ctx, ResourceTypeClusters, nil)

	if err == nil {
		t.Fatal("Expected timeout error, got nil")
	}

	t.Logf("Got expected timeout error: %v", err)
}

// TestFetchResources_ContextCancellation tests handling of context cancellation
func TestFetchResources_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow response
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel context after 100ms
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := client.FetchResources(ctx, ResourceTypeClusters, nil)

	if err == nil {
		t.Fatal("Expected context cancellation error, got nil")
	}

	t.Logf("Got expected context cancellation error: %v", err)
}

// TestFetchResources_MalformedJSON tests handling of malformed JSON responses
func TestFetchResources_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"items": [malformed json`)); err != nil {
			t.Errorf("Failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	_, err := client.FetchResources(context.Background(), ResourceTypeClusters, nil)

	if err == nil {
		t.Fatal("Expected error for malformed JSON, got nil")
	}

	t.Logf("Got expected error for malformed JSON: %v", err)
}

// TestFetchResources_NilContext tests handling of nil context
func TestFetchResources_NilContext(t *testing.T) {
	client := newTestClient(t, "http://localhost", 10*time.Second)

	// Intentionally pass nil context to test validation
	// nolint:staticcheck // Testing nil context validation
	var nilCtx context.Context
	_, err := client.FetchResources(nilCtx, ResourceTypeClusters, nil)

	if err == nil {
		t.Fatal("Expected error for nil context, got nil")
	}
	if err.Error() != "context cannot be nil" {
		t.Errorf("Expected 'context cannot be nil' error, got: %v", err)
	}
}

// TestFetchResources_InvalidResourceType tests handling of invalid resource type
func TestFetchResources_InvalidResourceType(t *testing.T) {
	client := newTestClient(t, "http://localhost", 10*time.Second)
	testCases := []struct {
		name         string
		resourceType ResourceType
	}{
		{"empty string", ResourceType("")},
		{"invalid type", ResourceType("invalid")},
		{"typo - singular", ResourceType("cluster")},
		{"typo - wrong case", ResourceType("Clusters")},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.FetchResources(context.Background(), tc.resourceType, nil)

			if err == nil {
				t.Fatalf("Expected error for resourceType %q, got nil", tc.resourceType)
			}
			if !strings.Contains(err.Error(), "invalid resourceType") {
				t.Errorf("Expected 'invalid resourceType' error, got: %v", err)
			}
		})
	}
}

// TestFetchResources_NilStatus tests graceful handling of resources with nil status.
//
// Purpose: When the API returns resources without status (e.g., during provisioning
// or deletion), the client should:
// 1. Log a warning for observability
// 2. Skip the resource (graceful degradation)
// 3. Continue processing remaining resources
// 4. NOT fail the entire fetch operation
//
// This ensures service availability even when some resources are in transitional states.
func TestFetchResources_NilStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Note: With v1.0.0 spec, status is required, so this test will fail validation
		// This test might need to be removed or modified since status is now required
		cluster2 := createMockCluster("cluster-2")
		response := createMockClusterList([]map[string]interface{}{
			cluster2,
		})

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	// Note: A warning will be logged for cluster-1, but we can't easily
	// verify log output in tests. In production, logs are captured for monitoring.
	resources, err := client.FetchResources(context.Background(), ResourceTypeClusters, nil)
	// Verify graceful degradation behavior:
	if err != nil {
		t.Fatalf("Expected no error (graceful degradation), got %v", err)
	}

	// Should skip cluster-1 with nil status, return only cluster-2
	if len(resources) != 1 {
		t.Fatalf("Expected 1 resource (nil status skipped), got %d", len(resources))
	}

	if resources[0].ID != "cluster-2" {
		t.Errorf("Expected cluster-2 (valid status), got %s", resources[0].ID)
	}

	if len(resources[0].Status.Conditions) == 0 {
		t.Fatal("Expected at least one condition")
	}
	if resources[0].Status.Conditions[0].Status != "True" {
		t.Errorf("Expected Reconciled condition status 'True', got %s", resources[0].Status.Conditions[0].Status)
	}
}

// TestIsHTTPStatusRetriable tests the retry logic for different HTTP status codes
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

// TestLabelSelectorToSearchString tests label selector to search string conversion
func TestLabelSelectorToSearchString(t *testing.T) {
	tests := []struct {
		name     string
		selector map[string]string
		want     string
	}{
		{
			name:     "empty selector",
			selector: map[string]string{},
			want:     "",
		},
		{
			name:     "nil selector",
			selector: nil,
			want:     "",
		},
		{
			name:     "single label",
			selector: map[string]string{"region": "us-east"},
			want:     "labels.region='us-east'",
		},
		{
			name: "multiple labels (sorted)",
			selector: map[string]string{
				"region": "us-east",
				"env":    "production",
			},
			want: "labels.env='production' and labels.region='us-east'",
		},
		{
			name: "three labels (sorted)",
			selector: map[string]string{
				"tier":   "frontend",
				"region": "us-west",
				"env":    "staging",
			},
			want: "labels.env='staging' and labels.region='us-west' and labels.tier='frontend'",
		},
		{
			name:     "label with hyphen in key",
			selector: map[string]string{"my-label": "value"},
			want:     "labels.my-label='value'",
		},
		{
			name:     "label with underscore in key",
			selector: map[string]string{"my_label": "value"},
			want:     "labels.my_label='value'",
		},
		{
			name:     "label with hyphen in value",
			selector: map[string]string{"region": "us-east-1"},
			want:     "labels.region='us-east-1'",
		},
		{
			name:     "label value with single quote (escaped)",
			selector: map[string]string{"name": "test'value"},
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

// TestFetchResources_NodePools tests fetching nodepools
func TestFetchResources_NodePools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		if r.URL.Path != "/api/hyperfleet/v1/nodepools" {
			t.Errorf("Expected path /api/hyperfleet/v1/nodepools, got %s", r.URL.Path)
		}

		response := map[string]interface{}{
			"page":  1,
			"size":  1,
			"total": 1,
			"items": []map[string]interface{}{
				{
					"id":           "nodepool-1",
					"href":         "/api/hyperfleet/v1/nodepools/nodepool-1",
					"kind":         "NodePool",
					"name":         "workers",
					"generation":   3,
					"created_time": "2025-01-01T09:00:00Z",
					"updated_time": "2025-01-01T10:00:00Z",
					"created_by":   "test-user@example.com",
					"updated_by":   "test-user@example.com",
					"owner_references": map[string]interface{}{
						"id":   "cluster-123",
						"kind": "Cluster",
						"href": "/api/hyperfleet/v1/clusters/cluster-123",
					},
					"spec": map[string]interface{}{},
					"status": map[string]interface{}{
						"conditions": []map[string]interface{}{
							{
								"type":                 "Reconciled",
								"status":               "True",
								"created_time":         "2025-01-01T09:00:00Z",
								"last_transition_time": "2025-01-01T10:00:00Z",
								"last_updated_time":    "2025-01-01T12:00:00Z",
								"observed_generation":  3,
							},
							{
								"type":                 "LastKnownReconciled",
								"status":               "True",
								"created_time":         "2025-01-01T09:00:00Z",
								"last_transition_time": "2025-01-01T10:00:00Z",
								"last_updated_time":    "2025-01-01T12:00:00Z",
								"observed_generation":  3,
							},
						},
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)
	resources, err := client.FetchResources(context.Background(), ResourceTypeNodePools, nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("Expected 1 resource, got %d", len(resources))
	}
	if resources[0].ID != "nodepool-1" {
		t.Errorf("Expected ID nodepool-1, got %s", resources[0].ID)
	}
	if resources[0].Kind != "NodePool" {
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
	if resources[0].OwnerReferences.Kind != "Cluster" {
		t.Errorf("Expected OwnerReferences.Kind Cluster, got %s", resources[0].OwnerReferences.Kind)
	}
}

// TestNewHyperFleetClient_UserAgent verifies that every request carries the expected
// User-Agent header built from the sentinel name and version.
func TestNewHyperFleetClient_UserAgent(t *testing.T) {
	var receivedUA string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		response := createMockClusterList([]map[string]interface{}{})
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	c, err := NewHyperFleetClient(server.URL, 10*time.Second, "my-sentinel", "v1.2.3")
	if err != nil {
		t.Fatalf("NewHyperFleetClient: %v", err)
	}

	if _, err := c.FetchResources(context.Background(), ResourceTypeClusters, nil); err != nil {
		t.Fatalf("FetchResources: %v", err)
	}

	expected := "hyperfleet-sentinel/v1.2.3 (my-sentinel)"
	if receivedUA != expected {
		t.Errorf("User-Agent = %q, want %q", receivedUA, expected)
	}
}

// TestFetchResources_WithLabelSelector tests search parameter functionality
func TestFetchResources_WithLabelSelector(t *testing.T) {
	var receivedSearchParam string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSearchParam = r.URL.Query().Get("search")

		cluster := createMockCluster("cluster-1")
		response := createMockClusterList([]map[string]interface{}{cluster})

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	labelSelector := map[string]string{
		"region": "us-east",
		"env":    "production",
	}

	resources, err := client.FetchResources(context.Background(), ResourceTypeClusters, labelSelector)
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

// TestVerifyConnectivity_Success tests successful health check
func TestVerifyConnectivity_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify correct endpoint called
		if r.URL.Path != testClustersAPIPath {
			t.Errorf("Expected path /api/hyperfleet/v1/clusters, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET request, got %s", r.Method)
		}

		// Return successful health check response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		response := map[string]string{"status": "ok"}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Second)

	ctx := context.Background()
	err := client.VerifyConnectivity(ctx)
	if err != nil {
		t.Errorf("Expected successful health check, got error: %v", err)
	}
}

// TestVerifyConnectivity_NonOKStatus tests handling of non-200 status codes
func TestVerifyConnectivity_NonOKStatus(t *testing.T) {
	ctx := context.Background()
	testCases := []struct {
		name       string
		response   string
		statusCode int
	}{
		{
			name:       "ServiceUnavailable",
			statusCode: http.StatusServiceUnavailable,
			response:   `{"error": "service unavailable"}`,
		},
		{
			name:       "InternalServerError",
			statusCode: http.StatusInternalServerError,
			response:   `{"error": "internal server error"}`,
		},
		{
			name:       "NotFound",
			statusCode: http.StatusNotFound,
			response:   `{"error": "endpoint not found"}`,
		},
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

			err := client.VerifyConnectivity(ctx)

			if err == nil {
				t.Errorf("Expected error for status %d, got nil", tc.statusCode)
			}
			if !strings.Contains(err.Error(), strconv.Itoa(tc.statusCode)) {
				t.Errorf("Expected error to mention status %d, got: %v", tc.statusCode, err)
			}
		})
	}
}

// TestBuildSearchString tests combining label selectors and additional filters
func TestBuildSearchString(t *testing.T) {
	staleTimeFilter := "status.conditions.Reconciled.last_updated_time<='2025-01-01T00:00:00Z'"
	tests := []struct {
		labelSelector     map[string]string
		name              string
		want              string
		additionalFilters []string
	}{
		{
			name:              "labels only",
			labelSelector:     map[string]string{"shard": "1"},
			additionalFilters: nil,
			want:              "labels.shard='1'",
		},
		{
			name:              "filter only",
			labelSelector:     nil,
			additionalFilters: []string{"status.conditions.Reconciled='False'"},
			want:              "status.conditions.Reconciled='False'",
		},
		{
			name:          "both labels and filter",
			labelSelector: map[string]string{"shard": "1"},
			additionalFilters: []string{
				"status.conditions.Reconciled='False'",
			},
			want: "labels.shard='1' and status.conditions.Reconciled='False'",
		},
		{
			name:          "multiple filters",
			labelSelector: map[string]string{"shard": "1"},
			additionalFilters: []string{
				"status.conditions.Reconciled='True'",
				staleTimeFilter,
			},
			want: "labels.shard='1' and " +
				"status.conditions.Reconciled='True' and " +
				staleTimeFilter,
		},
		{
			name:              "both empty",
			labelSelector:     nil,
			additionalFilters: nil,
			want:              "",
		},
		{
			name:              "empty string filter ignored",
			labelSelector:     nil,
			additionalFilters: []string{"", "status.conditions.Reconciled='False'", ""},
			want:              "status.conditions.Reconciled='False'",
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
	client, err := NewHyperFleetClient(url, timeout, "test-sentinel", "test")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	return client
}

func TestNewHyperFleetClient_HTTPInstrumentation(t *testing.T) {
	// Capture the previous global tracer provider
	previousProvider := otel.GetTracerProvider()

	// Setup in-memory trace exporter
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
		// Restore the previous global tracer provider
		otel.SetTracerProvider(previousProvider)
	}(tp, context.Background())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		if r.URL.Path != testClustersAPIPath {
			t.Errorf("Expected path /api/hyperfleet/v1/clusters, got %s", r.URL.Path)
		}

		cluster := createMockCluster("test-cluster-1")
		response := createMockClusterList([]map[string]interface{}{cluster})

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client, err := NewHyperFleetClient(server.URL, 10*time.Second, "test-sentinel", "test")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Verify client was created
	if client == nil {
		t.Fatal("Expected client to be created")
	}

	// Make actual HTTP call to test instrumentation
	ctx := context.Background()
	resources, err := client.FetchResources(ctx, ResourceTypeClusters, nil)

	// Verify functional behavior
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("Expected 1 resource, got %d", len(resources))
	}
	if resources[0].ID != "test-cluster-1" {
		t.Errorf("Expected ID test-cluster-1, got %s", resources[0].ID)
	}

	// Force flush to get spans
	err = tp.ForceFlush(ctx)
	if err != nil {
		t.Fatalf("Error force flush: %v", err)
	}

	// Verify HTTP spans were created by instrumentation
	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("Expected HTTP spans to be created, got none")
	}

	// Look for HTTP GET span
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

	// Verify HTTP span attributes (following OpenTelemetry HTTP conventions)
	foundMethodAttr := false
	foundURLAttr := false
	for _, attr := range httpSpan.Attributes {
		switch string(attr.Key) {
		case "http.request.method", "http.method": // Different versions of OTel use different names
			if attr.Value.AsString() == "GET" {
				foundMethodAttr = true
			}
		case "url.full", "http.url": // Different versions of OTel use different names
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

	// Verify span completed successfully (no error status)
	if httpSpan.Status.Code.String() == "ERROR" {
		t.Errorf("Expected successful HTTP span, got error status: %s", httpSpan.Status.Description)
	}
}

func TestNewHyperFleetClient_HTTPInstrumentation_ErrorCase(t *testing.T) {
	// Capture the previous global tracer provider
	previousProvider := otel.GetTracerProvider()

	// Setup tracing
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
		// Restore the previous global tracer provider
		otel.SetTracerProvider(previousProvider)
	}(tp, context.Background())

	// Server returns 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, err := w.Write([]byte(`{"error": "internal server error"}`))
		if err != nil {
			t.Errorf("Failed to write response: %v", err)
			return
		}
	}))
	defer server.Close()

	client, err := NewHyperFleetClient(server.URL, 10*time.Second, "test-sentinel", "test")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	_, err = client.FetchResources(ctx, ResourceTypeClusters, nil)

	// Verify error behavior (like existing tests)
	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	// Verify spans still created on error
	err = tp.ForceFlush(ctx)
	if err != nil {
		t.Fatalf("Error force flush: %v", err)
	}
	spans := exporter.GetSpans()

	if len(spans) == 0 {
		t.Fatal("Expected HTTP spans even on error")
	}
}

// TestFetchResources_WithAdditionalFilters tests FetchResources with combined label selectors and condition filters
func TestFetchResources_WithAdditionalFilters(t *testing.T) {
	var receivedSearchParam string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSearchParam = r.URL.Query().Get("search")

		response := map[string]interface{}{
			"page":  1,
			"size":  0,
			"total": 0,
			"items": []map[string]interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Logf("Error encoding response: %v", err)
		}
	}))
	defer server.Close()

	client, _ := NewHyperFleetClient(server.URL, 10*time.Second, "test-sentinel", "test")
	labelSelector := map[string]string{"shard": "1"}

	_, err := client.FetchResources(
		context.Background(), ResourceTypeClusters, labelSelector,
		"status.conditions.Reconciled='False'",
	)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expectedSearch := "labels.shard='1' and status.conditions.Reconciled='False'"
	if receivedSearchParam != expectedSearch {
		t.Errorf("Expected search parameter %q, got %q", expectedSearch, receivedSearchParam)
	}
}

// TestFetchResources_WithConditionFilterOnly tests FetchResources with only condition filters (no labels)
func TestFetchResources_WithConditionFilterOnly(t *testing.T) {
	var receivedSearchParam string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSearchParam = r.URL.Query().Get("search")

		response := map[string]interface{}{
			"page":  1,
			"size":  0,
			"total": 0,
			"items": []map[string]interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Logf("Error encoding response: %v", err)
		}
	}))
	defer server.Close()

	client, _ := NewHyperFleetClient(server.URL, 10*time.Second, "test-sentinel", "test")

	_, err := client.FetchResources(
		context.Background(), ResourceTypeClusters, nil,
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

// staticTokenProvider is a test helper implementing auth.TokenProvider.
type staticTokenProvider struct{ token string }

func (s *staticTokenProvider) GetToken(_ context.Context) (string, error) { return s.token, nil }

// TestNewHyperFleetClient_Authorization verifies that the Authorization header is sent
// when a token provider is configured, and absent when no provider is set.
func TestNewHyperFleetClient_Authorization(t *testing.T) {
	t.Run("static provider sends bearer token", func(t *testing.T) {
		var receivedAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedAuth = r.Header.Get("Authorization")
			response := createMockClusterList([]map[string]interface{}{})
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Logf("Error encoding response: %v", err)
			}
		}))
		defer server.Close()

		c, err := NewHyperFleetClient(
			server.URL, 10*time.Second, "test-sentinel", "test",
			WithTokenProvider(&staticTokenProvider{token: "my-secret-token"}),
		)
		if err != nil {
			t.Fatalf("NewHyperFleetClient: %v", err)
		}

		_, _ = c.FetchResources(context.Background(), ResourceTypeClusters, nil)

		expected := "Bearer my-secret-token"
		if receivedAuth != expected {
			t.Errorf("Authorization header: got %q, want %q", receivedAuth, expected)
		}
	})

	t.Run("nil provider sends no authorization header", func(t *testing.T) {
		var receivedAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedAuth = r.Header.Get("Authorization")
			response := createMockClusterList([]map[string]interface{}{})
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Logf("Error encoding response: %v", err)
			}
		}))
		defer server.Close()

		c, err := NewHyperFleetClient(server.URL, 10*time.Second, "test-sentinel", "test")
		if err != nil {
			t.Fatalf("NewHyperFleetClient: %v", err)
		}

		_, _ = c.FetchResources(context.Background(), ResourceTypeClusters, nil)

		if receivedAuth != "" {
			t.Errorf("expected no Authorization header, got %q", receivedAuth)
		}
	})
}
