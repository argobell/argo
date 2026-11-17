package schema

import (
	"encoding/json/v2"
	"math"
	"math/big"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func explainTestSchema(t *testing.T, source string, options compilerOptions) *explainer {
	t.Helper()
	c, _ := compileTestSchema(t, source, options)
	e, err := newExplainer(c, c.schema)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestExplainKeywords(t *testing.T) {
	for _, tc := range []struct {
		name, schema, value string
		want                []ValidationError
	}{
		{"false", `false`, `1`, []ValidationError{{"", "false", "no value is allowed here"}}},
		{"type", `{"type":"string"}`, `1`, []ValidationError{{"", "type", "expected string, got integer"}}},
		{"type union", `{"type":["string","null"]}`, `1.5`, []ValidationError{{"", "type", "expected string or null, got number"}}},
		{"type takes precedence", `{"type":"string","enum":[1],"const":2}`, `false`, []ValidationError{{"", "type", "expected string, got boolean"}}},
		{"enum and const", `{"enum":[1,2],"const":2}`, `3`, []ValidationError{{"", "enum", "expected one of [1,2]"}, {"", "const", "expected 2"}}},
		{"empty enum", `{"enum":[]}`, `null`, []ValidationError{{"", "enum", "expected one of []"}}},
		{"object const", `{"const":{"z":2,"a":[1]}}`, `{}`, []ValidationError{{"", "const", `expected {"a":[1],"z":2}`}}},
		{"string min", `{"minLength":3}`, `"😀a"`, []ValidationError{{"", "minLength", "expected at least 3 characters"}}},
		{"string max", `{"maxLength":1}`, `"😀a"`, []ValidationError{{"", "maxLength", "expected at most 1 characters"}}},
		{"pattern", `{"pattern":"^(?=.*[0-9]).+$"}`, `"abc"`, []ValidationError{{"", "pattern", "expected to match ^(?=.*[0-9]).+$"}}},
		{"number minimum", `{"minimum":2}`, `1`, []ValidationError{{"", "minimum", "expected >= 2"}}},
		{"number maximum", `{"maximum":2}`, `3`, []ValidationError{{"", "maximum", "expected <= 2"}}},
		{"number exclusive minimum", `{"exclusiveMinimum":2}`, `2`, []ValidationError{{"", "exclusiveMinimum", "expected > 2"}}},
		{"number exclusive maximum", `{"exclusiveMaximum":2}`, `2`, []ValidationError{{"", "exclusiveMaximum", "expected < 2"}}},
		{"multiple", `{"multipleOf":0.1}`, `0.15`, []ValidationError{{"", "multipleOf", "expected a multiple of 0.1"}}},
		{"object errors", `{"properties":{"path":{"type":"string"},"offset":{"minimum":1}},"required":["path"],"additionalProperties":false}`, `{"offset":0,"extra":true}`, []ValidationError{{"/path", "required", "missing"}, {"/offset", "minimum", "expected >= 1"}, {"/extra", "additionalProperties", "unknown field"}}},
		{"pointer escaping", `{"properties":{"a/b~c":{"type":"string"}}}`, `{"a/b~c":1}`, []ValidationError{{"/a~1b~0c", "type", "expected string, got integer"}}},
		{"pattern properties", `{"patternProperties":{"^a":{"type":"number"},"z$":{"type":"boolean"}}}`, `{"az":"x"}`, []ValidationError{{"/az", "type", "expected number, got string"}, {"/az", "type", "expected boolean, got string"}}},
		{"additional schema", `{"additionalProperties":{"type":"string"}}`, `{"a":1}`, []ValidationError{{"/a", "type", "expected string, got integer"}}},
		{"property names", `{"propertyNames":{"pattern":"^[a-z]+$"}}`, `{"a/b~":1}`, []ValidationError{{"/a~1b~0", "propertyNames", "invalid property name"}}},
		{"min properties", `{"minProperties":2}`, `{"a":1}`, []ValidationError{{"", "minProperties", "expected at least 2 properties"}}},
		{"max properties", `{"maxProperties":0}`, `{"a":1}`, []ValidationError{{"", "maxProperties", "expected at most 0 properties"}}},
		{"dependent required", `{"dependentRequired":{"a":["b"]}}`, `{"a":1}`, []ValidationError{{"/b", "dependentRequired", "required when a is present"}}},
		{"legacy dependency schema", `{"dependencies":{"a":{"required":["b"]}}}`, `{"a":1}`, []ValidationError{{"/b", "required", "missing"}}},
		{"dependent schema", `{"dependentSchemas":{"a":{"properties":{"b":{"type":"string"}}}}}`, `{"a":1,"b":2}`, []ValidationError{{"/b", "type", "expected string, got integer"}}},
		{"array items", `{"items":{"type":"string"}}`, `["ok",1]`, []ValidationError{{"/1", "type", "expected string, got integer"}}},
		{"prefix and items", `{"prefixItems":[{"type":"string"}],"items":false}`, `[1,2]`, []ValidationError{{"/0", "type", "expected string, got integer"}, {"/1", "false", "no value is allowed here"}}},
		{"legacy tuple", `{"items":[{"type":"string"}],"additionalItems":false}`, `[1,2]`, []ValidationError{{"/0", "type", "expected string, got integer"}, {"/1", "false", "no value is allowed here"}}},
		{"min items", `{"minItems":2}`, `[1]`, []ValidationError{{"", "minItems", "expected at least 2 items"}}},
		{"max items", `{"maxItems":0}`, `[1]`, []ValidationError{{"", "maxItems", "expected at most 0 items"}}},
		{"unique", `{"uniqueItems":true}`, `[1,1]`, []ValidationError{{"", "uniqueItems", "expected unique items"}}},
		{"contains", `{"contains":{"type":"string"}}`, `[1]`, []ValidationError{{"", "contains", "expected at least 1 matching item"}}},
		{"contains min", `{"contains":true,"minContains":2}`, `[1]`, []ValidationError{{"", "contains", "expected at least 2 matching items"}}},
		{"contains max", `{"contains":true,"minContains":0,"maxContains":1}`, `[1,2]`, []ValidationError{{"", "contains", "expected at most 1 matching item"}}},
		{"contains range", `{"contains":true,"minContains":1,"maxContains":2}`, `[]`, []ValidationError{{"", "contains", "expected between 1 and 2 matching items"}}},
		{"allOf", `{"allOf":[{"minimum":2},{"multipleOf":2}]}`, `1`, []ValidationError{{"", "minimum", "expected >= 2"}, {"", "multipleOf", "expected a multiple of 2"}}},
		{"anyOf deepest", `{"anyOf":[{"type":"string"},{"type":"object","properties":{"a":{"type":"string"}}}]}`, `{"a":1}`, []ValidationError{{"/a", "type", "expected string, got integer"}}},
		{"anyOf empty", `{"anyOf":[]}`, `1`, []ValidationError{{"", "anyOf", "matches no alternative"}}},
		{"oneOf empty", `{"oneOf":[]}`, `1`, []ValidationError{{"", "oneOf", "matches no alternative"}}},
		{"oneOf multiple", `{"oneOf":[{"type":"number"},{"type":"integer"}]}`, `1`, []ValidationError{{"", "oneOf", "matches more than one alternative"}}},
		{"not", `{"not":{"type":"string"}}`, `"x"`, []ValidationError{{"", "not", "must not match the schema in not"}}},
		{"then", `{"if":{"type":"number"},"then":{"minimum":2},"else":{"type":"string"}}`, `1`, []ValidationError{{"", "minimum", "expected >= 2"}}},
		{"else", `{"if":{"type":"number"},"then":false,"else":{"type":"string"}}`, `null`, []ValidationError{{"", "type", "expected string, got null"}}},
		{"reference", `{"$defs":{"n":{"type":"integer"}},"$ref":"#/$defs/n"}`, `"x"`, []ValidationError{{"", "type", "expected integer, got string"}}},
		{"boolean reference", `{"$defs":{"n":false},"$ref":"#/$defs/n"}`, `{}`, []ValidationError{{"", "false", "no value is allowed here"}}},
		{"unevaluated properties", `{"properties":{"a":true},"unevaluatedProperties":false}`, `{"a":1,"b":2}`, []ValidationError{{"/b", "unevaluatedProperties", "unknown field"}}},
		{"unevaluated property schema", `{"unevaluatedProperties":{"type":"string"}}`, `{"a":1}`, []ValidationError{{"/a", "type", "expected string, got integer"}}},
		{"unevaluated items", `{"prefixItems":[true],"unevaluatedItems":false}`, `[1,2]`, []ValidationError{{"/1", "unevaluatedItems", "unexpected item"}}},
		{"unevaluated item schema", `{"unevaluatedItems":{"type":"string"}}`, `[1]`, []ValidationError{{"/0", "type", "expected string, got integer"}}},
		{"contains indexes", `{"contains":{"const":2},"unevaluatedItems":false}`, `[2,1,2]`, []ValidationError{{"/1", "unevaluatedItems", "unexpected item"}}},
		{"dynamic fallback", `{"$defs":{"n":{"$dynamicAnchor":"n","type":"integer"}},"$dynamicRef":"#n"}`, `"x"`, []ValidationError{{"", "$dynamicRef", "does not match the dynamically referenced schema"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := explainTestSchema(t, tc.schema, compilerOptions{})
			var value any
			if err := json.Unmarshal([]byte(tc.value), &value); err != nil {
				t.Fatal(err)
			}
			if got := e.errors(value); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("errors = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestExplainExtendedValues(t *testing.T) {
	for _, tc := range []struct {
		schema string
		value  any
		want   []ValidationError
	}{
		{`{"type":"number"}`, math.NaN(), []ValidationError{{"", "type", "expected number, got NaN"}}},
		{`{"type":"number"}`, math.Inf(1), []ValidationError{{"", "type", "expected number, got Infinity"}}},
		{`{"type":"integer"}`, math.Inf(-1), []ValidationError{{"", "type", "expected integer, got -Infinity"}}},
		{`{"type":"string"}`, Undefined, []ValidationError{{"", "type", "expected string, got undefined"}}},
		{`{"type":"string"}`, []byte{1}, []ValidationError{{"", "type", "expected string, got bytes"}}},
		{`{"type":"string"}`, big.NewInt(1), []ValidationError{{"", "type", "expected string, got bigint"}}},
		{`{"type":"string"}`, []any{}, []ValidationError{{"", "type", "expected string, got array"}}},
		{`{"type":"bytes","minLength":2}`, []byte{1}, []ValidationError{{"", "minLength", "expected at least 2 bytes"}}},
		{`{"type":"bytes","maxLength":1}`, []byte{1, 2}, []ValidationError{{"", "maxLength", "expected at most 1 bytes"}}},
		{`{"properties":{"a":{"type":"string"}},"required":["a"]}`, map[string]any{"a": Undefined}, []ValidationError{{"/a", "required", "missing"}}},
		{`{"properties":{"a":{"type":"undefined"}},"required":["a"]}`, map[string]any{}, []ValidationError{{"/a", "required", "missing"}}},
		{`{"dependentRequired":{"a":["b"]}}`, map[string]any{"a": Undefined}, []ValidationError{{"/b", "dependentRequired", "required when a is present"}}},
		{`{"properties":{"a":{"type":"undefined"}},"required":["a"]}`, map[string]any{"a": "x"}, []ValidationError{{"/a", "type", "expected undefined, got string"}}},
	} {
		e := explainTestSchema(t, tc.schema, compilerOptions{})
		if got := e.errors(tc.value); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: errors = %+v, want %+v", tc.schema, got, tc.want)
		}
	}
	root := map[string]any{"type": "bigint", "minimum": big.NewInt(0), "multipleOf": big.NewInt(2)}
	e, err := newExplainer(newCompiler(root, compilerOptions{}), root)
	if err != nil {
		t.Fatal(err)
	}
	want := []ValidationError{{"", "minimum", "expected >= 0"}, {"", "multipleOf", "expected a multiple of 2"}}
	if got := e.errors(big.NewInt(-3)); !reflect.DeepEqual(got, want) {
		t.Fatalf("bigint errors = %+v", got)
	}
}

func TestExplainFormatsAndDrafts(t *testing.T) {
	e := explainTestSchema(t, `{"minLength":2,"pattern":"^a","format":"suffix"}`, compilerOptions{formats: map[string]func(string) bool{"suffix": func(s string) bool { return strings.HasSuffix(s, "z") }}})
	want := []ValidationError{{"", "minLength", "expected at least 2 characters"}, {"", "pattern", "expected to match ^a"}, {"", "format", "expected format suffix"}}
	if got := e.errors("b"); !reflect.DeepEqual(got, want) {
		t.Fatalf("format errors = %+v", got)
	}
	for _, draft := range []string{"7", "2020-12"} {
		e := explainTestSchema(t, `{"$defs":{"n":{"type":"number"}},"$ref":"#/$defs/n","const":1}`, compilerOptions{draft: draft})
		want := []ValidationError{{"", "type", "expected number, got string"}}
		if draft != "7" {
			want = append([]ValidationError{{"", "const", "expected 1"}}, want...)
		}
		if got := e.errors("x"); !reflect.DeepEqual(got, want) {
			t.Fatalf("draft %s: %+v", draft, got)
		}
		e = explainTestSchema(t, `{"contains":{"type":"string"},"minContains":2,"maxContains":3}`, compilerOptions{draft: draft})
		message := "expected between 2 and 3 matching items"
		if draft == "7" {
			message = "expected at least 1 matching item"
		}
		if got := e.errors([]any{1}); !reflect.DeepEqual(got, []ValidationError{{"", "contains", message}}) {
			t.Fatalf("draft %s contains: %+v", draft, got)
		}
	}
	e = explainTestSchema(t, `{"properties":{"t":{"$ref":"#missing"}}}`, compilerOptions{rejectUnresolved: true})
	if got := e.errors(map[string]any{"t": 1}); !reflect.DeepEqual(got, []ValidationError{{"/t", "$ref", "unresolved reference #missing"}}) {
		t.Fatalf("unresolved errors = %+v", got)
	}
	remote := map[string]any{"$id": "https://example.com/n", "type": "integer"}
	e = explainTestSchema(t, `{"$ref":"https://example.com/n"}`, compilerOptions{schemas: map[string]any{"https://example.com/n": remote}})
	if got := e.errors("x"); !reflect.DeepEqual(got, []ValidationError{{"", "type", "expected integer, got string"}}) {
		t.Fatalf("remote errors = %+v", got)
	}
}

func TestExplainUnionSelection(t *testing.T) {
	for _, refs := range []bool{false, true} {
		a := map[string]any{"type": "object", "properties": map[string]any{"kind": map[string]any{"const": "text"}, "text": map[string]any{"type": "string"}}, "required": []any{"kind", "text"}}
		b := map[string]any{"type": "object", "properties": map[string]any{"kind": map[string]any{"const": "image"}, "width": map[string]any{"minimum": 0}}, "required": []any{"kind", "width"}}
		branches := []any{a, b}
		if refs {
			branches = []any{map[string]any{"$ref": "#/$defs/a"}, map[string]any{"$ref": "#/$defs/b"}}
		}
		root := map[string]any{"anyOf": branches, "$defs": map[string]any{"a": a, "b": b}}
		e, err := newExplainer(newCompiler(root, compilerOptions{}), root)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			value any
			want  []ValidationError
		}{
			{map[string]any{"kind": "image", "width": -1}, []ValidationError{{"/width", "minimum", "expected >= 0"}}},
			{map[string]any{"kind": "video"}, []ValidationError{{"/kind", "anyOf", `expected one of ["text","image"]`}}},
			{map[string]any{}, []ValidationError{{"/kind", "anyOf", `expected one of ["text","image"]`}}},
		} {
			if got := e.errors(tc.value); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("union errors = %+v, want %+v", got, tc.want)
			}
		}
	}
	e := explainTestSchema(t, `{"anyOf":[{"type":"object","properties":{"a":true,"b":false},"required":["veryLongMissingField"]},{"type":"object","properties":{"a":{"type":"string"},"b":true},"required":["other"]}]}`, compilerOptions{})
	want := []ValidationError{{"/veryLongMissingField", "required", "missing"}, {"/b", "false", "no value is allowed here"}}
	if got := e.errors(map[string]any{"a": 1, "b": 1}); !reflect.DeepEqual(got, want) {
		t.Fatalf("first error depth selection = %+v", got)
	}
	// Pointer lengths use UTF-16 units, independent of their UTF-8 byte lengths.
	for _, key := range []string{"中", "😀"} {
		root := map[string]any{"anyOf": []any{
			map[string]any{"required": []any{key}}, map[string]any{"required": []any{"abc"}},
		}}
		e, err := newExplainer(newCompiler(root, compilerOptions{}), root)
		if err != nil {
			t.Fatal(err)
		}
		if got := e.errors(map[string]any{}); !reflect.DeepEqual(got, []ValidationError{{"/abc", "required", "missing"}}) {
			t.Fatalf("Unicode pointer selection = %+v", got)
		}
	}
	// More declared properties take precedence over a deeper first error.
	e = explainTestSchema(t, `{"anyOf":[{"type":"object","properties":{"a":true},"required":["veryLongMissingField"]},{"type":"object","properties":{"a":{"type":"string"},"b":true}}]}`, compilerOptions{})
	if got := e.errors(map[string]any{"a": 1, "b": 1}); !reflect.DeepEqual(got, []ValidationError{{"/a", "type", "expected string, got integer"}}) {
		t.Fatalf("declared key selection = %+v", got)
	}
}

func TestExplainSortedUnionSelection(t *testing.T) {
	const source = `{"anyOf":[{"properties":{"verylong":{"type":"string"},"a":{"type":"string"}}},{"properties":{"mid":{"type":"string"},"a":true}}]}`
	e := explainTestSchema(t, source, compilerOptions{})
	want := []ValidationError{{"/mid", "type", "expected string, got integer"}}
	// Rebuild maps in different orders; the first branch's first error is /a.
	for range 50 {
		for _, keys := range [][]string{{"verylong", "a", "mid"}, {"mid", "a", "verylong"}} {
			value := make(map[string]any)
			for _, key := range keys {
				value[key] = 1
			}
			if got := e.errors(value); !reflect.DeepEqual(got, want) {
				t.Fatalf("sorted branch selection = %+v, want %+v", got, want)
			}
		}
	}
}

func TestExplainUnevaluatedCache(t *testing.T) {
	for _, tc := range []struct {
		name, schema string
		value        any
		want         []ValidationError
	}{
		{"properties", `{"properties":{"a":true},"unevaluatedProperties":false}`,
			map[string]any{"extra": 1}, []ValidationError{{"/extra", "unevaluatedProperties", "unknown field"}}},
		{"items", `{"prefixItems":[true],"unevaluatedItems":false}`,
			[]any{1, 2}, []ValidationError{{"/1", "unevaluatedItems", "unexpected item"}}},
		{"referenced schema", `{"$defs":{"n":{"$id":"schemas/n.json","properties":{"a":true},"unevaluatedProperties":{"type":"integer"}}},"$ref":"#/$defs/n"}`,
			map[string]any{"extra": "x"}, []ValidationError{{"/extra", "type", "expected integer, got string"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := explainTestSchema(t, tc.schema, compilerOptions{})
			if got := e.errors(tc.value); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("errors = %+v, want %+v", got, tc.want)
			}
			checks, annotations := len(e.c.cache), len(e.c.annotatingCache)
			if len(e.evaluatedChecks) != 1 {
				t.Fatal("expected one auxiliary annotation check")
			}
			for range 1000 {
				if got := e.errors(tc.value); !reflect.DeepEqual(got, tc.want) {
					t.Fatal("cached explanation changed its errors")
				}
			}
			if len(e.evaluatedChecks) != 1 || len(e.c.cache) != checks || len(e.c.annotatingCache) != annotations {
				t.Fatal("repeated explanations grew the compilation caches")
			}
		})
	}
}

func TestExplainUnevaluatedCacheConcurrent(t *testing.T) {
	e := explainTestSchema(t, `{"properties":{"a":true},"unevaluatedProperties":false}`, compilerOptions{})
	value := map[string]any{"extra": 1}
	want := []ValidationError{{"/extra", "unevaluatedProperties", "unknown field"}}
	annotations := len(e.c.annotatingCache)
	// Race on the first explanation, before its auxiliary check is cached.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if got := e.errors(value); !reflect.DeepEqual(got, want) {
					t.Error("concurrent cached explanations differ")
					return
				}
			}
		})
	}
	wg.Wait()
	if len(e.evaluatedChecks) != 1 || len(e.c.annotatingCache) != annotations+1 {
		t.Fatal("concurrent explanations compiled duplicate auxiliary checks")
	}
}

func TestExplainUnevaluatedBases(t *testing.T) {
	e := explainTestSchema(t, `{"$id":"schemas/root.json","$defs":{"n":{"type":"integer"}},"$ref":"#/$defs/object","unevaluatedProperties":{"$ref":"#/$defs/n"},"properties":{"a":true}}`, compilerOptions{rejectUnresolved: true})
	// Rejected references take precedence over unevaluated errors.
	if got := e.errors(map[string]any{"b": "x"}); !reflect.DeepEqual(got, []ValidationError{{"", "$ref", "unresolved reference #/$defs/object"}}) {
		t.Fatalf("reference errors = %+v", got)
	}
	e = explainTestSchema(t, `{"$id":"schemas/root.json","$defs":{"n":{"type":"integer"}},"properties":{"a":true},"unevaluatedProperties":{"$ref":"#/$defs/n"}}`, compilerOptions{})
	if got := e.errors(map[string]any{"a": true, "b": "x"}); !reflect.DeepEqual(got, []ValidationError{{"/b", "type", "expected integer, got string"}}) {
		t.Fatalf("relative $id reapplied: %+v", got)
	}
	e = explainTestSchema(t, `{"allOf":[{"properties":{"a":true}},{"properties":{"b":true}}],"unevaluatedProperties":false}`, compilerOptions{})
	if got := e.errors(map[string]any{"a": 1, "b": 2, "c": 3}); !reflect.DeepEqual(got, []ValidationError{{"/c", "unevaluatedProperties", "unknown field"}}) {
		t.Fatalf("merged annotations = %+v", got)
	}
}

func TestExplainMemoAndRecursion(t *testing.T) {
	e := explainTestSchema(t, `{"$defs":{"node":{"anyOf":[{"type":"null"},{"type":"object","properties":{"value":{"type":"integer"},"next":{"$ref":"#/$defs/node"}},"required":["value","next"]}]}},"$ref":"#/$defs/node"}`, compilerOptions{})
	value := any(map[string]any{"value": "bad", "next": nil})
	path := ""
	for range 10 {
		value = map[string]any{"value": 1, "next": value}
		path += "/next"
	}
	want := []ValidationError{{path + "/value", "type", "expected integer, got string"}}
	if got := e.errors(value); !reflect.DeepEqual(got, want) {
		t.Fatalf("recursive errors = %+v", got)
	}
	e = explainTestSchema(t, `{"$defs":{"n":{"type":"object","properties":{"n":{"type":"integer"}}}},"properties":{"a":{"$ref":"#/$defs/n"},"b":{"$ref":"#/$defs/n"}}}`, compilerOptions{})
	shared := map[string]any{"n": "x"}
	want = []ValidationError{{"/a/n", "type", "expected integer, got string"}, {"/b/n", "type", "expected integer, got string"}}
	if got := e.errors(map[string]any{"a": shared, "b": shared}); !reflect.DeepEqual(got, want) {
		t.Fatalf("shared value paths = %+v", got)
	}
	var out []ValidationError
	walk := explanation{explainer: e}
	walk.walkRef("#/$defs/n", defaultBase, shared, "/same", &out)
	walk.walkRef("#/$defs/n", defaultBase, shared, "/same", &out)
	if len(walk.memo) != 1 || len(out) != 2 || out[0] != out[1] {
		t.Fatal("reference errors were not memoized")
	}
	// Slices with the same backing array and different lengths need separate entries.
	e = explainTestSchema(t, `{"$defs":{"n":{"items":{"type":"integer"},"minItems":3}},"$ref":"#/$defs/n"}`, compilerOptions{})
	walk = explanation{explainer: e}
	array := []any{"x", 2}
	out = nil
	walk.walkRef("#/$defs/n", defaultBase, array[:1], "", &out)
	walk.walkRef("#/$defs/n", defaultBase, array, "", &out)
	if len(walk.memo) != 2 {
		t.Fatal("slice views shared an error cache entry")
	}
}

func TestExplainRecursiveOneOf(t *testing.T) {
	e := explainTestSchema(t, `{"type":"object","properties":{"where":{"$ref":"#/$defs/F"}},"$defs":{"F":{"oneOf":[{"type":"object","properties":{"field":{"type":"string"},"eq":{"type":"string"}},"required":["field","eq"],"additionalProperties":false},{"type":"object","properties":{"and":{"type":"array","items":{"$ref":"#/$defs/F"}}},"required":["and"],"additionalProperties":false}]}}}`, compilerOptions{})
	value := map[string]any{"where": map[string]any{"and": []any{map[string]any{"field": "a", "eq": 1, "x": 2}}}}
	want := []ValidationError{
		{"/where/and/0/eq", "type", "expected string, got integer"},
		{"/where/and/0/x", "additionalProperties", "unknown field"},
	}
	if got := e.errors(value); !reflect.DeepEqual(got, want) {
		t.Fatalf("recursive oneOf errors = %+v", got)
	}
}

func TestExplainConcurrentAndReentrant(t *testing.T) {
	var e *explainer
	formats := map[string]func(string) bool{"nested": func(s string) bool {
		if s == "outer" {
			return len(e.errors("inner")) == 0
		}
		return false
	}}
	e = explainTestSchema(t, `{"type":"string","format":"nested"}`, compilerOptions{formats: formats})
	want := []ValidationError{{"", "format", "expected format nested"}}
	if got := e.errors("outer"); !reflect.DeepEqual(got, want) {
		t.Fatalf("reentrant errors = %+v", got)
	}
	e = explainTestSchema(t, `{"$id":"https://example.com/root","$dynamicAnchor":"node","$ref":"tree","unevaluatedProperties":false,"$defs":{"tree":{"$id":"tree","type":"object","properties":{"data":{"type":"integer"},"children":{"items":{"$dynamicRef":"https://example.com/root#node"}}}}}}`, compilerOptions{})
	value := map[string]any{"data": "x", "children": []any{map[string]any{"data": 1, "extra": 2}}}
	want = e.errors(value)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				if got := e.errors(value); !reflect.DeepEqual(got, want) {
					t.Error("concurrent explanations differ")
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestExplainMessages(t *testing.T) {
	for _, tc := range []struct {
		value float64
		want  string
	}{
		{math.NaN(), "NaN"}, {math.Inf(1), "Infinity"}, {math.Inf(-1), "-Infinity"},
		{math.Copysign(0, -1), "0"}, {1e-6, "0.000001"}, {1e-7, "1e-7"}, {1e20, "100000000000000000000"}, {1e21, "1e+21"},
	} {
		if got := numberText(tc.value); got != tc.want {
			t.Fatalf("number text = %s, want %s", got, tc.want)
		}
	}
	for _, tc := range []struct {
		min, max float64
		want     string
	}{
		{1, math.Inf(1), "expected at least 1 matching item"}, {2, math.Inf(1), "expected at least 2 matching items"},
		{0, 1, "expected at most 1 matching item"}, {0, 2, "expected at most 2 matching items"}, {1, 2, "expected between 1 and 2 matching items"},
	} {
		if got := containsMessage(tc.min, tc.max); got != tc.want {
			t.Fatalf("contains text = %s", got)
		}
	}
	values := make([]any, 25)
	for i := range values {
		values[i] = i
	}
	want := "[0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19, ... and 5 more]"
	if got := listValues(values); got != want {
		t.Fatalf("listed values = %s", got)
	}
	for _, tc := range []struct {
		value any
		want  string
	}{
		{Undefined, "undefined"}, {[]any{Undefined, math.NaN(), math.Inf(1)}, "[null,null,null]"},
		{map[string]any{"z": Undefined, "a": "<x>\n"}, `{"a":"<x>\n"}`}, {[]byte{1, 2}, `{"0":1,"1":2}`},
	} {
		if got := errorJSON(tc.value); got != tc.want {
			t.Fatalf("JSON text = %q, want %q", got, tc.want)
		}
	}
	for _, value := range []any{big.NewInt(1), []any{big.NewInt(1)}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("bigint JSON text must fail")
				}
			}()
			errorJSON(value)
		}()
	}
}

func TestExplainFallbackAndValidation(t *testing.T) {
	if _, err := newExplainer(newCompiler(nil, compilerOptions{}), nil); err == nil {
		t.Fatal("expected an invalid schema error")
	}
	e := explainTestSchema(t, `true`, compilerOptions{})
	if got := e.errors(1); !reflect.DeepEqual(got, []ValidationError{{"", "schema", "invalid"}}) {
		t.Fatalf("fallback = %+v", got)
	}
	var out []ValidationError
	walk := explanation{explainer: e}
	walk.walk(nil, defaultBase, 1, "", &out)
	walk.walk(true, defaultBase, 1, "", &out)
	if len(out) != 0 {
		t.Fatal("non-asserting schemas produced errors")
	}
	root := map[string]any{"enum": []any{1, 2, 3}}
	e, err := newExplainer(newCompiler(root, compilerOptions{}), root)
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(root, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	first := e.errors(4)
	first[0].Message = "changed"
	if got := e.errors(4); !reflect.DeepEqual(got, []ValidationError{{"", "enum", "expected one of [1,2,3]"}}) {
		t.Fatal("errors shared mutable output")
	}
	after, err := json.Marshal(root, json.Deterministic(true))
	if err != nil || !slices.Equal(before, after) {
		t.Fatal("explanation changed the schema", err)
	}
	if got := pointer("/a", strconv.Itoa(2)); got != "/a/2" {
		t.Fatal("array pointer differs")
	}
}
