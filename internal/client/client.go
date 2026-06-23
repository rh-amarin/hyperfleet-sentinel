package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/auth"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/pkg/api/openapi"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/pkg/logger"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Retry configuration constants
const (
	// DefaultInitialInterval is the initial retry interval
	DefaultInitialInterval = 500 * time.Millisecond
	// DefaultMaxInterval is the maximum retry interval
	DefaultMaxInterval = 8 * time.Second
	// DefaultMaxElapsedTime is the maximum total time for retries
	DefaultMaxElapsedTime = 30 * time.Second
	// DefaultMultiplier is the backoff multiplier
	DefaultMultiplier = 2.0
	// DefaultRandomizationFactor adds jitter to retry intervals
	DefaultRandomizationFactor = 0.1
)

// ResourceType represents the type of HyperFleet resource
type ResourceType string

// Resource type constants
const (
	// ResourceTypeClusters represents cluster resources
	ResourceTypeClusters ResourceType = "clusters"
	// ResourceTypeNodePools represents nodepool resources
	ResourceTypeNodePools ResourceType = "nodepools"
)

// HyperFleetClient wraps the OpenAPI-generated client
type HyperFleetClient struct {
	apiClient     *openapi.ClientWithResponses
	log           logger.HyperFleetLogger
	tokenProvider auth.TokenProvider // nil = no Authorization header
}

// ClientOption configures a HyperFleetClient.
type ClientOption func(*HyperFleetClient)

// WithTokenProvider sets a token provider that injects Authorization: Bearer <token>
// on every outbound request.
func WithTokenProvider(p auth.TokenProvider) ClientOption {
	return func(c *HyperFleetClient) { c.tokenProvider = p }
}

// NewHyperFleetClient creates a new HyperFleet API client using OpenAPI-generated client.
// sentinelName and version are used to build the User-Agent header sent with every request.
// Optional ClientOption values (e.g. WithTokenProvider) configure additional behavior.
func NewHyperFleetClient(
	endpoint string, timeout time.Duration, sentinelName, version string,
	opts ...ClientOption,
) (*HyperFleetClient, error) {
	hc := &HyperFleetClient{log: logger.NewHyperFleetLogger()}
	for _, opt := range opts {
		opt(hc)
	}
	provider := hc.tokenProvider // captured for closure; nil = no auth

	userAgent := fmt.Sprintf("hyperfleet-sentinel/%s (%s)", version, sentinelName)

	client, err := openapi.NewClientWithResponses(endpoint,
		openapi.WithHTTPClient(&http.Client{
			Timeout:   timeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		}),
		openapi.WithRequestEditorFn(func(ctx context.Context, req *http.Request) error {
			req.Header.Set("User-Agent", userAgent)
			if provider != nil {
				token, tokenErr := provider.GetToken(ctx)
				if tokenErr != nil {
					return fmt.Errorf("failed to get authorization token: %w", tokenErr)
				}
				req.Header.Set("Authorization", "Bearer "+token)
			}
			return nil
		}),
	)
	if err != nil {
		// This should only fail if the endpoint URL is invalid
		return nil, fmt.Errorf("failed to create OpenAPI client: %v", err)
	}

	hc.apiClient = client
	return hc, nil
}

// OwnerReference identifies the owner of a resource
type OwnerReference struct {
	ID   string `json:"id"`
	Href string `json:"href"`
	Kind string `json:"kind"`
}

// Resource represents a HyperFleet resource (cluster, nodepool, etc.)
type Resource struct {
	CreatedTime     time.Time              `json:"created_time"`
	UpdatedTime     time.Time              `json:"updated_time"`
	Labels          map[string]string      `json:"labels"`
	OwnerReferences *OwnerReference        `json:"owner_references,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	ID              string                 `json:"id"`
	Href            string                 `json:"href"`
	Kind            string                 `json:"kind"`
	Status          ResourceStatus         `json:"status"`
	Generation      int32                  `json:"generation"`
}

// ResourceStatus represents the status of a resource.
// All status data is accessed through Conditions only.
type ResourceStatus struct {
	Conditions []Condition `json:"conditions,omitempty"`
}

// Condition represents a status condition
type Condition struct {
	LastTransitionTime time.Time `json:"lastTransitionTime"`
	LastUpdatedTime    time.Time `json:"lastUpdatedTime"`
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason,omitempty"`
	Message            string    `json:"message,omitempty"`
	ObservedGeneration int32     `json:"observedGeneration"`
}

// FetchResources fetches resources from the HyperFleet API with retry logic.
//
// Retry behavior:
//   - Automatically retries on transient failures (5xx, timeouts, network errors)
//   - Does NOT retry on client errors (4xx) as they are not retriable
//
// Graceful degradation:
//   - Resources with nil status are logged and skipped
//   - This maintains service availability during resource provisioning/deletion
//   - Only resources with valid status are returned
//
// The additionalFilters parameter accepts optional TSL condition expressions
// (e.g., "status.conditions.Reconciled='False'") that are combined with label
// selectors using "and" to form the final search query.
//
// Returns a slice of resources and an error if the fetch operation fails.
func (c *HyperFleetClient) FetchResources(
	ctx context.Context,
	resourceType ResourceType,
	labelSelector map[string]string,
	additionalFilters ...string,
) ([]Resource, error) {
	// Validate inputs
	if ctx == nil {
		return nil, fmt.Errorf("context cannot be nil")
	}

	// Validate resourceType against known types
	switch resourceType {
	case ResourceTypeClusters, ResourceTypeNodePools:
		// Valid type
	default:
		return nil, fmt.Errorf("invalid resourceType: %q (must be one of: %q, %q)",
			resourceType, ResourceTypeClusters, ResourceTypeNodePools)
	}

	// Configure exponential backoff
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = DefaultInitialInterval
	b.MaxInterval = DefaultMaxInterval
	b.Multiplier = DefaultMultiplier
	b.RandomizationFactor = DefaultRandomizationFactor

	// Retry operation with backoff (v5 API)
	operation := func() ([]Resource, error) {
		resources, err := c.fetchResourcesOnce(ctx, resourceType, labelSelector, additionalFilters)
		if err != nil {
			// Check if error is retriable
			if isRetriable(err) {
				c.log.Debugf(ctx, "Retriable error fetching %s: %v (will retry)", resourceType, err)
				return nil, err // Retry
			}
			// Non-retriable error - stop retrying
			c.log.Debugf(ctx, "Non-retriable error fetching %s: %v (will not retry)", resourceType, err)
			return nil, backoff.Permanent(err)
		}
		return resources, nil
	}

	// Execute with retry using v5 API
	// Note: MaxElapsedTime is now a Retry option, not a BackOff field
	resources, err := backoff.Retry(
		ctx,
		operation,
		backoff.WithBackOff(b),
		backoff.WithMaxElapsedTime(DefaultMaxElapsedTime),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s after retries: %w", resourceType, err)
	}

	return resources, nil
}

// VerifyConnectivity checks the client connectivity by calling the /clusters endpoint
func (c *HyperFleetClient) VerifyConnectivity(ctx context.Context) error {
	params := &openapi.GetClustersParams{}
	search := labelSelectorToSearchString(map[string]string{"non_existing_label": "value"})
	params.Search = &search

	response, err := c.apiClient.GetClustersWithResponse(ctx, params)
	if err != nil {
		return fmt.Errorf("an error occurred while fetching clusters: %w", err)
	}
	if response == nil {
		return fmt.Errorf("could not verify connectivity: received nil response")
	}
	if response.StatusCode() == http.StatusOK {
		return nil
	}
	return fmt.Errorf("could not verify connectivity: response status code %d", response.StatusCode())
}

// labelSelectorToSearchString converts a label selector map to TSL (Tree Search Language) search parameter string
// Format: "labels.key1='value1' and labels.key2='value2'"
// TSL syntax requires:
// - Label keys prefixed with "labels."
// - Values quoted with single quotes (single quotes in values are escaped by doubling)
// - Multiple conditions joined with " and "
func labelSelectorToSearchString(labelSelector map[string]string) string {
	if len(labelSelector) == 0 {
		return ""
	}

	parts := make([]string, 0, len(labelSelector))
	for k, v := range labelSelector {
		// Escape single quotes by doubling them ('' is the TSL escape sequence for a literal ')
		escapedValue := strings.ReplaceAll(v, "'", "''")
		parts = append(parts, fmt.Sprintf("labels.%s='%s'", k, escapedValue))
	}
	// Sort for deterministic output in tests
	sort.Strings(parts)
	return strings.Join(parts, " and ")
}

// buildSearchString combines label selectors and additional TSL filters into a
// single search query string. Label selectors are converted to TSL format
// (e.g., "labels.key='value'") and joined with additional filters using "and".
func buildSearchString(labelSelector map[string]string, additionalFilters []string) string {
	parts := make([]string, 0, len(additionalFilters)+1)

	labelSearch := labelSelectorToSearchString(labelSelector)
	if labelSearch != "" {
		parts = append(parts, labelSearch)
	}

	for _, f := range additionalFilters {
		if f != "" {
			parts = append(parts, f)
		}
	}

	return strings.Join(parts, " and ")
}

// fetchResourcesOnce performs a single fetch operation without retry logic
func (c *HyperFleetClient) fetchResourcesOnce(
	ctx context.Context,
	resourceType ResourceType,
	labelSelector map[string]string,
	additionalFilters []string,
) ([]Resource, error) {
	// Build search parameter from label selector and additional filters
	searchParam := buildSearchString(labelSelector, additionalFilters)

	// Call appropriate endpoint based on resource type
	switch resourceType {
	case ResourceTypeClusters:
		return c.fetchClusters(ctx, searchParam)
	case ResourceTypeNodePools:
		return c.fetchNodePools(ctx, searchParam)
	default:
		return nil, fmt.Errorf("unsupported resource type: %s", resourceType)
	}
}

// fetchClusters fetches cluster resources from the API
func (c *HyperFleetClient) fetchClusters(ctx context.Context, searchParam string) ([]Resource, error) {
	params := &openapi.GetClustersParams{}
	if searchParam != "" {
		search := searchParam
		params.Search = &search
	}

	response, err := c.apiClient.GetClustersWithResponse(ctx, params)
	if err != nil {
		// Network/timeout error - use errors.As for proper error unwrapping
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Timeout() {
			return nil, &APIError{
				StatusCode: 0,
				Message:    "request timeout",
				Retriable:  true,
			}
		}
		return nil, &APIError{
			StatusCode: 0,
			Message:    fmt.Sprintf("network error: %v", err),
			Retriable:  true, // Assume network errors are retriable
		}
	}

	// Check HTTP response status
	if response.HTTPResponse != nil && response.HTTPResponse.StatusCode >= 400 {
		return nil, &APIError{
			StatusCode: response.HTTPResponse.StatusCode,
			Message:    fmt.Sprintf("API request failed with status %d", response.HTTPResponse.StatusCode),
			Retriable:  isHTTPStatusRetriable(response.HTTPResponse.StatusCode),
		}
	}

	// Nil check for response body
	if response.JSON200 == nil {
		return nil, &APIError{
			StatusCode: 0,
			Message:    "received nil response from API",
			Retriable:  false,
		}
	}

	resourceList := response.JSON200

	// Convert OpenAPI models to internal models
	resources := make([]Resource, 0, len(resourceList.Items))
	for _, item := range resourceList.Items {
		// Get ID and Kind with defaults for optional pointer fields
		id := ""
		if item.Id != nil {
			id = *item.Id
		}
		href := ""
		if item.Href != nil {
			href = *item.Href
		}
		kind := ""
		if item.Kind != nil {
			kind = *item.Kind
		}

		resource := Resource{
			ID:          id,
			Href:        href,
			Kind:        kind,
			Generation:  item.Generation,
			CreatedTime: item.CreatedTime,
			UpdatedTime: item.UpdatedTime,
			Status:      ResourceStatus{},
		}

		// Handle optional labels
		if item.Labels != nil {
			resource.Labels = *item.Labels
		}

		// Convert conditions from OpenAPI model
		if len(item.Status.Conditions) > 0 {
			resource.Status.Conditions = make([]Condition, 0, len(item.Status.Conditions))
			for _, cond := range item.Status.Conditions {
				condition := Condition{
					Type:               cond.Type,
					Status:             string(cond.Status),
					LastTransitionTime: cond.LastTransitionTime,
					LastUpdatedTime:    cond.LastUpdatedTime,
					ObservedGeneration: cond.ObservedGeneration,
				}
				if cond.Reason != nil {
					condition.Reason = *cond.Reason
				}
				if cond.Message != nil {
					condition.Message = *cond.Message
				}
				resource.Status.Conditions = append(resource.Status.Conditions, condition)
			}
		}

		resources = append(resources, resource)
	}

	return resources, nil
}

// fetchNodePools fetches nodepool resources from the API
func (c *HyperFleetClient) fetchNodePools(ctx context.Context, searchParam string) ([]Resource, error) {
	params := &openapi.GetNodePoolsParams{}
	if searchParam != "" {
		search := searchParam
		params.Search = &search
	}

	response, err := c.apiClient.GetNodePoolsWithResponse(ctx, params)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Timeout() {
			return nil, &APIError{
				StatusCode: 0,
				Message:    "request timeout",
				Retriable:  true,
			}
		}
		return nil, &APIError{
			StatusCode: 0,
			Message:    fmt.Sprintf("network error: %v", err),
			Retriable:  true,
		}
	}

	// Check HTTP response status
	if response.HTTPResponse != nil && response.HTTPResponse.StatusCode >= 400 {
		return nil, &APIError{
			StatusCode: response.HTTPResponse.StatusCode,
			Message:    fmt.Sprintf("API request failed with status %d", response.HTTPResponse.StatusCode),
			Retriable:  isHTTPStatusRetriable(response.HTTPResponse.StatusCode),
		}
	}

	if response.JSON200 == nil {
		return nil, &APIError{
			StatusCode: 0,
			Message:    "received nil response from API",
			Retriable:  false,
		}
	}

	resourceList := response.JSON200

	// Convert OpenAPI models to internal models
	resources := make([]Resource, 0, len(resourceList.Items))
	for _, item := range resourceList.Items {
		// Get ID and Href with defaults for optional pointer fields
		id := ""
		if item.Id != nil {
			id = *item.Id
		}
		href := ""
		if item.Href != nil {
			href = *item.Href
		}
		kind := ""
		if item.Kind != nil {
			kind = *item.Kind
		}

		resource := Resource{
			ID:          id,
			Href:        href,
			Kind:        kind,
			Generation:  item.Generation,
			CreatedTime: item.CreatedTime,
			UpdatedTime: item.UpdatedTime,
			Status:      ResourceStatus{},
		}

		// Handle optional labels
		if item.Labels != nil {
			resource.Labels = *item.Labels
		}

		// Map owner references
		ownerRef := item.OwnerReferences
		ref := &OwnerReference{}
		if ownerRef.Id != nil {
			ref.ID = *ownerRef.Id
		}
		if ownerRef.Href != nil {
			ref.Href = *ownerRef.Href
		}
		if ownerRef.Kind != nil {
			ref.Kind = *ownerRef.Kind
		}
		if ref.ID != "" || ref.Href != "" || ref.Kind != "" {
			resource.OwnerReferences = ref
		}

		// Convert conditions from OpenAPI model
		if len(item.Status.Conditions) > 0 {
			resource.Status.Conditions = make([]Condition, 0, len(item.Status.Conditions))
			for _, cond := range item.Status.Conditions {
				condition := Condition{
					Type:               cond.Type,
					Status:             string(cond.Status),
					LastTransitionTime: cond.LastTransitionTime,
					LastUpdatedTime:    cond.LastUpdatedTime,
					ObservedGeneration: cond.ObservedGeneration,
				}
				if cond.Reason != nil {
					condition.Reason = *cond.Reason
				}
				if cond.Message != nil {
					condition.Message = *cond.Message
				}
				resource.Status.Conditions = append(resource.Status.Conditions, condition)
			}
		}

		resources = append(resources, resource)
	}

	return resources, nil
}

// APIError represents an API error with retry information
type APIError struct {
	Message    string
	StatusCode int
	Retriable  bool
}

func (e *APIError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("API error (status %d): %s", e.StatusCode, e.Message)
	}
	return e.Message
}

// isRetriable determines if an error should be retried
// Uses errors.As for proper error unwrapping (Go 1.13+)
func isRetriable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retriable
	}
	// Unknown errors are not retriable by default
	return false
}

// isHTTPStatusRetriable determines if an HTTP status code is retriable
func isHTTPStatusRetriable(statusCode int) bool {
	// 5xx server errors are retriable
	if statusCode >= 500 && statusCode < 600 {
		return true
	}
	// 429 Too Many Requests is retriable
	if statusCode == http.StatusTooManyRequests {
		return true
	}
	// 408 Request Timeout is retriable
	if statusCode == http.StatusRequestTimeout {
		return true
	}
	// 4xx client errors are NOT retriable (except 408 and 429 above)
	// 2xx and 3xx are successful, no retry needed
	return false
}
