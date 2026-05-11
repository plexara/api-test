package oapi

import (
	"reflect"
	"strings"
	"time"
)

// schemaFromType returns a *Schema for the given Go type. The reflector
// honors JSON tags (rename, omit, omitempty) and recurses through pointer,
// struct, slice, array, and map types.
//
// Unsupported or untyped values (interface{}, channels, funcs) emit an
// empty schema, which JSON Schema treats as "any value". That's the
// correct shape for echo's Body field, which holds arbitrary JSON.
func schemaFromType(t reflect.Type) *Schema {
	if t == nil {
		return &Schema{}
	}
	// Unwrap pointers; pointer-ness is encoded by request/response
	// shape, not by the schema itself.
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	// time.Time renders as RFC 3339 string.
	if t == reflect.TypeOf(time.Time{}) {
		return &Schema{Type: "string", Format: "date-time"}
	}

	switch t.Kind() {
	case reflect.Bool:
		return &Schema{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &Schema{Type: "integer"}
	case reflect.Float32, reflect.Float64:
		return &Schema{Type: "number"}
	case reflect.String:
		return &Schema{Type: "string"}
	case reflect.Slice, reflect.Array:
		return &Schema{Type: "array", Items: schemaFromType(t.Elem())}
	case reflect.Map:
		// JSON only allows string-keyed maps; if a non-string key
		// sneaks in, emit a generic object.
		if t.Key().Kind() != reflect.String {
			return &Schema{Type: "object"}
		}
		return &Schema{
			Type:                 "object",
			AdditionalProperties: schemaFromType(t.Elem()),
		}
	case reflect.Struct:
		return structSchema(t)
	default:
		// interface{}, chan, func, unsafe.Pointer — emit "any".
		return &Schema{}
	}
}

// structSchema builds an object schema from a struct type, walking
// exported fields. JSON tags drive property naming and omission; fields
// without ",omitempty" are marked required.
func structSchema(t reflect.Type) *Schema {
	out := &Schema{
		Type:       "object",
		Properties: map[string]*Schema{},
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, omitEmpty, skip := parseJSONTag(f)
		if skip {
			continue
		}
		out.Properties[name] = schemaFromType(f.Type)
		if !omitEmpty {
			out.Required = append(out.Required, name)
		}
	}
	if len(out.Properties) == 0 {
		out.Properties = nil
	}
	return out
}

// parseJSONTag returns the wire name, whether the field is omitempty,
// and whether the field should be skipped entirely.
func parseJSONTag(f reflect.StructField) (name string, omitEmpty bool, skip bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false, true
	}
	if tag == "" {
		return f.Name, false, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = f.Name
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitEmpty = true
		}
	}
	return name, omitEmpty, false
}

// lookupFieldByJSONName finds the struct field whose JSON wire name
// matches target. Used to map a path parameter like {key} to the
// matching field in a PathParams struct. Returns the field's reflected
// type and whether a match was found.
func lookupFieldByJSONName(t reflect.Type, target string) (reflect.Type, bool) {
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil, false
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, skip := parseJSONTag(f)
		if skip {
			continue
		}
		if name == target || strings.EqualFold(name, target) {
			return f.Type, true
		}
	}
	return nil, false
}
