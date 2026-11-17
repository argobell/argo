package schema

// Errors of a failed value: a walk over the schema that reports each failing
// keyword at the value it failed on. It runs only for values that failed the
// check, using the compiler's checks for subschemas.

import (
	"encoding/json/v2"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/dlclark/regexp2/v2"
	"github.com/dlclark/regexp2/v2/compat"
)

// Explains failures of one compiled schema.
type explainer struct {
	c      *compiler
	schema any
	// Checks of each schema with unevaluated* removed, protected by c.mu.
	evaluatedChecks map[uintptr]annotatingCheck
}

func newExplainer(c *compiler, schema any) (*explainer, error) {
	if _, err := c.root(); err != nil {
		return nil, err
	}
	return &explainer{c: c, schema: schema, evaluatedChecks: make(map[uintptr]annotatingCheck)}, nil
}

// The errors of value, at least one (value failed the check).
func (e *explainer) errors(value any) []ValidationError {
	walk := explanation{explainer: e}
	out := []ValidationError{}
	walk.walk(e.schema, defaultBase, value, "", &out)
	if len(out) == 0 {
		out = append(out, ValidationError{Keyword: "schema", Message: "invalid"})
	}
	return out
}

// The errors of $ref targets for object values, within one errors call: a
// recursive union would otherwise explain the same children once per branch,
// exponentially in the depth. Slice lengths distinguish views of one backing
// array, and the path distinguishes shared values at different locations.
type explanation struct {
	*explainer
	memo map[referenceValue]referenceErrors
}

type referenceValue struct {
	schema uintptr
	value  uintptr
	kind   reflect.Kind
	length int
}

type referenceErrors struct {
	path   string
	errors []ValidationError
}

func (e *explanation) check(node any, base string) compiledCheck {
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	check, err := e.c.check(node, base)
	if err != nil {
		panic(err)
	}
	return check
}

func (e *explanation) ok(node any, base string, v any) bool {
	return e.check(node, base).check(v)
}

func (e *explanation) baseFor(node map[string]any, base string) string {
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	return e.c.r.baseFor(node, base)
}

func (e *explanation) resolve(ref, base string) (resolved, bool) {
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	target, err := e.c.r.resolve(ref, base)
	if err != nil {
		panic(err)
	}
	return target, e.c.r.isUnresolved(ref, base)
}

// For $dynamicRef schemas this check runs outside the original dynamic scope,
// so their errors are approximate.
func (e *explanation) walk(value any, base string, v any, path string, out *[]ValidationError) {
	if truth, ok := value.(bool); ok {
		if !truth {
			*out = append(*out, ValidationError{path, "false", "no value is allowed here"})
		}
		return
	}
	if !isSchemaObject(value) {
		return
	}
	node := value.(map[string]any)
	b := e.baseFor(node, base)
	if e.ok(node, b, v) {
		return
	}
	start := len(*out)
	push := func(keyword, message string, at ...string) {
		p := path
		if len(at) > 0 {
			p = at[0]
		}
		*out = append(*out, ValidationError{p, keyword, message})
	}
	if ref, ok := node["$ref"].(string); ok && e.c.r.draft == "7" {
		e.walkRef(ref, b, v, path, out)
		return
	}
	if hasKeyword(node, "type") {
		types, ok := node["type"].([]any)
		if !ok {
			types = []any{node["type"]}
		}
		matched := false
		names := make([]string, len(types))
		for i, value := range types {
			if value != nil && !isUndefined(value) {
				names[i] = propertyKey(value)
			}
			if name, ok := value.(string); ok {
				if check, found := typeChecks[name]; found && check.check(v) {
					matched = true
				}
			}
		}
		if !matched {
			push("type", "expected "+strings.Join(names, " or ")+", got "+typeName(v))
			return
		}
	}
	if values, ok := node["enum"].([]any); ok && !enumCheck(values).check(v) {
		push("enum", "expected one of "+listValues(values))
	}
	if value, found := node["const"]; found && !constCheck(value).check(v) {
		push("const", "expected "+errorJSON(value))
	}
	if text, ok := v.(string); ok && node["type"] != "bytes" {
		e.walkString(node, text, push)
	}
	if bytes, ok := v.([]byte); ok && node["type"] == "bytes" {
		e.walkBytes(node, bytes, push)
	}
	if _, ok := numericValue(v); ok || isBigInt(v) {
		e.walkNumber(node, v, push)
	}
	if object, ok := v.(map[string]any); ok {
		e.walkObject(node, b, object, path, out, push)
	}
	if array, ok := v.([]any); ok {
		e.walkArray(node, b, array, path, out, push)
	}
	if branches, ok := node["allOf"].([]any); ok {
		for _, branch := range branches {
			e.walk(branch, b, v, path, out)
		}
	}
	if branches, ok := node["anyOf"].([]any); ok {
		e.walkUnion(branches, b, v, path, out, false)
	}
	if branches, ok := node["oneOf"].([]any); ok {
		e.walkUnion(branches, b, v, path, out, true)
	}
	if hasKeyword(node, "not") && e.ok(node["not"], b, v) {
		push("not", "must not match the schema in not")
	}
	if hasKeyword(node, "if") {
		keyword := "else"
		if e.ok(node["if"], b, v) {
			keyword = "then"
		}
		if hasKeyword(node, keyword) {
			e.walk(node[keyword], b, v, path, out)
		}
	}
	if ref, ok := node["$ref"].(string); ok {
		e.walkRef(ref, b, v, path, out)
	}
	if len(*out) == start && (hasKeyword(node, "unevaluatedProperties") || hasKeyword(node, "unevaluatedItems")) {
		e.walkUnevaluated(node, b, v, path, out)
	}
	if _, ok := node["$dynamicRef"].(string); ok && len(*out) == start {
		push("$dynamicRef", "does not match the dynamically referenced schema")
	}
}

func (e *explanation) walkRef(ref, base string, v any, path string, out *[]ValidationError) {
	target, unresolved := e.resolve(ref, base)
	if target.node == false && unresolved {
		*out = append(*out, ValidationError{path, "$ref", "unresolved reference " + ref})
		return
	}
	if !isSchemaObject(target.node) || !isObject(v) && !isArray(v) && !isBytes(v) {
		e.walk(target.node, target.base, v, path, out)
		return
	}
	value := reflect.ValueOf(v)
	key := referenceValue{schema: schemaID(target.node.(map[string]any)), value: uintptr(value.UnsafePointer()), kind: value.Kind()}
	if value.Kind() == reflect.Slice {
		key.length = value.Len()
	}
	if known, found := e.memo[key]; found && known.path == path {
		*out = append(*out, known.errors...)
		return
	}
	var errors []ValidationError
	e.walk(target.node, target.base, v, path, &errors)
	if e.memo == nil {
		e.memo = make(map[referenceValue]referenceErrors)
	}
	e.memo[key] = referenceErrors{path: path, errors: errors}
	*out = append(*out, errors...)
}

type pushError func(keyword, message string, at ...string)

func (e *explanation) walkString(node map[string]any, v string, push pushError) {
	n := float64(codePoints(v))
	if min, ok := numericValue(node["minLength"]); ok && n < min {
		push("minLength", "expected at least "+numberText(min)+" characters")
	}
	if max, ok := numericValue(node["maxLength"]); ok && n > max {
		push("maxLength", "expected at most "+numberText(max)+" characters")
	}
	if pattern, ok := node["pattern"].(string); ok {
		re, err := compat.Compile(pattern, regexp2.ECMAScript|regexp2.Unicode, regexp2.OptionMaxBacktrackingStackSize(-1))
		if err != nil {
			panic(err)
		}
		if !re.MatchString(v) {
			push("pattern", "expected to match "+pattern)
		}
	}
	if name, ok := node["format"].(string); ok {
		if format := e.c.formats[name]; format != nil && !format(v) {
			push("format", "expected format "+name)
		}
	}
}

func (e *explanation) walkBytes(node map[string]any, v []byte, push pushError) {
	if min, ok := numericValue(node["minLength"]); ok && float64(len(v)) < min {
		push("minLength", "expected at least "+numberText(min)+" bytes")
	}
	if max, ok := numericValue(node["maxLength"]); ok && float64(len(v)) > max {
		push("maxLength", "expected at most "+numberText(max)+" bytes")
	}
}

// Numbers and bigints alike: bounds of either kind compare with values of either kind.
func (e *explanation) walkNumber(node map[string]any, v Bound, push pushError) {
	for _, keyword := range [...]string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"} {
		bound := node[keyword]
		if _, ok := numericValue(bound); !ok && !isBigInt(bound) {
			continue
		}
		if keyword == "multipleOf" {
			if !isMultiple(v, bound) {
				push(keyword, "expected a multiple of "+boundText(bound))
			}
			continue
		}
		cmp, ordered := compareBound(v, bound)
		if !ordered {
			continue
		}
		op := ""
		switch keyword {
		case "minimum":
			if cmp < 0 {
				op = ">="
			}
		case "maximum":
			if cmp > 0 {
				op = "<="
			}
		case "exclusiveMinimum":
			if cmp <= 0 {
				op = ">"
			}
		case "exclusiveMaximum":
			if cmp >= 0 {
				op = "<"
			}
		}
		if op != "" {
			push(keyword, "expected "+op+" "+boundText(bound))
		}
	}
}

func (e *explanation) walkObject(node map[string]any, b string, v map[string]any, path string, out *[]ValidationError, push pushError) {
	properties, _ := node["properties"].(map[string]any)
	required, _ := node["required"].([]any)
	for _, value := range required {
		key, ok := value.(string)
		if !ok {
			continue
		}
		item, present := v[key]
		// Present and set to Undefined: missing unless its schema takes Undefined.
		accepted := false
		if isUndefined(item) {
			if schema, found := properties[key]; found {
				accepted = e.ok(schema, b, Undefined)
			}
		}
		if !present || isUndefined(item) && !accepted {
			push("required", "missing", pointer(path, key))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(properties)) {
		if item, found := v[key]; found && !isUndefined(item) {
			e.walk(properties[key], b, item, pointer(path, key), out)
		}
	}
	patterns, _ := node["patternProperties"].(map[string]any)
	patternKeys := slices.Sorted(maps.Keys(patterns))
	regexes := make([]*compat.Regexp, len(patternKeys))
	for i, pattern := range patternKeys {
		re, err := compat.Compile(pattern, regexp2.ECMAScript|regexp2.Unicode, regexp2.OptionMaxBacktrackingStackSize(-1))
		if err != nil {
			panic(err)
		}
		regexes[i] = re
	}
	for _, key := range slices.Sorted(maps.Keys(v)) {
		_, matched := properties[key]
		for i, pattern := range patternKeys {
			if regexes[i].MatchString(key) {
				matched = true
				e.walk(patterns[pattern], b, v[key], pointer(path, key), out)
			}
		}
		if !matched && hasKeyword(node, "additionalProperties") {
			if node["additionalProperties"] == false {
				push("additionalProperties", "unknown field", pointer(path, key))
			} else {
				e.walk(node["additionalProperties"], b, v[key], pointer(path, key), out)
			}
		}
		if hasKeyword(node, "propertyNames") && !e.ok(node["propertyNames"], b, key) {
			push("propertyNames", "invalid property name", pointer(path, key))
		}
	}
	if min, ok := numericValue(node["minProperties"]); ok && float64(len(v)) < min {
		push("minProperties", "expected at least "+numberText(min)+" properties")
	}
	if max, ok := numericValue(node["maxProperties"]); ok && float64(len(v)) > max {
		push("maxProperties", "expected at most "+numberText(max)+" properties")
	}
	dependencies := make(map[string]any)
	if e.c.r.draft != "7" {
		if entries, ok := node["dependentRequired"].(map[string]any); ok {
			maps.Copy(dependencies, entries)
		}
	}
	if entries, ok := node["dependencies"].(map[string]any); ok {
		maps.Copy(dependencies, entries)
	}
	for _, key := range slices.Sorted(maps.Keys(dependencies)) {
		if _, present := v[key]; !present {
			continue
		}
		if list, ok := dependencies[key].([]any); ok {
			for _, name := range list {
				required := propertyKey(name)
				if _, present := v[required]; !present {
					push("dependentRequired", "required when "+key+" is present", pointer(path, required))
				}
			}
		} else {
			e.walk(dependencies[key], b, v, path, out)
		}
	}
	if e.c.r.draft != "7" {
		entries, _ := node["dependentSchemas"].(map[string]any)
		for _, key := range slices.Sorted(maps.Keys(entries)) {
			if _, present := v[key]; present {
				e.walk(entries[key], b, v, path, out)
			}
		}
	}
}

func (e *explanation) walkArray(node map[string]any, b string, v []any, path string, out *[]ValidationError, push pushError) {
	var prefix []any
	rest, hasRest := node["items"], hasKeyword(node, "items")
	if tuple, ok := rest.([]any); ok {
		prefix = tuple
		rest, hasRest = node["additionalItems"], hasKeyword(node, "additionalItems")
	} else if e.c.r.draft != "7" {
		prefix, _ = node["prefixItems"].([]any)
	}
	for i, value := range v {
		schema, present := rest, hasRest
		if i < len(prefix) {
			schema, present = prefix[i], !isUndefined(prefix[i])
		}
		if present {
			e.walk(schema, b, value, path+"/"+strconv.Itoa(i), out)
		}
	}
	if min, ok := numericValue(node["minItems"]); ok && float64(len(v)) < min {
		push("minItems", "expected at least "+numberText(min)+" items")
	}
	if max, ok := numericValue(node["maxItems"]); ok && float64(len(v)) > max {
		push("maxItems", "expected at most "+numberText(max)+" items")
	}
	if node["uniqueItems"] == true && !uniqueCheck(v) {
		push("uniqueItems", "expected unique items")
	}
	if hasKeyword(node, "contains") {
		min, max := 1.0, math.Inf(1)
		if e.c.r.draft != "7" {
			if n, ok := numericValue(node["minContains"]); ok {
				min = n
			}
			if n, ok := numericValue(node["maxContains"]); ok {
				max = n
			}
		}
		if !containsCheck(e.check(node["contains"], b), min, max).check(v) {
			push("contains", containsMessage(min, max))
		}
	}
}

func (e *explanation) walkUnion(branches []any, b string, v any, path string, out *[]ValidationError, exactlyOne bool) {
	matches := 0
	for _, branch := range branches {
		if e.ok(branch, b, v) {
			matches++
		}
	}
	if matches == 1 || !exactlyOne && matches > 0 {
		return
	}
	keyword := "anyOf"
	if exactlyOne {
		keyword = "oneOf"
	}
	if matches > 1 {
		*out = append(*out, ValidationError{path, keyword, "matches more than one alternative"})
		return
	}
	// A tagged union reports the branch its tag selects; otherwise the branch
	// whose first error is deepest.
	if key, values, ok := e.tagOf(branches, b); ok && isObject(v) {
		value, present := v.(map[string]any)[key]
		if !present {
			value = Undefined
		}
		selected := -1
		for i, tag := range values {
			if deepEqual(tag, value) {
				selected = i
				break
			}
		}
		if selected < 0 {
			*out = append(*out, ValidationError{pointer(path, key), keyword, "expected one of " + listValues(values)})
		} else {
			e.walk(branches[selected], b, v, path, out)
		}
		return
	}
	// An object is blamed on the branch that declares most of its properties,
	// then on the branch whose first error is deepest. Property errors follow
	// sorted key order, so the selected branch is deterministic for Go maps.
	var best []ValidationError
	bestDeclared := 0
	for _, branch := range branches {
		var errors []ValidationError
		e.walk(branch, b, v, path, &errors)
		if len(errors) == 0 {
			continue
		}
		declared := 0
		if object, ok := v.(map[string]any); ok {
			declared = e.declaredKeys(branch, b, object)
		}
		if best == nil || declared > bestDeclared || declared == bestDeclared && pointerLength(errors[0].Path) > pointerLength(best[0].Path) {
			best, bestDeclared = errors, declared
		}
	}
	if len(best) > 0 {
		*out = append(*out, best...)
	} else {
		*out = append(*out, ValidationError{path, keyword, "matches no alternative"})
	}
}

// How many of the keys of v the branch s (through a $ref) declares in properties.
func (e *explanation) declaredKeys(s any, b string, v map[string]any) int {
	if isSchemaObject(s) {
		node := s.(map[string]any)
		if ref, ok := node["$ref"].(string); ok {
			target, _ := e.resolve(ref, e.baseFor(node, b))
			s = target.node
		}
	}
	if !isSchemaObject(s) {
		return 0
	}
	properties, _ := s.(map[string]any)["properties"].(map[string]any)
	n := 0
	for key := range v {
		if _, found := properties[key]; found {
			n++
		}
	}
	return n
}

func (e *explanation) tagOf(branches []any, b string) (string, []any, bool) {
	nodes := make([]map[string]any, 0, len(branches))
	for _, branch := range branches {
		if isSchemaObject(branch) {
			node := branch.(map[string]any)
			if ref, ok := node["$ref"].(string); ok {
				target, _ := e.resolve(ref, e.baseFor(node, b))
				branch = target.node
			}
		}
		node, _ := branch.(map[string]any)
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		return "", nil, false
	}
	properties, _ := nodes[0]["properties"].(map[string]any)
	for _, key := range slices.Sorted(maps.Keys(properties)) {
		var values []any
		for _, node := range nodes {
			properties, _ := node["properties"].(map[string]any)
			tag, _ := properties[key].(map[string]any)
			value, present := tag["const"]
			if !present || isUndefined(value) || isObject(value) || isArray(value) || isBytes(value) {
				break
			}
			duplicate := false
			for _, previous := range values {
				x, xn := numericValue(value)
				y, yn := numericValue(previous)
				if deepEqual(value, previous) || xn && yn && math.IsNaN(x) && math.IsNaN(y) {
					duplicate = true
					break
				}
			}
			if duplicate {
				break
			}
			values = append(values, value)
		}
		if len(values) == len(nodes) {
			return key, values, true
		}
	}
	return "", nil, false
}

func (e *explanation) walkUnevaluated(node map[string]any, b string, v any, path string, out *[]ValidationError) {
	check := func() annotatingCheck {
		e.c.mu.Lock()
		defer e.c.mu.Unlock()
		identity := schemaID(node)
		if check, found := e.evaluatedChecks[identity]; found {
			return check
		}
		// Reuse one copy per schema. b already applies its $id.
		rest := maps.Clone(node)
		delete(rest, "unevaluatedProperties")
		delete(rest, "unevaluatedItems")
		delete(rest, "$id")
		check, err := e.c.annotating(rest, b)
		if err != nil {
			panic(err)
		}
		e.evaluatedChecks[identity] = check
		return check
	}()
	var a annotations
	check(v, &a, nil)
	if object, ok := v.(map[string]any); ok && hasKeyword(node, "unevaluatedProperties") && !a.allProps {
		for _, key := range slices.Sorted(maps.Keys(object)) {
			if _, found := a.props[key]; found {
				continue
			}
			if node["unevaluatedProperties"] == false {
				*out = append(*out, ValidationError{pointer(path, key), "unevaluatedProperties", "unknown field"})
			} else {
				e.walk(node["unevaluatedProperties"], b, object[key], pointer(path, key), out)
			}
		}
	}
	if array, ok := v.([]any); ok && hasKeyword(node, "unevaluatedItems") && !a.allItems {
		for i := a.items; i < len(array); i++ {
			if _, found := a.indexes[i]; found {
				continue
			}
			if node["unevaluatedItems"] == false {
				*out = append(*out, ValidationError{path + "/" + strconv.Itoa(i), "unevaluatedItems", "unexpected item"})
			} else {
				e.walk(node["unevaluatedItems"], b, array[i], path+"/"+strconv.Itoa(i), out)
			}
		}
	}
}

// Enum errors name the allowed values; long lists are cut to keep messages readable.
const listedValues = 20

// values as JSON, at most listedValues of them, then how many more there are.
func listValues(values []any) string {
	if len(values) <= listedValues {
		return errorJSON(values)
	}
	listed := errorJSON(values[:listedValues])
	return listed[:len(listed)-1] + ", ... and " + strconv.Itoa(len(values)-listedValues) + " more]"
}

func containsMessage(min, max float64) string {
	if math.IsInf(max, 1) {
		item := "items"
		if min == 1 {
			item = "item"
		}
		return "expected at least " + numberText(min) + " matching " + item
	}
	if min == 0 {
		item := "items"
		if max == 1 {
			item = "item"
		}
		return "expected at most " + numberText(max) + " matching " + item
	}
	return "expected between " + numberText(min) + " and " + numberText(max) + " matching items"
}

func pointer(path, key string) string {
	return path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

// Union branches compare pointer lengths in UTF-16 code units.
func pointerLength(path string) int {
	n := 0
	for _, r := range path {
		n++
		if r > 0xffff {
			n++
		}
	}
	return n
}

func typeName(v any) string {
	switch {
	case v == nil:
		return "null"
	case isArray(v):
		return "array"
	case isBytes(v):
		return "bytes"
	case isBigInt(v):
		return "bigint"
	case isInteger(v):
		return "integer"
	case isUndefined(v):
		return "undefined"
	case isObject(v):
		return "object"
	case isString(v):
		return "string"
	case isBoolean(v):
		return "boolean"
	}
	if n, ok := numericValue(v); ok {
		if !isNumber(n) {
			return numberText(n)
		}
		return "number"
	}
	return reflect.TypeOf(v).Kind().String()
}

func boundText(value Bound) string {
	if n, ok := numericValue(value); ok {
		return numberText(n)
	}
	return fmt.Sprint(value)
}

func numberText(n float64) string {
	switch {
	case math.IsNaN(n):
		return "NaN"
	case math.IsInf(n, 1):
		return "Infinity"
	case math.IsInf(n, -1):
		return "-Infinity"
	case n == 0:
		return "0"
	case math.Abs(n) >= 1e-6 && math.Abs(n) < 1e21:
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	text := strconv.FormatFloat(n, 'e', -1, 64)
	mantissa, exponent, _ := strings.Cut(text, "e")
	exp, _ := strconv.Atoi(exponent)
	sign := ""
	if exp >= 0 {
		sign = "+"
	}
	return mantissa + "e" + sign + strconv.Itoa(exp)
}

// JSON text in diagnostic messages follows JSON.stringify for Undefined,
// non-finite numbers and bytes. Object keys are sorted for deterministic output.
func errorJSON(value any) string {
	if isUndefined(value) {
		return "undefined"
	}
	if value == nil {
		return "null"
	}
	if n, ok := numericValue(value); ok {
		if !isNumber(n) {
			return "null"
		}
		return numberText(n)
	}
	switch v := value.(type) {
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			if isUndefined(item) {
				parts[i] = "null"
			} else {
				parts[i] = errorJSON(item)
			}
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		var parts []string
		for _, key := range slices.Sorted(maps.Keys(v)) {
			if !isUndefined(v[key]) {
				parts = append(parts, errorJSON(key)+":"+errorJSON(v[key]))
			}
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []byte:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = errorJSON(strconv.Itoa(i)) + ":" + strconv.Itoa(int(item))
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	if isBigInt(value) {
		panic("cannot serialize a bigint as JSON")
	}
	text, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(text)
}
