package oapi

import (
	"reflect"
	"testing"
	"time"
)

type sampleStruct struct {
	Required string `json:"required"`
	Optional int    `json:"optional,omitempty"`
	Renamed  bool   `json:"flag"`
	Hidden   string `json:"-"`
	Untagged float64
	When     time.Time `json:"when"`
}

type nested struct {
	Inner sampleStruct `json:"inner"`
	List  []string     `json:"list"`
	Bag   map[string]int
	Free  any `json:"free,omitempty"`
}

func TestSchemaFromType_Primitives(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want Schema
	}{
		{"bool", false, Schema{Type: "boolean"}},
		{"int", 0, Schema{Type: "integer"}},
		{"float", 0.0, Schema{Type: "number"}},
		{"string", "", Schema{Type: "string"}},
		{"time", time.Time{}, Schema{Type: "string", Format: "date-time"}},
		{"slice", []string{}, Schema{Type: "array", Items: &Schema{Type: "string"}}},
		{"map", map[string]int{}, Schema{Type: "object", AdditionalProperties: &Schema{Type: "integer"}}},
		{"any", any(nil), Schema{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := schemaFromType(reflect.TypeOf(c.in))
			if got == nil {
				t.Fatalf("schemaFromType returned nil")
			}
			if got.Type != c.want.Type {
				t.Errorf("Type = %q, want %q", got.Type, c.want.Type)
			}
			if got.Format != c.want.Format {
				t.Errorf("Format = %q, want %q", got.Format, c.want.Format)
			}
			if (got.Items == nil) != (c.want.Items == nil) {
				t.Errorf("Items presence mismatch")
			} else if got.Items != nil && got.Items.Type != c.want.Items.Type {
				t.Errorf("Items.Type = %q, want %q", got.Items.Type, c.want.Items.Type)
			}
			if (got.AdditionalProperties == nil) != (c.want.AdditionalProperties == nil) {
				t.Errorf("AdditionalProperties presence mismatch")
			} else if got.AdditionalProperties != nil && got.AdditionalProperties.Type != c.want.AdditionalProperties.Type {
				t.Errorf("AdditionalProperties.Type = %q, want %q",
					got.AdditionalProperties.Type, c.want.AdditionalProperties.Type)
			}
		})
	}
}

func TestSchemaFromType_Struct(t *testing.T) {
	s := schemaFromType(reflect.TypeOf(sampleStruct{}))
	if s.Type != "object" {
		t.Fatalf("Type = %q, want object", s.Type)
	}

	want := map[string]string{
		"required": "string",
		"optional": "integer",
		"flag":     "boolean",
		"Untagged": "number",
		"when":     "string",
	}
	if len(s.Properties) != len(want) {
		t.Fatalf("Properties count = %d, want %d (got keys: %v)",
			len(s.Properties), len(want), keys(s.Properties))
	}
	for name, typ := range want {
		got, ok := s.Properties[name]
		if !ok {
			t.Errorf("missing property %q", name)
			continue
		}
		if got.Type != typ {
			t.Errorf("property %q Type = %q, want %q", name, got.Type, typ)
		}
	}
	if _, present := s.Properties["Hidden"]; present {
		t.Errorf(`property "Hidden" should be omitted by json:"-"`)
	}
	if _, present := s.Properties["-"]; present {
		t.Errorf(`property "-" should not appear`)
	}

	requiredSet := map[string]bool{}
	for _, r := range s.Required {
		requiredSet[r] = true
	}
	wantRequired := []string{"required", "flag", "Untagged", "when"}
	for _, r := range wantRequired {
		if !requiredSet[r] {
			t.Errorf("required missing %q (have %v)", r, s.Required)
		}
	}
	if requiredSet["optional"] {
		t.Errorf("optional should not be required (json:omitempty)")
	}
}

func TestSchemaFromType_Nested(t *testing.T) {
	s := schemaFromType(reflect.TypeOf(nested{}))
	if s.Type != "object" {
		t.Fatalf("Type = %q, want object", s.Type)
	}
	inner, ok := s.Properties["inner"]
	if !ok || inner.Type != "object" || inner.Properties["required"].Type != "string" {
		t.Errorf("nested.inner did not recurse: %+v", inner)
	}
	list, ok := s.Properties["list"]
	if !ok || list.Type != "array" || list.Items.Type != "string" {
		t.Errorf("nested.list shape wrong: %+v", list)
	}
	bag, ok := s.Properties["Bag"]
	if !ok || bag.Type != "object" || bag.AdditionalProperties.Type != "integer" {
		t.Errorf("nested.Bag shape wrong: %+v", bag)
	}
	free, ok := s.Properties["free"]
	if !ok || free.Type != "" {
		t.Errorf("nested.free should be any (empty schema): %+v", free)
	}
}

func TestSchemaFromType_PointerUnwrap(t *testing.T) {
	type holder struct {
		P *string `json:"p,omitempty"`
	}
	s := schemaFromType(reflect.TypeOf((*holder)(nil)))
	if s.Type != "object" {
		t.Fatalf("Type = %q, want object", s.Type)
	}
	if s.Properties["p"].Type != "string" {
		t.Errorf("pointer field should unwrap to string, got %+v", s.Properties["p"])
	}
}

func TestParseJSONTag(t *testing.T) {
	type sample struct {
		A string `json:"a"`
		B string `json:"b,omitempty"`
		C string `json:"-"`
		D string `json:",omitempty"`
		E string
	}
	t.Run("plain", func(t *testing.T) {
		name, omit, skip := parseJSONTag(reflect.TypeOf(sample{}).Field(0))
		if name != "a" || omit || skip {
			t.Errorf("got (%q,%v,%v), want (a,false,false)", name, omit, skip)
		}
	})
	t.Run("omitempty", func(t *testing.T) {
		name, omit, skip := parseJSONTag(reflect.TypeOf(sample{}).Field(1))
		if name != "b" || !omit || skip {
			t.Errorf("got (%q,%v,%v), want (b,true,false)", name, omit, skip)
		}
	})
	t.Run("dash", func(t *testing.T) {
		_, _, skip := parseJSONTag(reflect.TypeOf(sample{}).Field(2))
		if !skip {
			t.Errorf("dash tag should skip")
		}
	})
	t.Run("empty-name-with-options", func(t *testing.T) {
		name, omit, _ := parseJSONTag(reflect.TypeOf(sample{}).Field(3))
		if name != "D" || !omit {
			t.Errorf("got (%q,%v), want (D,true)", name, omit)
		}
	})
	t.Run("no-tag", func(t *testing.T) {
		name, omit, _ := parseJSONTag(reflect.TypeOf(sample{}).Field(4))
		if name != "E" || omit {
			t.Errorf("got (%q,%v), want (E,false)", name, omit)
		}
	})
}

func TestLookupFieldByJSONName(t *testing.T) {
	type params struct {
		Key string `json:"key"`
		ID  int    `json:"id"`
	}
	ft, ok := lookupFieldByJSONName(reflect.TypeOf(params{}), "key")
	if !ok || ft.Kind() != reflect.String {
		t.Errorf("key lookup got (%v,%v)", ft, ok)
	}
	ft, ok = lookupFieldByJSONName(reflect.TypeOf((*params)(nil)), "id")
	if !ok || ft.Kind() != reflect.Int {
		t.Errorf("id lookup through pointer got (%v,%v)", ft, ok)
	}
	if _, ok := lookupFieldByJSONName(reflect.TypeOf(params{}), "missing"); ok {
		t.Errorf("missing field should return ok=false")
	}
	if _, ok := lookupFieldByJSONName(nil, "key"); ok {
		t.Errorf("nil type should return ok=false")
	}
}

func keys(m map[string]*Schema) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
