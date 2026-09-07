// Package tool defines the typed tool protocol Praxis agents use to act on the
// world: a small JSON-schema-style parameter model, argument validation and
// coercion, a tool registry, and a dispatcher that turns a core.ToolCall into
// a core.Observation.
//
// This is the hand-built equivalent of an LLM "function-calling" layer. The
// reasoner proposes a tool name and a bag of JSON arguments; Validate turns
// that loose bag into strongly-typed, defaulted arguments (or a clear error),
// and Dispatch runs the tool and records timing.
package tool

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParamType is the accepted type of a tool parameter.
type ParamType string

const (
	TypeString      ParamType = "string"
	TypeInt         ParamType = "int"
	TypeNumber      ParamType = "number"
	TypeBool        ParamType = "bool"
	TypeEnum        ParamType = "enum"
	TypeStringArray ParamType = "string[]"
)

// Param describes a single tool argument.
type Param struct {
	Name        string    `json:"name"`
	Type        ParamType `json:"type"`
	Description string    `json:"description,omitempty"`
	Required    bool      `json:"required,omitempty"`
	Enum        []string  `json:"enum,omitempty"`
	Default     any       `json:"default,omitempty"`
}

// Schema is the ordered set of parameters a tool accepts.
type Schema struct {
	Params []Param `json:"params"`
}

// Args holds validated, coerced tool arguments. After Validate, values have
// canonical Go types: string, int64, float64, bool, or []string.
type Args map[string]any

// Validate checks a loose argument bag against the schema, coercing values to
// their declared types, filling defaults, and enforcing required params. It
// ignores unknown keys so imperfect model output degrades gracefully.
func (s Schema) Validate(raw map[string]any) (Args, error) {
	out := Args{}
	for _, p := range s.Params {
		v, ok := raw[p.Name]
		if !ok || v == nil {
			if p.Required {
				return nil, fmt.Errorf("missing required argument %q", p.Name)
			}
			if p.Default != nil {
				out[p.Name] = p.Default
			}
			continue
		}
		cv, err := coerce(p, v)
		if err != nil {
			return nil, fmt.Errorf("argument %q: %w", p.Name, err)
		}
		out[p.Name] = cv
	}
	return out, nil
}

func coerce(p Param, v any) (any, error) {
	switch p.Type {
	case TypeString:
		return toString(v)
	case TypeEnum:
		s, err := toString(v)
		if err != nil {
			return nil, err
		}
		if !contains(p.Enum, s) {
			return nil, fmt.Errorf("must be one of [%s]", strings.Join(p.Enum, ", "))
		}
		return s, nil
	case TypeInt:
		return toInt(v)
	case TypeNumber:
		return toNumber(v)
	case TypeBool:
		return toBool(v)
	case TypeStringArray:
		return toStringArray(v)
	default:
		return nil, fmt.Errorf("unknown parameter type %q", p.Type)
	}
}

// Typed accessors. They assume Validate has run, so types are canonical.

func (a Args) String(k string) string        { s, _ := a[k].(string); return s }
func (a Args) Int(k string) int64            { i, _ := a[k].(int64); return i }
func (a Args) Number(k string) float64       { f, _ := a[k].(float64); return f }
func (a Args) Bool(k string) bool            { b, _ := a[k].(bool); return b }
func (a Args) StringArray(k string) []string { s, _ := a[k].([]string); return s }
func (a Args) Has(k string) bool             { _, ok := a[k]; return ok }

func toString(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case int:
		return strconv.Itoa(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case json.Number:
		return x.String(), nil
	default:
		return "", fmt.Errorf("expected string, got %T", v)
	}
}

func toInt(v any) (int64, error) {
	switch x := v.(type) {
	case float64:
		if x != math.Trunc(x) {
			return 0, fmt.Errorf("expected integer, got %v", x)
		}
		return int64(x), nil
	case int:
		return int64(x), nil
	case int64:
		return x, nil
	case json.Number:
		return x.Int64()
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("expected integer, got %q", x)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("expected integer, got %T", v)
	}
}

func toNumber(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case json.Number:
		return x.Float64()
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil {
			return 0, fmt.Errorf("expected number, got %q", x)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("expected number, got %T", v)
	}
}

func toBool(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "yes", "1":
			return true, nil
		case "false", "no", "0":
			return false, nil
		}
		return false, fmt.Errorf("expected bool, got %q", x)
	case float64:
		return x != 0, nil
	default:
		return false, fmt.Errorf("expected bool, got %T", v)
	}
}

func toStringArray(v any) ([]string, error) {
	switch x := v.(type) {
	case []string:
		return x, nil
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, err := toString(e)
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, nil
	case string:
		return []string{x}, nil
	default:
		return nil, fmt.Errorf("expected string array, got %T", v)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
