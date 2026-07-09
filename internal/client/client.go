package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/cenkalti/backoff/v5"
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

const (
	DefaultPageSize int32 = 20
)

// HyperFleetClient wraps the HTTP client for the HyperFleet API
type HyperFleetClient struct {
	httpClient  *http.Client
	log         logger.HyperFleetLogger
	tokenSource *fileTokenSource
	baseURL     string
	userAgent   string
	pageSize    int32
}

// NewHyperFleetClient creates a new HyperFleet API client.
// sentinelName and version are used to build the User-Agent header sent with every request.
// tokenPath is optional; when non-empty the client reads a bearer token from that file and
// injects it as an Authorization header on every request. tokenCacheTTL controls how long
// the token is cached before the file is re-read; 0 disables caching and re-reads the file on every request.
func NewHyperFleetClient(
	endpoint string, timeout time.Duration, sentinelName, version string, pageSize int32,
	tokenPath string, tokenCacheTTL time.Duration,
) (*HyperFleetClient, error) {
	u, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: invalid endpoint URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("failed to create client: endpoint must use http or https scheme, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("failed to create client: endpoint must include a host")
	}
	if u.RawQuery != "" {
		return nil, fmt.Errorf("failed to create client: endpoint must not contain a query string")
	}

	httpClient := &http.Client{
		Timeout:   timeout,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}

	var ts *fileTokenSource
	if tokenPath != "" {
		ts = newFileTokenSource(tokenPath, tokenCacheTTL)
	}

	return &HyperFleetClient{
		httpClient:  httpClient,
		baseURL:     strings.TrimRight(endpoint, "/"),
		userAgent:   fmt.Sprintf("hyperfleet-sentinel/%s (%s)", version, sentinelName),
		log:         logger.NewHyperFleetLogger(),
		pageSize:    pageSize,
		tokenSource: ts,
	}, nil
}

// ObjectReference identifies a related resource
type ObjectReference struct {
	ID   string `json:"id"`
	Href string `json:"href"`
	Kind string `json:"kind"`
}

// Resource represents a HyperFleet resource (cluster, nodepool, or any generic entity)
type Resource struct {
	CreatedTime     time.Time                    `json:"created_time"`
	UpdatedTime     time.Time                    `json:"updated_time"`
	Labels          map[string]string            `json:"labels"`
	OwnerReferences *ObjectReference             `json:"owner_references,omitempty"`
	References      map[string][]ObjectReference `json:"references,omitempty"`
	Metadata        map[string]interface{}       `json:"metadata,omitempty"`
	Spec            map[string]interface{}       `json:"spec,omitempty"`
	ID              string                       `json:"id"`
	Href            string                       `json:"href"`
	Kind            string                       `json:"kind"`
	Name            string                       `json:"name"`
	Status          ResourceStatus               `json:"status"`
	Generation      int32                        `json:"generation"`
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

// ToMap converts the Resource into a plain map suitable for CEL evaluation or
// payload building. Generation is cast to int64 for CEL arithmetic. Status
// conditions are included as a nested map.
func (r *Resource) ToMap() map[string]interface{} {
	status := map[string]interface{}{}
	if len(r.Status.Conditions) > 0 {
		conditions := make([]interface{}, len(r.Status.Conditions))
		for i, c := range r.Status.Conditions {
			cond := map[string]interface{}{
				"type":                 c.Type,
				"status":               c.Status,
				"last_transition_time": c.LastTransitionTime.Format(time.RFC3339Nano),
				"last_updated_time":    c.LastUpdatedTime.Format(time.RFC3339Nano),
				"observed_generation":  c.ObservedGeneration,
			}
			if c.Reason != "" {
				cond["reason"] = c.Reason
			}
			if c.Message != "" {
				cond["message"] = c.Message
			}
			conditions[i] = cond
		}
		status["conditions"] = conditions
	}

	m := map[string]interface{}{
		"id":           r.ID,
		"href":         r.Href, //nolint:goconst // map key, not a magic string
		"kind":         r.Kind, //nolint:goconst // map key, not a magic string
		"name":         r.Name,
		"created_time": r.CreatedTime.Format(time.RFC3339Nano),
		"updated_time": r.UpdatedTime.Format(time.RFC3339Nano),
		"generation":   int64(r.Generation),
		"status":       status,
	}

	if len(r.Spec) > 0 {
		m["spec"] = r.Spec
	}

	if len(r.Labels) > 0 {
		labels := make(map[string]interface{}, len(r.Labels))
		for k, v := range r.Labels {
			labels[k] = v
		}
		m["labels"] = labels
	}

	if r.OwnerReferences != nil {
		m["owner_references"] = map[string]interface{}{
			"id":   r.OwnerReferences.ID,
			"href": r.OwnerReferences.Href,
			"kind": r.OwnerReferences.Kind,
		}
	}

	if len(r.References) > 0 {
		refs := make(map[string]interface{}, len(r.References))
		for key, refList := range r.References {
			converted := make([]interface{}, len(refList))
			for i, ref := range refList {
				converted[i] = map[string]interface{}{
					"id":   ref.ID,
					"href": ref.Href,
					"kind": ref.Kind,
				}
			}
			refs[key] = converted
		}
		m["references"] = refs
	}

	if r.Metadata != nil {
		m["metadata"] = r.Metadata
	}

	return m
}

// validateResourceType ensures resourceType is a safe, single URL path segment.
func validateResourceType(resourceType string) error {
	if resourceType == "" {
		return fmt.Errorf("resourceType cannot be empty")
	}
	if strings.TrimSpace(resourceType) != resourceType {
		return fmt.Errorf("resourceType must not contain leading or trailing whitespace")
	}
	if resourceType == "." || resourceType == ".." {
		return fmt.Errorf("resourceType must be a single URL path segment")
	}
	if strings.ContainsAny(resourceType, "/?#%\\") {
		return fmt.Errorf("resourceType must be a single URL path segment")
	}
	for _, r := range resourceType {
		if unicode.IsSpace(r) {
			return fmt.Errorf("resourceType must not contain whitespace")
		}
	}
	return nil
}

// FetchResources fetches resources from the HyperFleet API with retry logic.
//
// resourceType is the plural path segment (e.g. "clusters", "nodepools", "wifconfigs").
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
	resourceType string,
	labelSelector map[string]string,
	additionalFilters ...string,
) ([]Resource, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context cannot be nil")
	}

	if err := validateResourceType(resourceType); err != nil {
		return nil, err
	}

	b := backoff.NewExponentialBackOff()
	b.InitialInterval = DefaultInitialInterval
	b.MaxInterval = DefaultMaxInterval
	b.Multiplier = DefaultMultiplier
	b.RandomizationFactor = DefaultRandomizationFactor

	operation := func() ([]Resource, error) {
		resources, err := c.fetchResourcesOnce(ctx, resourceType, labelSelector, additionalFilters)
		if err != nil {
			if isRetriable(err) {
				c.log.Debugf(ctx, "Retriable error fetching %s: %v (will retry)", resourceType, err)
				return nil, err
			}
			c.log.Debugf(ctx, "Non-retriable error fetching %s: %v (will not retry)", resourceType, err)
			return nil, backoff.Permanent(err)
		}
		return resources, nil
	}

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

// setAuthHeader attaches the Authorization header to req if a token source is configured.
// Returns a *TokenError if the token cannot be read.
func (c *HyperFleetClient) setAuthHeader(req *http.Request) error {
	if c.tokenSource == nil {
		return nil
	}
	tok, err := c.tokenSource.get()
	if err != nil {
		return &TokenError{cause: err}
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

// VerifyConnectivity checks the client connectivity by calling the API for the given resource type
func (c *HyperFleetClient) VerifyConnectivity(ctx context.Context, resourceType string) error {
	if err := validateResourceType(resourceType); err != nil {
		return fmt.Errorf("could not verify connectivity: %w", err)
	}

	search := labelSelectorToSearchString(map[string]string{"non_existing_label": "value"})
	size := int32(1)

	reqURL := fmt.Sprintf("%s/api/hyperfleet/v1/%s?search=%s&size=%d",
		c.baseURL, resourceType, url.QueryEscape(search), size)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("could not verify connectivity: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	if authErr := c.setAuthHeader(req); authErr != nil {
		return fmt.Errorf("bearer token unavailable: %w", authErr)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("an error occurred while fetching %s: %w", resourceType, err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.log.Debugf(ctx, "failed to close response body: %v", closeErr)
		}
	}()

	if resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("could not verify connectivity: response status code %d", resp.StatusCode)
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
		escapedValue := strings.ReplaceAll(v, "'", "''")
		parts = append(parts, fmt.Sprintf("labels.%s='%s'", k, escapedValue))
	}
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

func (c *HyperFleetClient) fetchResourcesOnce(
	ctx context.Context,
	resourceType string,
	labelSelector map[string]string,
	additionalFilters []string,
) ([]Resource, error) {
	searchParam := buildSearchString(labelSelector, additionalFilters)
	return c.fetchResources(ctx, resourceType, searchParam)
}

func (c *HyperFleetClient) fetchResources(ctx context.Context, resourceType, searchParam string) ([]Resource, error) {
	return fetchPaginated(ctx, c, searchParam,
		func(ctx context.Context, page, pageSize int32, search string) ([]openapi.Resource, int64, error) {
			return c.fetchResourcesPage(ctx, resourceType, page, pageSize, search)
		},
		convertResource, resourceType)
}

// fetchPaginated iterates through all pages of an API endpoint, collecting
// resources until every item has been fetched.
func fetchPaginated[T any](
	ctx context.Context,
	c *HyperFleetClient,
	searchParam string,
	fetchPage func(ctx context.Context, page, pageSize int32, searchParam string) ([]T, int64, error),
	convert func(T) Resource,
	resourceLabel string,
) ([]Resource, error) {
	var allResources []Resource
	page := int32(1)

	for {
		items, total, err := fetchPage(ctx, page, c.pageSize, searchParam)
		if err != nil {
			return nil, err
		}

		if allResources == nil {
			allResources = make([]Resource, 0, len(items))
		}

		for _, item := range items {
			allResources = append(allResources, convert(item))
		}

		c.log.Debugf(ctx, "Fetched %s page=%d size=%d total=%d", resourceLabel, page, len(items), total)

		if int64(len(allResources)) >= total || len(items) == 0 {
			break
		}
		page++
	}

	return allResources, nil
}

func (c *HyperFleetClient) fetchResourcesPage(
	ctx context.Context, resourceType string, page, pageSize int32, searchParam string,
) ([]openapi.Resource, int64, error) {
	reqURL := fmt.Sprintf("%s/api/hyperfleet/v1/%s?page=%d&size=%d",
		c.baseURL, resourceType, page, pageSize)
	if searchParam != "" {
		reqURL += "&search=" + url.QueryEscape(searchParam)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, 0, &APIError{StatusCode: 0, Message: fmt.Sprintf("failed to create request: %v", err), Retriable: false}
	}
	req.Header.Set("User-Agent", c.userAgent)
	if authErr := c.setAuthHeader(req); authErr != nil {
		return nil, 0, &APIError{StatusCode: 0, Message: authErr.Error(), Retriable: false, cause: authErr}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, wrapNetworkError(err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.log.Debugf(ctx, "failed to close response body: %v", closeErr)
		}
	}()

	if httpErr := checkHTTPStatus(resp); httpErr != nil {
		return nil, 0, httpErr
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		msg := fmt.Sprintf("failed to read response body: %v", err)
		return nil, 0, &APIError{StatusCode: 0, Message: msg, Retriable: false}
	}

	var resourceList openapi.ResourceList
	if err := json.Unmarshal(body, &resourceList); err != nil {
		msg := fmt.Sprintf("failed to decode response: %v", err)
		return nil, 0, &APIError{StatusCode: 0, Message: msg, Retriable: false}
	}

	return resourceList.Items, resourceList.Total, nil
}

func convertResource(item openapi.Resource) Resource {
	href := ""
	if item.Href != nil {
		href = *item.Href
	}

	resource := Resource{
		ID:          item.Id,
		Href:        href,
		Kind:        item.Kind,
		Name:        item.Name,
		Generation:  item.Generation,
		CreatedTime: item.CreatedTime,
		UpdatedTime: item.UpdatedTime,
		Spec:        item.Spec,
		Status:      ResourceStatus{},
	}

	if item.Labels != nil {
		resource.Labels = *item.Labels
	}

	if item.OwnerReferences != nil {
		ref := &ObjectReference{Kind: item.OwnerReferences.Kind}
		if item.OwnerReferences.Id != nil {
			ref.ID = *item.OwnerReferences.Id
		}
		if item.OwnerReferences.Href != nil {
			ref.Href = *item.OwnerReferences.Href
		}
		if ref.ID != "" || ref.Href != "" || ref.Kind != "" {
			resource.OwnerReferences = ref
		}
	}

	if item.References != nil {
		resource.References = make(map[string][]ObjectReference, len(*item.References))
		for key, refs := range *item.References {
			converted := make([]ObjectReference, len(refs))
			for i, r := range refs {
				ref := ObjectReference{Kind: r.Kind}
				if r.Id != nil {
					ref.ID = *r.Id
				}
				if r.Href != nil {
					ref.Href = *r.Href
				}
				converted[i] = ref
			}
			resource.References[key] = converted
		}
	}

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

	return resource
}

// wrapNetworkError wraps a transport-level error into an APIError with retry metadata.
func wrapNetworkError(err error) *APIError {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return &APIError{StatusCode: 0, Message: "request timeout", Retriable: true}
	}
	return &APIError{StatusCode: 0, Message: fmt.Sprintf("network error: %v", err), Retriable: true}
}

// checkHTTPStatus validates the HTTP response status code and returns an
// APIError for error status codes (>= 400).
func checkHTTPStatus(resp *http.Response) error {
	if resp != nil && resp.StatusCode >= 400 {
		return &APIError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("API request failed with status %d", resp.StatusCode),
			Retriable:  isHTTPStatusRetriable(resp.StatusCode),
		}
	}
	return nil
}

// APIError represents an API error with retry information
type APIError struct {
	cause      error
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

func (e *APIError) Unwrap() error { return e.cause }

func isRetriable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retriable
	}
	return false
}

func isHTTPStatusRetriable(statusCode int) bool {
	if statusCode >= 500 && statusCode < 600 {
		return true
	}
	if statusCode == http.StatusTooManyRequests {
		return true
	}
	if statusCode == http.StatusRequestTimeout {
		return true
	}
	return false
}
