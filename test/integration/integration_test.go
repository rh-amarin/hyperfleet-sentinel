//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/client"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/config"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/engine"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/metrics"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/sentinel"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/pkg/logger"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/pkg/telemetry"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestMain provides centralized setup/teardown for integration tests
func TestMain(m *testing.M) {
	log := logger.NewHyperFleetLogger()
	ctx := context.Background()
	log.Infof(ctx, "Starting integration test using go version %s", runtime.Version())

	// Initialize shared test helper (creates RabbitMQ container once)
	helper := NewHelper()

	// Run all tests
	exitCode := m.Run()

	// Cleanup shared resources
	helper.Teardown()

	log.Infof(ctx, "Integration tests completed with exit code %d", exitCode)
	os.Exit(exitCode)
}

// createMockCluster creates a mock cluster response
func createMockCluster(id string, generation int, observedGeneration int, reconciled bool, lastUpdated time.Time) map[string]interface{} {
	return createMockClusterWithLabels(id, generation, observedGeneration, reconciled, lastUpdated, nil)
}

// createMockClusterWithLabels creates a mock cluster response with labels
func createMockClusterWithLabels(id string, generation int, observedGeneration int, reconciled bool, lastUpdated time.Time, labels map[string]string) map[string]interface{} {
	reconciledStatus := "False"
	if reconciled {
		reconciledStatus = "True"
	}

	reconciledCondition := map[string]interface{}{
		"type":                 "Reconciled",
		"status":               reconciledStatus,
		"created_time":         "2025-01-01T09:00:00Z",
		"last_transition_time": "2025-01-01T10:00:00Z",
		"last_updated_time":    lastUpdated.Format(time.RFC3339),
		"observed_generation":  observedGeneration,
	}

	cluster := map[string]interface{}{
		"id":         id,
		"href":       "/api/hyperfleet/v1/clusters/" + id,
		"kind":       "Cluster",
		"name":       id,
		"generation": generation,
		"created_at": "2025-01-01T09:00:00Z",
		"updated_at": "2025-01-01T10:00:00Z",
		"created_by": "test-user@example.com",
		"updated_by": "test-user@example.com",
		"spec":       map[string]interface{}{},
		"status": map[string]interface{}{
			"conditions": []map[string]interface{}{
				reconciledCondition,
				{
					"type":                 "LastKnownReconciled",
					"status":               reconciledStatus,
					"created_time":         "2025-01-01T09:00:00Z",
					"last_transition_time": "2025-01-01T10:00:00Z",
					"last_updated_time":    lastUpdated.Format(time.RFC3339),
					"observed_generation":  observedGeneration,
				},
			},
		},
	}

	if len(labels) > 0 {
		cluster["labels"] = labels
	}

	return cluster
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

// newTestDecisionEngine creates a CEL-based decision engine with default config.
func newTestDecisionEngine(t *testing.T) *engine.DecisionEngine {
	t.Helper()
	de, err := engine.NewDecisionEngine(config.DefaultMessageDecision())
	if err != nil {
		t.Fatalf("NewDecisionEngine failed: %v", err)
	}
	return de
}

// newTestSentinelConfig creates a config for testing.
func newTestSentinelConfig() *config.SentinelConfig {
	return &config.SentinelConfig{
		ResourceType:    "clusters",
		PollInterval:    100 * time.Millisecond,
		MessageDecision: config.DefaultMessageDecision(),
		Clients: config.ClientsConfig{
			HyperFleetAPI: &config.HyperFleetAPIConfig{},
			Broker:        &config.BrokerConfig{},
		},
		MessageData: map[string]interface{}{
			"id":   "resource.id",
			"kind": "resource.kind",
		},
	}
}

// TestIntegration_EndToEnd tests the full Sentinel workflow end-to-end with real RabbitMQ
func TestIntegration_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	helper := NewHelper()

	now := time.Now()
	var pollCycleCount atomic.Int32

	// Single query mock - returns stale cluster on first poll only
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cycle := pollCycleCount.Add(1)

		var clusters []map[string]interface{}
		if cycle == 1 {
			clusters = []map[string]interface{}{
				createMockCluster("cluster-1", 2, 2, true, now.Add(-31*time.Minute)),
			}
		}

		response := createMockClusterList(clusters)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	hyperfleetClient, err := client.NewHyperFleetClient(
		server.URL, 10*time.Second, "test-sentinel", "test", client.DefaultPageSize, "", 0)
	if err != nil {
		t.Fatalf("failed to create HyperFleet client: %v", err)
	}
	decisionEngine := newTestDecisionEngine(t)
	log := logger.NewHyperFleetLogger()

	registry := prometheus.NewRegistry()
	metrics.NewSentinelMetrics(registry, "test")

	cfg := newTestSentinelConfig()

	s, err := sentinel.NewSentinel(cfg, hyperfleetClient, decisionEngine, helper.RabbitMQ.Publisher(), log)
	if err != nil {
		t.Fatalf("NewSentinel failed: %v", err)
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- s.Start(ctx)
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	if pollCycleCount.Load() < 2 {
		t.Errorf("Expected at least 2 polling cycles, got %d", pollCycleCount.Load())
	}

	select {
	case err := <-errChan:
		if err != nil && err != context.Canceled {
			t.Fatalf("Sentinel failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Sentinel did not stop within timeout")
	}

	t.Log("Integration test with real RabbitMQ broker completed successfully")
}

// TestIntegration_LabelSelectorFiltering tests resource filtering with label selectors and real RabbitMQ
func TestIntegration_LabelSelectorFiltering(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	helper := NewHelper()

	now := time.Now()

	var capturedSearchParams []string
	var captureMu sync.Mutex

	// Single query mock - validates label selectors in search params
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searchParam := r.URL.Query().Get("search")
		captureMu.Lock()
		capturedSearchParams = append(capturedSearchParams, searchParam)
		captureMu.Unlock()

		var filteredClusters []map[string]interface{}
		if strings.Contains(searchParam, "labels.shard='1'") {
			filteredClusters = []map[string]interface{}{
				createMockClusterWithLabels(
					"cluster-shard-1",
					2, 2, true,
					now.Add(-31*time.Minute),
					map[string]string{"shard": "1"},
				),
			}
		}

		response := createMockClusterList(filteredClusters)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	hyperfleetClient, err := client.NewHyperFleetClient(
		server.URL, 10*time.Second, "test-sentinel", "test", client.DefaultPageSize, "", 0)
	if err != nil {
		t.Fatalf("failed to create HyperFleet client: %v", err)
	}
	decisionEngine := newTestDecisionEngine(t)
	log := logger.NewHyperFleetLogger()

	registry := prometheus.NewRegistry()
	metrics.NewSentinelMetrics(registry, "test")

	cfg := newTestSentinelConfig()
	cfg.ResourceSelector = []config.LabelSelector{
		{Label: "shard", Value: "1"},
	}

	s, err := sentinel.NewSentinel(cfg, hyperfleetClient, decisionEngine, helper.RabbitMQ.Publisher(), log)
	if err != nil {
		t.Fatalf("NewSentinel failed: %v", err)
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- s.Start(ctx)
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	startErr := <-errChan
	if startErr != nil && startErr != context.Canceled {
		t.Errorf("Expected Start to return nil or context.Canceled, got: %v", startErr)
	}

	captureMu.Lock()
	params := make([]string, len(capturedSearchParams))
	copy(params, capturedSearchParams)
	captureMu.Unlock()

	if len(params) == 0 {
		t.Fatal("No search queries were captured — Sentinel may not have polled")
	}

	for _, search := range params {
		if !strings.Contains(search, "labels.shard='1'") {
			t.Errorf("Expected search to contain label selector \"labels.shard='1'\", got %q", search)
		}
	}

	t.Logf("Label selector filtering test completed with %d queries", len(params))
}

// TestIntegration_TSLSyntaxMultipleLabels validates TSL syntax with multiple label selectors
func TestIntegration_TSLSyntaxMultipleLabels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	helper := NewHelper()

	now := time.Now()

	var receivedSearchParams []string
	var searchMu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		search := r.URL.Query().Get("search")
		searchMu.Lock()
		receivedSearchParams = append(receivedSearchParams, search)
		searchMu.Unlock()

		var filteredClusters []map[string]interface{}

		labelPrefix := "labels.env='production' and labels.region='us-east'"
		if strings.HasPrefix(search, labelPrefix) {
			filteredClusters = []map[string]interface{}{
				createMockClusterWithLabels(
					"cluster-region-env-match",
					2, 2, true,
					now.Add(-31*time.Minute),
					map[string]string{"region": "us-east", "env": "production"},
				),
			}
		}

		response := createMockClusterList(filteredClusters)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	hyperfleetClient, err := client.NewHyperFleetClient(
		server.URL, 10*time.Second, "test-sentinel", "test", client.DefaultPageSize, "", 0)
	if err != nil {
		t.Fatalf("failed to create HyperFleet client: %v", err)
	}
	decisionEngine := newTestDecisionEngine(t)
	log := logger.NewHyperFleetLogger()

	registry := prometheus.NewRegistry()
	metrics.NewSentinelMetrics(registry, "test")

	cfg := newTestSentinelConfig()
	cfg.ResourceSelector = []config.LabelSelector{
		{Label: "region", Value: "us-east"},
		{Label: "env", Value: "production"},
	}

	s, err := sentinel.NewSentinel(cfg, hyperfleetClient, decisionEngine, helper.RabbitMQ.Publisher(), log)
	if err != nil {
		t.Fatalf("NewSentinel failed: %v", err)
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- s.Start(ctx)
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	labelPrefix := "labels.env='production' and labels.region='us-east'"
	searchMu.Lock()
	paramsCopy := make([]string, len(receivedSearchParams))
	copy(paramsCopy, receivedSearchParams)
	searchMu.Unlock()

	if len(paramsCopy) == 0 {
		t.Fatal("No search queries were captured — Sentinel may not have polled")
	}

	for _, search := range paramsCopy {
		if !strings.HasPrefix(search, labelPrefix) {
			t.Errorf("Expected search to start with label selectors %q, got %q", labelPrefix, search)
		}
	}

	startErr := <-errChan
	if startErr != nil && startErr != context.Canceled {
		t.Errorf("Expected Start to return nil or context.Canceled, got: %v", startErr)
	}

	t.Logf("TSL syntax validation completed with %d queries", len(paramsCopy))
}

// TestIntegration_BrokerLoggerContext validates context propagation through logging
func TestIntegration_BrokerLoggerContext(t *testing.T) {
	const sentinelComponent = "sentinel"
	const testVersion = "test"
	const testHost = "testhost"
	const testTopic = "test-topic"

	var logBuffer bytes.Buffer
	now := time.Now()
	var callCount atomic.Int32
	readyChan := make(chan bool, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := os.Setenv("OTEL_TRACES_SAMPLER", "always_on")
	if err != nil {
		t.Errorf("Failed to set OTEL_TRACES_SAMPLER: %v", err)
	}
	defer func() {
		err := os.Unsetenv("OTEL_TRACES_SAMPLER")
		if err != nil {
			t.Errorf("Failed to unset OTEL_TRACES_SAMPLER: %v", err)
		}
	}()

	tp, err := telemetry.InitTraceProvider(ctx, "sentinel", "test")
	if err != nil {
		t.Fatalf("Failed to initialize OpenTelemetry: %v", err)
	}
	defer telemetry.Shutdown(context.Background(), tp)

	globalConfig := logger.GetGlobalConfig()
	multiWriter := io.MultiWriter(globalConfig.Output, &logBuffer)

	helper := NewHelper()
	logCfg := &logger.LogConfig{
		Level:     logger.LevelInfo,
		Format:    logger.FormatJSON,
		Output:    multiWriter,
		Component: sentinelComponent,
		Version:   testVersion,
		Hostname:  testHost,
		OTel: logger.OTelConfig{
			Enabled: true,
		},
	}
	log := logger.NewHyperFleetLoggerWithConfig(logCfg)

	// Single query mock - returns resources that trigger events
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if callCount.Add(1) >= 2 {
			select {
			case readyChan <- true:
			default:
			}
		}

		clusters := []map[string]interface{}{
			createMockCluster("cluster-not-reconciled", 2, 2, false, now.Add(-15*time.Second)),
			createMockCluster("cluster-old", 2, 2, true, now.Add(-35*time.Minute)),
		}
		response := createMockClusterList(clusters)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	hyperfleetClient, err := client.NewHyperFleetClient(
		server.URL, 10*time.Second, "test-sentinel", "test", client.DefaultPageSize, "", 0)
	if err != nil {
		t.Fatalf("failed to create HyperFleet client: %v", err)
	}
	decisionEngine := newTestDecisionEngine(t)

	sentinelConfig := newTestSentinelConfig()
	sentinelConfig.Clients.Broker.Topic = testTopic
	sentinelConfig.ResourceSelector = []config.LabelSelector{
		{Label: "region", Value: "us-east"},
		{Label: "env", Value: "production"},
	}

	s, err := sentinel.NewSentinel(sentinelConfig, hyperfleetClient, decisionEngine, helper.RabbitMQ.Publisher(), log)
	if err != nil {
		t.Fatalf("NewSentinel failed: %v", err)
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- s.Start(ctx)
	}()

	select {
	case <-readyChan:
		t.Log("Sentinel completed required polling cycles")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for sentinel polling")
	}
	cancel()

	select {
	case err := <-errChan:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Sentinel failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Sentinel did not stop within timeout")
	}

	logOutput := logBuffer.String()
	t.Logf("Captured log output:\n%s", logOutput)

	logLines := strings.Split(strings.TrimSpace(logOutput), "\n")

	var foundSentinelEventLog bool
	var foundBrokerOperationLog bool

	for _, line := range logLines {
		if line == "" {
			continue
		}

		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Logf("Skipping non-JSON line: %s", line)
			continue
		}

		msg, hasMsg := entry["message"].(string)
		component, hasComponent := entry["component"].(string)

		if hasMsg && hasComponent && component == sentinelComponent &&
			strings.Contains(msg, "Published event") {
			foundSentinelEventLog = true

			if entry["decision_reason"] == nil {
				t.Errorf("Sentinel event log missing decision_reason: %v", entry)
			}
			if entry["topic"] == nil {
				t.Errorf("Sentinel event log missing topic: %v", entry)
			}
			if entry["subset"] == nil {
				t.Errorf("Sentinel event log missing subset: %v", entry)
			}
			if entry["trace_id"] == nil {
				t.Errorf("Sentinel event log missing trace_id: %v", entry)
			}
			if entry["span_id"] == nil {
				t.Errorf("Sentinel event log missing span_id: %v", entry)
			}

			t.Logf("Found Sentinel event log with context: decision_reason=%v topic=%v subset=%v",
				entry["decision_reason"], entry["topic"], entry["subset"])
		}

		if hasMsg && hasComponent && component == sentinelComponent &&
			(strings.Contains(msg, "broker") || strings.Contains(msg, "publish") ||
				strings.Contains(msg, "Creating publisher") || strings.Contains(msg, "publisher initialized")) {
			foundBrokerOperationLog = true

			if entry["component"] != sentinelComponent {
				t.Errorf("Broker operation log missing component=%s: %v", sentinelComponent, entry)
			}
			if entry["version"] != testVersion {
				t.Errorf("Broker operation log missing version=%s: %v", testVersion, entry)
			}
			if entry["hostname"] != testHost {
				t.Errorf("Broker operation log missing hostname=%s: %v", testHost, entry)
			}

			t.Logf("Found broker operation log with component=sentinel: %s", msg)
		}
	}

	if !foundSentinelEventLog {
		t.Error("No Sentinel event publishing logs found - events may not have been published")
	}

	if !foundBrokerOperationLog {
		t.Error("No broker operation logs found - broker may not be logging")
	}

	if foundSentinelEventLog && foundBrokerOperationLog {
		t.Log("SUCCESS: Logger context inheritance verified - both Sentinel and broker operations log with component=sentinel")
	}
}

// TestIntegration_EndToEndSpanHierarchy validates the complete OpenTelemetry span hierarchy
func TestIntegration_EndToEndSpanHierarchy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSampler(trace.AlwaysSample()),
		trace.WithBatcher(exporter),
	)
	prevTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(prevTP)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := tp.Shutdown(cleanupCtx); err != nil {
			t.Logf("Warning: shutdown of tracer provider failed: %v", err)
		}
	}()

	now := time.Now()
	var callCount atomic.Int32
	readyChan := make(chan bool, 1)

	// Single query mock - returns resources that trigger different decision paths
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if callCount.Add(1) > 2 {
			select {
			case readyChan <- true:
			default:
			}
		}

		clusters := []map[string]interface{}{
			// Triggers not_reconciled_and_debounced
			createMockCluster("cluster-not-reconciled-old", 2, 2, false, now.Add(-15*time.Second)),
			// Triggers reconciled_and_stale
			createMockCluster("cluster-reconciled-old", 2, 2, true, now.Add(-35*time.Minute)),
		}
		response := createMockClusterList(clusters)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	helper := NewHelper()

	hyperfleetClient, clientErr := client.NewHyperFleetClient(
		server.URL, 10*time.Second, "test-sentinel", "test", client.DefaultPageSize, "", 0)
	if clientErr != nil {
		t.Fatalf("failed to create HyperFleet client: %v", clientErr)
	}
	decisionEngine := newTestDecisionEngine(t)
	log := logger.NewHyperFleetLogger()

	registry := prometheus.NewRegistry()
	metrics.NewSentinelMetrics(registry, "test")

	cfg := newTestSentinelConfig()
	cfg.Clients.Broker.Topic = "test-spans-topic"

	pub := helper.RabbitMQ.Publisher()
	s, err := sentinel.NewSentinel(cfg, hyperfleetClient, decisionEngine, pub, log)
	if err != nil {
		t.Fatalf("NewSentinel failed: %v", err)
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- s.Start(ctx)
	}()

	select {
	case <-readyChan:
		t.Log("Sentinel completed required polling cycles")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for sentinel polling")
	}
	cancel()

	select {
	case err := <-errChan:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Sentinel failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Sentinel did not stop within timeout")
	}

	flushCtx, flushCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer flushCancel()

	err = tp.ForceFlush(flushCtx)
	if err != nil {
		t.Fatalf("force flush error: %v", err)
	}

	spans := exporter.GetSpans()
	t.Logf("Captured %d spans", len(spans))

	spansByName := make(map[string][]tracetest.SpanStub)
	spansByID := make(map[string]tracetest.SpanStub)
	spansByParentID := make(map[string][]tracetest.SpanStub)

	for _, span := range spans {
		spansByName[span.Name] = append(spansByName[span.Name], span)
		spansByID[span.SpanContext.SpanID().String()] = span

		if span.Parent.IsValid() {
			parentID := span.Parent.SpanID().String()
			spansByParentID[parentID] = append(spansByParentID[parentID], span)
		}
	}

	t.Log("Span hierarchy:")
	for _, span := range spans {
		parentInfo := "ROOT"
		if span.Parent.IsValid() {
			parentInfo = "child of " + span.Parent.SpanID().String()
		}
		t.Logf("  %s (%s) - %s", span.Name, span.SpanContext.SpanID().String()[:8], parentInfo)
	}

	requiredSpans := []string{
		"sentinel.poll",
		"sentinel.evaluate",
		"test-spans-topic publish",
	}

	for _, requiredSpan := range requiredSpans {
		if _, exists := spansByName[requiredSpan]; !exists {
			t.Errorf("Required span '%s' not found. Available spans: %v", requiredSpan, getSpanNames(spans))
		}
	}

	pollSpans := spansByName["sentinel.poll"]
	if len(pollSpans) == 0 {
		t.Fatal("No sentinel.poll spans found")
	}

	pollSpansToValidate := pollSpans
	if len(pollSpansToValidate) > 2 {
		pollSpansToValidate = pollSpansToValidate[:2]
	}

	for _, pollSpan := range pollSpansToValidate {
		pollSpanID := pollSpan.SpanContext.SpanID().String()
		directChildren := spansByParentID[pollSpanID]

		t.Logf("Poll span %s has %d direct children", pollSpanID[:8], len(directChildren))

		hasEvaluateChild := false
		evaluateChildCount := 0

		for _, child := range directChildren {
			if child.Name == "sentinel.evaluate" {
				hasEvaluateChild = true
				evaluateChildCount++
			}
		}

		if !hasEvaluateChild {
			t.Errorf("Poll span %s missing sentinel.evaluate child", pollSpanID[:8])
			continue
		}

		publishGrandchildCount := 0
		for _, child := range directChildren {
			if child.Name == "sentinel.evaluate" {
				childID := child.SpanContext.SpanID().String()
				grandchildren := spansByParentID[childID]
				for _, grandchild := range grandchildren {
					if strings.HasSuffix(grandchild.Name, " publish") {
						publishGrandchildCount++
					}
				}
			}
		}

		t.Logf("Poll span %s: evaluate children=%d, publish grandchildren=%d",
			pollSpanID[:8], evaluateChildCount, publishGrandchildCount)

		if evaluateChildCount > 0 && publishGrandchildCount == 0 {
			t.Errorf("Poll span %s has %d evaluate children but no publish grandchildren",
				pollSpanID[:8], evaluateChildCount)
		}
	}

	evaluateSpans := spansByName["sentinel.evaluate"]
	if len(evaluateSpans) < 2 {
		t.Errorf("Expected at least 2 sentinel.evaluate spans (one per test resource), got %d", len(evaluateSpans))
	}

	publishSpans := spansByName["test-spans-topic publish"]
	if len(publishSpans) < 2 {
		t.Errorf("Expected at least 2 publish spans (one per test event), got %d", len(publishSpans))
	}

	validateSpanAttribute(t, publishSpans, "test-spans-topic publish", "messaging.system", pub.BrokerType())
	validateSpanAttribute(t, publishSpans, "test-spans-topic publish", "messaging.operation.type", "publish")
	validateSpanAttribute(t, publishSpans, "test-spans-topic publish", "messaging.destination.name", cfg.Clients.Broker.Topic)

	for _, publishSpan := range publishSpans {
		if !publishSpan.Parent.IsValid() {
			t.Errorf("Publish span %s should have a parent", publishSpan.SpanContext.SpanID().String()[:8])
			continue
		}

		parentSpan, exists := spansByID[publishSpan.Parent.SpanID().String()]
		if !exists {
			t.Errorf("Publish span parent not found")
			continue
		}

		if parentSpan.Name != "sentinel.evaluate" {
			t.Errorf("Publish span parent should be sentinel.evaluate, got %s", parentSpan.Name)
		}
	}

	traceIDs := make(map[string]bool)
	for _, span := range spans {
		traceIDs[span.SpanContext.TraceID().String()] = true
	}

	if len(traceIDs) > len(pollSpans) {
		t.Errorf("Expected spans to belong to %d traces (one per poll), but found %d trace IDs", len(pollSpans), len(traceIDs))
	}
}

func getSpanNames(spans []tracetest.SpanStub) []string {
	names := make([]string, len(spans))
	for i, span := range spans {
		names[i] = span.Name
	}
	return names
}

func validateSpanAttribute(t *testing.T, spans []tracetest.SpanStub, spanName, attrKey, expectedValue string) {
	for _, span := range spans {
		if span.Name == spanName {
			for _, attr := range span.Attributes {
				if string(attr.Key) == attrKey {
					if attr.Value.AsString() != expectedValue {
						t.Errorf("Span '%s': expected %s=%s, got %s", spanName, attrKey, expectedValue, attr.Value.AsString())
					}
					return
				}
			}
			t.Errorf("Span '%s': attribute '%s' not found", spanName, attrKey)
			return
		}
	}
	t.Errorf("Span '%s' not found", spanName)
}
