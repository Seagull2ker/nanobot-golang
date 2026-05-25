package tool

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SchemaType is the JSON Schema type of a parameter.
type SchemaType string

const (
	TypeString  SchemaType = "string"
	TypeInteger SchemaType = "integer"
	TypeNumber  SchemaType = "number"
	TypeBoolean SchemaType = "boolean"
	TypeArray   SchemaType = "array"
	TypeObject  SchemaType = "object"
)

// Schema represents a JSON Schema fragment for tool parameters.
type Schema interface {
	ToJSONSchema() map[string]any
	Validate(value any, path string) []string
}

// StringSchema is a string parameter.
type StringSchema struct {
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	MinLength   *int     `json:"minLength,omitempty"`
	MaxLength   *int     `json:"maxLength,omitempty"`
	Nullable    bool     `json:"nullable,omitempty"`
}

func (s *StringSchema) ToJSONSchema() map[string]any {
	m := map[string]any{"type": "string"}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if len(s.Enum) > 0 {
		m["enum"] = strsToAnys(s.Enum)
	}
	if s.MinLength != nil {
		m["minLength"] = *s.MinLength
	}
	if s.MaxLength != nil {
		m["maxLength"] = *s.MaxLength
	}
	return m
}

func (s *StringSchema) Validate(value any, path string) []string {
	if s.Nullable && value == nil {
		return nil
	}
	str, ok := value.(string)
	if !ok {
		return []string{fmt.Sprintf("%s: expected string, got %T", path, value)}
	}
	if s.MinLength != nil && len(str) < *s.MinLength {
		return []string{fmt.Sprintf("%s: min length %d, got %d", path, *s.MinLength, len(str))}
	}
	if s.MaxLength != nil && len(str) > *s.MaxLength {
		return []string{fmt.Sprintf("%s: max length %d, got %d", path, *s.MaxLength, len(str))}
	}
	if len(s.Enum) > 0 {
		for _, e := range s.Enum {
			if str == e {
				return nil
			}
		}
		return []string{fmt.Sprintf("%s: must be one of [%s]", path, strings.Join(s.Enum, ", "))}
	}
	return nil
}

// IntegerSchema is an integer parameter.
type IntegerSchema struct {
	Description string `json:"description,omitempty"`
	Minimum     *int   `json:"minimum,omitempty"`
	Maximum     *int   `json:"maximum,omitempty"`
	Enum        []int  `json:"enum,omitempty"`
	Nullable    bool   `json:"nullable,omitempty"`
}

func (s *IntegerSchema) ToJSONSchema() map[string]any {
	m := map[string]any{"type": "integer"}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if s.Minimum != nil {
		m["minimum"] = *s.Minimum
	}
	if s.Maximum != nil {
		m["maximum"] = *s.Maximum
	}
	if len(s.Enum) > 0 {
		m["enum"] = intsToAnys(s.Enum)
	}
	return m
}

func (s *IntegerSchema) Validate(value any, path string) []string {
	if s.Nullable && value == nil {
		return nil
	}
	var v int
	switch n := value.(type) {
	case int:
		v = n
	case float64:
		v = int(n)
	case json.Number:
		val, err := n.Int64()
		if err != nil {
			return []string{fmt.Sprintf("%s: invalid integer", path)}
		}
		v = int(val)
	default:
		return []string{fmt.Sprintf("%s: expected integer, got %T", path, value)}
	}
	if s.Minimum != nil && v < *s.Minimum {
		return []string{fmt.Sprintf("%s: minimum %d, got %d", path, *s.Minimum, v)}
	}
	if s.Maximum != nil && v > *s.Maximum {
		return []string{fmt.Sprintf("%s: maximum %d, got %d", path, *s.Maximum, v)}
	}
	if len(s.Enum) > 0 {
		for _, e := range s.Enum {
			if v == e {
				return nil
			}
		}
		return []string{fmt.Sprintf("%s: must be one of %v", path, s.Enum)}
	}
	return nil
}

// NumberSchema is a float parameter.
type NumberSchema struct {
	Description string  `json:"description,omitempty"`
	Minimum     *float64 `json:"minimum,omitempty"`
	Maximum     *float64 `json:"maximum,omitempty"`
	Nullable    bool    `json:"nullable,omitempty"`
}

func (s *NumberSchema) ToJSONSchema() map[string]any {
	m := map[string]any{"type": "number"}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if s.Minimum != nil {
		m["minimum"] = *s.Minimum
	}
	if s.Maximum != nil {
		m["maximum"] = *s.Maximum
	}
	return m
}

func (s *NumberSchema) Validate(value any, path string) []string {
	if s.Nullable && value == nil {
		return nil
	}
	var v float64
	switch n := value.(type) {
	case float64:
		v = n
	case float32:
		v = float64(n)
	case int:
		v = float64(n)
	case json.Number:
		val, err := n.Float64()
		if err != nil {
			return []string{fmt.Sprintf("%s: invalid number", path)}
		}
		v = val
	default:
		return []string{fmt.Sprintf("%s: expected number, got %T", path, value)}
	}
	if s.Minimum != nil && v < *s.Minimum {
		return []string{fmt.Sprintf("%s: minimum %f, got %f", path, *s.Minimum, v)}
	}
	if s.Maximum != nil && v > *s.Maximum {
		return []string{fmt.Sprintf("%s: maximum %f, got %f", path, *s.Maximum, v)}
	}
	return nil
}

// BooleanSchema is a boolean parameter.
type BooleanSchema struct {
	Description string `json:"description,omitempty"`
	Nullable    bool   `json:"nullable,omitempty"`
}

func (s *BooleanSchema) ToJSONSchema() map[string]any {
	m := map[string]any{"type": "boolean"}
	if s.Description != "" {
		m["description"] = s.Description
	}
	return m
}

func (s *BooleanSchema) Validate(value any, path string) []string {
	if s.Nullable && value == nil {
		return nil
	}
	_, ok := value.(bool)
	if !ok {
		return []string{fmt.Sprintf("%s: expected boolean, got %T", path, value)}
	}
	return nil
}

// ArraySchema is an array parameter.
type ArraySchema struct {
	Description string `json:"description,omitempty"`
	Items       Schema `json:"-"`
	MinItems    *int   `json:"minItems,omitempty"`
	MaxItems    *int   `json:"maxItems,omitempty"`
	Nullable    bool   `json:"nullable,omitempty"`
}

func (s *ArraySchema) ToJSONSchema() map[string]any {
	m := map[string]any{"type": "array"}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if s.Items != nil {
		m["items"] = s.Items.ToJSONSchema()
	}
	if s.MinItems != nil {
		m["minItems"] = *s.MinItems
	}
	if s.MaxItems != nil {
		m["maxItems"] = *s.MaxItems
	}
	return m
}

func (s *ArraySchema) Validate(value any, path string) []string {
	if s.Nullable && value == nil {
		return nil
	}
	arr, ok := value.([]any)
	if !ok {
		return []string{fmt.Sprintf("%s: expected array, got %T", path, value)}
	}
	if s.MinItems != nil && len(arr) < *s.MinItems {
		return []string{fmt.Sprintf("%s: min items %d, got %d", path, *s.MinItems, len(arr))}
	}
	if s.MaxItems != nil && len(arr) > *s.MaxItems {
		return []string{fmt.Sprintf("%s: max items %d, got %d", path, *s.MaxItems, len(arr))}
	}
	var errs []string
	if s.Items != nil {
		for i, item := range arr {
			errs = append(errs, s.Items.Validate(item, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return errs
}

// ObjectSchema is an object parameter.
type ObjectSchema struct {
	Description          string               `json:"description,omitempty"`
	Properties           map[string]Schema    `json:"-"`
	Required             []string             `json:"required,omitempty"`
	AdditionalProperties bool                 `json:"additionalProperties,omitempty"`
	Nullable             bool                 `json:"nullable,omitempty"`
}

func (s *ObjectSchema) ToJSONSchema() map[string]any {
	m := map[string]any{"type": "object"}
	if s.Description != "" {
		m["description"] = s.Description
	}
	props := make(map[string]any)
	for name, schema := range s.Properties {
		props[name] = schema.ToJSONSchema()
	}
	m["properties"] = props
	if len(s.Required) > 0 {
		m["required"] = s.Required
	}
	if !s.AdditionalProperties {
		m["additionalProperties"] = false
	}
	return m
}

func (s *ObjectSchema) Validate(value any, path string) []string {
	if s.Nullable && value == nil {
		return nil
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("%s: expected object, got %T", path, value)}
	}
	var errs []string
	for _, name := range s.Required {
		if _, ok := obj[name]; !ok {
			errs = append(errs, fmt.Sprintf("%s: missing required field '%s'", path, name))
		}
	}
	for name, schema := range s.Properties {
		if val, ok := obj[name]; ok {
			errs = append(errs, schema.Validate(val, fmt.Sprintf("%s.%s", path, name))...)
		}
	}
	if !s.AdditionalProperties {
		for key := range obj {
			if _, ok := s.Properties[key]; !ok {
				errs = append(errs, fmt.Sprintf("%s: unknown property '%s'", path, key))
			}
		}
	}
	return errs
}

func strsToAnys(s []string) []any {
	a := make([]any, len(s))
	for i, v := range s {
		a[i] = v
	}
	return a
}

func intsToAnys(s []int) []any {
	a := make([]any, len(s))
	for i, v := range s {
		a[i] = v
	}
	return a
}
