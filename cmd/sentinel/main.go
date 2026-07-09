package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-sentinel/pkg/telemetry"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.opentelemetry.io/otel/sdk/trace"
	"gopkg.in/yaml.v3"

	"github.com/openshift-hyperfleet/hyperfleet-broker/broker"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/client"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/config"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/engine"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/health"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/metrics"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/sentinel"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/pkg/logger"
)

var (
	version = "0.0.0-dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "sentinel",
		Short: "HyperFleet Sentinel - Resource polling and event publishing service",
		Long: `HyperFleet Sentinel watches HyperFleet API resources and publishes
reconciliation events to a message broker based on configurable max age intervals.`,
	}

	rootCmd.AddCommand(newServeCommand())
	rootCmd.AddCommand(newConfigDumpCommand())
	rootCmd.AddCommand(newVersionCommand())

	if err := rootCmd.Execute(); err != nil {
		// Print error to stderr since SilenceErrors is true and logging may not be initialized
		cmd := strings.Join(os.Args[1:], " ")
		if cmd == "" {
			cmd = "(no command)"
		}
		fmt.Fprintf(os.Stderr, "Error executing command 'sentinel %s': %v\n", cmd, err)
		os.Exit(1)
	}
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Version:    %s\n", version)
			fmt.Printf("Commit:     %s\n", commit)
			fmt.Printf("Build Date: %s\n", date)
		},
	}
}

func newServeCommand() *cobra.Command {
	var (
		configFile         string
		healthBindAddress  string
		metricsBindAddress string
	)

	cmd := &cobra.Command{
		Use:           "serve",
		Short:         "Start the Sentinel service",
		Long:          `Start the HyperFleet Sentinel service with the specified configuration.`,
		SilenceUsage:  true, // Don't print usage on error
		SilenceErrors: true, // Don't print errors - we handle logging ourselves
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load configuration with CLI flags, env vars, and file
			// Precedence: flags → environment variables → config file → defaults
			cfg, err := config.LoadConfig(configFile, cmd.Flags())
			if err != nil {
				return err
			}

			// Initialize logging with merged configuration
			logCfg, err := initLogging(&cfg.Log, cfg.TracingEnabled)
			if err != nil {
				return fmt.Errorf("failed to initialize logging: %w", err)
			}

			return runServe(cfg, logCfg, healthBindAddress, metricsBindAddress)
		},
	}

	// Add --config flag for YAML file path
	cmd.Flags().StringVarP(&configFile, "config", "c", "", "Path to configuration file (YAML)")

	// Server bind address flags
	cmd.Flags().StringVar(&healthBindAddress, "health-server-bindaddress", ":8080", "Health server bind address")
	cmd.Flags().StringVar(&metricsBindAddress, "metrics-server-bindaddress", ":9090", "Metrics server bind address")

	// Add config override flags
	addConfigOverrideFlags(cmd)

	return cmd
}

func newConfigDumpCommand() *cobra.Command {
	var configFile string

	cmd := &cobra.Command{
		Use:   "config-dump",
		Short: "Load and print the merged sentinel configuration as YAML",
		Long: `Load the sentinel configuration from config file, environment variables,
and CLI flags, then print the merged result as YAML to stdout.
Exits with code 0 on success, non-zero on error.

Priority order (lowest to highest): config file < env vars < CLI flags`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigDump(configFile, cmd.Flags())
		},
	}

	cmd.Flags().StringVarP(&configFile, "config", "c", "", "Path to configuration file (YAML)")
	addConfigOverrideFlags(cmd)

	return cmd
}

// addConfigOverrideFlags adds CLI flags for overriding configuration values
func addConfigOverrideFlags(cmd *cobra.Command) {
	// General
	cmd.Flags().Bool("debug-config", false, "Log the full merged configuration after load. Env: HYPERFLEET_DEBUG_CONFIG")

	// Sentinel
	cmd.Flags().StringP("name", "n", "", "Sentinel component name. Env: HYPERFLEET_SENTINEL_NAME")

	cmd.Flags().StringP("log-level", "l", "", "Log level: debug, info, warn, error. Env: HYPERFLEET_LOG_LEVEL")
	cmd.Flags().StringP("log-format", "f", "", "Log format: text, json. Env: HYPERFLEET_LOG_FORMAT")
	cmd.Flags().String("log-output", "", "Log output: stdout, stderr. Env: HYPERFLEET_LOG_OUTPUT")

	// HyperFleet API
	cmd.Flags().String("hyperfleet-api-base-url", "", "HyperFleet API base URL. Env: HYPERFLEET_API_BASE_URL")
	cmd.Flags().String("hyperfleet-api-version", "", "HyperFleet API version. Env: HYPERFLEET_API_VERSION")
	cmd.Flags().String("hyperfleet-api-timeout", "", "HyperFleet API timeout (e.g., 10s). Env: HYPERFLEET_API_TIMEOUT")

	// Broker
	cmd.Flags().String("broker-topic", "", "Broker topic. Env: HYPERFLEET_BROKER_TOPIC")

	// Sentinel-specific
	cmd.Flags().String("resource-type", "", "Resource type to watch (clusters, nodepools). Env: HYPERFLEET_RESOURCE_TYPE")
	cmd.Flags().String("poll-interval", "", "Poll interval (e.g., 5s). Env: HYPERFLEET_POLL_INTERVAL")

	// Observability
	cmd.Flags().Bool("tracing-enabled", true, "Enable OpenTelemetry tracing. Env: HYPERFLEET_TRACING_ENABLED")
}

// initLogging initializes the logging configuration from the already-merged LogConfig.
// Precedence (config file < env vars < CLI flags) is resolved by LoadConfig via viper.
func initLogging(logCfg *config.LogConfig, tracingEnabled bool) (*logger.LogConfig, error) {
	cfg := logger.DefaultConfig()
	cfg.Version = version
	cfg.Component = "sentinel"

	if logCfg.Level != "" {
		level, err := logger.ParseLogLevel(logCfg.Level)
		if err != nil {
			return nil, err
		}
		cfg.Level = level
	}

	if logCfg.Format != "" {
		format, err := logger.ParseLogFormat(logCfg.Format)
		if err != nil {
			return nil, err
		}
		cfg.Format = format
	}

	if logCfg.Output != "" {
		output, err := logger.ParseLogOutput(logCfg.Output)
		if err != nil {
			return nil, err
		}
		cfg.Output = output
	}

	cfg.OTel.Enabled = tracingEnabled

	logger.SetGlobalConfig(cfg)

	return cfg, nil
}

func runServe(
	cfg *config.SentinelConfig, logCfg *logger.LogConfig, healthBindAddress, metricsBindAddress string,
) error {
	// Initialize context and logger
	ctx := context.Background()
	log := logger.NewHyperFleetLoggerWithConfig(logCfg)

	serviceName := "hyperfleet-sentinel"
	// Use OTEL_SERVICE_NAME if set, otherwise default
	if envServiceName := os.Getenv("OTEL_SERVICE_NAME"); envServiceName != "" {
		serviceName = envServiceName
	}

	var tp *trace.TracerProvider
	if logCfg.OTel.Enabled {
		traceProvider, err := telemetry.InitTraceProvider(ctx, serviceName, version)
		if err != nil {
			log.Extra("error", err).Warn(ctx, "Failed to initialize OpenTelemetry")
		} else {
			tp = traceProvider
			defer func() {
				otelShutdownCtx, otelShutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer otelShutdownCancel()
				if err := telemetry.Shutdown(otelShutdownCtx, tp); err != nil {
					log.Extra("error", err).Error(otelShutdownCtx, "Failed to shutdown OpenTelemetry")
				}
			}()
		}
	} else {
		log.Extra("tracing_enabled", false).Info(ctx, "OpenTelemetry disabled")
	}

	log.Extra("commit", commit).
		Extra("log_level", logCfg.Level.String()).
		Extra("log_format", logCfg.Format.String()).
		Info(ctx, "Starting HyperFleet Sentinel")

	// Log full merged configuration if debug_config is enabled; sensitive values are redacted
	if cfg.DebugConfig {
		data, err := yaml.Marshal(cfg.RedactedCopy())
		if err != nil {
			log.Warnf(ctx, "Failed to marshal config for debug logging: %v", err)
		} else {
			log.Infof(ctx, "Debug config enabled - merged configuration:\n%s", string(data))
		}
	}

	// Initialize Prometheus metrics registry
	registry := prometheus.NewRegistry()
	// Register metrics once (uses sync.Once internally)
	metrics.NewSentinelMetrics(registry, version)

	// Initialize components
	tokenPath := ""
	var tokenCacheTTL time.Duration
	if cfg.Clients.HyperFleetAPI.Auth != nil {
		tokenPath = cfg.Clients.HyperFleetAPI.Auth.TokenPath
		tokenCacheTTL = cfg.Clients.HyperFleetAPI.Auth.TokenCacheTTL
	}
	hyperfleetClient, err := client.NewHyperFleetClient(
		cfg.Clients.HyperFleetAPI.BaseURL, cfg.Clients.HyperFleetAPI.Timeout,
		cfg.Sentinel.Name, version, cfg.Clients.HyperFleetAPI.PageSize,
		tokenPath, tokenCacheTTL,
	)
	if err != nil {
		log.Errorf(ctx, "Failed to initialize OpenAPI client: %v", err)
		return fmt.Errorf("failed to initialize OpenAPI client: %w", err)
	}

	// verify HyperFleet client connectivity
	if err = hyperfleetClient.VerifyConnectivity(ctx, cfg.ResourceType); err != nil {
		log.Errorf(ctx, "Failed to verify HyperFleet client connectivity: %v", err)
		return fmt.Errorf("failed to verify HyperFleet client connectivity: %w", err)
	}
	log.Info(ctx, "Initialized HyperFleet client")

	decisionEngine, err := engine.NewDecisionEngine(cfg.MessageDecision)
	if err != nil {
		log.Errorf(ctx, "Failed to create decision engine: %v", err)
		return fmt.Errorf("failed to create decision engine: %w", err)
	}

	// Initialize broker metrics recorder
	// Broker metrics (messages_published_total, errors_total, etc.) are registered
	// in the same Prometheus registry used by sentinel metrics.
	brokerMetrics := broker.NewMetricsRecorder("sentinel", version, registry)

	// Initialize publisher using hyperfleet-broker library
	// Configuration is loaded from broker.yaml or BROKER_CONFIG_FILE env var
	pub, err := broker.NewPublisher(log, brokerMetrics)
	if err != nil {
		log.Errorf(ctx, "Failed to initialize broker publisher: %v", err)
		return fmt.Errorf("failed to initialize broker publisher: %w", err)
	}
	if pub != nil {
		defer func() {
			if closeErr := pub.Close(); closeErr != nil {
				log.Errorf(ctx, "Error closing publisher: %v", closeErr)
			}
		}()
	}
	log.Info(ctx, "Initialized broker publisher")

	// Initialize readiness checker with dependency checks.
	// Checks are evaluated on each /readyz request.
	readiness := health.NewReadinessChecker(log)
	readiness.AddCheck("broker", func() error {
		if pub == nil {
			return fmt.Errorf("broker publisher not initialized")
		}
		return pub.Health(ctx)
	})
	readiness.SetReady(true)

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Initialize sentinel
	s, err := sentinel.NewSentinel(cfg, hyperfleetClient, decisionEngine, pub, log)
	if err != nil {
		return fmt.Errorf("failed to initialize sentinel: %w", err)
	}

	readiness.AddCheck("sentinel_poll", func() error {
		if s.LastSuccessfulPoll().IsZero() {
			return fmt.Errorf("no successful poll completed yet")
		}
		return nil
	})

	// Health server on port 8080 (/healthz, /readyz)
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/healthz", readiness.HealthzHandler(s.LastSuccessfulPoll, 3*cfg.PollInterval))
	healthMux.HandleFunc("/readyz", readiness.ReadyzHandler())

	healthServer := &http.Server{
		Addr:         healthBindAddress,
		Handler:      healthMux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Metrics server on port 9090 (/metrics)
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

	metricsServer := &http.Server{
		Addr:         metricsBindAddress,
		Handler:      metricsMux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start HTTP servers in background
	go func() {
		log.Infof(ctx, "Starting health server on %s", healthBindAddress)
		if err := healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf(ctx, "Health server error: %v", err)
		}
	}()

	go func() {
		log.Infof(ctx, "Starting metrics server on %s", metricsBindAddress)
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf(ctx, "Metrics server error: %v", err)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Info(ctx, "Received shutdown signal")
		// Set readiness to false so /readyz returns 503 during shutdown
		readiness.SetReady(false)
		cancel()

		// Shutdown HTTP servers (20s timeout per graceful-shutdown standard)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer shutdownCancel()
		if err := healthServer.Shutdown(shutdownCtx); err != nil {
			log.Errorf(shutdownCtx, "Health server shutdown error: %v", err)
		}
		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			log.Errorf(shutdownCtx, "Metrics server shutdown error: %v", err)
		}
	}()

	// Start sentinel
	log.Info(ctx, "Starting sentinel loop")
	if err := s.Start(ctx); err != nil && err != context.Canceled {
		return fmt.Errorf("sentinel failed: %w", err)
	}

	log.Info(ctx, "Sentinel stopped gracefully")
	return nil
}

// runConfigDump loads the full sentinel configuration and prints it as YAML to stdout.
func runConfigDump(configFile string, flags *pflag.FlagSet) error {
	cfg, err := config.LoadConfig(configFile, flags)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg.RedactedCopy())
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	fmt.Print(string(data))
	return nil
}
