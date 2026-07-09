package payload

import (
	"context"
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/client"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/pkg/logger"
)

// ValueDef is the result of parsing a raw YAML node into a typed definition.
type ValueDef struct {
	Literal       interface{}            // constant value (int, bool, nil)
	Children      map[string]interface{} // nested sub-object (recurse)
	Expression    string                 // CEL expression string (from any YAML string value)
	ExpressionSet bool                   // true when the raw input contained an "expression" field (string type)
}

// ParseValueDef parses a raw YAML value into a ValueDef.
//
// Rules:
//   - string → Expression (evaluated as CEL against "resource" and "reason")
//   - int/bool/nil → Literal
//   - map → Children (recurse)
//   - unknown type → error
func ParseValueDef(raw interface{}) (*ValueDef, error) {
	switch v := raw.(type) {
	case string:
		return &ValueDef{Expression: v, ExpressionSet: true}, nil
	case int, int32, int64, float32, float64, bool:
		return &ValueDef{Literal: v}, nil
	case nil:
		return nil, fmt.Errorf("ParseValueDef: nil is not a valid value; every leaf must be a CEL expression string")
	case map[string]interface{}:
		return &ValueDef{Children: v}, nil
	default:
		return nil, fmt.Errorf("unsupported value type %T", raw)
	}
}

// compiledNode is a pre-compiled node in a build definition tree.
// Exactly one of program, children, or isLiteral is set.
type compiledNode struct {
	program   cel.Program              // non-nil if this node is a CEL expression
	children  map[string]*compiledNode // non-nil if this node is a nested object
	literal   interface{}              // value if isLiteral is true
	isLiteral bool                     // true for literal nodes (including nil)
}

// Builder builds event payloads from a resource using a pre-compiled build definition.
type Builder struct {
	compiled map[string]*compiledNode
	log      logger.HyperFleetLogger
}

// newCELEnv creates a CEL environment with the standard variable declarations.
func newCELEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("resource", cel.DynType),
		cel.Variable("reason", cel.StringType),
	)
}

// NewBuilder creates a new Builder. All CEL expressions are compiled once here.
func NewBuilder(buildDef interface{}, log logger.HyperFleetLogger) (*Builder, error) {
	defMap, ok := buildDef.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("build definition must be a map, got %T", buildDef)
	}
	env, err := newCELEnv()
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}
	compiled, err := compileMap(defMap, env)
	if err != nil {
		return nil, fmt.Errorf("failed to compile build definition: %w", err)
	}
	return &Builder{compiled: compiled, log: log}, nil
}

// compileMap recursively compiles a definition map into compiled nodes.
func compileMap(def map[string]interface{}, env *cel.Env) (map[string]*compiledNode, error) {
	result := make(map[string]*compiledNode, len(def))
	for key, rawVal := range def {
		node, err := compileNode(rawVal, env)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", key, err)
		}
		result[key] = node
	}
	return result, nil
}

// compileNode compiles a single raw value into a compiledNode.
func compileNode(raw interface{}, env *cel.Env) (*compiledNode, error) {
	vd, err := ParseValueDef(raw)
	if err != nil {
		return nil, err
	}
	switch {
	case vd.Children != nil:
		children, err := compileMap(vd.Children, env)
		if err != nil {
			return nil, err
		}
		return &compiledNode{children: children}, nil
	case vd.ExpressionSet && vd.Expression == "":
		return nil, fmt.Errorf(
			"compileNode: ParseValueDef returned an empty expression (vd.Expression=%q); "+
				"a compiledNode cannot be created from an empty CEL expression",
			vd.Expression,
		)
	case vd.Expression != "":
		ast, issues := env.Compile(vd.Expression)
		if issues != nil && issues.Err() != nil {
			return nil, fmt.Errorf("CEL expression %q: %w", vd.Expression, issues.Err())
		}
		prg, err := env.Program(ast)
		if err != nil {
			return nil, fmt.Errorf("CEL program for %q: %w", vd.Expression, err)
		}
		return &compiledNode{program: prg}, nil
	default:
		return &compiledNode{literal: vd.Literal, isLiteral: true}, nil
	}
}

// BuildPayload builds an event payload map from the given resource and decision reason.
// The reason is available to CEL expressions as the "reason" variable.
// ctx is used for correlated warning logs if CEL evaluation fails.
func (b *Builder) BuildPayload(ctx context.Context, resource *client.Resource, reason string) map[string]interface{} {
	return b.evalCompiledMap(ctx, b.compiled, resource.ToMap(), reason)
}

// evalCompiledMap evaluates a compiled map against the resource and reason.
func (b *Builder) evalCompiledMap(
	ctx context.Context,
	nodes map[string]*compiledNode,
	resourceMap map[string]interface{},
	reason string,
) map[string]interface{} {
	result := make(map[string]interface{})
	for key, node := range nodes {
		val := b.evalCompiledNode(ctx, node, resourceMap, reason)
		if val != nil {
			result[key] = val
		}
	}
	return result
}

// evalCompiledNode evaluates a single compiled node.
// Returns nil for missing or null values (fail-safe: misconfigured fields are omitted).
func (b *Builder) evalCompiledNode(
	ctx context.Context,
	node *compiledNode,
	resourceMap map[string]interface{},
	reason string,
) interface{} {
	if node.children != nil {
		nested := b.evalCompiledMap(ctx, node.children, resourceMap, reason)
		if len(nested) == 0 {
			return nil
		}
		return nested
	}
	if node.program != nil {
		out, _, err := node.program.Eval(map[string]interface{}{
			"resource": resourceMap,
			"reason":   reason,
		})
		if err != nil {
			b.log.Warnf(ctx, "CEL expression evaluation failed: %v", err)
			return nil
		}
		if out == nil {
			b.log.Warn(ctx, "CEL expression evaluated to nil")
			return nil
		}
		return out.Value()
	}
	// Literal node (isLiteral == true)
	return node.literal
}
