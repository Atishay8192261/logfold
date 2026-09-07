package tool

import "testing"

func TestValidateRequiredAndDefaults(t *testing.T) {
	s := Schema{Params: []Param{
		{Name: "path", Type: TypeString, Required: true},
		{Name: "lines", Type: TypeInt, Default: int64(20)},
	}}

	if _, err := s.Validate(map[string]any{}); err == nil {
		t.Fatal("expected error for missing required arg")
	}

	args, err := s.Validate(map[string]any{"path": "a.log"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if args.String("path") != "a.log" {
		t.Fatalf("path = %q", args.String("path"))
	}
	if args.Int("lines") != 20 {
		t.Fatalf("default lines = %d, want 20", args.Int("lines"))
	}
}

func TestCoercion(t *testing.T) {
	s := Schema{Params: []Param{
		{Name: "port", Type: TypeInt},
		{Name: "ratio", Type: TypeNumber},
		{Name: "force", Type: TypeBool},
		{Name: "mode", Type: TypeEnum, Enum: []string{"read", "write"}},
		{Name: "tags", Type: TypeStringArray},
	}}

	// JSON numbers arrive as float64; strings should coerce too.
	args, err := s.Validate(map[string]any{
		"port":  float64(8080),
		"ratio": "0.5",
		"force": "true",
		"mode":  "write",
		"tags":  []any{"a", "b"},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if args.Int("port") != 8080 {
		t.Fatalf("port = %d", args.Int("port"))
	}
	if args.Number("ratio") != 0.5 {
		t.Fatalf("ratio = %v", args.Number("ratio"))
	}
	if !args.Bool("force") {
		t.Fatal("force should be true")
	}
	if got := args.StringArray("tags"); len(got) != 2 || got[0] != "a" {
		t.Fatalf("tags = %v", got)
	}
}

func TestCoercionErrors(t *testing.T) {
	s := Schema{Params: []Param{{Name: "port", Type: TypeInt}}}
	if _, err := s.Validate(map[string]any{"port": 3.14}); err == nil {
		t.Fatal("expected error for non-integer float")
	}
	s2 := Schema{Params: []Param{{Name: "mode", Type: TypeEnum, Enum: []string{"a"}}}}
	if _, err := s2.Validate(map[string]any{"mode": "z"}); err == nil {
		t.Fatal("expected error for out-of-enum value")
	}
}
