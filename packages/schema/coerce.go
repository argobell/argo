package schema

// Coercion turns a loosely typed value into the value a schema most likely
// meant, before it is checked. Three passes run in order: optional nulls,
// TypeBox's conversion rules, then primitive coercion by type.
// Every pass copies only along changed paths and leaves the input untouched.

import (
	"maps"
	"math"
	"math/big"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/dlclark/regexp2/v2"
	"github.com/dlclark/regexp2/v2/compat"
)

// Coerces values for one compiled schema.
type coercer struct {
	c      *compiler
	schema any
}

func newCoercer(c *compiler, schema any) *coercer {
	return &coercer{c: c, schema: schema}
}

// value coerced: a copy along the changed paths only, or value itself.
func (c *coercer) coerce(value any) any {
	pass := conversion{coercer: c}
	withoutNulls := pass.dropOptionalNulls(value, c.schema, defaultBase)
	converted := pass.convert(c.schema, defaultBase, withoutNulls)
	return pass.coerceTypes(converted, c.schema, defaultBase)
}

// Pass 2 alone, as TypeBox's Value.Convert.
func (c *coercer) convertOnly(value any) any {
	pass := conversion{coercer: c}
	return pass.convert(c.schema, defaultBase, value)
}

// Re-entry and reference results belong to one call, including concurrent and
// reentrant calls. Results computed across a recursion cut are not memoized.
type conversion struct {
	*coercer
	entered []conversionEntry
	cuts    int
	memo    map[uintptr]map[any]conversionResult
}

type conversionEntry struct {
	node  uintptr
	value any
}

type conversionResult struct {
	input  any // Retains the value whose identity keys the cached result.
	output any
}

type coercionReference struct {
	pointer uintptr
	length  int
	typeOf  reflect.Type
}

// A comparable key: objects by identity, primitives by value (SameValueZero).
func coercionIdentity(value any) any {
	if n, ok := numericValue(value); ok {
		if math.IsNaN(n) {
			return struct{ nan bool }{true}
		}
		return n
	}
	switch v := value.(type) {
	case *big.Int:
		return struct{ bigint string }{v.String()}
	case map[string]any:
		return coercionReference{pointer: schemaID(v), typeOf: reflect.TypeOf(v)}
	case []any:
		return coercionReference{uintptr(reflect.ValueOf(v).UnsafePointer()), len(v), reflect.TypeOf(v)}
	case []byte:
		return coercionReference{uintptr(reflect.ValueOf(v).UnsafePointer()), len(v), reflect.TypeOf(v)}
	default:
		r := reflect.ValueOf(value)
		if r.IsValid() && !r.Comparable() {
			return r
		}
		return value
	}
}

// Strict equality for copy-on-write and primitive literals; NaN is unequal to itself.
func sameCoercionValue(a, b any) bool {
	x, xn := numericValue(a)
	y, yn := numericValue(b)
	if xn || yn {
		return xn && yn && x == y
	}
	return coercionIdentity(a) == coercionIdentity(b)
}

// node converted at a re-entry point: unchanged if that pair is already active.
func (p *conversion) reenter(node map[string]any, base string, value any, memoize bool) any {
	identity := schemaID(node)
	for _, entry := range p.entered {
		if entry.node != identity {
			continue
		}
		same := sameCoercionValue(entry.value, value)
		if x, xn := numericValue(entry.value); xn {
			y, yn := numericValue(value)
			same = yn && (math.Float64bits(x) == math.Float64bits(y) || math.IsNaN(x) && math.IsNaN(y))
		}
		if same {
			p.cuts++
			return value
		}
	}
	key := coercionIdentity(value)
	if memoize {
		if known, found := p.memo[identity][key]; found {
			return known.output
		}
	}
	cuts := p.cuts
	p.entered = append(p.entered, conversionEntry{identity, value})
	result := func() any {
		defer func() { p.entered = p.entered[:len(p.entered)-1] }()
		return p.convert(node, base, value)
	}()
	if memoize && p.cuts == cuts {
		if p.memo == nil {
			p.memo = make(map[uintptr]map[any]conversionResult)
		}
		if p.memo[identity] == nil {
			p.memo[identity] = make(map[any]conversionResult)
		}
		p.memo[identity][key] = conversionResult{value, result}
	}
	return result
}

// Whether value passes node; known is false when compilation or checking fails.
func (c *coercer) passes(node any, base string, value any) (valid, known bool) {
	defer func() {
		if recover() != nil {
			valid, known = false, false
		}
	}()
	check, err := func() (compiledCheck, error) {
		c.c.mu.Lock()
		defer c.c.mu.Unlock()
		if isSchemaObject(node) {
			base = c.c.r.baseFor(node.(map[string]any), base)
		}
		return c.c.check(node, base)
	}()
	if err != nil {
		return false, false
	}
	return check.check(value), true
}

func (c *coercer) baseOf(node map[string]any, base string) string {
	c.c.mu.Lock()
	defer c.c.mu.Unlock()
	return c.c.r.baseFor(node, base)
}

// Pass 1: optional nulls.
func (p *conversion) dropOptionalNulls(value, schema any, base string) any {
	if !isSchemaObject(schema) {
		return value
	}
	node := schema.(map[string]any)
	b := p.baseOf(node, base)
	if items, ok := value.([]any); ok {
		return mapItems(items, func(item any, i int) any {
			itemSchema := itemSchemaAt(node, i)
			if isUndefined(itemSchema) {
				return item
			}
			return p.dropOptionalNulls(item, itemSchema, b)
		})
	}
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	properties, _ := node["properties"].(map[string]any)
	patterns := coercionPatterns(node)
	additional := node["additionalProperties"]
	if len(patterns) == 0 && !isSchemaObject(additional) && !isSchemaObject(node["properties"]) {
		return value
	}
	required, _ := node["required"].([]any)
	var out map[string]any
	set := func(key string, next any) {
		current := object
		if out != nil {
			current = out
		}
		previous, present := current[key]
		if !present {
			previous = Undefined
		}
		if sameCoercionValue(next, previous) {
			return
		}
		if out == nil {
			out = maps.Clone(object)
		}
		out[key] = next
	}
	for _, key := range slices.Sorted(maps.Keys(properties)) {
		v, present := object[key]
		if !present {
			continue
		}
		property := properties[key]
		isRef := false
		if s, ok := property.(map[string]any); ok {
			_, isRef = s["$ref"].(string)
		}
		if v == nil && !slices.ContainsFunc(required, func(r any) bool { return sameCoercionValue(r, key) }) && !isRef {
			valid, known := p.passes(property, b, nil)
			if known && !valid {
				if out == nil {
					out = maps.Clone(object)
				}
				delete(out, key)
				continue
			}
		}
		set(key, p.dropOptionalNulls(v, property, b))
	}
	// Pattern and additional values are walked, but their keys stay.
	if len(patterns) > 0 || isSchemaObject(additional) {
		for _, key := range slices.Sorted(maps.Keys(object)) {
			_, matched := properties[key]
			for _, pattern := range patterns {
				if !pattern.pattern.MatchString(key) {
					continue
				}
				matched = true
				current := object
				if out != nil {
					current = out
				}
				v, present := current[key]
				if !present {
					v = Undefined
				}
				set(key, p.dropOptionalNulls(v, pattern.schema, b))
			}
			if !matched && isSchemaObject(additional) {
				set(key, p.dropOptionalNulls(object[key], additional, b))
			}
		}
	}
	if out != nil {
		return out
	}
	return value
}

// Pass 2: conversion rules ported from TypeBox 1.3.27's Value.Convert (MIT).
func (p *conversion) convert(schema any, base string, value any) any {
	if !isSchemaObject(schema) {
		return value
	}
	node := schema.(map[string]any)
	b := p.baseOf(node, base)
	if ref, ok := node["$ref"].(string); ok {
		target, err := func() (resolved, error) {
			p.c.mu.Lock()
			defer p.c.mu.Unlock()
			return p.c.r.resolve(ref, b)
		}()
		if err != nil || !isSchemaObject(target.node) {
			return value
		}
		return p.reenter(target.node.(map[string]any), target.base, value, true)
	}
	return p.convertNode(node, b, value)
}

// A node's first keyword among const, enum, the unions, allOf and type.
func (p *conversion) convertNode(node map[string]any, b string, value any) any {
	if literal, present := node["const"]; present {
		return convertLiteral(literal, value)
	}
	if values, ok := node["enum"].([]any); ok {
		return p.convertEnum(node, values, b, value)
	}
	if branches, ok := node["anyOf"].([]any); ok {
		return p.convertUnion(node, branches, b, value)
	}
	if branches, ok := node["oneOf"].([]any); ok {
		return p.convertUnion(node, branches, b, value)
	}
	if members, ok := node["allOf"].([]any); ok {
		next := value
		for _, member := range members {
			next = p.convert(member, b, next)
		}
		return p.convertMembers(node, b, next)
	}
	typeName, _ := node["type"].(string)
	switch typeName {
	case "string":
		if next, ok := tryString(value); ok {
			return next
		}
	case "number":
		if next, ok := tryNumber(value); ok {
			return next
		}
	case "integer":
		if next, ok := tryNumber(value); ok {
			return math.Trunc(next)
		}
	case "boolean":
		if next, ok := tryBoolean(value); ok {
			return next
		}
	case "null":
		if tryNull(value) {
			return nil
		}
	case "bigint":
		if next, ok := tryBigInt(value); ok {
			return next
		}
	case "undefined":
		if tryUndefined(value) {
			return Undefined
		}
	case "array":
		return p.convertArray(node, b, value)
	case "object":
		return p.convertObject(node, b, value)
	default:
		if !hasKeyword(node, "type") {
			if hasKeyword(node, "properties") || hasKeyword(node, "patternProperties") {
				return p.convertObject(node, b, value)
			}
			if hasKeyword(node, "items") || hasKeyword(node, "prefixItems") {
				return p.convertArray(node, b, value)
			}
		}
	}
	return value
}

// An enum is a union of its values: a member stays, else the first conversion accepted.
func (p *conversion) convertEnum(node map[string]any, values []any, base string, value any) any {
	for _, member := range values {
		if sameCoercionValue(member, value) {
			return value
		}
	}
	for _, member := range values {
		candidate := convertLiteral(member, value)
		if !sameCoercionValue(candidate, value) {
			if valid, _ := p.passes(node, base, candidate); valid {
				return candidate
			}
		}
	}
	return value
}

// Keep an accepted value; otherwise take the first candidate the whole union
// accepts. Sibling members convert before each candidate is checked.
func (p *conversion) convertUnion(node map[string]any, branches []any, base string, value any) any {
	for _, branch := range branches {
		if valid, _ := p.passes(branch, base, value); valid {
			return p.convertMembers(node, base, value)
		}
	}
	for _, branch := range branches {
		candidate := p.convertMembers(node, base, p.convert(branch, base, value))
		if valid, _ := p.passes(node, base, candidate); valid {
			return candidate
		}
	}
	return p.convertMembers(node, base, value)
}

func (p *conversion) convertMembers(node map[string]any, base string, value any) any {
	if hasKeyword(node, "properties") || hasKeyword(node, "patternProperties") || hasKeyword(node, "additionalProperties") {
		return p.convertObject(node, base, value)
	}
	return value
}

func (p *conversion) convertArray(node map[string]any, base string, value any) any {
	prefix, tuple := node["items"].([]any)
	legacy := tuple
	if !tuple {
		prefix, tuple = node["prefixItems"].([]any)
	}
	items, array := value.([]any)
	if tuple {
		if !array {
			return value
		}
		rest := node["items"]
		if legacy {
			rest = Undefined
		}
		return mapItems(items, func(item any, i int) any {
			schema := rest
			if i < len(prefix) {
				schema = prefix[i]
			}
			return p.convert(schema, base, item)
		})
	}
	if array {
		if !hasKeyword(node, "items") {
			return value
		}
		return mapItems(items, func(item any, _ int) any { return p.convert(node["items"], base, item) })
	}
	item := node["items"]
	if isSchemaObject(item) {
		value = p.reenter(item.(map[string]any), base, value, false)
	}
	return []any{value}
}

func (p *conversion) convertObject(node map[string]any, base string, value any) any {
	object, ok := coercionObject(value)
	if !ok {
		return value
	}
	properties, _ := node["properties"].(map[string]any)
	patterns := coercionPatterns(node)
	additional := node["additionalProperties"]
	var out map[string]any
	for _, key := range slices.Sorted(maps.Keys(object)) {
		if isUndefined(object[key]) {
			continue
		}
		next := object[key]
		property, matched := properties[key]
		if matched {
			next = p.convert(property, base, next)
		}
		for _, pattern := range patterns {
			if !pattern.pattern.MatchString(key) {
				continue
			}
			matched = true
			next = p.convert(pattern.schema, base, next)
		}
		if !matched && isSchemaObject(additional) {
			next = p.convert(additional, base, next)
		}
		if !sameCoercionValue(next, object[key]) {
			if out == nil {
				out = maps.Clone(object)
			}
			out[key] = next
		}
	}
	if out != nil {
		return out
	}
	return value
}

// Pass 3: primitive coercion by type, including type arrays.
func (p *conversion) coerceTypes(value, schema any, base string) any {
	if !isSchemaObject(schema) {
		return value
	}
	node := schema.(map[string]any)
	b := p.baseOf(node, base)
	next := value
	if members, ok := node["allOf"].([]any); ok {
		for _, member := range members {
			next = p.coerceTypes(next, member, b)
		}
	}
	if branches, ok := node["anyOf"].([]any); ok {
		next = p.coerceUnion(next, branches, b)
	}
	if branches, ok := node["oneOf"].([]any); ok {
		next = p.coerceUnion(next, branches, b)
	}
	types := typesOf(node)
	matches := len(types) > 1 && slices.ContainsFunc(types, func(t string) bool { return matchesType(next, t) })
	if !matches {
		for _, t := range types {
			candidate := coercePrimitive(next, t)
			if !sameCoercionValue(candidate, next) {
				next = candidate
				break
			}
		}
	}
	if slices.Contains(types, "object") {
		next = p.coerceProperties(next, node, b)
	}
	if items, ok := next.([]any); ok && slices.Contains(types, "array") {
		next = p.coerceItems(items, node, b)
	}
	return next
}

func (p *conversion) coerceUnion(value any, branches []any, base string) any {
	for _, branch := range branches {
		if valid, _ := p.passes(branch, base, value); valid {
			return value
		}
	}
	for _, branch := range branches {
		coerced := p.coerceTypes(value, branch, base)
		if valid, _ := p.passes(branch, base, coerced); valid {
			return coerced
		}
	}
	return value
}

func (p *conversion) coerceProperties(value any, schema map[string]any, base string) any {
	object, ok := coercionObject(value)
	if !ok {
		return value
	}
	properties, _ := schema["properties"].(map[string]any)
	patterns := coercionPatterns(schema)
	additional := schema["additionalProperties"]
	var out map[string]any
	set := func(key string, next any) {
		current := object
		if out != nil {
			current = out
		}
		if sameCoercionValue(next, current[key]) {
			return
		}
		if out == nil {
			out = maps.Clone(object)
		}
		out[key] = next
	}
	for _, key := range slices.Sorted(maps.Keys(properties)) {
		if v, present := object[key]; present {
			set(key, p.coerceTypes(v, properties[key], base))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(object)) {
		_, matched := properties[key]
		for _, pattern := range patterns {
			if !pattern.pattern.MatchString(key) {
				continue
			}
			matched = true
			current := object
			if out != nil {
				current = out
			}
			set(key, p.coerceTypes(current[key], pattern.schema, base))
		}
		if !matched && isSchemaObject(additional) {
			set(key, p.coerceTypes(object[key], additional, base))
		}
	}
	if out != nil {
		return out
	}
	return value
}

func (p *conversion) coerceItems(value []any, schema map[string]any, base string) []any {
	return mapItems(value, func(item any, i int) any {
		itemSchema := itemSchemaAt(schema, i)
		if isSchemaObject(itemSchema) {
			return p.coerceTypes(item, itemSchema, base)
		}
		return item
	})
}

// Byte arrays expose indexed properties; a changed member becomes an object.
func coercionObject(value any) (map[string]any, bool) {
	if object, ok := value.(map[string]any); ok {
		return object, true
	}
	if bytes, ok := value.([]byte); ok {
		object := make(map[string]any, len(bytes))
		for i, b := range bytes {
			object[strconv.Itoa(i)] = float64(b)
		}
		return object, true
	}
	return nil, false
}

type coercionPattern struct {
	pattern *compat.Regexp
	schema  any
}

// Overlapping patterns convert in sorted order, independent of map iteration.
func coercionPatterns(node map[string]any) []coercionPattern {
	schemas, _ := node["patternProperties"].(map[string]any)
	var patterns []coercionPattern
	for _, pattern := range slices.Sorted(maps.Keys(schemas)) {
		re, err := compat.Compile(pattern, regexp2.ECMAScript|regexp2.Unicode, regexp2.OptionMaxBacktrackingStackSize(-1))
		if err != nil {
			panic(err)
		}
		patterns = append(patterns, coercionPattern{re, schemas[pattern]})
	}
	return patterns
}

// A tuple's prefix, then items. Draft 7's additionalItems is not coerced.
func itemSchemaAt(schema map[string]any, index int) any {
	if prefix, ok := schema["items"].([]any); ok {
		if index < len(prefix) {
			return prefix[index]
		}
		return Undefined
	}
	if prefix, ok := schema["prefixItems"].([]any); ok && index < len(prefix) {
		return prefix[index]
	}
	if items, present := schema["items"]; present {
		return items
	}
	return Undefined
}

// The same array when every item maps to itself.
func mapItems(items []any, mapItem func(any, int) any) []any {
	var out []any
	for i, item := range items {
		next := mapItem(item, i)
		if sameCoercionValue(next, item) {
			continue
		}
		if out == nil {
			out = slices.Clone(items)
		}
		out[i] = next
	}
	if out != nil {
		return out
	}
	return items
}

// A primitive literal taken from another type only when it converts exactly.
func convertLiteral(literal, value any) any {
	if sameCoercionValue(literal, value) {
		return value
	}
	var next any
	var ok bool
	if _, numeric := numericValue(literal); numeric {
		next, ok = tryNumber(value)
	} else {
		switch literal.(type) {
		case string:
			next, ok = tryString(value)
		case bool:
			next, ok = tryBoolean(value)
		case *big.Int:
			next, ok = tryBigInt(value)
		}
	}
	if ok && sameCoercionValue(next, literal) {
		return next
	}
	return value
}

func tryString(value any) (string, bool) {
	if n, ok := numericValue(value); ok && isNumber(n) {
		return numberText(n), true
	}
	switch v := value.(type) {
	case *big.Int:
		return v.String(), true
	case bool:
		return strconv.FormatBool(v), true
	case nil:
		return "null", true
	case string:
		return v, true
	case undefinedValue:
		return "", true
	default:
		return "", false
	}
}

func tryNumber(value any) (float64, bool) {
	if n, ok := numericValue(value); ok {
		return n, isNumber(n)
	}
	switch v := value.(type) {
	case *big.Int:
		n, _ := new(big.Float).SetInt(v).Float64()
		return n, v.IsInt64() && v.Int64() >= -9007199254740991 && v.Int64() <= 9007199254740991
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	case nil, undefinedValue:
		return 0, true
	case string:
		if n, ok := coercionNumber(v); ok {
			return n, true
		}
		switch strings.ToLower(v) {
		case "false":
			return 0, true
		case "true":
			return 1, true
		}
		if n, ok := bigIntOfString(v); ok {
			return tryNumber(n)
		}
	}
	return 0, false
}

var coercionDecimalPattern = compat.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`, regexp2.ECMAScript|regexp2.Unicode, regexp2.OptionMaxBacktrackingStackSize(-1))
var coercionRadixPattern = compat.MustCompile(`^0(?:[xX][0-9a-fA-F]+|[bB][01]+|[oO][0-7]+)$`, regexp2.ECMAScript|regexp2.Unicode, regexp2.OptionMaxBacktrackingStackSize(-1))
var coercionBigIntPattern = compat.MustCompile(`^-?(0|[1-9]\d*)(n|\.\d+)?$`, regexp2.ECMAScript|regexp2.Unicode, regexp2.OptionMaxBacktrackingStackSize(-1))

// The finite subset of Number(string), including radix prefixes and ECMAScript whitespace.
func coercionNumber(value string) (float64, bool) {
	text := trimCoercionSpace(value)
	if text == "" {
		return 0, true
	}
	if coercionRadixPattern.MatchString(text) {
		base := 16
		switch text[1] {
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
		n, _ := new(big.Int).SetString(text[2:], base)
		result, _ := new(big.Float).SetInt(n).Float64()
		return result, isNumber(result)
	}
	if !coercionDecimalPattern.MatchString(text) {
		return 0, false
	}
	n, _ := strconv.ParseFloat(text, 64)
	return n, isNumber(n)
}

func trimCoercionSpace(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r >= '\t' && r <= '\r' || r == '\u2028' || r == '\u2029' || r == '\uFEFF' || unicode.Is(unicode.Zs, r)
	})
}

// Bigint-like strings: 12n, 12.5 (truncated), or 12.
func bigIntOfString(value string) (*big.Int, bool) {
	if !coercionBigIntPattern.MatchString(value) {
		return nil, false
	}
	text := value
	if strings.Contains(text, "n") {
		text = text[:len(text)-1]
	} else if i := strings.IndexByte(text, '.'); i >= 0 {
		text = text[:i]
	}
	n, _ := new(big.Int).SetString(text, 10)
	return n, true
}

func tryBigInt(value any) (*big.Int, bool) {
	if n, ok := numericValue(value); ok && isNumber(n) {
		result, _ := new(big.Float).SetFloat64(math.Trunc(n)).Int(nil)
		return result, true
	}
	switch v := value.(type) {
	case *big.Int:
		return v, true
	case bool:
		if v {
			return big.NewInt(1), true
		}
		return big.NewInt(0), true
	case nil, undefinedValue:
		return big.NewInt(0), true
	case string:
		if n, ok := bigIntOfString(v); ok {
			return n, true
		}
		switch strings.ToLower(v) {
		case "false":
			return big.NewInt(0), true
		case "true":
			return big.NewInt(1), true
		}
	}
	return nil, false
}

func tryUndefined(value any) bool {
	if value == nil || isUndefined(value) {
		return true
	}
	if n, ok := numericValue(value); ok {
		return n == 0
	}
	switch v := value.(type) {
	case *big.Int:
		return v.Sign() == 0
	case bool:
		return !v
	case string:
		lower := strings.ToLower(v)
		return lower == "undefined" || lower == "null" || v == "" || v == "0"
	}
	return false
}

func tryBoolean(value any) (bool, bool) {
	if n, ok := numericValue(value); ok {
		return n == 1, n == 0 || n == 1
	}
	switch v := value.(type) {
	case *big.Int:
		return v.Cmp(big.NewInt(1)) == 0, v.Sign() == 0 || v.Cmp(big.NewInt(1)) == 0
	case bool:
		return v, true
	case nil, undefinedValue:
		return false, true
	case string:
		lower := strings.ToLower(v)
		if lower == "false" || v == "0" {
			return false, true
		}
		if lower == "true" || v == "1" {
			return true, true
		}
	}
	return false, false
}

func tryNull(value any) bool {
	return tryUndefined(value)
}

func typesOf(schema map[string]any) []string {
	if t, ok := schema["type"].(string); ok {
		return []string{t}
	}
	var types []string
	if values, ok := schema["type"].([]any); ok {
		for _, value := range values {
			if t, ok := value.(string); ok {
				types = append(types, t)
			}
		}
	}
	return types
}

func matchesType(value any, typeName string) bool {
	switch typeName {
	case "number":
		n, ok := numericValue(value)
		return ok && isNumber(n)
	case "integer":
		n, ok := numericValue(value)
		return ok && isInteger(n)
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "null":
		return value == nil
	case "bigint":
		_, ok := value.(*big.Int)
		return ok
	case "undefined":
		return isUndefined(value)
	case "array":
		return isArray(value)
	case "object":
		return isObject(value) || isBytes(value)
	default:
		return false
	}
}

func coercePrimitive(value any, typeName string) any {
	switch typeName {
	case "number", "integer":
		if value == nil {
			return float64(0)
		}
		if text, ok := value.(string); ok && trimCoercionSpace(text) != "" {
			if n, ok := coercionNumber(text); ok && (typeName == "number" || isInteger(n)) {
				return n
			}
		}
		if b, ok := value.(bool); ok {
			if b {
				return float64(1)
			}
			return float64(0)
		}
	case "boolean":
		if value == nil {
			return false
		}
		if text, ok := value.(string); ok {
			if text == "true" {
				return true
			}
			if text == "false" {
				return false
			}
		}
		if n, ok := numericValue(value); ok {
			if n == 1 {
				return true
			}
			if n == 0 {
				return false
			}
		}
	case "string":
		if value == nil {
			return ""
		}
		if n, ok := numericValue(value); ok {
			return numberText(n)
		}
		if b, ok := value.(bool); ok {
			return strconv.FormatBool(b)
		}
	case "null":
		if text, ok := value.(string); ok && text == "" {
			return nil
		}
		if n, ok := numericValue(value); ok && n == 0 {
			return nil
		}
		if b, ok := value.(bool); ok && !b {
			return nil
		}
	}
	return value
}
