package schema

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func validatorSchema(t *testing.T, source string) any {
	t.Helper()
	var schema any
	if err := json.Unmarshal([]byte(source), &schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

func validatorTestSchema(t *testing.T, source string, options *CompileOptions) *Validator {
	t.Helper()
	v, err := Compile(validatorSchema(t, source), options)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestValidatorChecksAndErrors(t *testing.T) {
	v := validatorTestSchema(t, `{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":1}},"required":["path"],"additionalProperties":false}`, nil)
	good := map[string]any{"path": "a.ts"}
	bad := map[string]any{"offset": 0, "extra": true}
	if !v.Check(good) || v.Check(bad) || v.Check(map[string]any{"path": Undefined}) || !v.Check(map[string]any{"path": "a.ts", "offset": Undefined}) {
		t.Fatal("compiled check rejected a valid value or accepted an invalid value")
	}
	want := []ValidationError{{"/path", "required", "missing"}, {"/offset", "minimum", "expected >= 1"}, {"/extra", "additionalProperties", "unknown field"}}
	if got := v.Errors(bad); !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %+v, want %+v", got, want)
	}
	if got := v.Errors(good); got == nil || len(got) != 0 {
		t.Fatalf("valid errors = %+v, want an empty slice", got)
	}
	if err := v.Validate(good); err != nil {
		t.Fatal(err)
	}
	var failures ValidationErrors
	if err := v.Validate(bad); !errors.As(err, &failures) || !reflect.DeepEqual([]ValidationError(failures), want) {
		t.Fatalf("validation error = %v", err)
	}
	var check Check = v.Check
	if !check(good) || check(bad) {
		t.Fatal("detached check changed its behavior")
	}
	// Returned errors belong to the call, even when they came through references.
	got := v.Errors(bad)
	got[0].Message = "changed"
	if got := v.Errors(bad); !reflect.DeepEqual(got, want) {
		t.Fatal("error results were shared between calls")
	}
}

func TestValidatorDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, schema string
		input        any
		want         []ValidationError
	}{
		{"false", `false`, nil, []ValidationError{{"", "false", "no value is allowed here"}}},
		{"pointer", `{"properties":{"a/b~c":{"type":"string"}}}`, map[string]any{"a/b~c": 1}, []ValidationError{{"/a~1b~0c", "type", "expected string, got integer"}}},
		{"bytes", `{"properties":{"data":{"type":"bytes","maxLength":4}}}`, map[string]any{"data": make([]byte, 5)}, []ValidationError{{"/data", "maxLength", "expected at most 4 bytes"}}},
		{"nonfinite", `{"type":"number"}`, math.NaN(), []ValidationError{{"", "type", "expected number, got NaN"}}},
		{"bigint", `{"type":"bigint","minimum":0}`, big.NewInt(-2), []ValidationError{{"", "minimum", "expected >= 0"}}},
		{"undefined", `{"properties":{"a":{"type":"undefined"}},"required":["a"]}`, map[string]any{}, []ValidationError{{"/a", "required", "missing"}}},
		{"tagged", `{"type":"array","items":{"anyOf":[{"properties":{"kind":{"const":"text"},"text":{"type":"string"}},"required":["kind","text"]},{"properties":{"kind":{"const":"image"},"width":{"type":"integer","minimum":0}},"required":["kind","width"]}]}}`, []any{map[string]any{"kind": "image", "width": -1}}, []ValidationError{{"/0/width", "minimum", "expected >= 0"}}},
		{"ref", `{"$defs":{"P":{"properties":{"x":{"type":"number"}},"required":["x"]}},"type":"array","items":{"$ref":"#/$defs/P"}}`, []any{map[string]any{"x": 1}, map[string]any{}}, []ValidationError{{"/1/x", "required", "missing"}}},
		{"recursive", `{"type":"object","properties":{"value":{"type":"number"},"children":{"type":"array","items":{"$ref":"#"}}},"required":["value"]}`, map[string]any{"value": 1, "children": []any{map[string]any{"value": 2, "children": []any{map[string]any{}}}}}, []ValidationError{{"/children/0/children/0/value", "required", "missing"}}},
		{"relative resource", `{"$id":"https://example.com/root.json","allOf":[{"$ref":"#/$defs/child"}],"$defs":{"child":{"$id":"schemas/tool.json","$ref":"#/$defs/props","unevaluatedProperties":false,"$defs":{"props":{"properties":{"count":{"type":"integer"}}}}}}}`, map[string]any{"count": 2, "extra": 1}, []ValidationError{{"/extra", "unevaluatedProperties", "unknown field"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := validatorTestSchema(t, tc.schema, nil)
			if v.Check(tc.input) {
				t.Fatal("invalid value passed")
			}
			if got := v.Errors(tc.input); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("errors = %+v, want %+v", got, tc.want)
			}
			var failures ValidationErrors
			if err := v.Validate(tc.input); !errors.As(err, &failures) || !reflect.DeepEqual([]ValidationError(failures), tc.want) {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
	v := validatorTestSchema(t, `true`, nil)
	if !v.Check(Undefined) || v.Validate(nil) != nil || len(v.Errors(false)) != 0 {
		t.Fatal("true schema rejected a value")
	}
}

func TestValidatorOptions(t *testing.T) {
	schema := validatorSchema(t, `{"type":"string","format":"starts-a"}`)
	annotation, err := Compile(schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !annotation.Check("bad") {
		t.Fatal("format without a registered check should remain an annotation")
	}
	v, err := Compile(schema, &CompileOptions{Formats: map[string]func(string) bool{"starts-a": func(s string) bool { return strings.HasPrefix(s, "a") }}})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Check("abc") || v.Check("bad") {
		t.Fatal("format option was ignored")
	}
	if got := v.Errors("bad"); !reflect.DeepEqual(got, []ValidationError{{"", "format", "expected format starts-a"}}) {
		t.Fatalf("format errors = %+v", got)
	}
	for _, draft := range []string{"", "2020-12", "7"} {
		v := validatorTestSchema(t, `{"definitions":{"s":{"type":"string"}},"$ref":"#/definitions/s","maxLength":1}`, &CompileOptions{Draft: draft})
		if !v.Check("a") || v.Check("ab") != (draft == "7") {
			t.Fatalf("incorrect draft %q behavior", draft)
		}
		if draft != "7" && len(v.Errors("ab")) != 1 {
			t.Fatal("draft diagnostics were not configured")
		}
	}
	external := map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "number"}}, "required": []any{"x"}}
	v = validatorTestSchema(t, `{"$ref":"https://example.com/point.json"}`, &CompileOptions{Schemas: map[string]any{"https://example.com/point.json": external}})
	if !v.Check(map[string]any{"x": 1}) || v.Check(map[string]any{}) {
		t.Fatal("registered document did not resolve")
	}
	if got := v.Errors(map[string]any{}); !reflect.DeepEqual(got, []ValidationError{{"/x", "required", "missing"}}) {
		t.Fatalf("registered document errors = %+v", got)
	}
}

func TestValidatorOptionSnapshots(t *testing.T) {
	options := &CompileOptions{Draft: "2020-12", Formats: map[string]func(string) bool{"good": func(s string) bool { return s == "ok" }}, Schemas: map[string]any{"https://example.com/s": map[string]any{"type": "string", "format": "good"}}}
	v := validatorTestSchema(t, `{"$ref":"https://example.com/s"}`, options)
	options.Draft = "7"
	options.RejectUnresolved = true
	options.Formats["good"] = func(string) bool { return true }
	options.Schemas["https://example.com/s"] = false
	delete(options.Formats, "good")
	if !v.Check("ok") || v.Check("bad") {
		t.Fatal("check changed with option maps")
	}
	if got := v.Errors("bad"); !reflect.DeepEqual(got, []ValidationError{{"", "format", "expected format good"}}) {
		t.Fatalf("lazy compilation did not use the option snapshot: %+v", got)
	}
}

func TestValidatorCompileErrors(t *testing.T) {
	for _, schema := range []any{nil, Undefined, 1, []any{}, map[string]any(nil), map[string]any{"properties": map[string]any{"a": 1}}, map[string]any{"$ref": "missing"}, map[string]any{"pattern": "("}} {
		if v, err := Compile(schema, nil); err == nil || v != nil {
			t.Fatalf("Compile(%v) = %v, %v; want an error and nil validator", schema, v, err)
		}
		if v, err := CompileCoercing(schema, nil); err == nil || v != nil {
			t.Fatalf("CompileCoercing(%v) = %v, %v; want an error and nil validator", schema, v, err)
		}
	}
	if v, err := Compile(true, &CompileOptions{Draft: "2019-09"}); v != nil || err == nil || !strings.Contains(err.Error(), "unsupported schema draft") {
		t.Fatalf("unsupported draft result = %v, %v", v, err)
	}
}

func TestValidatorUnresolved(t *testing.T) {
	for _, ref := range []string{"#/definitions/Missing", "https://example.com/missing.json"} {
		schema := map[string]any{"type": "object", "properties": map[string]any{"t": map[string]any{"$ref": ref}}}
		if _, err := Compile(schema, nil); err == nil {
			t.Fatal("unresolved reference compiled without a policy")
		}
		v, err := CompileCoercing(schema, &CompileOptions{RejectUnresolved: true})
		if err != nil {
			t.Fatal(err)
		}
		if !v.Check(map[string]any{}) || v.Check(map[string]any{"t": 1}) {
			t.Fatal("reject policy affected the wrong values")
		}
		input := map[string]any{"t": "1"}
		if !sameCoercionValue(v.Coerce(input), input) {
			t.Fatal("unresolved value was coerced")
		}
		want := []ValidationError{{"/t", "$ref", "unresolved reference " + ref}}
		if got := v.Errors(input); !reflect.DeepEqual(got, want) {
			t.Fatalf("unresolved errors = %+v", got)
		}
		var failures ValidationErrors
		if got, err := v.Parse(input); got != nil || !errors.As(err, &failures) || !reflect.DeepEqual([]ValidationError(failures), want) {
			t.Fatalf("unresolved parse = %v, %v", got, err)
		}
	}
}

func TestCoercingValidatorParse(t *testing.T) {
	schema := validatorSchema(t, `{"type":"object","properties":{"count":{"type":"integer"},"tags":{"type":"array","items":{"type":"string"}},"offset":{"type":"integer"}},"required":["count","tags"]}`)
	v, err := CompileCoercing(schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"count": "42", "tags": "a", "offset": nil}
	want := map[string]any{"count": 42, "tags": []any{"a"}}
	if v.Check(input) || len(v.Errors(input)) == 0 || v.Validate(input) == nil {
		t.Fatal("strict methods performed coercion")
	}
	if got := v.Coerce(input); !deepEqual(got, want) {
		t.Fatalf("coerce = %v, want %v", got, want)
	}
	got, err := v.Parse(input)
	if err != nil || !deepEqual(got, want) {
		t.Fatalf("parse = %v, %v", got, err)
	}
	if !sameCoercionValue(v.Coerce(got), got) {
		t.Fatal("unchanged parsed value was copied")
	}
	if !deepEqual(input, map[string]any{"count": "42", "tags": "a", "offset": nil}) {
		t.Fatal("parse changed the input")
	}
	bad := map[string]any{"count": "many", "tags": 7}
	var failures ValidationErrors
	if got, err := v.Parse(bad); got != nil || !errors.As(err, &failures) || !reflect.DeepEqual([]ValidationError(failures), []ValidationError{{"/count", "type", "expected integer, got string"}}) {
		t.Fatalf("failed parse = %v, %v", got, err)
	}
	if !deepEqual(bad, map[string]any{"count": "many", "tags": 7}) {
		t.Fatal("failed parse changed the input")
	}
	if v.coercer().c != v.explainer().c {
		t.Fatal("conversion and explanation did not share a diagnostic compiler")
	}
	// Both a successful null and a failure have a nil value; the error distinguishes them.
	null, err := CompileCoercing(map[string]any{"type": "null"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := null.Parse(""); got != nil || err != nil {
		t.Fatalf("successful null parse = %v, %v", got, err)
	}
	if got, err := null.Parse("bad"); got != nil || err == nil {
		t.Fatalf("failed null parse = %v, %v", got, err)
	}
}

func TestValidatorLazyInitialization(t *testing.T) {
	v, err := CompileCoercing(map[string]any{"type": "integer"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	getExplainer := v.explainer
	getCoercer := v.coercer
	var explained, coerced int
	v.explainer = func() *explainer { explained++; return getExplainer() }
	v.coercer = func() *coercer { coerced++; return getCoercer() }
	if !v.Check(1) || len(v.Errors(1)) != 0 || v.Validate(1) != nil {
		t.Fatal("valid strict value failed")
	}
	if explained != 0 || coerced != 0 {
		t.Fatal("valid strict paths initialized diagnostics or conversion")
	}
	if len(v.Errors("bad")) != 1 {
		t.Fatal("first invalid value did not initialize diagnostics")
	}
	if explained != 1 || coerced != 0 {
		t.Fatal("explanation initialized conversion")
	}
	if _, err := v.Parse("2"); err != nil {
		t.Fatal(err)
	}
	if coerced != 1 || explained != 1 {
		t.Fatal("successful parse did not initialize only conversion")
	}
	if v.coercer() != v.coercer() || v.explainer() != v.explainer() {
		t.Fatal("lazy helpers were rebuilt")
	}
}

func TestValidatorValidationErrors(t *testing.T) {
	var empty ValidationErrors
	if empty.Error() != "" {
		t.Fatal("empty failures should have an empty message")
	}
	failures := ValidationErrors{{"/a", "required", "missing"}, {"", "type", "expected integer, got string"}}
	if got := failures.Error(); got != "/a: missing (required); expected integer, got string (type)" {
		t.Fatalf("error string = %q", got)
	}
	var err error = failures
	var details ValidationErrors
	if !errors.As(err, &details) || !reflect.DeepEqual(details, failures) {
		t.Fatal("validation details could not be extracted")
	}
	data, marshalErr := json.Marshal(failures)
	if marshalErr != nil || string(data) != `[{"path":"/a","keyword":"required","message":"missing"},{"path":"","keyword":"type","message":"expected integer, got string"}]` {
		t.Fatalf("failure JSON = %s, %v", data, marshalErr)
	}
}

func TestValidatorConcurrentInitialization(t *testing.T) {
	v, err := CompileCoercing(validatorSchema(t, `{"$defs":{"n":{"type":"integer"}},"type":"object","properties":{"count":{"$ref":"#/$defs/n"}},"patternProperties":{"^flag_":{"type":"boolean"}},"unevaluatedProperties":false}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	good := map[string]any{"count": 2, "flag_active": true}
	bad := map[string]any{"count": "bad"}
	convert := map[string]any{"count": "2", "flag_active": "true"}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Go(func() {
			<-start
			if worker%2 == 0 {
				if got, err := v.Parse(convert); err != nil || !deepEqual(got, good) {
					t.Errorf("first concurrent parse = %v, %v", got, err)
					return
				}
			} else if len(v.Errors(bad)) != 1 {
				t.Error("first concurrent explanation did not report the failure")
				return
			}
			for range 100 {
				if !v.Check(good) || v.Check(bad) || v.Validate(good) != nil {
					t.Error("concurrent strict validation changed")
					return
				}
				if got := v.Errors(bad); !reflect.DeepEqual(got, []ValidationError{{"/count", "type", "expected integer, got string"}}) {
					t.Errorf("concurrent errors = %+v", got)
					return
				}
				if got, err := v.Parse(convert); err != nil || !deepEqual(got, good) {
					t.Errorf("concurrent parse = %v, %v", got, err)
					return
				}
			}
		})
	}
	close(start)
	wg.Wait()
}

func TestValidatorFormatReentry(t *testing.T) {
	var v *CoercingValidator
	var calls atomic.Int64
	var err error
	v, err = CompileCoercing(map[string]any{"anyOf": []any{map[string]any{"type": "integer"}, map[string]any{"type": "string", "format": "reenter"}}}, &CompileOptions{Formats: map[string]func(string) bool{"reenter": func(s string) bool {
		calls.Add(1)
		if s == "inner" {
			return true
		}
		if !v.Check("inner") || len(v.Errors("inner")) != 0 || v.Validate("inner") != nil {
			t.Error("reentrant validation failed")
		}
		if got, err := v.Parse("inner"); got != "inner" || err != nil {
			t.Error("reentrant parse failed")
		}
		return s == "outer"
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Check("outer") || len(v.Errors("bad")) == 0 {
		t.Fatal("format reentry changed check or explanation")
	}
	if got, err := v.Parse("outer"); got != "outer" || err != nil {
		t.Fatalf("reentrant parse = %v, %v", got, err)
	}
	if calls.Load() == 0 {
		t.Fatal("format check was never called")
	}
}

func TestValidatorReadOnlySchema(t *testing.T) {
	schema := validatorSchema(t, `{"$id":"schemas/tool.json","type":"object","properties":{"count":{"$ref":"#/$defs/n"}},"$defs":{"n":{"type":"integer"}},"unevaluatedProperties":false}`)
	before, err := json.Marshal(schema, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	v, err := CompileCoercing(schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Check(map[string]any{"count": 2}) {
		t.Fatal("relative root reference was applied twice")
	}
	if got := v.Errors(map[string]any{"count": 2, "extra": 1}); !reflect.DeepEqual(got, []ValidationError{{"/extra", "unevaluatedProperties", "unknown field"}}) {
		t.Fatalf("relative errors = %+v", got)
	}
	if got, err := v.Parse(map[string]any{"count": "2"}); err != nil || !deepEqual(got, map[string]any{"count": 2}) {
		t.Fatalf("relative parse = %v, %v", got, err)
	}
	after, err := json.Marshal(schema, json.Deterministic(true))
	if err != nil || !slices.Equal(before, after) {
		t.Fatalf("public operations changed the schema: %s", after)
	}
}

func TestValidatorDynamicRestart(t *testing.T) {
	schema := validatorSchema(t, `{"$id":"https://example.com/root","$ref":"middle","$defs":{"n":{"$dynamicAnchor":"n","type":"integer"},"middle":{"$id":"middle","$ref":"leaf","$defs":{"n":{"$dynamicAnchor":"n","type":"string"}}},"leaf":{"$id":"leaf","$dynamicRef":"#n","$defs":{"n":{"$dynamicAnchor":"n","type":"number"}}}}}`)
	v, err := CompileCoercing(schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Check(1) || v.Check(1.5) {
		t.Fatal("dynamic scope was not enabled during compilation")
	}
	if len(v.Errors(1.5)) == 0 {
		t.Fatal("dynamic validation did not produce diagnostics")
	}
	if got, err := v.Parse(1); err != nil || !deepEqual(got, 1) {
		t.Fatalf("dynamic parse = %v, %v", got, err)
	}
	if !v.compiler().dynamic {
		t.Fatal("lazy compiler did not restart with dynamic scope tracking")
	}
}

func ExampleCompile() {
	v, err := Compile(map[string]any{
		"type":       "object",
		"properties": map[string]any{"count": map[string]any{"type": "integer"}},
		"required":   []any{"count"},
	}, nil)
	if err != nil {
		panic(err)
	}
	fmt.Println(v.Check(map[string]any{"count": 2}))
	fmt.Println(v.Check(map[string]any{"count": "2"}))
	// Output:
	// true
	// false
}

func ExampleCompileCoercing() {
	v, err := CompileCoercing(map[string]any{
		"type":       "object",
		"properties": map[string]any{"count": map[string]any{"type": "integer"}},
		"required":   []any{"count"},
	}, nil)
	if err != nil {
		panic(err)
	}
	input := map[string]any{"count": "2"}
	value, err := v.Parse(input)
	if err != nil {
		panic(err)
	}
	fmt.Printf("parsed count: %v (%T)\n", value.(map[string]any)["count"], value.(map[string]any)["count"])
	fmt.Printf("input count: %v (%T)\n", input["count"], input["count"])
	// Output:
	// parsed count: 2 (float64)
	// input count: 2 (string)
}

func ExampleValidator_Validate() {
	v, err := Compile(map[string]any{
		"type":       "object",
		"properties": map[string]any{"count": map[string]any{"type": "integer"}},
	}, nil)
	if err != nil {
		panic(err)
	}
	var failures ValidationErrors
	if err := v.Validate(map[string]any{"count": "many"}); errors.As(err, &failures) {
		for _, failure := range failures {
			fmt.Println(failure.Path, failure.Keyword, failure.Message)
		}
	}
	// Output:
	// /count type expected integer, got string
}

func BenchmarkValidatorCheck(b *testing.B) {
	v, err := Compile(map[string]any{"type": "integer"}, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		v.Check(42)
	}
}
