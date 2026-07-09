package payload

import (
	"context"
	"testing"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/client"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/pkg/logger"
)

const (
	testClusterID    = "cls-abc"
	testResourceKind = "Cluster"
)

// ============================================================================
// ParseValueDef Tests
// ============================================================================

func TestParseValueDef_StringBecomesExpression(t *testing.T) {
	vd, err := ParseValueDef("resource.id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vd.Expression != "resource.id" {
		t.Errorf("expected expression 'resource.id', got %q", vd.Expression)
	}
	if vd.Literal != nil || vd.Children != nil {
		t.Errorf("unexpected non-zero fields: %+v", vd)
	}
}

func TestParseValueDef_LiteralBool(t *testing.T) {
	vd, err := ParseValueDef(true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vd.Literal != true {
		t.Errorf("expected literal true, got %v", vd.Literal)
	}
}

func TestParseValueDef_NilReturnsError(t *testing.T) {
	_, err := ParseValueDef(nil)
	if err == nil {
		t.Fatal("expected error for nil value, got nil")
	}
}

func TestParseValueDef_NestedMap(t *testing.T) {
	raw := map[string]interface{}{
		"id":   "resource.id",
		"kind": "resource.kind",
	}
	vd, err := ParseValueDef(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vd.Children == nil {
		t.Fatal("expected non-nil Children")
	}
	if len(vd.Children) != 2 {
		t.Errorf("expected 2 children, got %d", len(vd.Children))
	}
}

func TestParseValueDef_EmptyStringReturnsExpressionSet(t *testing.T) {
	vd, err := ParseValueDef("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !vd.ExpressionSet {
		t.Error("expected ExpressionSet=true for empty string input")
	}
	if vd.Expression != "" {
		t.Errorf("expected empty Expression, got %q", vd.Expression)
	}
}

func TestParseValueDef_UnknownType(t *testing.T) {
	_, err := ParseValueDef([]string{"a", "b"})
	if err == nil {
		t.Fatal("expected error for slice type, got nil")
	}
}

func TestNewBuilder_EmptyExpressionReturnsError(t *testing.T) {
	// Mirrors YAML: `id: ""` — an explicitly-set empty CEL expression.
	buildDef := map[string]interface{}{
		"id": "",
	}
	_, err := NewBuilder(buildDef, logger.NewHyperFleetLogger())
	if err == nil {
		t.Fatal("expected error for empty CEL expression, got nil")
	}
}

func TestNewBuilder_NilValueReturnsError(t *testing.T) {
	// Mirrors YAML: `id: ` — an unset leaf that YAML decodes as nil.
	buildDef := map[string]interface{}{
		"id": nil,
	}
	_, err := NewBuilder(buildDef, logger.NewHyperFleetLogger())
	if err == nil {
		t.Fatal("expected error for nil value in build definition, got nil")
	}
}

// ============================================================================
// BuildPayload Tests
// ============================================================================

func makeTestResource() *client.Resource {
	return &client.Resource{
		ID:         testClusterID,
		Kind:       testResourceKind,
		Href:       "/api/v1/clusters/cls-abc",
		Generation: 3,
		Status: client.ResourceStatus{
			Conditions: []client.Condition{
				{
					Type:            "Reconciled",
					Status:          "True",
					LastUpdatedTime: time.Now(),
				},
			},
		},
		Labels:      map[string]string{"region": "us-east"},
		CreatedTime: time.Now(),
		UpdatedTime: time.Now(),
	}
}

func makeTestNodePoolResource() *client.Resource {
	return &client.Resource{
		ID:         "np-xyz",
		Kind:       "NodePool",
		Href:       "/api/v1/nodepools/np-xyz",
		Generation: 2,
		Status: client.ResourceStatus{
			Conditions: []client.Condition{
				{
					Type:            "Reconciled",
					Status:          "True",
					LastUpdatedTime: time.Now(),
				},
			},
		},
		OwnerReferences: &client.ObjectReference{
			ID:   "cluster-123",
			Href: "/api/v1/clusters/cluster-123",
			Kind: testResourceKind,
		},
		CreatedTime: time.Now(),
		UpdatedTime: time.Now(),
	}
}

func TestBuildPayload_FlatFields(t *testing.T) {
	buildDef := map[string]interface{}{
		"id":   "resource.id",
		"kind": "resource.kind",
	}
	b, err := NewBuilder(buildDef, logger.NewHyperFleetLogger())
	if err != nil {
		t.Fatalf("NewBuilder failed: %v", err)
	}

	resource := makeTestResource()
	payload := b.BuildPayload(context.Background(), resource, "")

	if payload["id"] != testClusterID {
		t.Errorf("expected id %q, got %v", testClusterID, payload["id"])
	}
	if payload["kind"] != testResourceKind {
		t.Errorf("expected kind %q, got %v", testResourceKind, payload["kind"])
	}
}

func TestBuildPayload_NestedObject(t *testing.T) {
	buildDef := map[string]interface{}{
		"meta": map[string]interface{}{
			"id":   "resource.id",
			"kind": "resource.kind",
		},
	}
	b, err := NewBuilder(buildDef, logger.NewHyperFleetLogger())
	if err != nil {
		t.Fatalf("NewBuilder failed: %v", err)
	}

	payload := b.BuildPayload(context.Background(), makeTestResource(), "")

	nested, ok := payload["meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'meta' to be a map, got %T", payload["meta"])
	}
	if nested["id"] != testClusterID {
		t.Errorf("expected nested id %q, got %v", testClusterID, nested["id"])
	}
}

func TestBuildPayload_CELConditional(t *testing.T) {
	buildDef := map[string]interface{}{
		"gen_check": `resource.generation > 2 ? "high" : "low"`,
	}
	b, err := NewBuilder(buildDef, logger.NewHyperFleetLogger())
	if err != nil {
		t.Fatalf("NewBuilder failed: %v", err)
	}

	payload := b.BuildPayload(context.Background(), makeTestResource(), "")

	if payload["gen_check"] != "high" {
		t.Errorf("expected gen_check 'high', got %v", payload["gen_check"])
	}
}

func TestBuildPayload_MissingFieldOmitted(t *testing.T) {
	buildDef := map[string]interface{}{
		"id":      "resource.id",
		"missing": "resource.nonexistent_field",
	}
	b, err := NewBuilder(buildDef, logger.NewHyperFleetLogger())
	if err != nil {
		t.Fatalf("NewBuilder failed: %v", err)
	}

	payload := b.BuildPayload(context.Background(), makeTestResource(), "")

	if _, exists := payload["missing"]; exists {
		t.Errorf("expected absent 'missing' key (nil value omitted), but found it: %v", payload["missing"])
	}
	if payload["id"] != testClusterID {
		t.Errorf("expected id %q, got %v", testClusterID, payload["id"])
	}
}

func TestBuildPayload_CELStringLiteral(t *testing.T) {
	buildDef := map[string]interface{}{
		"origin": `"hyperfleet-sentinel"`,
	}
	b, err := NewBuilder(buildDef, logger.NewHyperFleetLogger())
	if err != nil {
		t.Fatalf("NewBuilder failed: %v", err)
	}

	payload := b.BuildPayload(context.Background(), makeTestResource(), "")

	if payload["origin"] != "hyperfleet-sentinel" {
		t.Errorf("expected origin 'hyperfleet-sentinel', got %v", payload["origin"])
	}
}

func TestBuildPayload_MixedTypes(t *testing.T) {
	buildDef := map[string]interface{}{
		"id":        "resource.id",
		"origin":    `"sentinel"`,
		"gen_check": `resource.generation > 2 ? "high" : "low"`,
		"nested": map[string]interface{}{
			"kind": "resource.kind",
		},
	}
	b, err := NewBuilder(buildDef, logger.NewHyperFleetLogger())
	if err != nil {
		t.Fatalf("NewBuilder failed: %v", err)
	}

	payload := b.BuildPayload(context.Background(), makeTestResource(), "")

	if payload["id"] != testClusterID {
		t.Errorf("expected id %q, got %v", testClusterID, payload["id"])
	}
	if payload["origin"] != "sentinel" {
		t.Errorf("expected origin 'sentinel', got %v", payload["origin"])
	}
	if payload["gen_check"] != "high" {
		t.Errorf("expected gen_check 'high', got %v", payload["gen_check"])
	}
	nested, ok := payload["nested"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'nested' to be a map, got %T", payload["nested"])
	}
	if nested["kind"] != testResourceKind {
		t.Errorf("expected nested kind %q, got %v", testResourceKind, nested["kind"])
	}
}

func TestBuildPayload_ReasonVariable(t *testing.T) {
	buildDef := map[string]interface{}{
		"id":     "resource.id",
		"reason": "reason",
	}
	b, err := NewBuilder(buildDef, logger.NewHyperFleetLogger())
	if err != nil {
		t.Fatalf("NewBuilder failed: %v", err)
	}

	payload := b.BuildPayload(context.Background(), makeTestResource(), "max_age_exceeded")

	if payload["reason"] != "max_age_exceeded" {
		t.Errorf("expected reason 'max_age_exceeded', got %v", payload["reason"])
	}
	if payload["id"] != testClusterID {
		t.Errorf("expected id %q, got %v", testClusterID, payload["id"])
	}
}

func TestBuildPayload_NodePool_OwnerReferences(t *testing.T) {
	buildDef := map[string]interface{}{
		"id":           "resource.id",
		"kind":         "resource.kind",
		"cluster_id":   "resource.owner_references.id",
		"cluster_kind": "resource.owner_references.kind",
	}
	b, err := NewBuilder(buildDef, logger.NewHyperFleetLogger())
	if err != nil {
		t.Fatalf("NewBuilder failed: %v", err)
	}

	payload := b.BuildPayload(context.Background(), makeTestNodePoolResource(), "")

	if payload["id"] != "np-xyz" {
		t.Errorf("expected id 'np-xyz', got %v", payload["id"])
	}
	if payload["kind"] != "NodePool" {
		t.Errorf("expected kind 'NodePool', got %v", payload["kind"])
	}
	if payload["cluster_id"] != "cluster-123" {
		t.Errorf("expected cluster_id 'cluster-123', got %v", payload["cluster_id"])
	}
	if payload["cluster_kind"] != testResourceKind {
		t.Errorf("expected cluster_kind %q, got %v", testResourceKind, payload["cluster_kind"])
	}
}

func TestBuildPayload_NestedObjectOmittedWhenAllChildrenMissing(t *testing.T) {
	// owner_references is a nested object whose fields reference resource.owner_references.*
	// When the resource has no owner (OwnerReferences == nil), all child expressions evaluate
	// to nil and the nested object should be omitted entirely, not returned as {}.
	buildDef := map[string]interface{}{
		"id": "resource.id",
		"owner_references": map[string]interface{}{
			"id":   "resource.owner_references.id",
			"kind": "resource.owner_references.kind",
		},
	}
	b, err := NewBuilder(buildDef, logger.NewHyperFleetLogger())
	if err != nil {
		t.Fatalf("NewBuilder failed: %v", err)
	}

	// makeTestResource() returns a Cluster with no OwnerReferences
	payload := b.BuildPayload(context.Background(), makeTestResource(), "")

	if _, ok := payload["owner_references"]; ok {
		t.Errorf("expected 'owner_references' to be omitted when no parent exists, got %v", payload["owner_references"])
	}
	if payload["id"] != testClusterID {
		t.Errorf("expected id %q, got %v", testClusterID, payload["id"])
	}
}

func TestBuildPayload_WithNameSpecReferences(t *testing.T) {
	buildDef := map[string]interface{}{
		"id":   "resource.id",
		"name": "resource.name",
	}
	b, err := NewBuilder(buildDef, logger.NewHyperFleetLogger())
	if err != nil {
		t.Fatalf("NewBuilder failed: %v", err)
	}

	resource := makeTestResource()
	resource.Name = "my-cluster"
	resource.Spec = map[string]interface{}{"cloud_provider": "gcp"}
	resource.References = map[string][]client.ObjectReference{
		"wif_config": {
			{ID: "wc-1", Kind: "WifConfig", Href: "/api/v1/resources/wc-1"},
		},
	}

	payload := b.BuildPayload(context.Background(), resource, "test-reason")

	if payload["name"] != "my-cluster" {
		t.Errorf("expected name 'my-cluster', got %v", payload["name"])
	}
}
