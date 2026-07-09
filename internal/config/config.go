package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-sentinel/pkg/logger"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	defaultConfigFile = "/etc/hyperfleet/config.yaml"
)

// EnvPrefix is the prefix for all environment variables that override sentinel config
const EnvPrefix = "HYPERFLEET"

// LabelSelector represents a label key-value pair for resource filtering
type LabelSelector struct {
	Label string `yaml:"label" mapstructure:"label"`
	Value string `yaml:"value" mapstructure:"value"`
}

// LabelSelectorList is a list of label selectors
type LabelSelectorList []LabelSelector

// Param is a named CEL expression. Params must be listed in dependency order:
// if param B references param A, A must appear before B so the CEL runtime
// can resolve it during evaluation. Out-of-order references cause a runtime
// evaluation error (not a startup error).
type Param struct {
	Name string `mapstructure:"name"`
	Expr string `mapstructure:"expr"`
}

// MessageDecisionConfig represents configurable CEL-based decision logic.
// Params are evaluated in the order they are defined.
// Result is a CEL expression that evaluates to a boolean.
type MessageDecisionConfig struct {
	Result string  `mapstructure:"result"`
	Params []Param `mapstructure:"params"`
}

// SentinelConfig represents the Sentinel configuration
type SentinelConfig struct {
	Log              LogConfig              `yaml:"log,omitempty" mapstructure:"log"`
	Sentinel         SentinelInfo           `yaml:"sentinel" mapstructure:"sentinel"`
	ResourceType     string                 `yaml:"resource_type" mapstructure:"resource_type"`
	Clients          ClientsConfig          `yaml:"clients" mapstructure:"clients"`
	MessageData      map[string]interface{} `yaml:"message_data,omitempty" mapstructure:"message_data"`
	MessageDecision  *MessageDecisionConfig `yaml:"message_decision,omitempty" mapstructure:"message_decision"`
	ResourceSelector LabelSelectorList      `yaml:"resource_selector,omitempty" mapstructure:"resource_selector"`
	PollInterval     time.Duration          `yaml:"poll_interval" mapstructure:"poll_interval"`
	DebugConfig      bool                   `yaml:"debug_config,omitempty" mapstructure:"debug_config"`
	TracingEnabled   bool                   `yaml:"tracing_enabled,omitempty" mapstructure:"tracing_enabled"`
}

// SentinelInfo contains basic sentinel information
type SentinelInfo struct {
	Name string `yaml:"name" mapstructure:"name"`
}

// LogConfig contains logging configuration.
// Priority (lowest to highest): config file < HYPERFLEET_LOG_* env vars < --log-* CLI flags
type LogConfig struct {
	Level  string `yaml:"level,omitempty" mapstructure:"level"`
	Format string `yaml:"format,omitempty" mapstructure:"format"`
	Output string `yaml:"output,omitempty" mapstructure:"output"`
}

// ClientsConfig contains all client configurations
type ClientsConfig struct {
	HyperFleetAPI *HyperFleetAPIConfig `yaml:"hyperfleet_api" mapstructure:"hyperfleet_api"`
	Broker        *BrokerConfig        `yaml:"broker,omitempty" mapstructure:"broker"`
}

// HyperFleetAPIAuthConfig defines optional JWT authentication via a Kubernetes
// projected service account token. When set, the bearer token is read from
// TokenPath and injected into every API request.
type HyperFleetAPIAuthConfig struct {
	TokenPath     string        `yaml:"token_path" mapstructure:"token_path"`
	TokenCacheTTL time.Duration `yaml:"token_cache_ttl" mapstructure:"token_cache_ttl"`
}

// Validate returns an error if the auth config is incomplete.
func (a *HyperFleetAPIAuthConfig) Validate() error {
	if a.TokenPath == "" {
		return fmt.Errorf("token_path is required")
	}
	if !filepath.IsAbs(a.TokenPath) {
		return fmt.Errorf("token_path must be an absolute path, got %q", a.TokenPath)
	}
	if a.TokenCacheTTL < 0 {
		return fmt.Errorf("token_cache_ttl must not be negative")
	}
	return nil
}

// HyperFleetAPIConfig defines the HyperFleet API client configuration
type HyperFleetAPIConfig struct {
	Auth     *HyperFleetAPIAuthConfig `yaml:"auth,omitempty" mapstructure:"auth"`
	BaseURL  string                   `yaml:"base_url" mapstructure:"base_url"`
	Version  string                   `yaml:"version,omitempty" mapstructure:"version"`
	Timeout  time.Duration            `yaml:"timeout" mapstructure:"timeout"`
	PageSize int32                    `yaml:"page_size,omitempty" mapstructure:"page_size"`
}

// BrokerConfig contains broker configuration
type BrokerConfig struct {
	Topic string `yaml:"topic,omitempty" mapstructure:"topic"`
}

// ToMap converts label selectors to a map for filtering
func (ls LabelSelectorList) ToMap() map[string]string {
	if len(ls) == 0 {
		return nil
	}

	result := make(map[string]string, len(ls))
	for _, selector := range ls {
		if selector.Label != "" {
			result[selector.Label] = selector.Value
		}
	}
	return result
}

// DefaultMessageDecision returns the default message_decision configuration
// used when message_decision is not set in the config file.
func DefaultMessageDecision() *MessageDecisionConfig {
	return &MessageDecisionConfig{
		Params: []Param{
			{Name: "ref_time", Expr: `condition("Reconciled").last_updated_time`},
			{Name: "is_reconciled", Expr: `condition("Reconciled").status == "True"`},
			{Name: "has_ref_time", Expr: `ref_time != ""`},
			{Name: "is_new_resource", Expr: `resource.generation == 1 && !has_ref_time`},
			{Name: "generation_mismatch", Expr: `resource.generation > condition("Reconciled").observed_generation`},
			{Name: "reconciled_and_stale", Expr: `is_reconciled && has_ref_time && now - timestamp(ref_time) > duration("30m")`},
			{
				Name: "not_reconciled_and_debounced",
				Expr: `!is_reconciled && has_ref_time && now - timestamp(ref_time) > duration("10s")`,
			},
		},
		Result: "is_new_resource || generation_mismatch || reconciled_and_stale || not_reconciled_and_debounced",
	}
}

// NewSentinelConfig creates a new configuration with defaults
func NewSentinelConfig() *SentinelConfig {
	return &SentinelConfig{
		Sentinel: SentinelInfo{
			Name: "hyperfleet-sentinel",
		},
		DebugConfig:    false,
		TracingEnabled: true,
		Log: LogConfig{
			Level:  "info",
			Format: "json",
			Output: "stdout",
		},
		Clients: ClientsConfig{
			HyperFleetAPI: &HyperFleetAPIConfig{
				Version:  "v1",
				Timeout:  10 * time.Second,
				PageSize: 20,
			},
			Broker: &BrokerConfig{},
		},
		// ResourceType is required and must be set in config file
		PollInterval:     5 * time.Second,
		ResourceSelector: []LabelSelector{}, // Empty means watch all resources
	}
}

// viperKeyMappings defines mappings from config paths to env variable suffixes
// The full env var name is EnvPrefix + "_" + suffix
// Note: Uses "::" as key delimiter to avoid conflicts with dots in YAML keys
// Complex types (maps, slices) are intentionally excluded — they cannot be expressed as scalar env vars.
var viperKeyMappings = map[string]string{
	"debug_config":                                   "DEBUG_CONFIG",
	"sentinel::name":                                 "SENTINEL_NAME",
	"log::level":                                     "LOG_LEVEL",
	"log::format":                                    "LOG_FORMAT",
	"log::output":                                    "LOG_OUTPUT",
	"clients::hyperfleet_api::base_url":              "API_BASE_URL",
	"clients::hyperfleet_api::version":               "API_VERSION",
	"clients::hyperfleet_api::timeout":               "API_TIMEOUT",
	"clients::hyperfleet_api::page_size":             "API_PAGE_SIZE",
	"clients::hyperfleet_api::auth::token_path":      "API_AUTH_TOKEN_PATH",
	"clients::hyperfleet_api::auth::token_cache_ttl": "API_AUTH_TOKEN_CACHE_TTL",
	"clients::broker::topic":                         "BROKER_TOPIC",
	"resource_type":                                  "RESOURCE_TYPE",
	"poll_interval":                                  "POLL_INTERVAL",
	"tracing_enabled":                                "TRACING_ENABLED",
}

// cliFlags defines mappings from CLI flag names to config paths
// Note: Uses "::" as key delimiter to avoid conflicts with dots in YAML keys
var cliFlags = map[string]string{
	"debug-config":             "debug_config",
	"name":                     "sentinel::name",
	"hyperfleet-api-base-url":  "clients::hyperfleet_api::base_url",
	"hyperfleet-api-version":   "clients::hyperfleet_api::version",
	"hyperfleet-api-timeout":   "clients::hyperfleet_api::timeout",
	"hyperfleet-api-page-size": "clients::hyperfleet_api::page_size",
	"broker-topic":             "clients::broker::topic",
	"resource-type":            "resource_type",
	"poll-interval":            "poll_interval",
	"log-level":                "log::level",
	"log-format":               "log::format",
	"log-output":               "log::output",
	"tracing-enabled":          "tracing_enabled",
}

// LoadConfig loads configuration from YAML file with environment variable and CLI flag overrides
// Precedence: CLI flags > Environment variables > YAML file > Defaults
func LoadConfig(configFile string, flags *pflag.FlagSet) (*SentinelConfig, error) {
	cfg := NewSentinelConfig()

	if configFile == "" {
		if env := os.Getenv("HYPERFLEET_CONFIG"); env != "" {
			configFile = env
		} else {
			configFile = defaultConfigFile
		}
	}

	log := logger.NewHyperFleetLogger()
	ctx := context.Background()
	log.Infof(ctx, "Loading configuration from %s", configFile)

	// Use "::" as key delimiter to avoid conflicts with dots in YAML keys
	v := viper.NewWithOptions(viper.KeyDelimiter("::"))
	v.SetConfigFile(configFile)

	// Read the YAML file
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Bind environment variables into viper's env layer so they sit below CLI
	// flag overrides (v.Set) but above the config file layer.
	for configPath, envSuffix := range viperKeyMappings {
		if err := v.BindEnv(configPath, EnvPrefix+"_"+envSuffix); err != nil {
			return nil, fmt.Errorf("failed to bind env var %s_%s: %w", EnvPrefix, envSuffix, err)
		}
	}

	// Bind CLI flags if provided
	if flags != nil {
		for flagName, configPath := range cliFlags {
			if flag := flags.Lookup(flagName); flag != nil && flag.Changed {
				v.Set(configPath, flag.Value.String())
			}
		}
	}

	// Unmarshal into SentinelConfig struct — ErrorUnused ensures unknown fields are rejected
	if err := v.UnmarshalExact(cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate message_data leaves against the raw viper value, because
	// mapstructure silently drops nil-valued keys during Unmarshal — meaning
	// a blank `id:` in the YAML disappears before Validate() ever sees it.
	if rawMD, ok := v.Get("message_data").(map[string]interface{}); ok {
		if err := validateMessageDataLeaves(rawMD, "message_data"); err != nil {
			return nil, fmt.Errorf("invalid config: %w", err)
		}
	}

	// Apply default message_decision if not configured
	if cfg.MessageDecision == nil {
		cfg.MessageDecision = DefaultMessageDecision()
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	log.Infof(ctx, "Configuration loaded successfully: name=%s resource_type=%s",
		cfg.Sentinel.Name, cfg.ResourceType)

	return cfg, nil
}

// fieldRemediation describes how a user can set a configuration field.
type fieldRemediation struct {
	Flag string // empty if not settable via CLI flag
	Env  string // empty if not settable via env var
	File string // YAML path in the config file
}

// fieldRemediations maps config field identifiers to their remediation hints.
var fieldRemediations = map[string]fieldRemediation{
	"sentinel.name": {
		Flag: "--name",
		Env:  "HYPERFLEET_SENTINEL_NAME",
		File: "sentinel.name",
	},
	"resource_type": {
		Flag: "--resource-type",
		Env:  "HYPERFLEET_RESOURCE_TYPE",
		File: "resource_type",
	},
	"clients.hyperfleet_api": {
		File: "clients.hyperfleet_api",
	},
	"clients.hyperfleet_api.base_url": {
		Flag: "--hyperfleet-api-base-url",
		Env:  "HYPERFLEET_API_BASE_URL",
		File: "clients.hyperfleet_api.base_url",
	},
	"clients.hyperfleet_api.page_size": {
		Flag: "--hyperfleet-api-page-size",
		Env:  "HYPERFLEET_API_PAGE_SIZE",
		File: "clients.hyperfleet_api.page_size",
	},
	"poll_interval": {
		Flag: "--poll-interval",
		Env:  "HYPERFLEET_POLL_INTERVAL",
		File: "poll_interval",
	},
	"message_decision": {
		File: "message_decision",
	},
	"message_data": {
		File: "message_data",
	},
	"tracing_enabled": {
		Flag: "--tracing-enabled",
		Env:  "HYPERFLEET_TRACING_ENABLED",
		File: "tracing_enabled",
	},
}

// validationErr returns a validation error for the given field with remediation guidance.
// The optional actualValue parameter includes the offending value in the error message,
// which is useful for invalid-value failures (e.g. poll_interval: -1s) so operators
// can immediately see what was set without having to re-inspect the config file.
func validationErr(field, reason string, actualValue ...string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Field '%s' failed validation: %s\n", field, reason)
	if len(actualValue) > 0 && actualValue[0] != "" {
		fmt.Fprintf(&b, "  Actual value: %s\n", actualValue[0])
	}
	fmt.Fprintf(&b, "Please provide via:\n")
	if rem, ok := fieldRemediations[field]; ok {
		if rem.Flag != "" {
			fmt.Fprintf(&b, "  • Flag: %s\n", rem.Flag)
		}
		if rem.Env != "" {
			fmt.Fprintf(&b, "  • Env:  %s\n", rem.Env)
		}
		if rem.File != "" {
			fmt.Fprintf(&b, "  • File: %s\n", rem.File)
		}
	}
	return errors.New(b.String())
}

// Validate validates the configuration
func (c *SentinelConfig) Validate() error {
	if c.Sentinel.Name == "" {
		return validationErr("sentinel.name", "required")
	}

	if c.ResourceType == "" {
		return validationErr("resource_type", "required")
	}

	if c.Clients.HyperFleetAPI == nil {
		return validationErr("clients.hyperfleet_api", "required")
	}

	if c.Clients.HyperFleetAPI.BaseURL == "" {
		return validationErr("clients.hyperfleet_api.base_url", "required")
	}

	if c.Clients.HyperFleetAPI.Timeout <= 0 {
		return validationErr("clients.hyperfleet_api.timeout", "must be positive",
			c.Clients.HyperFleetAPI.Timeout.String())
	}

	if c.Clients.HyperFleetAPI.PageSize <= 0 || c.Clients.HyperFleetAPI.PageSize > 500 {
		return validationErr("clients.hyperfleet_api.page_size",
			"must be between 1 and 500",
			fmt.Sprintf("%d", c.Clients.HyperFleetAPI.PageSize))
	}

	if c.Clients.HyperFleetAPI.Auth != nil {
		if err := c.Clients.HyperFleetAPI.Auth.Validate(); err != nil {
			return fmt.Errorf("clients.hyperfleet_api.auth: %w", err)
		}
	}

	if c.PollInterval <= 0 {
		return validationErr("poll_interval", "must be positive", c.PollInterval.String())
	}

	if c.MessageDecision == nil {
		return validationErr("message_decision", "required")
	}

	if err := c.MessageDecision.Validate(); err != nil {
		return fmt.Errorf("message_decision: %w", err)
	}

	if c.MessageData == nil {
		return validationErr("message_data", "required")
	}

	if err := validateMessageDataLeaves(c.MessageData, "message_data"); err != nil {
		return err
	}

	return nil
}

// Validate validates the message decision configuration.
func (md *MessageDecisionConfig) Validate() error {
	if md.Result == "" {
		return fmt.Errorf("result expression is required")
	}

	seenNames := make(map[string]bool, len(md.Params))
	for _, p := range md.Params {
		if p.Name == "" {
			return fmt.Errorf("param has empty name")
		}
		if p.Expr == "" {
			return fmt.Errorf("param %q has empty expression", p.Name)
		}
		if seenNames[p.Name] {
			return fmt.Errorf("param %q is defined more than once", p.Name)
		}
		seenNames[p.Name] = true
	}

	return nil
}

// validateMessageDataLeaves recursively checks that every leaf value in a
// message_data map is a non-empty string (CEL expression). nil values and
// empty strings are rejected early so that the error is reported at config
// load time rather than silently producing a broken payload.
func validateMessageDataLeaves(data map[string]interface{}, path string) error {
	for k, v := range data {
		fullKey := path + "." + k
		switch val := v.(type) {
		case nil:
			return fmt.Errorf(
				"%s: nil value is not allowed; every leaf must be a non-empty CEL expression string "+
					"(was the field left blank in the config?)",
				fullKey,
			)
		case string:
			if val == "" {
				return fmt.Errorf(
					"%s: empty CEL expression is not allowed; every leaf must be a non-empty CEL expression string",
					fullKey,
				)
			}
		case map[string]interface{}:
			if err := validateMessageDataLeaves(val, fullKey); err != nil {
				return err
			}
		}
	}
	return nil
}

// RedactedCopy returns a deep copy of the config. Use this copy when logging
// the merged configuration at startup so that any future sensitive fields are
// never accidentally shared by reference.
// Currently there are no sensitive params in the configuration, so this function is doing just a deep copy
func (c *SentinelConfig) RedactedCopy() *SentinelConfig {
	cp := *c

	if cp.Clients.HyperFleetAPI != nil {
		api := *cp.Clients.HyperFleetAPI
		cp.Clients.HyperFleetAPI = &api
	}

	if cp.Clients.Broker != nil {
		b := *cp.Clients.Broker
		cp.Clients.Broker = &b
	}

	if c.ResourceSelector != nil {
		rs := make(LabelSelectorList, len(c.ResourceSelector))
		copy(rs, c.ResourceSelector)
		cp.ResourceSelector = rs
	}

	if c.MessageData != nil {
		md := make(map[string]interface{}, len(c.MessageData))
		for k, v := range c.MessageData {
			md[k] = v
		}
		cp.MessageData = md
	}

	return &cp
}
