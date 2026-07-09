package engine

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/ext"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/client"
	"github.com/openshift-hyperfleet/hyperfleet-sentinel/internal/config"
)

// Decision represents the result of evaluating a resource
type Decision struct {
	Reason        string // Human-readable explanation for the decision
	ShouldPublish bool   // Indicates whether an event should be published for the resource
}

type paramEntry struct {
	prog cel.Program
	name string
}

// DecisionEngine evaluates whether a resource needs an event published
// using configurable CEL expressions.
type DecisionEngine struct {
	resultProg       cel.Program
	conditionsLookup map[string]map[string]interface{}
	params           []paramEntry
	mu               sync.Mutex
}

// NewDecisionEngine creates a new CEL-based decision engine from a MessageDecisionConfig.
// All CEL expressions are compiled at creation time for fail-fast validation.
func NewDecisionEngine(cfg *config.MessageDecisionConfig) (*DecisionEngine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("message_decision config is required")
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid message_decision config: %w", err)
	}

	de := &DecisionEngine{}

	// Build CEL environment with all variables and the condition() function.
	// The function declaration includes the implementation via FunctionBinding,
	// which reads from the engine's conditionsLookup (updated per-evaluation).
	envOpts := []cel.EnvOption{
		ext.Strings(),
		cel.Variable("resource", cel.DynType),
		cel.Variable("now", cel.TimestampType),
		cel.Function("condition",
			cel.Overload("condition_string_to_dyn",
				[]*cel.Type{cel.StringType},
				cel.DynType,
				cel.UnaryBinding(func(val ref.Val) ref.Val {
					name, ok := val.Value().(string)
					if !ok {
						return types.DefaultTypeAdapter.NativeToValue(zeroCondition())
					}
					de.mu.Lock()
					lookup := de.conditionsLookup
					de.mu.Unlock()
					if cond, exists := lookup[name]; exists {
						return types.DefaultTypeAdapter.NativeToValue(cond)
					}
					return types.DefaultTypeAdapter.NativeToValue(zeroCondition())
				}),
			),
		),
	}

	// Declare all param names as DynType variables for inter-param references
	for _, p := range cfg.Params {
		envOpts = append(envOpts, cel.Variable(p.Name, cel.DynType))
	}

	env, err := cel.NewEnv(envOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}

	// Compile params in authored order
	params := make([]paramEntry, 0, len(cfg.Params))
	for _, p := range cfg.Params {
		ast, issues := env.Compile(p.Expr)
		if issues != nil && issues.Err() != nil {
			return nil, fmt.Errorf("failed to compile param %q expression %q: %w", p.Name, p.Expr, issues.Err())
		}
		prg, prgErr := env.Program(ast)
		if prgErr != nil {
			return nil, fmt.Errorf("failed to create program for param %q: %w", p.Name, prgErr)
		}
		params = append(params, paramEntry{name: p.Name, prog: prg})
	}

	// Compile result expression
	resultAST, issues := env.Compile(cfg.Result)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("failed to compile result expression %q: %w", cfg.Result, issues.Err())
	}
	resultPrg, err := env.Program(resultAST)
	if err != nil {
		return nil, fmt.Errorf("failed to create result program: %w", err)
	}

	de.params = params
	de.resultProg = resultPrg

	return de, nil
}

// Evaluate determines if an event should be published for the resource.
// Returns a Decision indicating whether to publish and why.
func (e *DecisionEngine) Evaluate(resource *client.Resource, now time.Time) Decision {
	if resource == nil {
		return Decision{ShouldPublish: false, Reason: "resource is nil"}
	}
	if now.IsZero() {
		return Decision{ShouldPublish: false, Reason: "now time is zero"}
	}

	// Build resource map for CEL evaluation
	resourceMap := resource.ToMap()

	// Update the conditions lookup for the condition() function binding
	e.mu.Lock()
	e.conditionsLookup = buildConditionsLookup(resource.Status.Conditions)
	e.mu.Unlock()

	// Build base activation with resource and now
	activation := map[string]interface{}{
		"resource": resourceMap,
		"now":      now,
	}

	// Evaluate params in authored order
	paramValues := make(map[string]interface{}, len(e.params))
	for _, p := range e.params {
		// Merge param values into activation for inter-param references
		evalActivation := make(map[string]interface{}, len(activation)+len(paramValues))
		for k, v := range activation {
			evalActivation[k] = v
		}
		for k, v := range paramValues {
			evalActivation[k] = v
		}

		out, _, err := p.prog.Eval(evalActivation)
		if err != nil {
			return Decision{
				ShouldPublish: false,
				Reason:        fmt.Sprintf("param %q evaluation failed: %v", p.name, err),
			}
		}
		paramValues[p.name] = out.Value()
	}

	// Evaluate result expression
	resultActivation := make(map[string]interface{}, len(activation)+len(paramValues))
	for k, v := range activation {
		resultActivation[k] = v
	}
	for k, v := range paramValues {
		resultActivation[k] = v
	}

	out, _, err := e.resultProg.Eval(resultActivation)
	if err != nil {
		return Decision{
			ShouldPublish: false,
			Reason:        fmt.Sprintf("result evaluation failed: %v", err),
		}
	}

	shouldPublish, ok := out.Value().(bool)
	if !ok {
		return Decision{
			ShouldPublish: false,
			Reason:        fmt.Sprintf("result expression did not return bool, got %T", out.Value()),
		}
	}

	if shouldPublish {
		return Decision{
			ShouldPublish: true,
			Reason:        "message decision matched",
		}
	}

	return Decision{
		ShouldPublish: false,
		Reason:        "message decision result is false",
	}
}

// buildConditionsLookup creates a map from condition type name to condition data
// for use by the condition() CEL function.
func buildConditionsLookup(conditions []client.Condition) map[string]map[string]interface{} {
	lookup := make(map[string]map[string]interface{}, len(conditions))
	for _, c := range conditions {
		lookup[c.Type] = map[string]interface{}{
			"status":               c.Status,
			"observed_generation":  int64(c.ObservedGeneration),
			"last_updated_time":    c.LastUpdatedTime.Format(time.RFC3339Nano),
			"last_transition_time": c.LastTransitionTime.Format(time.RFC3339Nano),
			"reason":               c.Reason,
			"message":              c.Message,
		}
	}
	return lookup
}

// zeroCondition returns a zero-value condition map for safe field access
// when a condition is not found. Time fields are empty strings so that
// CEL expressions can guard against missing conditions with `ref_time != ""`.
func zeroCondition() map[string]interface{} {
	return map[string]interface{}{
		"status":               "",
		"observed_generation":  int64(0),
		"last_updated_time":    "",
		"last_transition_time": "",
		"reason":               "",
		"message":              "",
	}
}
