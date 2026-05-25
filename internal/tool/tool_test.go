package tool

import (
	"context"
	"testing"
)

type testTool struct{}

func (t *testTool) Name() string               { return "test" }
func (t *testTool) Description() string        { return "A test tool" }
func (t *testTool) Parameters() map[string]any { return map[string]any{} }
func (t *testTool) ReadOnly() bool             { return true }
func (t *testTool) ConcurrencySafe() bool      { return true }
func (t *testTool) Exclusive() bool            { return false }
func (t *testTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	return &Result{Content: "ok"}, nil
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register(&testTool{})

	tool, err := r.Get("test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if tool.Name() != "test" {
		t.Errorf("expected test, got %s", tool.Name())
	}
}

func TestRegistryGetNotFound(t *testing.T) {
	r := NewRegistry()
	_, err := r.Get("nonexistent")
	if err == nil {
		t.Error("expected error")
	}
}

func TestRegistryHas(t *testing.T) {
	r := NewRegistry()
	r.Register(&testTool{})

	if !r.Has("test") {
		t.Error("Has(test) returned false")
	}
	if r.Has("nonexistent") {
		t.Error("Has(nonexistent) returned true")
	}
}

func TestRegistryList(t *testing.T) {
	r := NewRegistry()
	r.Register(&testTool{})

	names := r.List()
	if len(names) != 1 || names[0] != "test" {
		t.Errorf("unexpected list: %v", names)
	}
}

func TestRegistryDefinitions(t *testing.T) {
	r := NewRegistry()
	r.Register(&testTool{})

	defs := r.Definitions()
	if len(defs) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(defs))
	}

	def := defs[0]
	if def["type"] != "function" {
		t.Errorf("expected type=function, got %v", def["type"])
	}

	fn, ok := def["function"].(map[string]any)
	if !ok {
		t.Fatal("missing function key")
	}
	if fn["name"] != "test" {
		t.Errorf("expected name=test, got %v", fn["name"])
	}
}

func TestRegistryExecute(t *testing.T) {
	r := NewRegistry()
	r.Register(&testTool{})

	result, err := r.Execute(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Content != "ok" {
		t.Errorf("expected 'ok', got '%s'", result.Content)
	}
}

func TestSchemaTypes(t *testing.T) {
	// String validation
	str := &StringSchema{Enum: []string{"a", "b"}}
	if errs := str.Validate("a", ""); len(errs) > 0 {
		t.Errorf("expected valid, got: %v", errs)
	}
	if errs := str.Validate("c", ""); len(errs) == 0 {
		t.Error("expected invalid enum value")
	}

	// Integer validation
	integer := &IntegerSchema{Minimum: intPtr(0), Maximum: intPtr(10)}
	if errs := integer.Validate(5, ""); len(errs) > 0 {
		t.Errorf("expected valid, got: %v", errs)
	}
	if errs := integer.Validate(11, ""); len(errs) == 0 {
		t.Error("expected out of range")
	}

	// Number validation
	num := &NumberSchema{Minimum: floatPtrPtr(0.0), Maximum: floatPtrPtr(1.0)}
	if errs := num.Validate(0.5, ""); len(errs) > 0 {
		t.Errorf("expected valid, got: %v", errs)
	}
	if errs := num.Validate(2.0, ""); len(errs) == 0 {
		t.Error("expected out of range")
	}

	// Boolean
	boolean := &BooleanSchema{}
	if errs := boolean.Validate(true, ""); len(errs) > 0 {
		t.Errorf("expected valid, got: %v", errs)
	}
	if errs := boolean.Validate("notbool", ""); len(errs) == 0 {
		t.Error("expected type error")
	}

	// Object
	obj := &ObjectSchema{
		Properties: map[string]Schema{
			"name": &StringSchema{},
		},
		Required: []string{"name"},
	}
	if errs := obj.Validate(map[string]any{"name": "test"}, ""); len(errs) > 0 {
		t.Errorf("expected valid, got: %v", errs)
	}
	if errs := obj.Validate(map[string]any{}, ""); len(errs) == 0 {
		t.Error("expected missing required field")
	}
}

func intPtr(i int) *int              { return &i }
func floatPtrPtr(f float64) *float64 { return &f }
