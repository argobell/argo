package schema

import (
	"encoding/json/v2"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"sync"
	"testing"
)

func coerceTestSchema(t *testing.T, source string, options compilerOptions) *coercer {
	t.Helper()
	c, _ := compileTestSchema(t, source, options)
	return newCoercer(c, c.schema)
}

func TestCoercePrimitives(t *testing.T) {
	for _, tc := range []struct {
		typeName    string
		input, want any
	}{
		{"number", "42", 42}, {"number", "", 0}, {"number", "true", 1},
		{"number", "FALSE", 0}, {"number", nil, 0}, {"number", Undefined, 0},
		{"number", "12n", 12}, {"number", "-12n", -12},
		{"number", big.NewInt(9007199254740991), float64(9007199254740991)},
		{"number", big.NewInt(9007199254740992), big.NewInt(9007199254740992)},
		{"number", "0x10", 16}, {"number", "0B11", 3}, {"number", "0o10", 8},
		{"number", "\uFEFF12\u00a0", 12}, {"number", ".5", .5},
		{"number", "1.", 1}, {"number", "1e2", 100}, {"number", "1e-999", 0},
		{"number", "1_000", "1_000"}, {"number", "0x1p2", "0x1p2"},
		{"number", "+0x1", "+0x1"}, {"number", "-0b1", "-0b1"},
		{"number", "Infinity", "Infinity"}, {"number", "NaN", "NaN"},
		{"number", "1e999", "1e999"}, {"number", " true ", " true "},
		{"number", "\u0085", "\u0085"}, {"number", "\u180e", "\u180e"},
		{"integer", "3.5", 3}, {"integer", -3.7, -3},
		{"integer", true, 1}, {"integer", false, 0},
		{"integer", "many", "many"},
		{"boolean", "TRUE", true}, {"boolean", "FALSE", false},
		{"boolean", "1", true}, {"boolean", "0", false},
		{"boolean", 1, true}, {"boolean", 0, false}, {"boolean", 2, 2},
		{"boolean", big.NewInt(1), true}, {"boolean", big.NewInt(0), false},
		{"boolean", big.NewInt(2), big.NewInt(2)}, {"boolean", nil, false},
		{"boolean", Undefined, false}, {"boolean", " true ", " true "},
		{"string", nil, "null"}, {"string", Undefined, ""},
		{"string", 42, "42"}, {"string", true, "true"},
		{"string", big.NewInt(12), "12"}, {"string", "hello", "hello"},
		{"string", 1e21, "1e+21"}, {"string", math.Inf(1), "Infinity"},
		{"string", math.Inf(-1), "-Infinity"}, {"string", math.NaN(), "NaN"},
		{"bigint", "12n", big.NewInt(12)}, {"bigint", "-12.9", big.NewInt(-12)},
		{"bigint", 3.7, big.NewInt(3)}, {"bigint", true, big.NewInt(1)},
		{"bigint", false, big.NewInt(0)}, {"bigint", nil, big.NewInt(0)},
		{"bigint", Undefined, big.NewInt(0)}, {"bigint", "TRUE", big.NewInt(1)},
		{"bigint", "FALSE", big.NewInt(0)}, {"bigint", big.NewInt(12), big.NewInt(12)},
		{"bigint", "01", "01"}, {"bigint", "0x10", "0x10"},
		{"bigint", "12n\n", "12n\n"}, {"bigint", "12.5\n", "12.5\n"},
		{"bigint", "12\n", "12\n"}, {"bigint", "x", "x"},
		{"null", "NULL", nil}, {"null", "undefined", nil},
		{"null", "", nil}, {"null", "0", nil}, {"null", false, nil},
		{"null", 0, nil}, {"null", big.NewInt(0), nil},
		{"null", big.NewInt(1), big.NewInt(1)}, {"null", Undefined, nil},
		{"undefined", nil, Undefined}, {"undefined", false, Undefined},
		{"undefined", 0, Undefined}, {"undefined", big.NewInt(0), Undefined},
		{"undefined", "UNDEFINED", Undefined}, {"undefined", "null", Undefined},
		{"undefined", "0", Undefined}, {"undefined", "", Undefined},
		{"undefined", "x", "x"},
	} {
		t.Run(tc.typeName+"/"+typeName(tc.input)+"/"+boundText(tc.input), func(t *testing.T) {
			c := coerceTestSchema(t, `{"type":"`+tc.typeName+`"}`, compilerOptions{})
			if got := c.coerce(tc.input); !deepEqual(got, tc.want) {
				t.Fatalf("coerce(%v) = %v (%T), want %v (%T)", tc.input, got, got, tc.want, tc.want)
			}
		})
	}
	c := coerceTestSchema(t, `{"type":"integer"}`, compilerOptions{})
	if got := c.coerce("-0").(float64); got != 0 || !math.Signbit(got) {
		t.Fatal("integer conversion lost negative zero")
	}
}

func TestCoercionNumberSyntax(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  float64
	}{
		{"", 0}, {"\uFEFF\t\n\u00a0", 0}, {"+12", 12}, {"-12", -12},
		{"01", 1}, {".5", .5}, {"1.", 1}, {"1.e2", 100}, {"-.5E+2", -50},
		{"1e-999", 0}, {"5e-324", math.SmallestNonzeroFloat64},
		{"1.7976931348623157e308", math.MaxFloat64},
		{"0xff", 255}, {"0XABCD", 43981}, {"0b101", 5}, {"0B11", 3},
		{"0o077", 63}, {"0O10", 8}, {"0x20000000000001", 9007199254740992},
	} {
		if got, ok := coercionNumber(tc.input); !ok || got != tc.want {
			t.Fatalf("number syntax %q = %v, %v; want %v", tc.input, got, ok, tc.want)
		}
	}
	for _, input := range []string{
		"+", "-", ".", "1e", "1e+", "1e-", "1e2e3", "1..0", "--1",
		"1_000", "1e1_0", "0x1p2", "+0x1p2", "-0x1", "0x", "0b", "0o",
		"0x:", "0x@", "0xG", "0b2", "0o8", "0x1_0", "0b1.0",
		"Infinity", "Inf", "NaN", "1e999", "١٢", "\u008512", "1 2", "12n",
	} {
		if got, ok := coercionNumber(input); ok {
			t.Fatalf("invalid number syntax %q accepted as %v", input, got)
		}
	}
	for _, input := range []string{"-0", "-0.0", "-1e-999"} {
		if got, ok := coercionNumber(input); !ok || got != 0 || !math.Signbit(got) {
			t.Fatalf("negative zero syntax %q = %v, %v", input, got, ok)
		}
	}
}

func TestCoerceKeywordCase(t *testing.T) {
	for _, name := range []string{"null", "undefined", "boolean", "number", "bigint"} {
		t.Run(name, func(t *testing.T) {
			c := coerceTestSchema(t, `{"type":"`+name+`"}`, compilerOptions{})
			for _, input := range []string{"UNDEFİNED", "undefıned", "falſe", "ＦＡＬＳＥ", " true "} {
				if got := c.coerce(input); got != input {
					t.Fatalf("coerce(%q) = %v, want the original string", input, got)
				}
			}
		})
	}
	for _, tc := range []struct {
		name, input string
		want        any
	}{
		{"null", "UnDeFiNeD", nil},
		{"null", "NuLl", nil},
		{"undefined", "UnDeFiNeD", Undefined},
		{"undefined", "NuLl", Undefined},
		{"boolean", "FaLsE", false},
		{"boolean", "TrUe", true},
		{"number", "FaLsE", 0},
		{"number", "TrUe", 1},
		{"bigint", "FaLsE", big.NewInt(0)},
		{"bigint", "TrUe", big.NewInt(1)},
	} {
		c := coerceTestSchema(t, `{"type":"`+tc.name+`"}`, compilerOptions{})
		if got := c.coerce(tc.input); !deepEqual(got, tc.want) {
			t.Fatalf("%s coercion of %q = %v, want %v", tc.name, tc.input, got, tc.want)
		}
	}
}

func TestCoerceRequiredNulls(t *testing.T) {
	c := coerceTestSchema(t, `{"type":"object","properties":{"required":{"type":"integer"},"optional":{"type":"integer"},"nullable":{"type":["integer","null"]},"missing":{"type":"integer"}},"required":["required","missing","required"]}`, compilerOptions{})
	input := map[string]any{"required": nil, "optional": nil, "nullable": nil}
	pass := conversion{coercer: c}
	if got := pass.dropOptionalNulls(input, c.schema, defaultBase); !deepEqual(got, map[string]any{"required": nil, "nullable": nil}) {
		t.Fatalf("optional null removal = %v", got)
	}
	if got := c.coerce(input); !deepEqual(got, map[string]any{"required": 0, "nullable": nil}) {
		t.Fatalf("required null conversion = %v", got)
	}
	if len(input) != 3 || input["required"] != nil || input["optional"] != nil {
		t.Fatal("null removal modified its input")
	}
}

func BenchmarkCoerce(b *testing.B) {
	for _, tc := range []struct {
		name, schema string
		input        any
	}{
		{"integer", `{"type":"integer"}`, "42"},
		{"object", `{"type":"object","properties":{"count":{"type":"integer"},"active":{"type":"boolean"},"name":{"type":"string"}},"required":["count","active","name"]}`, map[string]any{"count": "42", "active": "true", "name": "tool"}},
		{"unchanged object", `{"type":"object","properties":{"count":{"type":"integer"},"active":{"type":"boolean"},"name":{"type":"string"}},"required":["count","active","name"]}`, map[string]any{"count": 42, "active": true, "name": "tool"}},
		{"pattern rows", `{"type":"array","items":{"type":"object","patternProperties":{"^n_":{"type":"integer"},"^flag_":{"type":"boolean"}},"additionalProperties":{"type":"string"}}}`, func() any {
			rows := make([]any, 100)
			for i := range rows {
				rows[i] = map[string]any{"n_count": "42", "flag_active": "true", "label": "item"}
			}
			return rows
		}()},
		{"tuple", `{"type":"array","prefixItems":[{"type":["integer","null"]},{"type":"boolean"}]}`, []any{"42", "true"}},
		{"recursive", `{"anyOf":[{"type":"integer"},{"type":"array","items":{"$ref":"#"}}]}`, []any{"1", []any{"2", []any{"3"}}}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			var schema any
			if err := json.Unmarshal([]byte(tc.schema), &schema); err != nil {
				b.Fatal(err)
			}
			compiler := newCompiler(schema, compilerOptions{})
			if _, err := compiler.root(); err != nil {
				b.Fatal(err)
			}
			c := newCoercer(compiler, schema)
			c.coerce(tc.input)
			b.ReportAllocs()
			for b.Loop() {
				c.coerce(tc.input)
			}
		})
	}
}

func BenchmarkDropRequiredNulls(b *testing.B) {
	for _, count := range []int{1000, 2000, 4000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			properties := make(map[string]any, count)
			required := make([]any, count)
			value := make(map[string]any, count)
			for i := range count {
				key := "p" + strconv.Itoa(i)
				properties[key] = map[string]any{"type": "integer"}
				required[i] = key
				value[key] = nil
			}
			schema := map[string]any{"type": "object", "properties": properties, "required": required}
			c := newCoercer(newCompiler(schema, compilerOptions{}), schema)
			pass := conversion{coercer: c}
			pass.dropOptionalNulls(value, schema, defaultBase)
			b.ReportAllocs()
			for b.Loop() {
				pass.dropOptionalNulls(value, schema, defaultBase)
			}
		})
	}
}

func TestCoerceStructure(t *testing.T) {
	for _, tc := range []struct{ name, schema, input, want string }{
		{"optional nulls", `{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"number"},"nullable":{"type":["string","null"]},"metadata":{"type":"object","properties":{"enabled":{"type":"boolean"}}}},"required":["path","metadata"]}`, `{"path":"file.txt","offset":null,"nullable":null,"metadata":{"enabled":null}}`, `{"path":"file.txt","nullable":null,"metadata":{}}`},
		{"required null", `{"properties":{"name":{"type":"string"}},"required":["name"]}`, `{"name":null}`, `{"name":"null"}`},
		{"optional false", `{"properties":{"a":false}}`, `{"a":null}`, `{}`},
		{"optional ref", `{"properties":{"a":{"$ref":"#/$defs/n"}},"$defs":{"n":{"type":"integer"}}}`, `{"a":null}`, `{"a":0}`},
		{"optional union", `{"properties":{"a":{"anyOf":[{"type":"string"},{"type":"null"}]}}}`, `{"a":null}`, `{"a":null}`},
		{"pattern optional nulls", `{"type":"object","patternProperties":{"^entry$":{"properties":{"n":{"type":"integer"}}}}}`, `{"entry":{"n":null}}`, `{"entry":{}}`},
		{"additional optional nulls", `{"type":"object","additionalProperties":{"properties":{"n":{"type":"integer"}}}}`, `{"entry":{"n":null}}`, `{"entry":{}}`},
		{"pattern keeps key", `{"type":"object","patternProperties":{"^n":{"type":"integer"}}}`, `{"n":null}`, `{"n":0}`},
		{"deleted matching property", `{"properties":{"a":{"type":"number"}},"patternProperties":{"^a":{"type":"number"}}}`, `{"a":null}`, `{}`},
		{"array optional nulls", `{"type":"array","items":{"properties":{"n":{"type":"integer"}}}}`, `[{"n":null}]`, `[{}]`},
		{"tuple optional nulls", `{"type":"array","prefixItems":[{"properties":{"n":{"type":"integer"}}}]}`, `[{"n":null}]`, `[{}]`},
		{"object members", `{"type":"object","properties":{"a":{"type":"number"}},"patternProperties":{"^n_":{"type":"number"}},"additionalProperties":{"type":"boolean"}}`, `{"a":"1","n_x":"2","other":"true"}`, `{"a":1,"n_x":2,"other":true}`},
		{"implicit object", `{"properties":{"a":{"type":"integer"}}}`, `{"a":"3.5"}`, `{"a":3}`},
		{"only additional", `{"additionalProperties":{"type":"integer"}}`, `{"a":"3.5"}`, `{"a":"3.5"}`},
		{"implicit array", `{"items":{"type":"number"}}`, `"1"`, `[1]`},
		{"wrap scalar", `{"type":"array","items":{"type":"number"}}`, `"1"`, `[1]`},
		{"wrap null", `{"type":"array","items":{"type":"string"}}`, `null`, `["null"]`},
		{"tuple", `{"type":"array","prefixItems":[{"type":"number"},{"type":"boolean"}],"items":{"type":"string"}}`, `["1","true",7]`, `[1,true,"7"]`},
		{"tuple never wraps", `{"type":"array","prefixItems":[]}`, `"1"`, `"1"`},
		{"legacy tuple", `{"type":"array","items":[{"type":"number"}],"additionalItems":{"type":"string"}}`, `["1",7]`, `[1,7]`},
		{"const", `{"const":1}`, `"1"`, `1`},
		{"const mismatch", `{"const":1}`, `"2"`, `"2"`},
		{"null literal", `{"const":null}`, `"null"`, `"null"`},
		{"enum", `{"enum":[1,2,"hello"]}`, `"2"`, `2`},
		{"enum keeps member", `{"enum":[1,"1"]}`, `"1"`, `"1"`},
		{"enum mismatch", `{"enum":[1,2]}`, `"other"`, `"other"`},
		{"anyOf", `{"anyOf":[{"type":"number"},{"type":"null"}]}`, `"42"`, `42`},
		{"union keeps match", `{"anyOf":[{"type":"number"},{"type":"string"}]}`, `"42"`, `"42"`},
		{"oneOf order", `{"oneOf":[{"type":"boolean"},{"type":"number"}]}`, `"1"`, `true`},
		{"oneOf primitive pass", `{"oneOf":[{"type":"number"},{"type":"integer"}]}`, `"1"`, `1`},
		{"allOf", `{"allOf":[{"properties":{"a":{"type":"number"}}},{"properties":{"b":{"type":"boolean"}}}]}`, `{"a":"1","b":"true"}`, `{"a":1,"b":true}`},
		{"union siblings", `{"type":"object","properties":{"c":{"type":"integer"}},"required":["c"],"anyOf":[{"properties":{"a":{"type":"array","items":{"type":"integer"}}},"required":["a"]},{"required":["b"]}]}`, `{"a":"3","c":"2"}`, `{"a":[3],"c":2}`},
		{"type array keeps match", `{"type":["number","string"]}`, `"1"`, `"1"`},
		{"type array first conversion", `{"type":["boolean","number"]}`, `"1"`, `1`},
		{"type array integer", `{"type":["integer","null"]}`, `"5"`, `5`},
		{"type array nullable", `{"type":["array","null"],"items":{"type":"number"}}`, `null`, `null`},
		{"type array tuple", `{"type":"array","prefixItems":[{"type":["integer","null"]},{"type":["boolean","null"]}]}`, `["5","true"]`, `[5,true]`},
		{"type array members", `{"type":"object","patternProperties":{"^n_":{"type":["integer","null"]}},"additionalProperties":{"type":"string"}}`, `{"n_a":"5","other":7}`, `{"n_a":5,"other":"7"}`},
		{"boolean schema", `false`, `{"a":"1"}`, `{"a":"1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := coerceTestSchema(t, tc.schema, compilerOptions{})
			var input, want any
			if err := json.Unmarshal([]byte(tc.input), &input); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			if got := c.coerce(input); !deepEqual(got, want) {
				t.Fatalf("coerce = %v, want %v", got, want)
			}
			before, _ := json.Marshal(input)
			var original any
			if err := json.Unmarshal([]byte(tc.input), &original); err != nil {
				t.Fatal(err)
			}
			if !deepEqual(input, original) {
				t.Fatalf("input was modified: %s", before)
			}
		})
	}
}

func TestConvertOnly(t *testing.T) {
	for _, tc := range []struct {
		schema      string
		input, want any
	}{
		{`{"type":["integer","null"]}`, "5", "5"},
		{`{"oneOf":[{"type":"number"},{"type":"integer"}]}`, "1", "1"},
		{`{"properties":{"a":{"type":"string"}}}`, map[string]any{"a": nil}, map[string]any{"a": "null"}},
		{`{"const":true}`, "true", true},
		{`{"const":"1"}`, 1, "1"},
		{`{"enum":[true,1]}`, "true", true},
		{`{"type":"string"}`, math.Inf(1), math.Inf(1)},
	} {
		c := coerceTestSchema(t, tc.schema, compilerOptions{})
		if got := c.convertOnly(tc.input); !deepEqual(got, tc.want) {
			t.Fatalf("convertOnly under %s = %v, want %v", tc.schema, got, tc.want)
		}
	}
	schema := map[string]any{"const": big.NewInt(12)}
	c := newCoercer(newCompiler(schema, compilerOptions{}), schema)
	if got := c.convertOnly("12n"); !deepEqual(got, big.NewInt(12)) {
		t.Fatal("bigint literal conversion failed")
	}
	c = coerceTestSchema(t, `{"type":"string"}`, compilerOptions{})
	if got := c.convertOnly(math.NaN()); !math.IsNaN(got.(float64)) {
		t.Fatal("conversion pass should leave nonfinite numbers unchanged")
	}
}

func TestCoerceCopyOnWrite(t *testing.T) {
	c := coerceTestSchema(t, `{"type":"object","properties":{"a":{"type":"object","properties":{"b":{"type":"array","items":{"type":"integer"}}}},"c":{"type":"object","properties":{"d":{"type":"integer"}}}}}`, compilerOptions{})
	unchanged := map[string]any{"b": []any{1, 2}}
	changed := map[string]any{"d": "1"}
	input := map[string]any{"a": unchanged, "c": changed}
	out := c.coerce(input).(map[string]any)
	if sameCoercionValue(out, input) || !sameCoercionValue(out["a"], unchanged) || sameCoercionValue(out["c"], changed) {
		t.Fatal("only changed paths should be copied")
	}
	if !sameCoercionValue(c.coerce(out), out) || changed["d"] != "1" {
		t.Fatal("unchanged references or input contents were modified")
	}
	items := []any{1, "2", []any{"3"}}
	array := coerceTestSchema(t, `{"type":"array","items":{"anyOf":[{"type":"integer"},{"type":"array","items":{"type":"integer"}}]}}`, compilerOptions{})
	converted := array.coerce(items).([]any)
	if sameCoercionValue(items, converted) || !deepEqual(converted, []any{1, 2, []any{3}}) || !deepEqual(items, []any{1, "2", []any{"3"}}) {
		t.Fatal("array conversion did not preserve its input")
	}
	untouched := []any{1, 2}
	if !sameCoercionValue(array.coerce(untouched), untouched) {
		t.Fatal("unchanged array was copied")
	}
}

func TestCoerceUndefinedAndBytes(t *testing.T) {
	c := coerceTestSchema(t, `{"type":"object","properties":{"a":{"type":"string"}}}`, compilerOptions{})
	input := map[string]any{"a": Undefined}
	if !sameCoercionValue(c.coerce(input), input) {
		t.Fatal("undefined property should count as absent during conversion")
	}
	c = coerceTestSchema(t, `{"type":"object","properties":{"a":{"type":["string","null"]}}}`, compilerOptions{})
	if got := c.coerce(input); !deepEqual(got, map[string]any{"a": Undefined}) {
		t.Fatalf("type coercion of undefined = %v", got)
	}
	bytes := []byte{1, 0}
	c = coerceTestSchema(t, `{"type":"object","properties":{"0":{"type":"string"}}}`, compilerOptions{})
	if got := c.coerce(bytes); !reflect.DeepEqual(got, map[string]any{"0": "1", "1": float64(0)}) {
		t.Fatalf("indexed bytes conversion = %v", got)
	}
	c = coerceTestSchema(t, `{"type":"object","properties":{"0":{"type":"number"}}}`, compilerOptions{})
	if !sameCoercionValue(c.coerce(bytes), bytes) || !reflect.DeepEqual(bytes, []byte{1, 0}) {
		t.Fatal("unchanged bytes should retain their reference")
	}
	c = coerceTestSchema(t, `{"type":["object","string"],"properties":{"0":{"type":["boolean","null"]}}}`, compilerOptions{})
	if got := c.coerce(bytes); !reflect.DeepEqual(got, map[string]any{"0": true, "1": float64(0)}) {
		t.Fatalf("type-array bytes conversion = %v", got)
	}
}

func TestCoerceObjectKeywordsConcurrent(t *testing.T) {
	c := coerceTestSchema(t, `{"type":"object","properties":{"drop":{"type":"integer"}},"patternProperties":{"^n_":{"type":"integer"},"^flag_":{"type":"boolean"}},"additionalProperties":{"type":"string"},"required":["n_count"]}`, compilerOptions{})
	input := map[string]any{"drop": nil, "n_count": "42", "flag_active": "TRUE", "label": "row"}
	want := map[string]any{"n_count": 42, "flag_active": true, "label": "row"}
	// Start with an empty cache, then share the prepared regular expressions.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if got := c.coerce(input); !deepEqual(got, want) {
					t.Errorf("concurrent object conversion = %v", got)
					return
				}
			}
		})
	}
	wg.Wait()
	if len(c.objects) != 1 {
		t.Fatalf("prepared %d object schemas, want 1", len(c.objects))
	}
	keywords := c.objects[schemaID(c.schema.(map[string]any))]
	if keywords == nil || len(keywords.patterns) != 2 {
		t.Fatal("prepared patterns were not retained")
	}
	for range 1000 {
		c.coerce(input)
	}
	if len(c.objects) != 1 || c.objects[schemaID(c.schema.(map[string]any))] != keywords {
		t.Fatal("repeated conversion replaced or grew the prepared schema cache")
	}
	if !deepEqual(input, map[string]any{"drop": nil, "n_count": "42", "flag_active": "TRUE", "label": "row"}) {
		t.Fatal("cached conversion changed its input")
	}
}

func TestCoerceObjectKeywordIsolation(t *testing.T) {
	c := coerceTestSchema(t, `{"type":"object","properties":{"a":{"type":"object","patternProperties":{"^x":{"type":"integer"}}},"b":{"type":"object","patternProperties":{"^x":{"type":"boolean"}}}},"required":["a","b"]}`, compilerOptions{})
	for range 10 {
		input := map[string]any{"a": map[string]any{"x": "1"}, "b": map[string]any{"x": "1"}}
		want := map[string]any{"a": map[string]any{"x": 1}, "b": map[string]any{"x": true}}
		if got := c.coerce(input); !deepEqual(got, want) {
			t.Fatalf("distinct schemas shared conversion rules: %v", got)
		}
	}
	if len(c.objects) != 3 {
		t.Fatalf("prepared %d object schemas, want 3", len(c.objects))
	}
}

func TestCoerceSortedPatterns(t *testing.T) {
	for _, source := range []string{
		`{"type":"object","patternProperties":{"z$":{"type":"number"},"^a":{"type":"string"}}}`,
		`{"type":"object","patternProperties":{"^a":{"type":"string"},"z$":{"type":"number"}}}`,
	} {
		c := coerceTestSchema(t, source, compilerOptions{})
		for range 20 {
			if got := c.coerce(map[string]any{"az": "12"}); !deepEqual(got, map[string]any{"az": 12}) {
				t.Fatal("overlapping patterns must convert in sorted order")
			}
		}
	}
}

func TestCoerceRelativeUnionBases(t *testing.T) {
	for _, keyword := range []string{"anyOf", "oneOf", "allOf"} {
		t.Run(keyword, func(t *testing.T) {
			c := coerceTestSchema(t, `{"$id":"https://example.com/root.json","`+keyword+`":[{"$id":"nested/part.json","type":"object","properties":{"a":{"$ref":"value.json"}}}]}`, compilerOptions{schemas: map[string]any{
				"https://example.com/nested/value.json": map[string]any{"type": "integer"},
			}})
			for range 3 {
				input := map[string]any{"a": "2"}
				want := map[string]any{"a": 2}
				if got := c.coerce(input); !deepEqual(got, want) {
					t.Fatalf("relative union conversion = %v", got)
				}
				if got := c.convertOnly(input); !deepEqual(got, want) {
					t.Fatalf("relative union conversion pass = %v", got)
				}
				if input["a"] != "2" {
					t.Fatal("relative union conversion changed its input")
				}
			}
		})
	}
}

func TestCoerceReferences(t *testing.T) {
	c := coerceTestSchema(t, `{"$id":"https://example.com/root.json","$dynamicAnchor":"node","type":"object","properties":{"count":{"$ref":"defs/n.json"},"children":{"type":"array","items":{"$dynamicRef":"#node"}}}}`, compilerOptions{schemas: map[string]any{"https://example.com/defs/n.json": map[string]any{"type": "integer"}}})
	for range 3 {
		if got := c.coerce(map[string]any{"count": "2"}); !deepEqual(got, map[string]any{"count": 2}) {
			t.Fatalf("relative reference conversion = %v", got)
		}
	}
	c = coerceTestSchema(t, `{"$defs":{"n":{"properties":{"n":{"type":"integer"}}}},"properties":{"a":{"$ref":"#/$defs/n"},"b":{"$ref":"#/$defs/n"}}}`, compilerOptions{})
	shared := map[string]any{"n": "7"}
	got := c.coerce(map[string]any{"a": shared, "b": shared}).(map[string]any)
	if !sameCoercionValue(got["a"], got["b"]) || sameCoercionValue(got["a"], shared) || shared["n"] != "7" {
		t.Fatal("reference memo should reuse a conversion without changing its input")
	}
	c = coerceTestSchema(t, `{"$defs":{"n":{"type":"array","items":{"type":"integer"}}},"properties":{"short":{"$ref":"#/$defs/n"},"long":{"$ref":"#/$defs/n"}}}`, compilerOptions{})
	items := []any{"1", "2"}
	if got := c.coerce(map[string]any{"short": items[:1], "long": items}); !deepEqual(got, map[string]any{"short": []any{1}, "long": []any{1, 2}}) {
		t.Fatal("reference memo confused slice views")
	}
	c = coerceTestSchema(t, `{"$defs":{"n":{"type":"integer"}},"$ref":"#/$defs/n","type":"string"}`, compilerOptions{})
	if got := c.convertOnly("7"); !deepEqual(got, 7) {
		t.Fatal("reference conversion should take precedence over siblings")
	}
}

func TestCoerceRecursion(t *testing.T) {
	for _, schema := range []any{
		map[string]any{"anyOf": []any{map[string]any{"type": "number"}, map[string]any{"type": "array", "items": map[string]any{"$ref": "#"}}}},
		func() any {
			node := map[string]any{}
			node["anyOf"] = []any{map[string]any{"type": "number"}, map[string]any{"type": "array", "items": node}}
			return node
		}(),
	} {
		compiler := newCompiler(schema, compilerOptions{})
		if _, err := compiler.root(); err != nil {
			t.Fatal(err)
		}
		c := newCoercer(compiler, schema)
		for range 20 {
			invalid := map[string]any{"a": 1}
			if !sameCoercionValue(c.coerce(invalid), invalid) {
				t.Fatal("unconvertible recursive value changed")
			}
			if got := c.coerce("42"); !deepEqual(got, 42) {
				t.Fatal("recursive state leaked between calls")
			}
			if got := c.coerce([]any{"1", []any{"2"}}); !deepEqual(got, []any{1, []any{2}}) {
				t.Fatalf("recursive conversion = %v", got)
			}
		}
	}
}

func TestCoerceRecursiveMemo(t *testing.T) {
	c := coerceTestSchema(t, `{"$defs":{"S":{"anyOf":[{"type":"object","required":["a"],"additionalProperties":{"$ref":"#/$defs/S"}},{"type":"object","required":["b"],"additionalProperties":{"$ref":"#/$defs/S"}}]}},"$ref":"#/$defs/S"}`, compilerOptions{})
	var value any = map[string]any{"x": 1}
	for range 40 {
		value = map[string]any{"c": value}
	}
	pass := conversion{coercer: c}
	if got := pass.convert(c.schema, defaultBase, value); !sameCoercionValue(got, value) {
		t.Fatal("failed recursive conversion changed the input")
	}
	count := 0
	for _, values := range pass.memo {
		count += len(values)
	}
	if count != 42 {
		t.Fatalf("memoized %d pairs, want one per value (42)", count)
	}
	// A wrapped recursive value is cut short and its result must stay out of the memo.
	c = coerceTestSchema(t, `{"anyOf":[{"type":"number"},{"type":"array","items":{"$ref":"#"}}]}`, compilerOptions{})
	pass = conversion{coercer: c}
	pass.convert(c.schema, defaultBase, map[string]any{})
	if pass.cuts == 0 || len(pass.entered) != 0 {
		t.Fatal("recursive re-entry was not cut and unwound")
	}
	for _, values := range pass.memo {
		if len(values) != 0 {
			t.Fatal("memoized a result computed across a recursion cut")
		}
	}
}

func TestCoerceConcurrent(t *testing.T) {
	c := coerceTestSchema(t, `{"anyOf":[{"type":"number"},{"type":"array","items":{"$ref":"#"}}]}`, compilerOptions{})
	input := []any{"1", []any{"2"}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if got := c.coerce(input); !deepEqual(got, []any{1, []any{2}}) {
					t.Error("concurrent conversion differs")
					return
				}
			}
		})
	}
	wg.Wait()
	if !deepEqual(input, []any{"1", []any{"2"}}) {
		t.Fatal("concurrent conversions modified shared input")
	}
}

func TestCoerceReentrant(t *testing.T) {
	var c *coercer
	entered := false
	calls := 0
	c = coerceTestSchema(t, `{"$defs":{"n":{"anyOf":[{"type":"integer"},{"type":"string","format":"nested"}]}},"$ref":"#/$defs/n"}`, compilerOptions{formats: map[string]func(string) bool{
		"nested": func(value string) bool {
			if !entered {
				entered = true
				defer func() { entered = false }()
				calls++
				if got := c.coerce(value); got != value {
					t.Fatal("reentrant conversion changed an accepted value")
				}
			}
			return true
		},
	}})
	if got := c.coerce("outer"); got != "outer" || calls == 0 {
		t.Fatal("format did not reenter the coercer")
	}
}

func TestCoerceUnsupportedValues(t *testing.T) {
	c := coerceTestSchema(t, `{"anyOf":[{"type":"number"},{"type":"array","items":{"$ref":"#"}}]}`, compilerOptions{})
	for _, input := range []any{[]int{1, 2}, struct{ Values []int }{[]int{1, 2}}} {
		if got := c.coerce(input); !reflect.DeepEqual(got, input) {
			t.Fatal("unconvertible native value changed")
		}
	}
	// A failed conversion must not retain re-entry state or compiler locks.
	schema := map[string]any{"type": "object", "patternProperties": map[string]any{"(": true}}
	c = newCoercer(newCompiler(schema, compilerOptions{}), schema)
	panicked := false
	func() { defer func() { panicked = recover() != nil }(); c.coerce(map[string]any{}) }()
	if !panicked || c.coerce("unchanged") != "unchanged" {
		t.Fatal("conversion did not recover cleanly after an invalid pattern")
	}
}

func TestCoerceUncompilable(t *testing.T) {
	for _, schema := range []any{nil, map[string]any{"$ref": "missing"}, map[string]any{"properties": map[string]any{"a": nil}}} {
		c := newCoercer(newCompiler(schema, compilerOptions{}), schema)
		if got := c.coerce("value"); got != "value" {
			t.Fatal("unconvertible schema changed its value")
		}
		if _, known := c.passes(schema, defaultBase, "value"); known {
			t.Fatal("an uncompilable schema produced a known result")
		}
	}
	c := coerceTestSchema(t, `{"type":"string","format":"panic"}`, compilerOptions{formats: map[string]func(string) bool{"panic": func(string) bool { panic("format failed") }}})
	if valid, known := c.passes(c.schema, defaultBase, "value"); valid || known {
		t.Fatal("failed format should leave the check result unknown")
	}
	if got := c.convertOnly("value"); got != "value" {
		t.Fatal("failed check retained the compiler lock")
	}
}
