package schema

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func mustResolve(t *testing.T, r *resolver, ref, base string) resolved {
	t.Helper()
	target, err := r.resolve(ref, base)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func TestResolverPointer(t *testing.T) {
	root := map[string]any{
		"": "empty", "a/b": "slash", "m~n": "tilde", "~1": "escape order",
		"~2": "literal tilde", "a+b": "plus", "中文": "unicode", "%": "percent",
		"list": []any{false, map[string]any{"value": true}, nil, Undefined},
		"null": nil, "undefined": Undefined,
		"object": map[string]any{"01": "object key", "-": "dash"},
	}
	r := newResolver(root, resolverOptions{})
	for _, tc := range []struct {
		ref  string
		want any
	}{
		{"#/", "empty"}, {"#/a~1b", "slash"}, {"#/m~0n", "tilde"},
		{"#/~01", "escape order"}, {"#/~2", "literal tilde"},
		{"#/a+b", "plus"}, {"#/%E4%B8%AD%E6%96%87", "unicode"}, {"#/%25", "percent"},
		{"#/list/0", false}, {"#/list/1/value", true}, {"#/list/2", nil},
		{"#/list/length", 4}, {"#/null", nil}, {"#/object/01", "object key"}, {"#/object/-", "dash"},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			target := mustResolve(t, r, tc.ref, defaultBase)
			if !deepEqual(target.node, tc.want) || target.base != defaultBase {
				t.Fatalf("target = %+v, want (%v, %s)", target, tc.want, defaultBase)
			}
		})
	}
	for _, ref := range []string{
		"#/missing", "#/undefined", "#/null/child", "#/list/3", "#/list/4", "#/list/-1",
		"#/list/01", "#/list/+1", "#/list/-", "#/list/1.0", "#/list/999999999999999999999999",
		"#/%", "#/%FF", "#/%C0%AF",
	} {
		t.Run(ref, func(t *testing.T) {
			if _, err := r.resolve(ref, defaultBase); err == nil {
				t.Fatal("expected a reference error")
			}
		})
	}
	for _, ref := range []string{"", "#"} {
		target := mustResolve(t, r, ref, defaultBase)
		if schemaID(target.node.(map[string]any)) != schemaID(root) {
			t.Fatal("an empty fragment must return the original root")
		}
	}
	for _, root := range []any{false, true, nil, []any{true}} {
		target := mustResolve(t, newResolver(root, resolverOptions{}), "#", defaultBase)
		if !deepEqual(target.node, root) {
			t.Fatalf("root %v was lost", root)
		}
	}
}

func TestResolverLazyIndexing(t *testing.T) {
	root := map[string]any{"properties": map[string]any{"a": map[string]any{"type": "integer"}}}
	external := map[string]any{"$id": "https://example.com/external.json", "$anchor": "external"}
	r := newResolver(root, resolverOptions{schemas: map[string]any{"external.json": external}})
	if r.draft != "2020-12" || r.indexed || len(r.baseOf) != 0 || len(r.registry) != 0 {
		t.Fatal("construction must not traverse schemas")
	}
	if got := r.baseFor(root, defaultBase); got != defaultBase || r.indexed {
		t.Fatal("a schema without $id must not trigger indexing")
	}
	mustResolve(t, r, "#/properties/a", defaultBase)
	if !r.indexed || len(r.baseOf) != 2 || len(r.registry) != 1 {
		t.Fatal("first reference must index the root and registered URIs")
	}
	if _, indexed := r.baseOf[schemaID(external)]; indexed {
		t.Fatal("unreferenced documents must not be indexed")
	}
	mustResolve(t, r, "external.json", defaultBase)
	if _, indexed := r.baseOf[schemaID(external)]; !indexed {
		t.Fatal("a referenced document must be indexed")
	}
	mustResolve(t, r, "https://example.com/external.json#external", defaultBase)
	count := len(r.baseOf)
	r.ensureIndexed()
	if len(r.baseOf) != count {
		t.Fatal("indexing must run once")
	}
}

func TestResolverBasesAndResources(t *testing.T) {
	leaf := map[string]any{"type": "integer"}
	child := map[string]any{
		"$id": "schemas/tool.json", "$anchor": "tool", "$dynamicAnchor": "node",
		"$defs": map[string]any{"count": leaf},
	}
	root := map[string]any{
		"$id": "https://example.com/root.json", "$anchor": "root",
		"$defs": map[string]any{"child": child},
	}
	r := newResolver(root, resolverOptions{})
	rootBase := r.baseFor(root, defaultBase)
	if rootBase != "https://example.com/root.json" {
		t.Fatalf("root base = %s", rootBase)
	}
	childBase := "https://example.com/schemas/tool.json"
	for _, tc := range []struct {
		ref  string
		node map[string]any
		base string
	}{
		{"#root", root, rootBase},
		{"#/$defs/child", child, childBase},
		{"#/$defs/child/$defs/count", leaf, childBase},
		{"schemas/tool.json", child, childBase},
		{"schemas/tool.json#tool", child, childBase},
		{"schemas/tool.json#node", child, childBase},
		{"schemas/tool.json#/$defs/count", leaf, childBase},
	} {
		target := mustResolve(t, r, tc.ref, rootBase)
		if schemaID(target.node.(map[string]any)) != schemaID(tc.node) || target.base != tc.base {
			t.Fatalf("%s: wrong target %+v", tc.ref, target)
		}
	}
	if r.baseFor(child, childBase) != childBase || r.baseFor(leaf, childBase) != childBase {
		t.Fatal("indexed relative identifiers must not be applied twice")
	}
	if !r.isResource(root, rootBase) || !r.isResource(child, childBase) || r.isResource(leaf, childBase) || r.isResource(nil, childBase) {
		t.Fatal("resource checks must compare schema identity")
	}
	if target := r.dynamicAnchors[childBase+"#node"]; target.base != childBase || schemaID(target.node.(map[string]any)) != schemaID(child) {
		t.Fatal("dynamic anchors must retain the target and its base")
	}
	// A copied node not in the index still applies its identifier to the supplied base.
	copy := map[string]any{"$id": "copy.json"}
	if got := r.baseFor(copy, rootBase); got != "https://example.com/copy.json" {
		t.Fatalf("unindexed node base = %s", got)
	}
}

func TestResolverRelativeRootID(t *testing.T) {
	root := map[string]any{
		"$id":   "schemas/tool.json",
		"$defs": map[string]any{"count": map[string]any{"type": "integer"}},
	}
	for _, reject := range []bool{false, true} {
		r := newResolver(root, resolverOptions{rejectUnresolved: reject})
		base := r.baseFor(root, defaultBase)
		if base != "https://json-schema.invalid/schemas/tool.json" || r.baseFor(root, base) != base {
			t.Fatal("relative root $id was applied more than once")
		}
		target := mustResolve(t, r, "#/$defs/count", base)
		if target.base != base || target.node.(map[string]any)["type"] != "integer" || r.isUnresolved("#/$defs/count", base) {
			t.Fatal("local reference under relative root $id failed")
		}
	}
}

func TestResolverRegisteredDocuments(t *testing.T) {
	number := map[string]any{"type": "number", "$anchor": "value"}
	boolean := false
	external := map[string]any{
		"$id": "canonical.json", "$defs": map[string]any{"number": number},
	}
	options := resolverOptions{schemas: map[string]any{
		"schemas/external.json#ignored":  external,
		"https://example.com/false.json": boolean,
		"https://example.com/null.json":  nil,
	}}
	r := newResolver(map[string]any{}, options)
	for _, tc := range []struct {
		ref  string
		want any
		base string
	}{
		{"schemas/external.json", external, "https://json-schema.invalid/schemas/external.json"},
		{"schemas/external.json#/$defs/number", number, "https://json-schema.invalid/schemas/canonical.json"},
		{"schemas/canonical.json#value", number, "https://json-schema.invalid/schemas/canonical.json"},
		{"https://example.com/false.json", false, "https://example.com/false.json"},
		{"https://example.com/null.json", nil, "https://example.com/null.json"},
	} {
		target := mustResolve(t, r, tc.ref, defaultBase)
		if !deepEqual(target.node, tc.want) || target.base != tc.base {
			t.Fatalf("%s: target = %+v", tc.ref, target)
		}
	}
}

func TestResolverDraft7(t *testing.T) {
	ignored := map[string]any{
		"$ref": "#", "$id": "ignored.json", "$anchor": "ignored",
		"properties": map[string]any{"x": map[string]any{"$id": "hidden.json"}},
	}
	fragment := map[string]any{"$id": "#legacy", "type": "integer"}
	child := map[string]any{"$id": "child.json#named", "$anchor": "new", "$dynamicAnchor": "dynamic"}
	root := map[string]any{
		"$id":   "https://example.com/root.json",
		"$defs": map[string]any{"ignored": ignored, "legacy": fragment, "child": child},
	}
	r := newResolver(root, resolverOptions{draft: "7"})
	base := r.baseFor(root, defaultBase)
	if r.draft != "7" || r.baseFor(ignored, base) != base || r.baseFor(fragment, base) != base {
		t.Fatal("reference siblings and fragment-only identifiers must not change the base")
	}
	for _, ref := range []string{"#legacy", "child.json#named"} {
		mustResolve(t, r, ref, base)
	}
	for _, ref := range []string{"ignored.json", "hidden.json", "child.json#new", "child.json#dynamic"} {
		if _, err := r.resolve(ref, base); err == nil {
			t.Fatalf("ignored identifier or anchor %s was indexed", ref)
		}
	}
	if len(r.dynamicAnchors) != 0 {
		t.Fatal("Draft 7 must not register dynamic anchors")
	}
	// Pointer traversal can still reach ignored sibling values, without indexing their identifiers.
	target := mustResolve(t, r, "#/$defs/ignored/properties/x", base)
	if target.base != base {
		t.Fatal("ignored sibling identifier changed a pointer's base")
	}
	if got := r.baseFor(map[string]any{"$id": "#copied"}, base); got != base {
		t.Fatal("unindexed fragment-only identifier changed the base")
	}
}

func TestResolverUnresolvedReferences(t *testing.T) {
	root := map[string]any{"$defs": map[string]any{"false": false, "null": nil}}
	for _, reject := range []bool{false, true} {
		r := newResolver(root, resolverOptions{rejectUnresolved: reject})
		for _, ref := range []string{"#missing", "#/$defs/missing", "https://not-registered.invalid/root.json", "#/%FF"} {
			target, err := r.resolve(ref, defaultBase)
			if reject {
				if err != nil || target.node != false || target.base != defaultBase || !r.isUnresolved(ref, defaultBase) {
					t.Fatalf("reference %s was not recorded as rejected: %+v, %v", ref, target, err)
				}
			} else if err == nil || r.isUnresolved(ref, defaultBase) {
				t.Fatalf("reference %s must return an error without recording a rejection", ref)
			}
		}
		for _, ref := range []string{"#/$defs/false", "#/$defs/null"} {
			mustResolve(t, r, ref, defaultBase)
			if r.isUnresolved(ref, defaultBase) {
				t.Fatal("existing false or null targets must not be marked unresolved")
			}
		}
	}
	r := newResolver(root, resolverOptions{rejectUnresolved: true})
	mustResolve(t, r, "./missing.json#x", defaultBase)
	if !r.isUnresolved("missing.json#x", defaultBase) || r.isUnresolved("missing.json#other", defaultBase) {
		t.Fatal("unresolved status must use the full resolved URI")
	}
}

func TestResolverIndexKeywords(t *testing.T) {
	for _, keyword := range []string{
		"additionalProperties", "propertyNames", "contains", "not", "if", "then", "else",
		"unevaluatedProperties", "unevaluatedItems", "additionalItems", "contentSchema", "items",
		"properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies",
		"allOf", "anyOf", "oneOf", "prefixItems", "tupleItems",
	} {
		t.Run(keyword, func(t *testing.T) {
			child := map[string]any{"$id": "child.json", "$anchor": "target"}
			var value any = child
			switch keyword {
			case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies":
				value = map[string]any{"child": child}
			case "allOf", "anyOf", "oneOf", "prefixItems", "tupleItems":
				value = []any{child}
			}
			if keyword == "tupleItems" {
				keyword = "items"
			}
			r := newResolver(map[string]any{keyword: value}, resolverOptions{})
			target := mustResolve(t, r, "child.json#target", defaultBase)
			if schemaID(target.node.(map[string]any)) != schemaID(child) {
				t.Fatal("subschema keyword did not register its child")
			}
		})
	}
	for _, keyword := range []string{"enum", "const", "examples", "default", "unknown"} {
		for _, draft := range []string{"2020-12", "7"} {
			child := map[string]any{"$id": "hidden.json", "$anchor": "hidden"}
			r := newResolver(map[string]any{keyword: child}, resolverOptions{draft: draft})
			if _, err := r.resolve("hidden.json", defaultBase); err == nil {
				t.Fatalf("identifier inside %s was indexed", keyword)
			}
			// JSON Pointers can access data outside the schema keywords as well.
			if got := mustResolve(t, r, "#/"+keyword, defaultBase); got.base != defaultBase {
				t.Fatal("a data value changed the reference base")
			}
		}
	}
}

func TestResolverSharedAndRecursiveSchemas(t *testing.T) {
	shared := map[string]any{"$id": "shared.json", "$anchor": "shared"}
	root := map[string]any{"$defs": map[string]any{"a": shared, "b": shared}}
	root["properties"] = map[string]any{"self": root, "child": map[string]any{"$ref": "#"}}
	r := newResolver(root, resolverOptions{})
	mustResolve(t, r, "shared.json#shared", defaultBase)
	mustResolve(t, r, "#/properties/self/properties/self", defaultBase)
	if len(r.baseOf) != 3 {
		t.Fatalf("indexed %d nodes, want 3 distinct schema objects", len(r.baseOf))
	}
	target := mustResolve(t, r, "#/properties/child", defaultBase)
	ref := target.node.(map[string]any)["$ref"].(string)
	back := mustResolve(t, r, ref, target.base)
	if schemaID(back.node.(map[string]any)) != schemaID(root) {
		t.Fatal("recursive reference did not locate the original root")
	}
}

func TestResolverReadOnlySchemas(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal([]byte(`{"$id":"schemas/root.json","$defs":{"child":{"$id":"child.json","$anchor":"child"}},"properties":{"x":{"$ref":"child.json"}}}`), &root); err != nil {
		t.Fatal(err)
	}
	options := resolverOptions{schemas: map[string]any{"https://example.com/external.json": map[string]any{"$anchor": "external"}}}
	before, err := json.Marshal([]any{root, options.schemas}, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	r := newResolver(root, options)
	base := r.baseFor(root, defaultBase)
	for range 3 {
		mustResolve(t, r, "child.json#child", base)
		mustResolve(t, r, "#/$defs/child", base)
		mustResolve(t, r, "https://example.com/external.json#external", base)
	}
	after, err := json.Marshal([]any{root, options.schemas}, json.Deterministic(true))
	if err != nil || string(before) != string(after) {
		t.Fatal("reference resolution modified a schema or registry", err)
	}
}

func TestSchemaObjects(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  bool
	}{
		{map[string]any{}, true}, {map[string]any{"$ref": "#"}, true},
		{nil, false}, {map[string]any(nil), false}, {true, false}, {false, false},
		{[]any{}, false}, {[]byte{}, false}, {"object", false}, {1, false}, {Undefined, false},
	} {
		if got := isSchemaObject(tc.value); got != tc.want {
			t.Fatalf("isSchemaObject(%T) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestResolverShapedKeywords(t *testing.T) {
	root := map[string]any{
		"$id": 1, "$anchor": false, "$dynamicAnchor": nil,
		"properties": []any{map[string]any{"$id": "ignored.json"}},
		"allOf":      "not an array", "contains": 42,
		"dependencies": map[string]any{"trigger": []any{"name"}},
	}
	r := newResolver(root, resolverOptions{})
	if r.baseFor(root, defaultBase) != defaultBase || r.indexed {
		t.Fatal("unrecognized identifier shape must not trigger indexing")
	}
	mustResolve(t, r, "#", defaultBase)
	if len(r.baseOf) != 1 || len(r.anchors) != 0 || len(r.dynamicAnchors) != 0 {
		t.Fatal("only recognized subschema shapes should be indexed")
	}
	if _, err := r.resolve("ignored.json", defaultBase); err == nil {
		t.Fatal("unrecognized map keyword shape registered a resource")
	}
	if root["$id"] != 1 {
		t.Fatal("identifier was changed")
	}
	// Error messages identify both the missing target and its lookup context.
	for _, ref := range []string{"#missing", "#/missing", "absent.json"} {
		_, err := r.resolve(ref, defaultBase)
		if err == nil || !strings.Contains(err.Error(), "unresolved $ref") {
			t.Fatalf("missing reference %s: %v", ref, err)
		}
	}
}

func TestDocumentOf(t *testing.T) {
	for _, tc := range []struct {
		uri  string
		want string
	}{
		{"", ""},
		{"https://example.com/root.json", "https://example.com/root.json"},
		{"https://example.com/root.json#", "https://example.com/root.json"},
		{"https://example.com/root.json#/$defs/value", "https://example.com/root.json"},
		{"https://example.com/root.json?name=%23#first#second", "https://example.com/root.json?name=%23"},
		{"urn:example:schema#anchor", "urn:example:schema"},
		{"relative/root.json#anchor", "relative/root.json"},
		{"#anchor", ""},
	} {
		if got := documentOf(tc.uri); got != tc.want {
			t.Errorf("documentOf(%q) = %q, want %q", tc.uri, got, tc.want)
		}
	}
}

func TestResolveURI(t *testing.T) {
	for _, tc := range []struct {
		name string
		ref  string
		base string
		want string
	}{
		{"sibling", "child.json", "https://example.com/schemas/root.json", "https://example.com/schemas/child.json"},
		{"parent", "../shared.json#/$defs/value", "https://example.com/schemas/root.json", "https://example.com/shared.json#/$defs/value"},
		{"root path", "/shared.json", "https://example.com/schemas/root.json", "https://example.com/shared.json"},
		{"dot segments", "./a/../child.json", "https://example.com/schemas/root.json", "https://example.com/schemas/child.json"},
		{"absolute", "https://other.example/root.json", "https://example.com/root.json", "https://other.example/root.json"},
		{"network path", "//other.example/root.json", "https://example.com/root.json", "https://other.example/root.json"},
		{"pointer", "#/$defs/value", "https://example.com/root.json#old", "https://example.com/root.json#/$defs/value"},
		{"anchor", "#name", "https://example.com/root.json", "https://example.com/root.json#name"},
		{"query", "?version=2", "https://example.com/root.json?version=1#old", "https://example.com/root.json?version=2"},
		{"empty query", "?", "https://example.com/root.json?old#anchor", "https://example.com/root.json?"},
		{"empty ref", "", "https://example.com/root.json?version=1#old", "https://example.com/root.json?version=1"},
		{"empty fragment", "#", "https://example.com/root.json#old", "https://example.com/root.json#"},
		{"empty fragment on path", "child.json#", "https://example.com/root.json", "https://example.com/child.json#"},
		{"fragment ending in hash", "#name#", "https://example.com/root.json", "https://example.com/root.json#name#"},
		{"literal and encoded fragment hashes", "#first%23second#third", "https://example.com/root.json", "https://example.com/root.json#first%23second#third"},
		{"encoded hash", "child%23name.json#value", "https://example.com/root.json", "https://example.com/child%23name.json#value"},
		{"unicode fragment", "#中文", "https://example.com/root.json", "https://example.com/root.json#%E4%B8%AD%E6%96%87"},
		{"urn fragment", "#/$defs/value", "urn:example:schema#old", "urn:example:schema#/$defs/value"},
		{"urn empty fragment", "#", "urn:example:schema#old", "urn:example:schema#"},
		{"urn relative", "child.json", "urn:example:schema", "child.json"},
		{"urn query", "?version=2", "urn:example:schema", "?version=2"},
		{"urn empty ref", "", "urn:example:schema", ""},
		{"urn absolute", "https://other.example/root.json", "urn:example:schema", "https://other.example/root.json"},
		{"hierarchical urn", "../child", "urn:/schemas/root", "urn:/child"},
		{"relative base fragment", "#x", "relative/root.json", "relative/root.json#x"},
		{"invalid base fragment", "#new", "https://[invalid", "https://[invalid#new"},
		{"invalid base relative", "child.json", "https://[invalid", "child.json"},
		{"invalid base absolute", "https://example.com/root.json", "invalid base", "https://example.com/root.json"},
		{"invalid ref", "http://[invalid", "https://example.com/root.json", "http://[invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveURI(tc.ref, tc.base); got != tc.want {
				t.Fatalf("resolveURI(%q, %q) = %q, want %q", tc.ref, tc.base, got, tc.want)
			}
		})
	}
}
