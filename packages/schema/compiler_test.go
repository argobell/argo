package schema

import (
	"encoding/json/v2"
	"math"
	"math/big"
	"slices"
	"strings"
	"sync"
	"testing"
)

func compileTestSchema(t *testing.T, source string, options compilerOptions) (*compiler, compiledCheck) {
	t.Helper()
	var schema any
	if err := json.Unmarshal([]byte(source), &schema); err != nil {
		t.Fatal(err)
	}
	c := newCompiler(schema, options)
	check, err := c.root()
	if err != nil {
		t.Fatal(err)
	}
	return c, check
}

func TestCompilerBasicSchemas(t *testing.T) {
	for _, tc := range []struct {
		source string
		good   []any
		bad    []any
	}{
		{`true`, []any{nil, Undefined, 1, "x", []any{}, map[string]any{}}, nil},
		{`false`, nil, []any{nil, Undefined, 1, "x", []any{}, map[string]any{}}},
		{`{}`, []any{nil, 1, "x"}, nil},
		{`{"type":"string"}`, []any{"", "x"}, []any{nil, 1, true}},
		{`{"type":"number"}`, []any{1, 1.5}, []any{math.NaN(), math.Inf(1), big.NewInt(1), "1"}},
		{`{"type":"integer"}`, []any{1, 1.0}, []any{1.5, math.NaN(), nil}},
		{`{"type":"boolean"}`, []any{true, false}, []any{0, "true", nil}},
		{`{"type":"null"}`, []any{nil}, []any{Undefined, false}},
		{`{"type":"array"}`, []any{[]any{}}, []any{[]byte{}, nil, map[string]any{}}},
		{`{"type":"object"}`, []any{map[string]any{}}, []any{[]any{}, nil}},
		{`{"type":"bytes"}`, []any{[]byte{1}}, []any{[]any{1}, nil}},
		{`{"type":"bigint"}`, []any{big.NewInt(1)}, []any{1, (*big.Int)(nil)}},
		{`{"type":"undefined"}`, []any{Undefined}, []any{nil, "undefined"}},
		{`{"type":["integer","null"]}`, []any{1, nil}, []any{1.5, "1", Undefined}},
		{`{"type":["unknown","string",17]}`, []any{"x"}, []any{1, nil}},
		{`{"type":[]}`, nil, []any{nil, 1, "x"}},
		{`{"type":"unknown"}`, nil, []any{nil, 1, "x"}},
		{`{"type":null}`, nil, []any{nil, 1, "x"}},
	} {
		t.Run(tc.source, func(t *testing.T) {
			_, check := compileTestSchema(t, tc.source, compilerOptions{})
			for _, value := range tc.good {
				if !check.check(value) {
					t.Fatalf("rejected %v", value)
				}
			}
			for _, value := range tc.bad {
				if check.check(value) {
					t.Fatalf("accepted %v", value)
				}
			}
		})
	}
}

func TestCompilerBareChecks(t *testing.T) {
	for _, name := range []string{"string", "number", "integer", "boolean", "null", "array", "object", "bytes", "bigint", "undefined"} {
		node := map[string]any{"type": name}
		for key := range neutralKeywords {
			node[key] = "annotation"
		}
		c := newCompiler(node, compilerOptions{})
		check, err := c.root()
		if err != nil || check.typeBit != typeChecks[name].typeBit || leafOf(check) != leafOf(typeChecks[name]) {
			t.Fatalf("bare type %s lost metadata: %v", name, err)
		}
		if c.r.indexed || len(c.cache) != 0 {
			t.Fatal("bare types must not build an index or cache entry")
		}
	}
	for _, node := range []map[string]any{{"title": "annotation"}, {"type": Undefined}} {
		c := newCompiler(node, compilerOptions{})
		check, err := c.root()
		if err != nil || check.leaf.Code != 6 || !check.check(nil) {
			t.Fatal("annotation-only schemas must accept every value", err)
		}
	}
}

func TestCompilerInvalidSchema(t *testing.T) {
	for _, value := range []any{nil, map[string]any(nil), []any{}, 1, "string", Undefined} {
		_, err := newCompiler(value, compilerOptions{}).root()
		if err == nil || !strings.Contains(err.Error(), "a schema must be an object or a boolean") {
			t.Fatalf("schema %T: %v", value, err)
		}
	}
	_, err := newCompiler(map[string]any{"$defs": map[string]any{"x": nil}, "$ref": "#/$defs/x"}, compilerOptions{}).root()
	if err == nil {
		t.Fatal("a resolved null is still an invalid schema")
	}
}

func TestCompilerReferences(t *testing.T) {
	for _, source := range []string{
		`{"$defs":{"value":{"type":"integer"}},"$ref":"#/$defs/value"}`,
		`{"$id":"schemas/tool.json","$defs":{"value":{"type":"integer"}},"$ref":"#/$defs/value"}`,
		`{"$defs":{"value":{"$id":"child.json","$anchor":"value","type":"integer"}},"$ref":"child.json#value"}`,
		`{"$defs":{"a":{"$ref":"#/$defs/b"},"b":{"type":"integer"}},"$ref":"#/$defs/a"}`,
	} {
		for _, draft := range []string{"7", "2020-12"} {
			// Draft 7 does not register $anchor.
			if draft == "7" && strings.Contains(source, "$anchor") {
				continue
			}
			_, check := compileTestSchema(t, source, compilerOptions{draft: draft})
			if !check.check(1) || check.check("1") || check.check(nil) {
				t.Fatalf("draft=%s source=%s: reference check disagrees", draft, source)
			}
		}
	}
	for _, draft := range []string{"7", "2020-12"} {
		_, check := compileTestSchema(t, `{"$defs":{"n":{"type":"integer"}},"$ref":"#/$defs/n","type":"string"}`, compilerOptions{draft: draft})
		if check.check(1) != (draft == "7") || check.check("x") {
			t.Fatalf("draft %s reference siblings disagree", draft)
		}
	}
	_, ignored := compileTestSchema(t, `{"$ref":1,"type":"string"}`, compilerOptions{})
	if !ignored.check("x") || ignored.check(1) {
		t.Fatal("only string references are recognized")
	}
}

func TestCompilerRegisteredAndMissingReferences(t *testing.T) {
	options := compilerOptions{schemas: map[string]any{
		"https://example.com/schema.json": map[string]any{"type": "integer"},
	}}
	_, check := compileTestSchema(t, `{"$ref":"https://example.com/schema.json"}`, options)
	if !check.check(1) || check.check("1") {
		t.Fatal("registered schema was not compiled")
	}
	for _, source := range []string{`{"$ref":"#missing"}`, `{"$ref":"https://not-registered.invalid/root.json"}`} {
		var root any
		if err := json.Unmarshal([]byte(source), &root); err != nil {
			t.Fatal(err)
		}
		c := newCompiler(root, compilerOptions{})
		for range 2 {
			if _, err := c.root(); err == nil || len(c.cache) != 0 {
				t.Fatal("failed compilation must return an error and remove its cache slot")
			}
		}
		_, rejected := compileTestSchema(t, source, compilerOptions{rejectUnresolved: true})
		if rejected.check(1) || rejected.check(nil) {
			t.Fatal("an unresolved reference must compile as false when configured")
		}
	}
}

func TestCompilerCacheAndRecursiveSlots(t *testing.T) {
	root := map[string]any{"type": "integer", "$ref": "#"}
	c := newCompiler(root, compilerOptions{})
	check, err := c.root()
	if err != nil || len(c.cache) != 1 {
		t.Fatal("recursive compilation must connect one cache entry", err)
	}
	first := c.cache[schemaID(root)].check
	if _, err := c.root(); err != nil || c.cache[schemaID(root)].check != first {
		t.Fatal("repeated compilation must reuse the same slot", err)
	}
	// This value fails the type before reaching the non-progressing reference.
	// Executing a reference-only cycle has no terminating value traversal.
	if check.check("wrong type") {
		t.Fatal("recursive check lost its type assertion")
	}
	// Replace the root with a terminating target to verify the cached indirection
	// observes the connected slot, without executing a reference-only cycle.
	*first = typeChecks["integer"]
	if !check.check(1) || check.check("x") {
		t.Fatal("recursive reference did not use the connected slot")
	}
}

func TestCompilerScalarGroups(t *testing.T) {
	for _, tc := range []struct {
		source string
		code   int
		good   []any
		bad    []any
	}{
		{`{"type":"string","minLength":2,"maxLength":3}`, 12, []any{"a😀", "中文"}, []any{1, nil, "😀", "abcd"}},
		{`{"minLength":2}`, 0, []any{1, nil, "ab"}, []any{"a"}},
		{`{"type":"string","pattern":"^a"}`, 13, []any{"ab"}, []any{1, "ba"}},
		{`{"pattern":"^a"}`, 0, []any{1, "ab"}, []any{"ba"}},
		{`{"type":["string","null"],"minLength":2}`, 0, []any{nil, "ab"}, []any{1, "a"}},
		{`{"type":["string"],"minLength":2}`, 12, []any{"ab"}, []any{1, "a"}},
		{`{"type":"bytes","minLength":1,"maxLength":2}`, 0, []any{[]byte{1}, []byte{1, 2}}, []any{[]byte{}, []byte{1, 2, 3}, "a", []any{1}}},
		{`{"type":"bytes","pattern":"[","minimum":10}`, 0, []any{[]byte{1}}, []any{"x", 12}},
		{`{"type":"number","minimum":0,"maximum":4}`, 11, []any{0, 1.5, 4}, []any{-1, 5, "1", nil}},
		{`{"type":"integer","minimum":0,"multipleOf":2}`, 0, []any{0, 2, 4}, []any{-2, 1, 2.5, "2"}},
		{`{"type":"integer","minimum":0}`, 10, []any{0, 2}, []any{-1, 1.5, nil}},
		{`{"minimum":0}`, 0, []any{nil, "x", 0, big.NewInt(1)}, []any{-1, big.NewInt(-1), math.NaN()}},
		{`{"exclusiveMinimum":1,"exclusiveMaximum":3}`, 0, []any{2, "x"}, []any{1, 3}},
		{`{"type":"number","multipleOf":0.0001}`, 0, []any{0.0075}, []any{0.00755}},
		{`{"type":"number","multipleOf":0}`, 0, nil, []any{0, 1, nil}},
		{`{"type":"bigint","minimum":1.5,"maximum":3}`, 0, []any{big.NewInt(2), big.NewInt(3)}, []any{big.NewInt(1), 2}},
		{`{"type":["integer","null"],"minimum":1}`, 0, []any{nil, 1}, []any{0, 1.5, "1"}},
		{`{"type":"string","minimum":1}`, 0, []any{"x"}, []any{1, 0, nil}},
		{`{"type":"number","minLength":2}`, 0, []any{1}, []any{"ab", "a", nil}},
		{`{"type":"string","minLength":"2","maxLength":null,"pattern":false,"format":1}`, 1, []any{"", "a"}, []any{1}},
		{`{"type":"number","minimum":"1","multipleOf":false}`, 2, []any{0, 1.5}, []any{"1"}},
		{`{"type":"string","minLength":1.5,"maxLength":2.5}`, 12, []any{"ab"}, []any{"a", "abc"}},
		{`{"type":"string","minLength":-1,"maxLength":0}`, 12, []any{""}, []any{"a"}},
	} {
		t.Run(tc.source, func(t *testing.T) {
			_, check := compileTestSchema(t, tc.source, compilerOptions{})
			if check.leaf.Code != tc.code {
				t.Fatalf("leaf code = %d, want %d", check.leaf.Code, tc.code)
			}
			for _, value := range tc.good {
				if !check.check(value) || !sameLeafResult(check, value) {
					t.Fatalf("rejected %v or leaf disagrees", value)
				}
			}
			for _, value := range tc.bad {
				if check.check(value) || !sameLeafResult(check, value) {
					t.Fatalf("accepted %v or leaf disagrees", value)
				}
			}
		})
	}
}

func TestCompilerFormats(t *testing.T) {
	calls := 0
	options := compilerOptions{formats: map[string]func(string) bool{
		"suffix": func(s string) bool { calls++; return strings.HasSuffix(s, "z") },
	}}
	_, check := compileTestSchema(t, `{"minLength":2,"pattern":"^a","format":"suffix"}`, options)
	if !check.check(1) || check.check("a") || check.check("bz") || calls != 0 {
		t.Fatal("format ran before the type, length or pattern checks")
	}
	if !check.check("az") || check.check("ab") || calls != 2 {
		t.Fatal("registered format returned the wrong result")
	}
	for _, opts := range []compilerOptions{{}, options} {
		_, unknown := compileTestSchema(t, `{"type":"string","format":"unknown"}`, opts)
		if !unknown.check("anything") || unknown.check(1) || unknown.typeBit != stringBit {
			// The non-neutral format keyword enters plain(), but absent assertions
			// still return the shared bare type check through and's single part.
			t.Fatal("unregistered formats must remain annotations")
		}
	}
}

func TestCompilerScalarReferencesAndErrors(t *testing.T) {
	for _, draft := range []string{"7", "2020-12"} {
		_, check := compileTestSchema(t, `{"$defs":{"s":{"type":"string","minLength":2}},"$ref":"#/$defs/s","pattern":"^a"}`, compilerOptions{draft: draft})
		if !check.check("ab") || check.check("a") || check.check(1) || check.check("bb") != (draft == "7") {
			t.Fatal("reference siblings or scalar target disagree")
		}
	}
	_, ignored := compileTestSchema(t, `{"$defs":{"s":{"type":"string"}},"$ref":"#/$defs/s","pattern":"["}`, compilerOptions{draft: "7"})
	if !ignored.check("x") {
		t.Fatal("Draft 7 must ignore sibling patterns")
	}
	c := newCompiler(map[string]any{"type": "string", "pattern": "["}, compilerOptions{})
	for range 2 {
		if _, err := c.root(); err == nil || !strings.Contains(err.Error(), "pattern") || len(c.cache) != 0 {
			t.Fatal("invalid patterns must return an error and remove their cache entry")
		}
	}
	large, _ := new(big.Int).SetString("9007199254740993", 10)
	check, err := newCompiler(map[string]any{"type": "bigint", "minimum": large}, compilerOptions{}).root()
	if err != nil || !check.check(new(big.Int).Set(large)) || check.check(big.NewInt(9007199254740992)) {
		t.Fatal("bigint schema bounds lost precision", err)
	}
	for _, min := range []any{math.NaN(), math.Inf(1)} {
		check, err := newCompiler(map[string]any{"type": "number", "minimum": min}, compilerOptions{}).root()
		if err != nil || check.check(1) {
			t.Fatal("non-finite bounds changed meaning", err)
		}
	}
}

func TestCompilerObjectGroups(t *testing.T) {
	for _, tc := range []struct {
		source string
		good   []any
		bad    []any
	}{
		{`{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"],"additionalProperties":false}`,
			[]any{map[string]any{"n": 1}}, []any{nil, []any{}, map[string]any{}, map[string]any{"n": nil}, map[string]any{"n": 1, "extra": true}}},
		{`{"properties":{"n":{"type":"integer"}}}`, []any{nil, "x", map[string]any{}, map[string]any{"n": Undefined}}, []any{map[string]any{"n": nil}}},
		{`{"properties":{"n":{"type":"undefined"}},"required":["n"]}`, []any{map[string]any{"n": Undefined}}, []any{map[string]any{}, map[string]any{"n": nil}}},
		{`{"required":["n",17,"n"]}`, []any{map[string]any{"n": nil}}, []any{map[string]any{}}},
		{`{"patternProperties":{"^x":{"type":"number"},"z$":{"minimum":0}},"additionalProperties":false}`,
			[]any{map[string]any{"xz": 1}, map[string]any{}}, []any{map[string]any{"xz": -1}, map[string]any{"xz": "x"}, map[string]any{"a": 1}}},
		{`{"properties":{"xz":{"type":"integer"}},"patternProperties":{"^x":{"minimum":0}},"additionalProperties":{"type":"boolean"}}`,
			[]any{map[string]any{"xz": 1, "a": true}}, []any{map[string]any{"xz": 1.5}, map[string]any{"xz": -1}, map[string]any{"a": nil}}},
		{`{"propertyNames":{"pattern":"^[a-z]+$"},"minProperties":1,"maxProperties":2}`,
			[]any{map[string]any{"a": nil}, 1}, []any{map[string]any{}, map[string]any{"A": 1}, map[string]any{"a": 1, "b": 2, "c": 3}}},
		{`{"type":"object","properties":{"a":true},"minProperties":1,"maxProperties":1,"additionalProperties":false}`,
			[]any{map[string]any{"a": nil}}, []any{map[string]any{}, map[string]any{"a": nil, "b": 1}, 1}},
		{`{"dependentRequired":{"a":["b"]},"dependentSchemas":{"a":{"properties":{"b":{"type":"string"}}}}}`,
			[]any{map[string]any{}, map[string]any{"a": Undefined, "b": "x"}}, []any{map[string]any{"a": nil}, map[string]any{"a": nil, "b": 1}}},
		{`{"dependencies":{"a":["b"],"b":{"required":["c"]}}}`,
			[]any{map[string]any{}, map[string]any{"a": nil, "b": nil, "c": Undefined}}, []any{map[string]any{"a": nil}, map[string]any{"b": nil}}},
		{`{"type":["object","null"],"required":["a"]}`, []any{nil, map[string]any{"a": nil}}, []any{1, map[string]any{}}},
		{`{"type":"object","properties":[],"patternProperties":false,"required":true,"dependentRequired":{"a":false}}`,
			[]any{map[string]any{}}, []any{1}},
	} {
		t.Run(tc.source, func(t *testing.T) {
			_, check := compileTestSchema(t, tc.source, compilerOptions{})
			for _, value := range tc.good {
				if !check.check(value) {
					t.Fatalf("rejected %v", value)
				}
			}
			for _, value := range tc.bad {
				if check.check(value) {
					t.Fatalf("accepted %v", value)
				}
			}
		})
	}
	for _, draft := range []string{"7", "2020-12"} {
		_, check := compileTestSchema(t, `{"dependentRequired":{"a":["b"]},"dependentSchemas":{"a":false}}`, compilerOptions{draft: draft})
		if check.check(map[string]any{"a": nil}) != (draft == "7") {
			t.Fatal("draft-specific dependencies disagree")
		}
		_, legacy := compileTestSchema(t, `{"dependencies":{"a":["b"]}}`, compilerOptions{draft: draft})
		if legacy.check(map[string]any{"a": nil}) || !legacy.check(map[string]any{"a": nil, "b": Undefined}) {
			t.Fatal("legacy dependencies must apply in both drafts")
		}
	}
}

func TestCompilerArrayGroups(t *testing.T) {
	for _, tc := range []struct {
		source string
		draft  string
		good   []any
		bad    []any
	}{
		{`{"type":"array","items":{"type":"integer"},"minItems":1,"maxItems":2}`, "2020-12",
			[]any{[]any{1}, []any{1, 2}}, []any{nil, []any{}, []any{1, 2, 3}, []any{"1"}}},
		{`{"items":{"type":"integer"}}`, "2020-12", []any{nil, "x", []any{}, []any{1}}, []any{[]any{1.5}}},
		{`{"prefixItems":[{"type":"string"}],"items":{"type":"integer"}}`, "2020-12",
			[]any{[]any{}, []any{"x"}, []any{"x", 1}}, []any{[]any{1}, []any{"x", "y"}}},
		{`{"prefixItems":[{"type":"string"}],"items":false}`, "2020-12", []any{[]any{}, []any{"x"}}, []any{[]any{"x", 1}}},
		{`{"prefixItems":[false],"items":{"type":"integer"}}`, "7", []any{[]any{1}}, []any{[]any{"x"}}},
		{`{"items":[{"type":"string"}],"additionalItems":false}`, "7", []any{[]any{}, []any{"x"}}, []any{[]any{1}, []any{"x", 1}}},
		{`{"items":[{"type":"string"}],"additionalItems":{"type":"integer"}}`, "2020-12",
			[]any{[]any{"x", 1}}, []any{[]any{"x", false}}},
		{`{"items":true,"additionalItems":false}`, "7", []any{[]any{1, "x"}}, nil},
		{`{"items":[],"additionalItems":false}`, "7", []any{[]any{}}, []any{[]any{1}}},
		{`{"contains":{"type":"integer"},"minContains":2,"maxContains":2}`, "2020-12",
			[]any{[]any{1, "x", 2}, nil}, []any{[]any{1}, []any{1, 2, 3}}},
		{`{"contains":{"type":"integer"},"minContains":2,"maxContains":0}`, "7",
			[]any{[]any{1}, []any{1, 2, 3}}, []any{[]any{}, []any{"x"}}},
		{`{"contains":false,"minContains":0,"maxContains":0}`, "2020-12", []any{[]any{}, []any{1}}, nil},
		{`{"minContains":2,"maxContains":0}`, "2020-12", []any{[]any{}, []any{1}}, nil},
		{`{"type":"array","items":{"type":"integer"},"contains":{"minimum":2},"uniqueItems":true,"minItems":2}`, "2020-12",
			[]any{[]any{1, 2}}, []any{[]any{1}, []any{1, 1}, []any{1, "x"}}},
		{`{"uniqueItems":true}`, "2020-12", []any{[]any{1, "1"}}, []any{[]any{1, 1.0}, []any{map[string]any{"a": 1}, map[string]any{"a": 1.0}}}},
		{`{"type":["array","null"],"minItems":1}`, "2020-12", []any{nil, []any{1}}, []any{1, []any{}}},
	} {
		t.Run(tc.draft+tc.source, func(t *testing.T) {
			_, check := compileTestSchema(t, tc.source, compilerOptions{draft: tc.draft})
			for _, value := range tc.good {
				if !check.check(value) {
					t.Fatalf("rejected %v", value)
				}
			}
			for _, value := range tc.bad {
				if check.check(value) {
					t.Fatalf("accepted %v", value)
				}
			}
		})
	}
}

func TestCompilerContainerErrors(t *testing.T) {
	for _, source := range []string{
		`{"properties":{"a":null}}`, `{"patternProperties":{"[":true}}`,
		`{"patternProperties":{"^a":1}}`, `{"additionalProperties":null}`, `{"propertyNames":null}`,
		`{"dependentSchemas":{"a":null}}`, `{"dependencies":{"a":1}}`,
		`{"items":null}`, `{"items":[null]}`, `{"prefixItems":[null]}`,
		`{"contains":null}`, `{"items":[],"additionalItems":null}`,
	} {
		t.Run(source, func(t *testing.T) {
			var node any
			if err := json.Unmarshal([]byte(source), &node); err != nil {
				t.Fatal(err)
			}
			if _, err := newCompiler(node, compilerOptions{}).root(); err == nil {
				t.Fatal("expected a subschema or pattern error")
			}
		})
	}
	_, check := compileTestSchema(t, `{"prefixItems":[null],"additionalItems":null,"dependentSchemas":{"a":null}}`, compilerOptions{draft: "7"})
	if !check.check([]any{1}) || !check.check(map[string]any{"a": 1}) {
		t.Fatal("ignored draft-specific keywords must not compile their children")
	}
}

func TestCompilerRecursiveContainers(t *testing.T) {
	c, check := compileTestSchema(t, `{"$id":"trees/root.json","type":"object","properties":{"value":{"type":"integer"},"children":{"type":"array","items":{"$ref":"#"}}},"required":["value"],"additionalProperties":false}`, compilerOptions{})
	valid := map[string]any{"value": 1, "children": []any{map[string]any{"value": 2, "children": []any{map[string]any{"value": 3}}}}}
	invalid := map[string]any{"value": 1, "children": []any{map[string]any{"value": "bad"}}}
	if !check.check(valid) || check.check(invalid) || check.check(map[string]any{}) {
		t.Fatal("recursive object/array traversal disagrees")
	}
	if _, err := c.root(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				if !check.check(valid) || check.check(invalid) {
					t.Error("recursive compiled checks are not reusable")
					return
				}
			}
		})
	}
	wg.Wait()
	_, mutual := compileTestSchema(t, `{"$defs":{"a":{"type":"object","properties":{"b":{"$ref":"#/$defs/b"}}},"b":{"type":"array","items":{"$ref":"#/$defs/a"}}},"$ref":"#/$defs/a"}`, compilerOptions{})
	if !mutual.check(map[string]any{"b": []any{map[string]any{}}}) || mutual.check(map[string]any{"b": []any{1}}) {
		t.Fatal("mutually recursive schemas were not connected")
	}
}

func TestCompilerContainerSchemasReadOnly(t *testing.T) {
	var root any
	source := `{"type":"object","properties":{"list":{"type":"array","prefixItems":[{"type":"string"}],"items":{"type":"integer"},"uniqueItems":true}},"patternProperties":{"^x":{"type":"number"}},"additionalProperties":false,"required":["list"],"dependentRequired":{"list":["x"]},"dependentSchemas":{"x":{"minProperties":2}}}`
	if err := json.Unmarshal([]byte(source), &root); err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(root, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	check, err := newCompiler(root, compilerOptions{}).root()
	if err != nil {
		t.Fatal(err)
	}
	if !check.check(map[string]any{"list": []any{"a", 1}, "x": 2}) || check.check(map[string]any{"list": []any{"a", 1, 1}, "x": 2}) {
		t.Fatal("compiled container checks disagree")
	}
	after, err := json.Marshal(root, json.Deterministic(true))
	if err != nil || string(before) != string(after) {
		t.Fatal("container compilation or checking modified the schema", err)
	}
}

func TestCompilerContainerReferenceBases(t *testing.T) {
	_, check := compileTestSchema(t, `{"$id":"https://example.com/root.json","type":"object","properties":{"child":{"$id":"schemas/child.json","type":"array","items":{"$ref":"#/$defs/n"},"$defs":{"n":{"type":"integer","minimum":1}}}}}`, compilerOptions{})
	if !check.check(map[string]any{"child": []any{1}}) || check.check(map[string]any{"child": []any{0}}) {
		t.Fatal("nested container reference used the wrong base")
	}
	_, rejected := compileTestSchema(t, `{"properties":{"x":{"$ref":"#missing"}}}`, compilerOptions{rejectUnresolved: true})
	if !rejected.check(map[string]any{}) || rejected.check(map[string]any{"x": 1}) {
		t.Fatal("unresolved references must reject only their property")
	}
}

func TestCompilerInPlaceAssertions(t *testing.T) {
	for _, tc := range []struct {
		source string
		good   []any
		bad    []any
	}{
		{`{"const":null}`, []any{nil}, []any{Undefined, false}},
		{`{"const":{"a":[1]}}`, []any{map[string]any{"a": []any{1.0}}}, []any{map[string]any{}, map[string]any{"a": []any{2}}}},
		{`{"enum":[null,1,"x",{"a":1}]}`, []any{nil, 1.0, "x", map[string]any{"a": 1}}, []any{false, "1", map[string]any{"a": 2}}},
		{`{"enum":[]}`, nil, []any{nil, 1}},
		{`{"enum":[1,2],"const":2}`, []any{2}, []any{1, 3}},
		{`{"allOf":[{"type":"integer"},{"minimum":2}]}`, []any{2, 3}, []any{1, 2.5, nil}},
		{`{"allOf":[]}`, []any{nil, 1}, nil},
		{`{"anyOf":[]}`, nil, []any{nil, 1}},
		{`{"oneOf":[]}`, nil, []any{nil, 1}},
		{`{"anyOf":[{"type":"string"},{"type":"null"}]}`, []any{"x", nil}, []any{1, false}},
		{`{"oneOf":[{"type":"integer"},{"type":"number"}]}`, []any{1.5}, []any{1, "1"}},
		{`{"anyOf":[{"const":1},{"const":2}]}`, []any{1, 2}, []any{3, "1"}},
		{`{"oneOf":[{"const":1},{"const":1}]}`, nil, []any{1, nil}},
		{`{"oneOf":[{"const":1},{"type":"number"}]}`, []any{2, 1.5}, []any{1, "1"}},
		{`{"not":{"type":"string"}}`, []any{nil, 1}, []any{"x"}},
		{`{"not":true}`, nil, []any{nil, 1}},
		{`{"not":false}`, []any{nil, 1}, nil},
		{`{"if":{"type":"string"},"then":{"minLength":2},"else":{"const":0}}`, []any{"ab", 0}, []any{"a", 1, nil}},
		{`{"if":{"type":"string"},"then":{"minLength":2}}`, []any{"ab", 1}, []any{"a"}},
		{`{"if":{"type":"string"},"else":false}`, []any{"x"}, []any{1}},
		{`{"then":null,"else":null}`, []any{1, nil}, nil},
		{`{"if":null}`, []any{1, nil}, nil},
		{`{"enum":false,"allOf":true,"anyOf":{},"oneOf":null}`, []any{1, nil}, nil},
		{`{"type":"array","items":{"anyOf":[{"type":"integer"},{"const":null}]},"not":{"maxItems":0}}`, []any{[]any{1, nil}}, []any{[]any{}, []any{"1"}}},
		{`{"properties":{"a":{"type":"integer"}},"anyOf":[{"required":["a"]},{"required":["b"]}]}`, []any{map[string]any{"a": 1}, map[string]any{"b": 1}}, []any{map[string]any{}, map[string]any{"a": "1"}}},
	} {
		t.Run(tc.source, func(t *testing.T) {
			_, check := compileTestSchema(t, tc.source, compilerOptions{})
			for _, value := range tc.good {
				if !check.check(value) || !sameLeafResult(check, value) {
					t.Fatalf("rejected %v or leaf disagrees", value)
				}
			}
			for _, value := range tc.bad {
				if check.check(value) || !sameLeafResult(check, value) {
					t.Fatalf("accepted %v or leaf disagrees", value)
				}
			}
		})
	}
	undefined, err := newCompiler(map[string]any{"const": Undefined}, compilerOptions{}).root()
	if err != nil || !undefined.check(Undefined) || undefined.check(nil) {
		t.Fatal("an explicitly undefined constant must remain an assertion", err)
	}
}

func TestCompilerUnionOptimizations(t *testing.T) {
	for _, tc := range []struct {
		source string
		code   int
	}{
		{`{"anyOf":[{"const":1,"title":"one"},{"const":2}]}`, 8},
		{`{"oneOf":[{"const":1},{"const":2}]}`, 8},
		{`{"oneOf":[{"const":1},{"const":1}]}`, 0},
		{`{"anyOf":[{"const":{"a":1}},{"const":{"a":2}}]}`, 0},
		{`{"anyOf":[{"const":1,"minimum":2},{"const":2}]}`, 0},
		{`{"anyOf":[{"type":"string"},{"type":"null"}]}`, 9},
		{`{"oneOf":[{"type":"string"},{"type":"null"}]}`, 0},
	} {
		_, check := compileTestSchema(t, tc.source, compilerOptions{})
		if check.leaf.Code != tc.code {
			t.Fatalf("%s: leaf=%d, want %d", tc.source, check.leaf.Code, tc.code)
		}
	}
	for _, keyword := range []string{"anyOf", "oneOf"} {
		for _, refs := range []bool{false, true} {
			a := map[string]any{"type": "object", "properties": map[string]any{"kind": map[string]any{"const": "a"}, "n": map[string]any{"type": "integer"}}, "required": []any{"kind", "n"}}
			b := map[string]any{"type": "object", "properties": map[string]any{"kind": map[string]any{"const": "b"}, "s": map[string]any{"type": "string"}}, "required": []any{"kind", "s"}}
			branches := []any{a, b}
			if refs {
				branches = []any{map[string]any{"$ref": "#/$defs/a"}, map[string]any{"$ref": "#/$defs/b"}}
			}
			root := map[string]any{keyword: branches, "$defs": map[string]any{"a": a, "b": b}}
			check, err := newCompiler(root, compilerOptions{}).root()
			if err != nil {
				t.Fatal(err)
			}
			if !check.check(map[string]any{"kind": "a", "n": 1}) || !check.check(map[string]any{"kind": "b", "s": "x"}) || check.check(map[string]any{"kind": "a", "n": "1"}) || check.check(map[string]any{"kind": "c"}) || check.check(nil) {
				t.Fatal("tagged union selection disagrees")
			}
		}
	}
	// Missing required markers or duplicate tags must use the general union path.
	_, general := compileTestSchema(t, `{"oneOf":[{"type":"object","properties":{"kind":{"const":"a"}}},{"type":"object","properties":{"kind":{"const":"a"}},"required":["kind"]}]}`, compilerOptions{})
	if !general.check(map[string]any{}) || general.check(map[string]any{"kind": "a"}) {
		t.Fatal("ineligible tagged union was optimized incorrectly")
	}
}

func TestCompilerInPlaceOrder(t *testing.T) {
	var calls []string
	formats := map[string]func(string) bool{}
	for _, name := range []string{"root", "all", "first", "second", "not", "condition", "then", "else", "ref"} {
		formats[name] = func(string) bool { calls = append(calls, name); return true }
	}
	formats["not"] = func(string) bool { calls = append(calls, "not"); return false }
	_, check := compileTestSchema(t, `{"format":"root","allOf":[{"format":"all"}],"anyOf":[{"format":"first"},{"format":"second"}],"not":{"format":"not"},"if":{"format":"condition"},"then":{"format":"then"},"else":{"format":"else"},"$ref":"#/$defs/s","$defs":{"s":{"format":"ref"}}}`, compilerOptions{formats: formats})
	if !check.check("x") || !slices.Equal(calls, []string{"root", "all", "first", "not", "condition", "then", "ref"}) {
		t.Fatalf("in-place check order = %v", calls)
	}
	calls = nil
	_, mask := compileTestSchema(t, `{"anyOf":[{"format":"first"},{"type":"string"}]}`, compilerOptions{formats: formats})
	if !mask.check("x") || len(calls) != 0 {
		t.Fatal("bare-type union path must precede the remaining checks")
	}
}

func TestCompilerInPlaceErrorsAndDrafts(t *testing.T) {
	for _, source := range []string{
		`{"allOf":[null]}`, `{"anyOf":[1]}`, `{"oneOf":["x"]}`, `{"not":null}`,
		`{"if":null,"then":true}`, `{"if":true,"then":null}`, `{"if":false,"else":null}`,
		`{"anyOf":[{"$ref":"#missing"},true]}`,
	} {
		var schema any
		if err := json.Unmarshal([]byte(source), &schema); err != nil {
			t.Fatal(err)
		}
		if _, err := newCompiler(schema, compilerOptions{}).root(); err == nil {
			t.Fatalf("expected compilation error for %s", source)
		}
	}
	for _, draft := range []string{"7", "2020-12"} {
		_, check := compileTestSchema(t, `{"$ref":"#/$defs/n","$defs":{"n":{"type":"integer"}},"const":2}`, compilerOptions{draft: draft})
		if !check.check(2) || check.check("2") || check.check(1) != (draft == "7") {
			t.Fatal("reference siblings disagree")
		}
	}
	_, ignored := compileTestSchema(t, `{"$ref":"#/$defs/n","$defs":{"n":true},"anyOf":[null],"not":null}`, compilerOptions{draft: "7"})
	if !ignored.check(1) {
		t.Fatal("Draft 7 must ignore invalid sibling assertions")
	}
}

func TestCompilerRecursiveUnions(t *testing.T) {
	_, check := compileTestSchema(t, `{"$defs":{"node":{"anyOf":[{"type":"null"},{"type":"object","properties":{"value":{"type":"integer"},"next":{"$ref":"#/$defs/node"}},"required":["value","next"],"additionalProperties":false}]}},"$ref":"#/$defs/node"}`, compilerOptions{})
	valid := map[string]any{"value": 1, "next": map[string]any{"value": 2, "next": nil}}
	invalid := map[string]any{"value": 1, "next": map[string]any{"value": "bad", "next": nil}}
	if !check.check(nil) || !check.check(valid) || check.check(invalid) || check.check(map[string]any{"value": 1}) {
		t.Fatal("recursive union did not traverse its values correctly")
	}
	_, conditional := compileTestSchema(t, `{"type":"object","properties":{"kind":{"enum":["number","text"]},"value":true},"required":["kind","value"],"if":{"properties":{"kind":{"const":"number"}}},"then":{"properties":{"value":{"type":"number"}}},"else":{"properties":{"value":{"type":"string"}}}}`, compilerOptions{})
	if !conditional.check(map[string]any{"kind": "number", "value": 1}) || !conditional.check(map[string]any{"kind": "text", "value": "x"}) || conditional.check(map[string]any{"kind": "text", "value": 1}) {
		t.Fatal("conditional object checks disagree")
	}
}

func TestCompilerUnevaluated(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		good, bad    []string
	}{
		{"properties", `{"properties":{"a":{"type":"integer"}},"unevaluatedProperties":false}`, []string{`{}`, `{"a":1}`, `null`, `[]`}, []string{`{"a":"x"}`, `{"b":1}`}},
		{"patterns", `{"properties":{"a":{"type":"integer"}},"patternProperties":{"^a":{"minimum":0},"^b":{"type":"string"}},"unevaluatedProperties":false}`, []string{`{"a":1,"b":"x"}`, `{"abc":1}`}, []string{`{"a":-1}`, `{"a":1.5}`, `{"b":2}`, `{"c":1}`}},
		{"additional true", `{"additionalProperties":true,"unevaluatedProperties":false}`, []string{`{"a":1}`}, nil},
		{"additional schema", `{"additionalProperties":{"type":"integer"},"unevaluatedProperties":false}`, []string{`{"a":1}`}, []string{`{"a":"x"}`}},
		{"additional false", `{"additionalProperties":false,"unevaluatedProperties":true}`, []string{`{}`}, []string{`{"a":1}`}},
		{"property schema", `{"properties":{"a":true},"unevaluatedProperties":{"type":"string"}}`, []string{`{"a":1,"b":"x"}`}, []string{`{"b":1}`}},
		{"all properties", `{"allOf":[{"properties":{"a":true}},{"properties":{"b":true}}],"unevaluatedProperties":false}`, []string{`{"a":1,"b":2}`}, []string{`{"a":1,"c":3}`}},
		{"any passing branches", `{"anyOf":[{"properties":{"a":true}},{"properties":{"b":true}}],"unevaluatedProperties":false}`, []string{`{"a":1,"b":2}`}, []string{`{"c":3}`}},
		{"failed branch annotations", `{"anyOf":[{"properties":{"a":true},"required":["a"]},{"properties":{"b":false,"c":true}}],"unevaluatedProperties":false}`, []string{`{"a":1}`}, []string{`{"a":1,"b":2,"c":3}`}},
		{"one passing branch", `{"oneOf":[{"properties":{"a":true},"required":["a"]},{"properties":{"b":true},"required":["b"]}],"unevaluatedProperties":false}`, []string{`{"a":1}`, `{"b":1}`}, []string{`{}`, `{"a":1,"b":1}`, `{"a":1,"c":1}`}},
		{"not contributes nothing", `{"not":{"properties":{"a":false},"required":["a"]},"unevaluatedProperties":false}`, []string{`{}`}, []string{`{"a":1}`}},
		{"propertyNames contributes nothing", `{"propertyNames":{"pattern":"^a"},"unevaluatedProperties":false}`, []string{`{}`}, []string{`{"a":1}`}},
		{"if alone contributes", `{"if":{"properties":{"a":{"type":"integer"}}},"unevaluatedProperties":false}`, []string{`{"a":1}`, `{}`}, []string{`{"a":"x"}`, `{"b":1}`}},
		{"conditional properties", `{"if":{"properties":{"a":{"type":"integer"}},"required":["a"]},"then":{"properties":{"b":true}},"else":{"properties":{"c":true}},"unevaluatedProperties":false}`, []string{`{"a":1,"b":2}`, `{"c":3}`}, []string{`{"a":"x","c":3}`, `{"a":1,"c":3}`}},
		{"conditional missing then", `{"if":{"properties":{"a":true},"required":["a"]},"else":{"properties":{"b":true}},"unevaluatedProperties":false}`, []string{`{"a":1}`, `{"b":2}`}, []string{`{"a":1,"b":2}`}},
		{"conditional missing else", `{"if":{"properties":{"a":true},"required":["a"]},"then":{"properties":{"b":true}},"unevaluatedProperties":false}`, []string{`{"a":1,"b":2}`, `{}`}, []string{`{"b":2}`}},
		{"dependent schemas", `{"properties":{"a":true},"dependentSchemas":{"a":{"properties":{"b":true}}},"unevaluatedProperties":false}`, []string{`{"a":1,"b":2}`}, []string{`{"b":2}`}},
		{"reference annotations", `{"$defs":{"p":{"properties":{"a":true}}},"$ref":"#/$defs/p","unevaluatedProperties":false}`, []string{`{"a":null}`}, []string{`{"b":null}`}},
		{"sibling isolation", `{"allOf":[{"properties":{"a":true}},{"unevaluatedProperties":false}],"unevaluatedProperties":false}`, []string{`{}`}, []string{`{"a":1}`}},
		{"child annotations merge", `{"allOf":[{"unevaluatedProperties":true}],"unevaluatedProperties":false}`, []string{`{"a":1}`}, nil},
		{"instance locations", `{"properties":{"a":{"properties":{"x":true}},"b":{"unevaluatedProperties":false}},"unevaluatedProperties":false}`, []string{`{"a":{"x":1},"b":{}}`}, []string{`{"a":{"x":1},"b":{"x":1}}`}},
		{"all empty combinators", `{"allOf":[],"unevaluatedItems":false}`, []string{`[]`}, []string{`[1]`}},
		{"any empty combinators", `{"anyOf":[],"unevaluatedItems":true}`, nil, []string{`[]`}},
		{"one empty combinators", `{"oneOf":[],"unevaluatedItems":true}`, nil, []string{`[]`}},
		{"items false", `{"unevaluatedItems":false}`, []string{`[]`, `null`, `{}`}, []string{`[null]`}},
		{"item schema", `{"prefixItems":[true],"unevaluatedItems":{"type":"string"}}`, []string{`[]`, `[1,"x"]`}, []string{`[1,2]`}},
		{"uniform items", `{"items":{"type":"number"},"unevaluatedItems":false}`, []string{`[1,2]`}, []string{`[1,"x"]`}},
		{"prefix and rest", `{"prefixItems":[{"type":"string"}],"items":{"type":"number"},"unevaluatedItems":false}`, []string{`["x",1]`}, []string{`[1]`, `["x","y"]`}},
		{"legacy tuple", `{"items":[{"type":"string"}],"additionalItems":true,"unevaluatedItems":false}`, []string{`["x",1]`}, []string{`[1]`}},
		{"closed tuple", `{"items":[true],"additionalItems":false,"unevaluatedItems":true}`, []string{`[1]`}, []string{`[1,2]`}},
		{"prefix without rest", `{"prefixItems":[true],"unevaluatedItems":false}`, []string{`[]`, `[1]`}, []string{`[1,2]`}},
		{"contains indexes", `{"prefixItems":[true],"contains":{"type":"string"},"unevaluatedItems":false}`, []string{`[1,"x","y"]`}, []string{`[1,2,"x"]`, `[1]`}},
		{"contains zero", `{"contains":{"type":"string"},"minContains":0,"unevaluatedItems":false}`, []string{`[]`, `["x","y"]`}, []string{`[1]`}},
		{"contains bounds", `{"contains":{"type":"string"},"minContains":1,"maxContains":1,"unevaluatedItems":true}`, []string{`[1,"x"]`}, []string{`[]`, `["x","y"]`}},
		{"any item annotations", `{"anyOf":[{"prefixItems":[true]},{"contains":{"const":2}}],"unevaluatedItems":false}`, []string{`[1,2]`, `[2,2]`}, []string{`[1,3]`}},
		{"one item annotations", `{"oneOf":[{"contains":{"const":1}},{"contains":{"const":2}}],"unevaluatedItems":false}`, []string{`[1,1]`, `[2]`}, []string{`[1,2]`, `[1,3]`}},
		{"if item annotations", `{"if":{"contains":{"const":2}},"unevaluatedItems":false}`, []string{`[]`, `[2,2]`}, []string{`[1]`, `[1,2]`}},
		{"array reference", `{"$defs":{"p":{"prefixItems":[true]}},"$ref":"#/$defs/p","unevaluatedItems":false}`, []string{`[1]`}, []string{`[1,2]`}},
		{"array sibling isolation", `{"allOf":[{"prefixItems":[true]},{"unevaluatedItems":false}],"unevaluatedItems":false}`, []string{`[]`}, []string{`[1]`}},
		{"nested arrays", `{"prefixItems":[{"prefixItems":[true]},{"unevaluatedItems":false}],"unevaluatedItems":false}`, []string{`[[1],[]]`}, []string{`[[1],[1]]`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, check := compileTestSchema(t, tc.source, compilerOptions{})
			for _, group := range []struct {
				values []string
				want   bool
			}{{tc.good, true}, {tc.bad, false}} {
				for _, source := range group.values {
					var value any
					if err := json.Unmarshal([]byte(source), &value); err != nil {
						t.Fatal(err)
					}
					if got := check.check(value); got != group.want {
						t.Fatalf("%s: got %v, want %v", source, got, group.want)
					}
				}
			}
		})
	}
	_, check := compileTestSchema(t, `{"properties":{"a":{"type":"undefined"}},"unevaluatedProperties":false}`, compilerOptions{})
	if !check.check(map[string]any{"a": Undefined}) || check.check(map[string]any{"a": nil}) || check.check(map[string]any{"b": Undefined}) {
		t.Fatal("explicit undefined values must be evaluated as present properties")
	}
}

func TestCompilerDynamicReferences(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		good, bad    []string
		dynamic      bool
	}{
		{"same resource", `{"$defs":{"n":{"$dynamicAnchor":"n","type":"integer"}},"$dynamicRef":"#n"}`, []string{`1`}, []string{`"x"`}, true},
		{"static anchor", `{"$defs":{"n":{"$anchor":"n","type":"integer"}},"$dynamicRef":"#n"}`, []string{`1`}, []string{`"x"`}, false},
		{"different dynamic anchor", `{"$defs":{"n":{"$anchor":"n","$dynamicAnchor":"other","type":"integer"}},"$dynamicRef":"#n"}`, []string{`1`}, []string{`"x"`}, false},
		{"pointer is static", `{"$defs":{"n":{"$dynamicAnchor":"n","type":"integer"}},"$dynamicRef":"#/$defs/n"}`, []string{`1`}, []string{`"x"`}, false},
		{"boolean target", `{"$defs":{"n":false},"$dynamicRef":"#/$defs/n"}`, nil, []string{`1`}, false},
		{"fragment absent", `{"$defs":{"n":{"$id":"n","type":"integer"}},"$dynamicRef":"n"}`, []string{`1`}, []string{`"x"`}, false},
		{"outermost anchor", `{"$id":"https://example.com/root","$defs":{"tag":{"$dynamicAnchor":"n","type":"integer"},"inner":{"$id":"inner","$dynamicRef":"#n","$defs":{"n":{"$dynamicAnchor":"n","type":"string"}}}},"$ref":"inner"}`, []string{`1`}, []string{`"x"`}, true},
		{"static anchor does not override", `{"$id":"https://example.com/root","$defs":{"tag":{"$anchor":"n","type":"integer"},"inner":{"$id":"inner","$dynamicRef":"#n","$defs":{"n":{"$dynamicAnchor":"n","type":"string"}}}},"$ref":"inner"}`, []string{`"x"`}, []string{`1`}, true},
		{"scope restored", `{"$id":"https://example.com/root","if":{"$id":"first","$defs":{"n":{"$dynamicAnchor":"n","type":"number"}}},"then":{"$id":"second","$ref":"start","$defs":{"n":{"$dynamicAnchor":"n","type":"null"}}},"$defs":{"start":{"$id":"start","$dynamicRef":"inner#n"},"inner":{"$id":"inner","$dynamicAnchor":"n","type":"string"}}}`, []string{`null`}, []string{`1`, `"x"`}, true},
		{"pointer resource scopes", `{"$id":"https://example.com/root","$ref":"first#/$defs/check","$defs":{"first":{"$id":"first","$defs":{"check":{"$ref":"second#/$defs/check"}}},"second":{"$id":"second","$defs":{"check":{"$ref":"third#/$defs/check"},"n":{"$dynamicAnchor":"n","maxLength":2}}},"third":{"$id":"third","$defs":{"check":{"$dynamicRef":"#n"},"n":{"$dynamicAnchor":"n","maxLength":3}}}}}`, []string{`"hi"`}, []string{`"hey"`}, true},
		{"annotating static reference", `{"$defs":{"p":{"$anchor":"p","properties":{"a":true}}},"$dynamicRef":"#p","unevaluatedProperties":false}`, []string{`{"a":1}`}, []string{`{"b":2}`}, false},
		{"annotating dynamic reference", `{"$defs":{"p":{"$dynamicAnchor":"p","properties":{"a":true}}},"$dynamicRef":"#p","unevaluatedProperties":false}`, []string{`{"a":1}`}, []string{`{"b":2}`}, true},
		{"annotating array reference", `{"$defs":{"p":{"$dynamicAnchor":"p","prefixItems":[true]}},"$dynamicRef":"#p","unevaluatedItems":false}`, []string{`[1]`}, []string{`[1,2]`}, true},
		{"annotating resource scopes", `{"$id":"https://example.com/root","$ref":"inner#/$defs/check","$defs":{"p":{"$dynamicAnchor":"p","properties":{"a":true}},"inner":{"$id":"inner","$defs":{"p":{"$dynamicAnchor":"p","properties":{"b":true}},"check":{"$dynamicRef":"#p"}}}},"unevaluatedProperties":false}`, []string{`{"a":1}`}, []string{`{"b":2}`}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, check := compileTestSchema(t, tc.source, compilerOptions{})
			if c.dynamic != tc.dynamic {
				t.Fatalf("dynamic tracking = %v, want %v", c.dynamic, tc.dynamic)
			}
			for _, group := range []struct {
				values []string
				want   bool
			}{{tc.good, true}, {tc.bad, false}} {
				for _, source := range group.values {
					var value any
					if err := json.Unmarshal([]byte(source), &value); err != nil {
						t.Fatal(err)
					}
					if got := check.check(value); got != group.want {
						t.Fatalf("%s: got %v, want %v", source, got, group.want)
					}
				}
			}
		})
	}
}

func TestCompilerAnnotationErrorsAndDrafts(t *testing.T) {
	for _, source := range []string{
		`{"unevaluatedProperties":null}`, `{"unevaluatedItems":null}`,
		`{"properties":{"a":null},"unevaluatedProperties":true}`,
		`{"patternProperties":{"[":true},"unevaluatedProperties":true}`,
		`{"patternProperties":{"a":null},"unevaluatedProperties":true}`,
		`{"additionalProperties":null,"unevaluatedProperties":true}`,
		`{"prefixItems":[null],"unevaluatedItems":true}`,
		`{"items":null,"unevaluatedItems":true}`, `{"contains":null,"unevaluatedItems":true}`,
		`{"allOf":[null],"unevaluatedItems":true}`, `{"anyOf":[null],"unevaluatedItems":true}`,
		`{"oneOf":[null],"unevaluatedItems":true}`, `{"if":null,"unevaluatedItems":true}`,
		`{"if":true,"then":null,"unevaluatedItems":true}`, `{"if":true,"else":null,"unevaluatedItems":true}`,
		`{"$ref":"#missing","unevaluatedItems":true}`, `{"$dynamicRef":"#missing"}`,
		`{"$dynamicRef":"#missing","unevaluatedItems":true}`,
		`{"dependentSchemas":{"a":null},"unevaluatedItems":true}`,
		`{"$dynamicRef":"#n","$defs":{"n":{"$dynamicAnchor":"n","not":null}}}`,
		`{"$dynamicRef":"#n","$defs":{"n":{"$dynamicAnchor":"n","not":null}},"unevaluatedProperties":true}`,
	} {
		var node any
		if err := json.Unmarshal([]byte(source), &node); err != nil {
			t.Fatal(err)
		}
		if _, err := newCompiler(node, compilerOptions{}).root(); err == nil {
			t.Fatalf("expected error for %s", source)
		}
	}
	_, ignored := compileTestSchema(t, `{"unevaluatedProperties":null,"unevaluatedItems":null,"$dynamicRef":"#missing"}`, compilerOptions{draft: "7"})
	if !ignored.check(map[string]any{"a": 1}) || !ignored.check([]any{1}) {
		t.Fatal("Draft 7 must ignore unevaluated keywords and dynamic references")
	}
	for _, source := range []string{`{"$dynamicRef":"#missing"}`, `{"$dynamicRef":"#missing","unevaluatedProperties":true}`} {
		_, rejected := compileTestSchema(t, source, compilerOptions{rejectUnresolved: true})
		if rejected.check(1) {
			t.Fatal("unresolved dynamic references must honor rejection policy")
		}
	}
	_, inactive := compileTestSchema(t, `{"$defs":{"unused":{"$dynamicRef":"#missing"}},"unevaluatedProperties":true}`, compilerOptions{})
	if !inactive.check(map[string]any{"a": 1}) {
		t.Fatal("unreferenced definitions must remain uncompiled")
	}
}

func TestCompilerDynamicTargetDiscovery(t *testing.T) {
	var root, b, c any
	for _, entry := range []struct {
		source string
		target *any
	}{
		{`{"$id":"https://example.com/root","$ref":"a","$defs":{"n":{"$dynamicAnchor":"n","$ref":"b"},"m":{"$dynamicAnchor":"m","type":"integer"},"a":{"$id":"a","$dynamicRef":"#n","$defs":{"n":{"$dynamicAnchor":"n","type":"string"}}}}}`, &root},
		{`{"$id":"https://example.com/b","$dynamicRef":"c#m"}`, &b},
		{`{"$id":"https://example.com/c","$dynamicAnchor":"m","type":"boolean"}`, &c},
	} {
		if err := json.Unmarshal([]byte(entry.source), entry.target); err != nil {
			t.Fatal(err)
		}
	}
	for _, annotate := range []bool{false, true} {
		if annotate {
			root.(map[string]any)["unevaluatedProperties"] = false
		}
		compiler := newCompiler(root, compilerOptions{schemas: map[string]any{"https://example.com/b": b, "https://example.com/c": c}})
		check, err := compiler.root()
		if err != nil {
			t.Fatal(err)
		}
		if !check.check(1) || check.check("x") || check.check(true) {
			t.Fatal("a selected dynamic target did not resolve its nested references")
		}
		for uri, target := range map[string]any{"https://example.com/b": b, "https://example.com/c": c} {
			if !compiler.r.isResource(target.(map[string]any), uri) {
				t.Fatalf("dynamic target resource %s was not loaded", uri)
			}
		}
	}
}

func TestCompilerInvalidDynamicTarget(t *testing.T) {
	// The overriding anchor is detached from the ordinary reference path.
	var root any
	if err := json.Unmarshal([]byte(`{"$id":"https://example.com/root","$ref":"inner","$defs":{"n":{"$dynamicAnchor":"n","not":null},"inner":{"$id":"inner","$dynamicRef":"#n","$defs":{"n":{"$dynamicAnchor":"n","type":"string"}}}}}`), &root); err != nil {
		t.Fatal(err)
	}
	for _, annotate := range []bool{false, true} {
		if annotate {
			root.(map[string]any)["unevaluatedProperties"] = false
		}
		check, err := newCompiler(root, compilerOptions{}).root()
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			func() {
				defer func() {
					if _, ok := recover().(error); !ok {
						t.Fatal("expected a selected target compilation error")
					}
				}()
				check.check(1)
			}()
		}
	}
}

func TestCompilerShadowedDynamicTarget(t *testing.T) {
	for _, annotate := range []bool{false, true} {
		var root any
		if err := json.Unmarshal([]byte(`{"$id":"https://example.com/root","$ref":"middle","$defs":{"n":{"$dynamicAnchor":"n","type":"integer"},"middle":{"$id":"middle","$ref":"leaf","$defs":{"n":{"$dynamicAnchor":"n","$ref":"missing"}}},"leaf":{"$id":"leaf","$dynamicRef":"#n","$defs":{"n":{"$dynamicAnchor":"n","type":"number"}}}}}`), &root); err != nil {
			t.Fatal(err)
		}
		if annotate {
			root.(map[string]any)["unevaluatedProperties"] = false
		}
		c := newCompiler(root, compilerOptions{})
		check, err := c.root()
		if err != nil {
			t.Fatal(err)
		}
		// Race on the first lookup, before either dynamic cache has been filled.
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				for range 25 {
					if !check.check(1) || check.check(1.5) {
						t.Error("the outermost dynamic anchor was not selected")
						return
					}
				}
			})
		}
		wg.Wait()
		if _, loaded := c.r.resources["https://example.com/missing"]; loaded {
			t.Fatal("a shadowed target loaded its reference")
		}
	}
}

func TestCompilerRecursiveAnnotationCache(t *testing.T) {
	_, check := compileTestSchema(t, `{"$defs":{"node":{"type":"object","properties":{"next":{"$ref":"#/$defs/node"}},"unevaluatedProperties":false}},"$ref":"#/$defs/node","unevaluatedProperties":false}`, compilerOptions{})
	if !check.check(map[string]any{"next": map[string]any{"next": map[string]any{}}}) || check.check(map[string]any{"next": map[string]any{"extra": 1}}) {
		t.Fatal("recursive annotation slots were not connected")
	}
}

func TestCompilerDynamicRecursiveTree(t *testing.T) {
	var tree any
	if err := json.Unmarshal([]byte(`{"$id":"https://example.com/tree","$dynamicAnchor":"node","type":"object","properties":{"data":true,"children":{"type":"array","items":{"$dynamicRef":"#node"}}}}`), &tree); err != nil {
		t.Fatal(err)
	}
	options := compilerOptions{schemas: map[string]any{"https://example.com/tree": tree}}
	_, check := compileTestSchema(t, `{"$id":"https://example.com/strict","$dynamicAnchor":"node","$ref":"tree","unevaluatedProperties":false}`, options)
	good := map[string]any{"data": 1, "children": []any{map[string]any{"data": 2, "children": []any{map[string]any{"data": 3}}}}}
	bad := map[string]any{"data": 1, "children": []any{map[string]any{"daat": 2}}}
	if !check.check(good) || check.check(bad) {
		t.Fatal("recursive references did not apply the outer resource's unevaluated constraint")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if !check.check(good) || check.check(bad) {
					t.Error("recursive dynamic check changed across concurrent evaluations")
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestCompilerDynamicReentrantChecks(t *testing.T) {
	const source = `{"$id":"https://example.com/main","properties":{"kind":true,"list":true},"if":{"properties":{"kind":{"const":"numbers"}},"required":["kind"]},"then":{"$ref":"numbers"},"else":{"$ref":"strings"},"$defs":{"generic":{"$id":"generic","properties":{"list":{"items":{"format":"reenter","$dynamicRef":"#item"}}},"$defs":{"item":{"$dynamicAnchor":"item"}}},"numbers":{"$id":"numbers","$ref":"generic","$defs":{"item":{"$dynamicAnchor":"item","type":"number"}}},"strings":{"$id":"strings","$ref":"generic","$defs":{"item":{"$dynamicAnchor":"item","type":"string"}}}}}`
	for _, annotate := range []bool{false, true} {
		var root any
		if err := json.Unmarshal([]byte(source), &root); err != nil {
			t.Fatal(err)
		}
		if annotate {
			root.(map[string]any)["unevaluatedProperties"] = false
		}
		before, err := json.Marshal(root, json.Deterministic(true))
		if err != nil {
			t.Fatal(err)
		}
		numbers := map[string]any{"kind": "numbers", "list": []any{1, 2}}
		strings := map[string]any{"kind": "strings", "list": []any{"outer"}}
		badNumbers := map[string]any{"kind": "numbers", "list": []any{"outer"}}
		badStrings := map[string]any{"kind": "strings", "list": []any{1}}
		var check compiledCheck
		formats := map[string]func(string) bool{"reenter": func(value string) bool { return value != "outer" || check.check(numbers) }}
		c := newCompiler(root, compilerOptions{formats: formats})
		check, err = c.root()
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				for range 50 {
					if !check.check(numbers) || !check.check(strings) || check.check(badNumbers) || check.check(badStrings) {
						t.Error("dynamic paths leaked scope across evaluations")
						return
					}
				}
			})
		}
		wg.Wait()
		after, err := json.Marshal(root, json.Deterministic(true))
		if err != nil || string(before) != string(after) {
			t.Fatal("compilation or validation changed the schema", err)
		}
		if _, err := c.root(); err != nil {
			t.Fatal("compiled root could not be reused", err)
		}
	}
}

func TestCompilerLongPatterns(t *testing.T) {
	value := strings.Repeat("a", 50000)
	for _, tc := range []struct {
		name, source string
		good, bad    any
	}{
		{"leaf", `{"type":"string","pattern":"^(a|b)+$"}`, value, value + "c"},
		{"loose", `{"pattern":"^(a|b)+$"}`, value, value + "c"},
		{"combined", `{"type":"string","minLength":1,"pattern":"^(a|b)+$"}`, value, value + "c"},
		{"array leaf", `{"type":"array","items":{"type":"string","pattern":"^(a|b)+$"}}`, []any{value}, []any{value + "c"}},
		{"pattern properties", `{"patternProperties":{"^(a|b)+$":true},"additionalProperties":false}`, map[string]any{value: 1}, map[string]any{value + "c": 1}},
		{"pattern annotations", `{"patternProperties":{"^(a|b)+$":true},"unevaluatedProperties":false}`, map[string]any{value: 1}, map[string]any{value + "c": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, check := compileTestSchema(t, tc.source, compilerOptions{})
			if !check.check(tc.good) || check.check(tc.bad) {
				t.Fatal("long pattern validation disagrees")
			}
		})
	}
}

func TestCompilerECMAScriptPatterns(t *testing.T) {
	for _, source := range []string{
		`{"pattern":"^\\141$"}`,
		`{"patternProperties":{"^\\141$":true}}`,
		`{"patternProperties":{"^\\141$":true},"unevaluatedProperties":false}`,
	} {
		var schema any
		if err := json.Unmarshal([]byte(source), &schema); err != nil {
			t.Fatal(err)
		}
		if _, err := newCompiler(schema, compilerOptions{}).root(); err == nil {
			t.Fatalf("expected Unicode pattern error for %s", source)
		}
	}
	for _, tc := range []struct {
		name, source string
		good, bad    any
	}{
		{"pattern leaf", `{"type":"string","pattern":"^(a+)\\1$"}`, "aaaa", "aaa"},
		{"pattern loose", `{"pattern":"^(?=.*[0-9]).+$"}`, "abc1", "abc"},
		{"pattern combined", `{"type":"string","minLength":2,"pattern":"^\\s+$"}`, "\u00a0\u00a0", "a "},
		{"property names", `{"propertyNames":{"pattern":"^(?=x).+$"}}`, map[string]any{"xyz": 1}, map[string]any{"abc": 1}},
		{"pattern properties", `{"patternProperties":{"^(?=x).+$":{"type":"integer"}},"additionalProperties":false}`, map[string]any{"xyz": 1}, map[string]any{"abc": 1}},
		{"pattern property assertion", `{"patternProperties":{"^(?=x).+$":{"type":"integer"}},"additionalProperties":false}`, map[string]any{"xyz": 1}, map[string]any{"xyz": "1"}},
		{"pattern annotations", `{"patternProperties":{"^(?=x).+$":{"type":"integer"}},"unevaluatedProperties":false}`, map[string]any{"xyz": 1}, map[string]any{"abc": 1}},
		{"pattern annotation assertion", `{"patternProperties":{"^(?=x).+$":{"type":"integer"}},"unevaluatedProperties":false}`, map[string]any{"xyz": 1}, map[string]any{"xyz": "1"}},
		{"nested pattern leaf", `{"type":"array","items":{"type":"string","pattern":"^(a+)\\1$"}}`, []any{"aaaa"}, []any{"aaa"}},
		{"dot semantics", `{"type":"string","pattern":"^.$"}`, "😀", "\r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, check := compileTestSchema(t, tc.source, compilerOptions{})
			if !check.check(tc.good) || check.check(tc.bad) || !sameLeafResult(check, tc.good) || !sameLeafResult(check, tc.bad) {
				t.Fatal("compiled pattern or leaf result disagrees")
			}
		})
	}
	_, check := compileTestSchema(t, `{"type":"object","patternProperties":{"^(?=x).+$":{"type":"string","pattern":"^(?<word>ab)\\k<word>$"}},"unevaluatedProperties":false}`, compilerOptions{})
	good, bad := map[string]any{"xyz": "abab"}, map[string]any{"xyz": "ab"}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if !check.check(good) || check.check(bad) {
					t.Error("concurrent pattern checks disagree")
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestCompilerReadOnlyAndConcurrentChecks(t *testing.T) {
	var root any
	if err := json.Unmarshal([]byte(`{"$id":"schemas/root.json","$defs":{"n":{"type":["integer","null"]}},"$ref":"#/$defs/n"}`), &root); err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(root, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	check, err := newCompiler(root, compilerOptions{}).root()
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(root, json.Deterministic(true))
	if err != nil || string(before) != string(after) {
		t.Fatal("compilation modified the schema", err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				if !check.check(1) || !check.check(nil) || check.check("x") {
					t.Error("compiled check returned inconsistent results")
					return
				}
			}
		})
	}
	wg.Wait()
}
