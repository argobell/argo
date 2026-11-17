package schema

import (
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The base URI of a root schema without $id; relative references resolve against it.
const defaultBase = "https://json-schema.invalid/root.json"

// A schema and the base URI its references resolve against.
type resolved struct {
	node any
	base string
}

type resolverOptions struct {
	draft            string
	schemas          map[string]any
	rejectUnresolved bool
}

// Resolves references in the root schema and registered documents. Indexing is
// deferred until the first reference or $id; registered documents load on demand.
// A resolver belongs to one compilation or diagnostic walk.
type resolver struct {
	draft          string
	root           any
	options        resolverOptions
	indexed        bool
	baseOf         map[uintptr]resolved
	registry       map[string]any
	resources      map[string]any
	anchors        map[string]resolved
	dynamicAnchors map[string]resolved
	unresolved     map[string]struct{}
}

func newResolver(root any, options resolverOptions) *resolver {
	draft := "2020-12"
	if options.draft == "7" {
		draft = "7"
	}
	return &resolver{
		draft: draft, root: root, options: options,
		baseOf:         make(map[uintptr]resolved),
		registry:       make(map[string]any),
		resources:      make(map[string]any),
		anchors:        make(map[string]resolved),
		dynamicAnchors: make(map[string]resolved),
		unresolved:     make(map[string]struct{}),
	}
}

// Map identity is used only while baseOf retains the original schema objects.
func schemaID(node map[string]any) uintptr {
	return uintptr(reflect.ValueOf(node).UnsafePointer())
}

// Whether value is a schema object (not a boolean, an array or a primitive).
func isSchemaObject(value any) bool {
	node, ok := value.(map[string]any)
	return ok && node != nil
}

// The base URI of node, whose parent has base. Indexed nodes already include
// their own $id, so a resolved reference target never applies a relative $id twice.
func (r *resolver) baseFor(node map[string]any, base string) string {
	if known, found := r.baseOf[schemaID(node)]; found {
		return known.base
	}
	id, ok := node["$id"].(string)
	if !ok {
		return base
	}
	if !r.indexed {
		r.ensureIndexed()
		if known, found := r.baseOf[schemaID(node)]; found {
			return known.base
		}
	}
	_, refOnly := node["$ref"].(string)
	if r.draft == "7" && (refOnly || strings.HasPrefix(id, "#")) {
		return base
	}
	return documentOf(resolveURI(id, base))
}

// Index the root schema and registered document URIs once.
func (r *resolver) ensureIndexed() {
	if r.indexed {
		return
	}
	r.indexed = true
	for _, uri := range slices.Sorted(maps.Keys(r.options.schemas)) {
		r.registry[documentOf(resolveURI(uri, defaultBase))] = r.options.schemas[uri]
	}
	r.resources[defaultBase] = r.root
	r.index(r.root, defaultBase)
}

// The schema a reference names. Missing references return an error, or resolve
// to false when rejectUnresolved is enabled. Only those references are recorded.
func (r *resolver) resolve(ref, base string) (resolved, error) {
	r.ensureIndexed()
	target, err := r.find(ref, base)
	if err != nil && r.options.rejectUnresolved {
		r.unresolved[resolveURI(ref, base)] = struct{}{}
		return resolved{node: false, base: base}, nil
	}
	return target, err
}

// Whether ref, from base, named nothing and resolved to false.
func (r *resolver) isUnresolved(ref, base string) bool {
	_, found := r.unresolved[resolveURI(ref, base)]
	return found
}

func (r *resolver) find(ref, base string) (resolved, error) {
	uri := resolveURI(ref, base)
	doc, fragment, _ := strings.Cut(uri, "#")
	fragment, err := url.PathUnescape(fragment)
	if err != nil || !utf8.ValidString(fragment) {
		return resolved{}, fmt.Errorf("invalid URI fragment in reference %q", ref)
	}
	root, found := r.resources[doc]
	if !found {
		root, found = r.load(doc)
	}
	if !found || isUndefined(root) {
		return resolved{}, fmt.Errorf("unresolved $ref %q: no schema registered for %q", ref, doc)
	}
	if fragment == "" {
		return resolved{node: root, base: doc}, nil
	}
	if strings.HasPrefix(fragment, "/") {
		return r.pointer(root, doc, fragment)
	}
	if target, found := r.anchors[doc+"#"+fragment]; found {
		return target, nil
	}
	return resolved{}, fmt.Errorf("unresolved $ref %q: no anchor %q in %q", ref, fragment, doc)
}

// Whether node is the root of the schema resource at base.
func (r *resolver) isResource(node map[string]any, base string) bool {
	r.ensureIndexed()
	root, ok := r.resources[base].(map[string]any)
	return ok && root != nil && node != nil && schemaID(root) == schemaID(node)
}

// Walk only keywords that hold subschemas. Identifiers inside const, enum,
// examples or unknown keywords do not register resources or anchors.
func (r *resolver) index(value any, base string) {
	if !isSchemaObject(value) {
		return
	}
	node := value.(map[string]any)
	identity := schemaID(node)
	if _, found := r.baseOf[identity]; found {
		return
	}
	_, refOnly := node["$ref"].(string)
	refOnly = refOnly && r.draft == "7"
	if id, ok := node["$id"].(string); ok && !refOnly {
		if r.draft == "7" && strings.HasPrefix(id, "#") {
			r.anchors[documentOf(base)+id] = resolved{node: node, base: base}
		} else {
			uri := resolveURI(id, base)
			base = documentOf(uri)
			r.resources[base] = node
			if len(uri) > len(base)+1 {
				r.anchors[uri] = resolved{node: node, base: base}
			}
		}
	}
	target := resolved{node: node, base: base}
	r.baseOf[identity] = target
	if refOnly {
		return
	}
	if r.draft != "7" {
		if name, ok := node["$anchor"].(string); ok {
			r.anchors[base+"#"+name] = target
		}
		if name, ok := node["$dynamicAnchor"].(string); ok {
			r.anchors[base+"#"+name] = target
			r.dynamicAnchors[base+"#"+name] = target
		}
	}
	for _, key := range schemaKeywords {
		r.index(node[key], base)
	}
	for _, key := range mapKeywords {
		if entries, ok := node[key].(map[string]any); ok {
			// Go maps have no declaration order. Sort keys for deterministic indexing.
			for _, name := range slices.Sorted(maps.Keys(entries)) {
				r.index(entries[name], base)
			}
		}
	}
	for _, key := range arrayKeywords {
		if list, ok := node[key].([]any); ok {
			for _, child := range list {
				r.index(child, base)
			}
		}
	}
}

func (r *resolver) load(doc string) (any, bool) {
	node, found := r.registry[doc]
	if !found || isUndefined(node) {
		return nil, false
	}
	r.resources[doc] = node
	r.index(node, doc)
	return node, true
}

// Follow a decoded JSON Pointer, updating the base when it enters an indexed
// subschema. A present null is a target; a missing key or Undefined is not.
func (r *resolver) pointer(root any, base, fragment string) (resolved, error) {
	node, currentBase := root, base
	for _, raw := range strings.Split(fragment, "/")[1:] {
		key := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		found := false
		switch parent := node.(type) {
		case map[string]any:
			node, found = parent[key]
		case []any:
			if key == "length" {
				node, found = len(parent), true
			} else if i, err := strconv.Atoi(key); err == nil && i >= 0 && i < len(parent) && strconv.Itoa(i) == key {
				node, found = parent[i], true
			}
		}
		if !found || isUndefined(node) {
			return resolved{}, fmt.Errorf("unresolved $ref #%s in %s", fragment, base)
		}
		if object, ok := node.(map[string]any); ok {
			if known, found := r.baseOf[schemaID(object)]; found {
				currentBase = known.base
			}
		}
	}
	return resolved{node: node, base: currentBase}, nil
}

var schemaKeywords = [...]string{
	"additionalProperties", "propertyNames", "contains", "not", "if", "then", "else",
	"unevaluatedProperties", "unevaluatedItems", "additionalItems", "contentSchema", "items",
}

var mapKeywords = [...]string{
	"properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies",
}

var arrayKeywords = [...]string{"allOf", "anyOf", "oneOf", "prefixItems", "items"}

// uri without its fragment.
func documentOf(uri string) string {
	document, _, _ := strings.Cut(uri, "#")
	return document
}

// ref resolved against base. URNs and other bases a URL parser rejects take
// fragment-only references.
func resolveURI(ref, base string) string {
	// Replace the base fragment rather than inheriting it.
	baseURL, baseErr := url.Parse(documentOf(base))
	refURL, refErr := url.Parse(ref)
	if baseErr != nil || !baseURL.IsAbs() || refErr != nil ||
		(baseURL.Opaque != "" && !refURL.IsAbs() && !strings.HasPrefix(ref, "#")) {
		if strings.HasPrefix(ref, "#") {
			return documentOf(base) + ref
		}
		return ref
	}
	resolved := baseURL.ResolveReference(refURL)
	if !refURL.IsAbs() && resolved.Host == "" {
		resolved.OmitHost = baseURL.OmitHost
	}
	_, fragment, found := strings.Cut(ref, "#")
	if !found {
		return resolved.String()
	}
	// Preserve the explicit # delimiters, escaping only the text between them.
	parts := strings.Split(fragment, "#")
	for i, part := range parts {
		u, _ := url.Parse("#" + part) // refURL already validated these escapes.
		parts[i] = u.EscapedFragment()
	}
	return documentOf(resolved.String()) + "#" + strings.Join(parts, "#")
}
