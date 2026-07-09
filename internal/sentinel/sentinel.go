package sentinel

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"
	"github.com/openshift-hyperfleet/hyperfleet-broker/broker"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/client"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/config"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/engine"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/metrics"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/payload"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/pkg/logger"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/pkg/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// otelMessagingSystem maps broker type identifiers to OTel semantic convention values
var otelMessagingSystem = map[string]string{
	"googlepubsub": "gcp_pubsub",
}

func brokerTypeToOTel(brokerType string) string {
	if v, ok := otelMessagingSystem[brokerType]; ok {
		return v
	}
	return brokerType
}

// Sentinel polls the HyperFleet API and triggers reconciliation events
type Sentinel struct {
	lastSuccessfulPoll time.Time
	publisher          broker.Publisher
	logger             logger.HyperFleetLogger
	config             *config.SentinelConfig
	client             *client.HyperFleetClient
	decisionEngine     *engine.DecisionEngine
	payloadBuilder     *payload.Builder
	mu                 sync.RWMutex
}

// NewSentinel creates a new sentinel
func NewSentinel(
	cfg *config.SentinelConfig,
	client *client.HyperFleetClient,
	decisionEngine *engine.DecisionEngine,
	pub broker.Publisher,
	log logger.HyperFleetLogger,
) (*Sentinel, error) {
	s := &Sentinel{
		config:         cfg,
		client:         client,
		decisionEngine: decisionEngine,
		publisher:      pub,
		logger:         log,
	}

	if cfg.MessageData != nil {
		builder, err := payload.NewBuilder(cfg.MessageData, log)
		if err != nil {
			return nil, fmt.Errorf("failed to create payload builder: %w", err)
		}
		s.payloadBuilder = builder
	}

	return s, nil
}

func (s *Sentinel) LastSuccessfulPoll() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastSuccessfulPoll
}

// Start starts the polling loop
func (s *Sentinel) Start(ctx context.Context) error {
	s.logger.Infof(ctx, "Starting sentinel resource_type=%s poll_interval=%s",
		s.config.ResourceType, s.config.PollInterval)

	ticker := time.NewTicker(s.config.PollInterval)
	defer ticker.Stop()

	// Run immediately on start
	if err := s.trigger(ctx); err != nil {
		s.logger.Errorf(ctx, "Initial trigger failed: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			s.logger.Info(ctx, "Stopping sentinel due to context cancellation")
			return ctx.Err()
		case <-ticker.C:
			if err := s.trigger(ctx); err != nil {
				s.logger.Errorf(ctx, "Trigger failed: %v", err)
			}
		}
	}
}

// trigger checks resources and publishes events to trigger reconciliation
func (s *Sentinel) trigger(ctx context.Context) error {
	startTime := time.Now()

	// span: sentinel.poll
	ctx, pollSpan := telemetry.StartSpan(ctx, "sentinel.poll",
		attribute.String("hyperfleet.resource_type", s.config.ResourceType))
	defer pollSpan.End()

	// Get metric labels
	resourceType := s.config.ResourceType
	resourceSelector := metrics.GetResourceSelectorLabel(s.config.ResourceSelector)
	topic := ""
	if s.config.Clients.Broker != nil {
		topic = s.config.Clients.Broker.Topic
	}

	// Add subset to context for structured logging
	ctx = logger.WithSubset(ctx, resourceType)
	ctx = logger.WithTopic(ctx, topic)

	s.logger.Debug(ctx, "Starting trigger cycle")

	// Convert label selectors to map for filtering
	labelSelector := s.config.ResourceSelector.ToMap()

	// Fetch all resources matching label selectors.
	// TODO(HYPERFLEET-805): Add optional server_filters config for server-side pre-filtering
	// to reduce the result set before CEL evaluation. Currently fetches the full result set
	// and evaluates each resource in-memory. At large scale, use resource_selector labels
	// to shard across multiple Sentinel instances.
	resources, err := s.client.FetchResources(ctx, s.config.ResourceType, labelSelector)
	if err != nil {
		// Record API error
		pollSpan.RecordError(err)
		pollSpan.SetStatus(codes.Error, "fetch resources failed")
		errorType := "fetch_error"
		if client.IsTokenError(err) {
			errorType = "auth_error"
		}
		metrics.UpdateAPIErrorsMetric(resourceType, resourceSelector, errorType)
		return fmt.Errorf("failed to fetch resources: %w", err)
	}

	s.logger.Infof(ctx, "Fetched resources count=%d label_selectors=%d", len(resources), len(s.config.ResourceSelector))

	now := time.Now()
	published := 0
	skipped := 0
	pending := 0

	// Evaluate each resource
	for i := range resources {
		resource := &resources[i]
		// span: sentinel.evaluate
		evalCtx, evalSpan := telemetry.StartSpan(ctx, "sentinel.evaluate",
			attribute.String("hyperfleet.resource_type", s.config.ResourceType),
			attribute.String("hyperfleet.resource_id", resource.ID),
		)

		if resource.ID == "" {
			s.logger.Warnf(ctx, "Skipping resource with empty ID kind=%s", resource.Kind)
			evalSpan.End()
			continue
		}

		decision := s.decisionEngine.Evaluate(resource, now)
		evalSpan.SetAttributes(attribute.String("hyperfleet.decision_reason", decision.Reason))

		if decision.ShouldPublish {
			pending++

			// Add decision reason to context for structured logging
			eventCtx := logger.WithDecisionReason(evalCtx, decision.Reason)

			eventData := s.buildEventData(eventCtx, resource, decision)

			// Create CloudEvent
			event := cloudevents.NewEvent()
			event.SetSpecVersion(cloudevents.VersionV1)
			event.SetType(fmt.Sprintf("com.redhat.hyperfleet.%s.reconcile", strings.ToLower(resource.Kind)))
			event.SetSource("hyperfleet-sentinel")

			// Generate UUID v7 for event ID
			eventID, err := uuid.NewV7()
			if err != nil {
				s.logger.Errorf(eventCtx, "Failed to generate UUID v7 for event ID resource_id=%s error=%v", resource.ID, err)
				evalSpan.RecordError(err)
				evalSpan.SetStatus(codes.Error, "generate event ID failed")
				evalSpan.End()
				continue
			}
			event.SetID(eventID.String())

			if err := event.SetData(cloudevents.ApplicationJSON, eventData); err != nil {
				s.logger.Errorf(eventCtx, "Failed to set event data resource_id=%s error=%v", resource.ID, err)
				evalSpan.RecordError(err)
				evalSpan.SetStatus(codes.Error, "set event data failed")
				evalSpan.End()
				continue
			}

			// span: publish (child of sentinel.evaluate)
			publishCtx, publishSpan := telemetry.StartSpan(eventCtx, fmt.Sprintf("%s publish", topic),
				attribute.String("messaging.system", brokerTypeToOTel(s.publisher.BrokerType())),
				attribute.String("messaging.operation.type", "publish"),
				attribute.String("messaging.destination.name", topic),
				attribute.String("messaging.message.id", event.ID()),
			)

			if publishSpan.SpanContext().IsValid() {
				telemetry.SetTraceContext(&event, publishSpan)
			}

			// Publish to broker using configured topic
			if err := s.publisher.Publish(publishCtx, topic, &event); err != nil {
				publishSpan.RecordError(err)
				publishSpan.SetStatus(codes.Error, "publish failed")
				// Record broker error
				metrics.UpdateBrokerErrorsMetric(resourceType, resourceSelector, "publish_error")
				s.logger.Errorf(publishCtx, "Failed to publish event resource_id=%s error=%v", resource.ID, err)
				publishSpan.End()
				evalSpan.End()
				continue
			}

			publishSpan.End()

			// Record successful event publication
			metrics.UpdateEventsPublishedMetric(resourceType, resourceSelector, decision.Reason)

			s.logger.Infof(eventCtx, "Published event resource_id=%s",
				resource.ID)
			published++
		} else {
			// Add decision reason to context for structured logging
			skipCtx := logger.WithDecisionReason(evalCtx, decision.Reason)

			// Record skipped resource
			metrics.UpdateResourcesSkippedMetric(resourceType, resourceSelector, decision.Reason)

			s.logger.Debugf(skipCtx, "Skipped resource resource_id=%s",
				resource.ID)
			skipped++
		}

		evalSpan.End()
	}

	// Record pending resources count
	metrics.UpdatePendingResourcesMetric(resourceType, resourceSelector, pending)

	// Record poll duration
	duration := time.Since(startTime).Seconds()
	metrics.UpdatePollDurationMetric(resourceType, resourceSelector, duration)

	s.logger.Infof(ctx, "Trigger cycle completed total=%d published=%d skipped=%d duration=%.3fs",
		len(resources), published, skipped, duration)

	s.mu.Lock()
	s.lastSuccessfulPoll = time.Now()
	s.mu.Unlock()
	metrics.UpdateLastSuccessfulPollTimestampMetric()

	return nil
}

// buildEventData builds the CloudEvent data payload for a resource using the
// configured payload builder.
func (s *Sentinel) buildEventData(
	ctx context.Context,
	resource *client.Resource,
	decision engine.Decision,
) map[string]interface{} {
	if s.payloadBuilder == nil {
		s.logger.Errorf(ctx, "payload builder not initialized for resource_id=%s", resource.ID)
		return map[string]interface{}{}
	}
	return s.payloadBuilder.BuildPayload(ctx, resource, decision.Reason)
}
