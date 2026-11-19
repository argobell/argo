package formats_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/argobell/argo/packages/schema"
	"github.com/argobell/argo/packages/schema/formats"
)

func TestCompileFormats(t *testing.T) {
	document := map[string]any{"type": "string", "format": "date"}
	annotation, err := schema.Compile(document, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !annotation.Check("bad") {
		t.Fatal("format should remain an annotation without checks")
	}
	v, err := schema.Compile(document, &schema.CompileOptions{Formats: formats.All()})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Check("2024-02-29") || v.Check("2023-02-29") {
		t.Fatal("built-in date check was not applied")
	}
	want := []schema.ValidationError{{Path: "", Keyword: "format", Message: "expected format date"}}
	if got := v.Errors("bad"); !reflect.DeepEqual(got, want) {
		t.Fatalf("format errors = %+v", got)
	}
	var failures schema.ValidationErrors
	if err := v.Validate("bad"); !errors.As(err, &failures) || !reflect.DeepEqual([]schema.ValidationError(failures), want) {
		t.Fatalf("format validation = %v", err)
	}
	coercing, err := schema.CompileCoercing(map[string]any{"type": "string", "format": "ipv4"}, &schema.CompileOptions{Formats: formats.All()})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := coercing.Parse("192.0.2.1"); err != nil || got != "192.0.2.1" {
		t.Fatalf("format parse = %v, %v", got, err)
	}
	if got, err := coercing.Parse("256.0.0.1"); err == nil || got != nil {
		t.Fatalf("invalid format parse = %v, %v", got, err)
	}
	unknown, err := schema.Compile(map[string]any{"format": "custom"}, &schema.CompileOptions{Formats: formats.All()})
	if err != nil || !unknown.Check("anything") {
		t.Fatal("unregistered format should remain an annotation")
	}
}

func ExampleAll() {
	v, err := schema.Compile(map[string]any{"type": "string", "format": "date"}, &schema.CompileOptions{Formats: formats.All()})
	if err != nil {
		panic(err)
	}
	fmt.Println(v.Check("2024-02-29"))
	fmt.Println(v.Check("2023-02-29"))
	// Output:
	// true
	// false
}
