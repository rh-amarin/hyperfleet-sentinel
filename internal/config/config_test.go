package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testResourceType = "clusters"
	testAPIEndpoint  = "http://api.example.com"
)

// Helper function to create a temporary config file for testing
func createTempConfigFile(t *testing.T, content string) string {
	t.Helper()
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	err := os.WriteFile(configPath, []byte(content), 0o600)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}
	return configPath
}

// newTestMessageDecision returns a valid message decision config for testing
func newTestMessageDecision() *MessageDecisionConfig {
	return &MessageDecisionConfig{
		Params: []Param{
			{Name: "is_reconciled", Expr: `condition("Reconciled").status == "True"`},
		},
		Result: "!is_reconciled",
	}
}

// ============================================================================
// Loading & Parsing Tests
// ============================================================================

func TestLoadConfig_ValidComplete(t *testing.T) {
	configPath := filepath.Join("testdata", "valid-complete.yaml")

	cfg, err := LoadConfig(configPath, nil)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify resource type
	if cfg.ResourceType != testResourceType {
		t.Errorf("Expected resource_type '%s', got '%s'", testResourceType, cfg.ResourceType)
	}

	// Verify durations
	if cfg.PollInterval != 5*time.Second {
		t.Errorf("Expected poll_interval 5s, got %v", cfg.PollInterval)
	}

	// Verify resource selector
	if len(cfg.ResourceSelector) != 2 {
		t.Errorf("Expected 2 resource selectors, got %d", len(cfg.ResourceSelector))
	}

	// Verify HyperFleet API config
	if cfg.Clients.HyperFleetAPI.BaseURL != "https://api.hyperfleet.example.com" {
		t.Errorf("Expected base_url 'https://api.hyperfleet.example.com', got '%s'", cfg.Clients.HyperFleetAPI.BaseURL)
	}
	if cfg.Clients.HyperFleetAPI.Timeout != 10*time.Second {
		t.Errorf("Expected timeout 10s, got %v", cfg.Clients.HyperFleetAPI.Timeout)
	}

	// Verify message_decision
	if cfg.MessageDecision == nil {
		t.Fatal("Expected message_decision to be set")
	}
	if cfg.MessageDecision.Result == "" {
		t.Error("Expected message_decision.result to be set")
	}
	if len(cfg.MessageDecision.Params) != 7 {
		t.Errorf("Expected 7 message_decision params, got %d", len(cfg.MessageDecision.Params))
	}

	// Verify message data
	if len(cfg.MessageData) != 3 {
		t.Errorf("Expected 3 message_data fields, got %d", len(cfg.MessageData))
	}
	resourceID, ok := cfg.MessageData["id"].(string)
	if !ok {
		t.Fatalf("Expected message_data.id to be a string, got %T", cfg.MessageData["id"])
	}
	if resourceID != "resource.id" {
		t.Errorf("Expected message_data.id 'resource.id', got '%v'", resourceID)
	}
}

func TestLoadConfig_Minimal(t *testing.T) {
	configPath := filepath.Join("testdata", "minimal.yaml")

	cfg, err := LoadConfig(configPath, nil)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify defaults were applied
	if cfg.PollInterval != 5*time.Second {
		t.Errorf("Expected default poll_interval 5s, got %v", cfg.PollInterval)
	}

	// Verify default message_decision was applied
	if cfg.MessageDecision == nil {
		t.Fatal("Expected default message_decision to be applied")
	}
	if cfg.MessageDecision.Result == "" {
		t.Error("Expected default message_decision.result to be set")
	}
	if len(cfg.MessageDecision.Params) != 7 {
		t.Errorf("Expected 7 default message_decision params, got %d", len(cfg.MessageDecision.Params))
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.yaml", nil)
	if err == nil {
		t.Fatal("Expected error for nonexistent file, got nil")
	}
}

func TestLoadConfig_EmptyPath_FallsBackToDefault(t *testing.T) {
	t.Setenv("HYPERFLEET_CONFIG", "")
	_, err := LoadConfig("", nil)
	if err == nil {
		t.Fatal("Expected error for missing default config file, got nil")
	}
	if !strings.Contains(err.Error(), "/etc/hyperfleet/config.yaml") {
		t.Errorf("Expected error to mention default path /etc/hyperfleet/config.yaml, got: %v", err)
	}
}

func TestLoadConfig_HyperfleetConfigEnvVar(t *testing.T) {
	yaml := `
sentinel:
  name: env-var-sentinel
resource_type: clusters
message_data:
  id: "resource.id"
  kind: "resource.kind"
poll_interval: 5s
clients:
  hyperfleet_api:
    base_url: https://example.com
    timeout: 10s
`
	configPath := createTempConfigFile(t, yaml)
	t.Setenv("HYPERFLEET_CONFIG", configPath)

	cfg, err := LoadConfig("", nil)
	if err != nil {
		t.Fatalf("Expected config to load from HYPERFLEET_CONFIG env var, got error: %v", err)
	}
	if cfg.Sentinel.Name != "env-var-sentinel" {
		t.Errorf("Expected sentinel name 'env-var-sentinel', got: %s", cfg.Sentinel.Name)
	}
}

func TestLoadConfig_TracingEnabledEnvVar(t *testing.T) {
	// Override the tracing_enabled value that is set in the config file
	t.Setenv("HYPERFLEET_TRACING_ENABLED", "true")
	yaml := `
sentinel:
  name: hyperfleet-sentinel-tracing-enabled
clients:
  hyperfleet_api:
    base_url: http://api.example.com
resource_type: clusters
message_data:
  id: "resource.id"
tracing_enabled: false
`
	configPath := createTempConfigFile(t, yaml)
	cfg, err := LoadConfig(configPath, nil)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if cfg.TracingEnabled != true {
		t.Errorf("Expected tracing_enabled to be true, got %v", cfg.TracingEnabled)
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	yaml := `
resource_type: clusters
invalid yaml here: [
  - broken
`
	configPath := createTempConfigFile(t, yaml)

	_, err := LoadConfig(configPath, nil)
	if err == nil {
		t.Fatal("Expected error for invalid YAML, got nil")
	}
}

// ============================================================================
// Default Values Tests
// ============================================================================

func TestNewSentinelConfig_Defaults(t *testing.T) {
	cfg := NewSentinelConfig()

	// ResourceType has no default - must be set in config file
	if cfg.ResourceType != "" {
		t.Errorf("Expected no default resource_type (empty string), got '%s'", cfg.ResourceType)
	}
	if cfg.PollInterval != 5*time.Second {
		t.Errorf("Expected default poll_interval 5s, got %v", cfg.PollInterval)
	}
	if cfg.Clients.HyperFleetAPI.Timeout != 10*time.Second {
		t.Errorf("Expected default timeout 10s, got %v", cfg.Clients.HyperFleetAPI.Timeout)
	}
	// BaseURL has no default - must be set in config file
	if cfg.Clients.HyperFleetAPI.BaseURL != "" {
		t.Errorf("Expected no default base_url (empty string), got '%s'", cfg.Clients.HyperFleetAPI.BaseURL)
	}
	if len(cfg.ResourceSelector) != 0 {
		t.Errorf("Expected empty resource_selector, got %d items", len(cfg.ResourceSelector))
	}
	if cfg.MessageData != nil {
		t.Errorf("Expected nil message_data, got %v", cfg.MessageData)
	}
	// MessageDecision has no default in NewSentinelConfig - LoadConfig applies it
	if cfg.MessageDecision != nil {
		t.Errorf("Expected nil message_decision in NewSentinelConfig, got %v", cfg.MessageDecision)
	}
}

// ============================================================================
// Validation Tests - Required Fields
// ============================================================================

func TestValidate_MissingSentinelName(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.Sentinel.Name = ""
	cfg.ResourceType = testResourceType
	cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Expected error for missing sentinel.name, got nil")
	}
	if !strings.Contains(err.Error(), "sentinel.name") || !strings.Contains(err.Error(), "required") {
		t.Errorf("Expected error about 'sentinel.name' being required, got: %v", err)
	}
}

func TestValidate_MissingResourceType(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.ResourceType = ""
	cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Expected error for missing resource_type, got nil")
	}
	if !strings.Contains(err.Error(), "resource_type") || !strings.Contains(err.Error(), "required") {
		t.Errorf("Expected error about 'resource_type' being required, got: %v", err)
	}
}

func TestValidate_MissingBaseURL(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.ResourceType = testResourceType // Set valid resource_type to test base_url validation
	cfg.Clients.HyperFleetAPI.BaseURL = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Expected error for missing base_url, got nil")
	}
	if !strings.Contains(err.Error(), "clients.hyperfleet_api.base_url") || !strings.Contains(err.Error(), "required") {
		t.Errorf("Expected error about 'clients.hyperfleet_api.base_url' being required, got: %v", err)
	}
}

// ============================================================================
// Validation Tests - Invalid Values
// ============================================================================

func TestValidate_InvalidResourceType(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.ResourceType = "invalid-type"
	cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Expected error for invalid resource_type, got nil")
	}
}

func TestValidate_ResourceTypes(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		shouldFail   bool
	}{
		{"valid clusters", testResourceType, false},
		{"valid nodepools", "nodepools", false},
		{"valid wifconfigs", "wifconfigs", false},
		{"valid custom type", "myresources", false},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewSentinelConfig()
			cfg.ResourceType = tt.resourceType
			cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint
			cfg.MessageData = map[string]interface{}{"id": "resource.id"}
			cfg.MessageDecision = newTestMessageDecision()

			err := cfg.Validate()
			if tt.shouldFail && err == nil {
				t.Errorf("Expected error for resource_type '%s', got nil", tt.resourceType)
			}
			if !tt.shouldFail && err != nil {
				t.Errorf("Expected no error for resource_type '%s', got: %v", tt.resourceType, err)
			}
		})
	}
}

func TestValidate_NegativeDurations(t *testing.T) {
	tests := []struct {
		modifier func(*SentinelConfig)
		name     string
	}{
		{
			name: "negative poll_interval",
			modifier: func(c *SentinelConfig) {
				c.PollInterval = -5 * time.Second
			},
		},
		{
			name: "zero poll_interval",
			modifier: func(c *SentinelConfig) {
				c.PollInterval = 0
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewSentinelConfig()
			cfg.ResourceType = testResourceType
			cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint
			cfg.MessageData = map[string]interface{}{"id": "resource.id"}
			cfg.MessageDecision = newTestMessageDecision()
			tt.modifier(cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Expected error for invalid duration, got nil")
			}
		})
	}
}

func TestValidate_PageSize(t *testing.T) {
	tests := []struct {
		name    string
		size    int32
		wantErr bool
	}{
		{"valid default", 20, false},
		{"valid large", 500, false},
		{"valid minimum", 1, false},
		{"zero", 0, true},
		{"negative", -1, true},
		{"exceeds max", 501, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewSentinelConfig()
			cfg.ResourceType = testResourceType
			cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint
			cfg.MessageData = map[string]interface{}{"id": "resource.id"}
			cfg.MessageDecision = newTestMessageDecision()
			cfg.Clients.HyperFleetAPI.PageSize = tt.size

			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("PageSize=%d: wantErr=%v, got %v", tt.size, tt.wantErr, err)
			}
		})
	}
}

// ============================================================================
// Label Selector Tests
// ============================================================================

func TestLabelSelectorList_ToMap(t *testing.T) {
	selectors := LabelSelectorList{
		{Label: "region", Value: "us-east"},
		{Label: "environment", Value: "production"},
	}

	m := selectors.ToMap()
	if m == nil {
		t.Fatal("Expected non-nil map, got nil")
	}
	if len(m) != 2 {
		t.Errorf("Expected map with 2 entries, got %d", len(m))
	}
	if m["region"] != "us-east" {
		t.Errorf("Expected region 'us-east', got '%s'", m["region"])
	}
	if m["environment"] != "production" {
		t.Errorf("Expected environment 'production', got '%s'", m["environment"])
	}
}

func TestLabelSelectorList_ToMap_Empty(t *testing.T) {
	selectors := LabelSelectorList{}

	m := selectors.ToMap()
	if m != nil {
		t.Errorf("Expected nil map for empty selector list, got: %v", m)
	}
}

func TestLabelSelectorList_ToMap_EmptyLabel(t *testing.T) {
	selectors := LabelSelectorList{
		{Label: "region", Value: "us-east"},
		{Label: "", Value: "ignored"},
		{Label: "environment", Value: "production"},
	}

	m := selectors.ToMap()
	if len(m) != 2 {
		t.Errorf("Expected map with 2 entries (empty label ignored), got %d", len(m))
	}
}

// ============================================================================
// Message Data Validation Tests
// ============================================================================

func TestValidate_ValidMessageDataFlat(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.ResourceType = testResourceType
	cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint
	cfg.MessageDecision = newTestMessageDecision()
	cfg.MessageData = map[string]interface{}{
		"id":     "resource.id",
		"kind":   "resource.kind",
		"region": "resource.labels.region",
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Expected no error for valid flat config, got: %v", err)
	}
}

func TestValidate_ValidMessageDataNested(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.ResourceType = testResourceType
	cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint
	cfg.MessageDecision = newTestMessageDecision()
	cfg.MessageData = map[string]interface{}{
		"origin": `"sentinel"`,
		"ref": map[string]interface{}{
			"id":   "resource.id",
			"kind": "resource.kind",
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Expected no error for valid nested config, got: %v", err)
	}
}

func TestValidate_NilMessageData(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.ResourceType = testResourceType
	cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint
	cfg.MessageDecision = newTestMessageDecision()
	// MessageData is nil by default — message_data is required so this must fail

	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for nil message_data, got nil")
	}
}

func TestValidate_NilLeafInMessageData(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.ResourceType = testResourceType
	cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint
	cfg.MessageDecision = newTestMessageDecision()
	cfg.MessageData = map[string]interface{}{
		"id":   nil,
		"kind": "resource.kind",
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for nil leaf in message_data, got nil")
	}
}

func TestValidate_EmptyStringLeafInMessageData(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.ResourceType = testResourceType
	cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint
	cfg.MessageDecision = newTestMessageDecision()
	cfg.MessageData = map[string]interface{}{
		"id":   "",
		"kind": "resource.kind",
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for empty-string leaf in message_data, got nil")
	}
}

func TestValidate_NilLeafInNestedMessageData(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.ResourceType = testResourceType
	cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint
	cfg.MessageDecision = newTestMessageDecision()
	cfg.MessageData = map[string]interface{}{
		"ref": map[string]interface{}{
			"id":   nil,
			"kind": "resource.kind",
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for nil leaf in nested message_data, got nil")
	}
}

// ============================================================================
// Message Decision Validation Tests
// ============================================================================

func TestValidate_MissingMessageDecision(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.ResourceType = testResourceType
	cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint
	cfg.MessageData = map[string]interface{}{"id": "resource.id"}
	cfg.MessageDecision = nil

	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for nil message_decision, got nil")
	}
}

func TestValidate_EmptyResultExpression(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.ResourceType = testResourceType
	cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint
	cfg.MessageData = map[string]interface{}{"id": "resource.id"}
	cfg.MessageDecision = &MessageDecisionConfig{
		Params: []Param{},
		Result: "",
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Expected error for empty result expression, got nil")
	}
}

func TestValidate_EmptyParamExpression(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.ResourceType = testResourceType
	cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint
	cfg.MessageData = map[string]interface{}{"id": "resource.id"}
	cfg.MessageDecision = &MessageDecisionConfig{
		Params: []Param{
			{Name: "is_reconciled", Expr: ""},
		},
		Result: "is_reconciled",
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Expected error for empty param expression, got nil")
	}
}

func TestValidate_EmptyParamName(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.ResourceType = testResourceType
	cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint
	cfg.MessageData = map[string]interface{}{"id": "resource.id"}
	cfg.MessageDecision = &MessageDecisionConfig{
		Params: []Param{
			{Name: "", Expr: `condition("Reconciled").status == "True"`},
		},
		Result: "true",
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Expected error for empty param name, got nil")
	}
}

func TestValidate_DuplicateParamName(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.ResourceType = testResourceType
	cfg.Clients.HyperFleetAPI.BaseURL = testAPIEndpoint
	cfg.MessageData = map[string]interface{}{"id": "resource.id"}
	cfg.MessageDecision = &MessageDecisionConfig{
		Params: []Param{
			{Name: "is_reconciled", Expr: `condition("Reconciled").status == "True"`},
			{Name: "is_reconciled", Expr: `condition("Reconciled").status == "False"`},
		},
		Result: "is_reconciled",
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Expected error for duplicate param name, got nil")
	}
	if !strings.Contains(err.Error(), "defined more than once") {
		t.Errorf("Expected duplicate param error, got: %v", err)
	}
}

// ============================================================================
// Param Order Validation Tests
// ============================================================================

func TestValidate_ParamsNoDependencies(t *testing.T) {
	md := &MessageDecisionConfig{
		Params: []Param{
			{Name: "a", Expr: `condition("Reconciled").status`},
			{Name: "b", Expr: `condition("LastKnownReconciled").status`},
		},
		Result: "a == b",
	}

	if err := md.Validate(); err != nil {
		t.Fatalf("Expected no error for independent params, got: %v", err)
	}
}

func TestValidate_ParamsValidOrder(t *testing.T) {
	md := &MessageDecisionConfig{
		Params: []Param{
			{Name: "is_reconciled", Expr: `condition("Reconciled").status == "True"`},
			{Name: "should_publish", Expr: `!is_reconciled`},
		},
		Result: "should_publish",
	}

	if err := md.Validate(); err != nil {
		t.Fatalf("Expected no error for params in dependency order, got: %v", err)
	}
}

func TestValidate_ParamsEmpty(t *testing.T) {
	md := &MessageDecisionConfig{
		Params: []Param{},
		Result: "true",
	}

	if err := md.Validate(); err != nil {
		t.Fatalf("Expected no error for empty params, got: %v", err)
	}
}

// ============================================================================
// Integration-like Test with Full Config
// ============================================================================

func TestLoadConfig_BlankMessageDataLeafReturnsError(t *testing.T) {
	// A blank leaf (e.g. `id:`) in message_data is decoded as nil by the YAML
	// parser. mapstructure then silently drops nil-valued keys during Unmarshal,
	// so the key disappears from cfg.MessageData before Validate() runs.
	// LoadConfig must catch this via the raw viper value.
	_, err := LoadConfig(filepath.Join("testdata", "message-data-blank-id.yaml"), nil)
	if err == nil {
		t.Fatal("expected error for blank message_data leaf, got nil")
	}
	if !strings.Contains(err.Error(), "message_data.id") {
		t.Errorf("expected error to mention message_data.id, got: %v", err)
	}
}

func TestLoadConfig_FullWorkflow(t *testing.T) {
	configPath := filepath.Join("testdata", "full-workflow.yaml")

	cfg, err := LoadConfig(configPath, nil)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify all parts are loaded correctly
	if cfg.ResourceType != "nodepools" {
		t.Errorf("Expected resource_type 'nodepools', got '%s'", cfg.ResourceType)
	}
	if cfg.PollInterval != 3*time.Second {
		t.Errorf("Expected poll_interval 3s, got %v", cfg.PollInterval)
	}
	if len(cfg.ResourceSelector) != 2 {
		t.Errorf("Expected 2 resource selectors, got %d", len(cfg.ResourceSelector))
	}
	md := cfg.MessageData
	if len(md) != 4 {
		t.Errorf("Expected 4 message_data fields, got %d", len(md))
	}

	// Verify message_decision is loaded from file
	if cfg.MessageDecision == nil {
		t.Fatal("Expected message_decision to be set")
	}
	if len(cfg.MessageDecision.Params) != 7 {
		t.Errorf("Expected 7 message_decision params, got %d", len(cfg.MessageDecision.Params))
	}
}

// ============================================================================
// RedactedCopy Tests
// ============================================================================

func TestRedactedCopy_NilBrokerHandled(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.Clients.Broker = nil

	redacted := cfg.RedactedCopy()

	if redacted.Clients.Broker != nil {
		t.Errorf("Expected nil Broker to stay nil after redaction")
	}
}

func TestRedactedCopy_DoesNotMutateOriginal(t *testing.T) {
	cfg := NewSentinelConfig()
	cfg.Clients.Broker = &BrokerConfig{Topic: "my-topic"}

	_ = cfg.RedactedCopy()

	if cfg.Clients.Broker.Topic != "my-topic" {
		t.Errorf("RedactedCopy must not mutate the original; got '%s'", cfg.Clients.Broker.Topic)
	}
}

// ============================================================================
// Unknown Field Tests
// ============================================================================

func TestLoadConfig_UnknownFieldReturnsError(t *testing.T) {
	_, err := LoadConfig(filepath.Join("testdata", "unknown-field.yaml"), nil)
	if err == nil {
		t.Fatal("Expected error for unknown field 'resouce_type', got nil")
	}
}

func TestLoadConfig_UnknownFieldInline(t *testing.T) {
	yaml := `
sentinel:
  name: test-sentinel
clients:
  hyperfleet_api:
    base_url: http://localhost:8000
resource_type: clusters
message_data:
  id: resource.id
hyperfleet_api:
  endpoint: http://old-format.example.com
`
	configPath := createTempConfigFile(t, yaml)

	_, err := LoadConfig(configPath, nil)
	if err == nil {
		t.Fatal("Expected error for unknown field 'hyperfleet_api', got nil")
	}
}

// ============================================================================
// Topic Tests
// ============================================================================

func TestLoadConfig_TopicFromEnvVar(t *testing.T) {
	t.Setenv("HYPERFLEET_BROKER_TOPIC", "test-namespace-clusters")

	configPath := filepath.Join("testdata", "minimal.yaml")

	cfg, err := LoadConfig(configPath, nil)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if cfg.Clients.Broker.Topic != "test-namespace-clusters" {
		t.Errorf("Expected topic 'test-namespace-clusters', got '%s'", cfg.Clients.Broker.Topic)
	}
}

func TestLoadConfig_TopicEnvVarOverridesConfig(t *testing.T) {
	t.Setenv("HYPERFLEET_BROKER_TOPIC", "env-topic")

	yaml := `
sentinel:
  name: test-sentinel
clients:
  hyperfleet_api:
    base_url: http://localhost:8000
  broker:
    topic: config-topic
resource_type: clusters
message_data:
  id: resource.id
`
	configPath := createTempConfigFile(t, yaml)

	cfg, err := LoadConfig(configPath, nil)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if cfg.Clients.Broker.Topic != "env-topic" {
		t.Errorf("Expected topic 'env-topic' (from env), got '%s'", cfg.Clients.Broker.Topic)
	}
}

func TestLoadConfig_TopicFromConfigFile(t *testing.T) {
	yaml := `
sentinel:
  name: test-sentinel
clients:
  hyperfleet_api:
    base_url: http://localhost:8000
  broker:
    topic: my-namespace-clusters
resource_type: clusters
message_data:
  id: resource.id
`
	configPath := createTempConfigFile(t, yaml)

	cfg, err := LoadConfig(configPath, nil)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if cfg.Clients.Broker.Topic != "my-namespace-clusters" {
		t.Errorf("Expected topic 'my-namespace-clusters', got '%s'", cfg.Clients.Broker.Topic)
	}
}

func TestLoadConfig_TopicEmpty(t *testing.T) {
	configPath := filepath.Join("testdata", "minimal.yaml")

	cfg, err := LoadConfig(configPath, nil)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if cfg.Clients.Broker.Topic != "" {
		t.Errorf("Expected empty topic, got '%s'", cfg.Clients.Broker.Topic)
	}
}

func TestHyperFleetAPIAuthConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		wantErr string
		cfg     HyperFleetAPIAuthConfig
	}{
		{
			name:    "missing token_path",
			cfg:     HyperFleetAPIAuthConfig{},
			wantErr: "token_path is required",
		},
		{
			name:    "relative token_path",
			cfg:     HyperFleetAPIAuthConfig{TokenPath: "token"},
			wantErr: "token_path must be an absolute path",
		},
		{
			name:    "relative token_path with subdirectory",
			cfg:     HyperFleetAPIAuthConfig{TokenPath: "secrets/token"},
			wantErr: "token_path must be an absolute path",
		},
		{
			name:    "negative token_cache_ttl",
			cfg:     HyperFleetAPIAuthConfig{TokenPath: "/var/run/secrets/token", TokenCacheTTL: -1},
			wantErr: "token_cache_ttl must not be negative",
		},
		{
			name: "valid absolute path",
			cfg:  HyperFleetAPIAuthConfig{TokenPath: "/var/run/secrets/hyperfleet/token", TokenCacheTTL: 30 * time.Second},
		},
		{
			name: "valid absolute path zero ttl",
			cfg:  HyperFleetAPIAuthConfig{TokenPath: "/var/run/secrets/token"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}
