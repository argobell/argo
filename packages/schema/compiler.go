package schema

// The compiler: a schema to one check closure, built once. Keywords group by the
// type they constrain, and common shapes (closed objects, arrays of one item
// schema, tagged unions, leaf schemas) get one specialized closure each.

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/dlclark/regexp2/v2"
	"github.com/dlclark/regexp2/v2/compat"
)

// A cached schema retains its identity and the slot recursive references use.
type cachedSchema struct {
	node  map[string]any
	check *compiledCheck
}

type compilerOptions struct {
	draft            string
	schemas          map[string]any
	rejectUnresolved bool
	formats          map[string]func(string) bool
}

type cachedAnnotatingSchema struct {
	node  map[string]any
	check *annotatingCheck
}

// Returned when compilation without dynamic scope tracking meets a bookended
// $dynamicRef: compile again with it.
var errDynamicScope = errors.New("dynamic scope required")

// One compile of a schema. A bookended $dynamicRef keeps its compiler for
// targets resolved at run time against the dynamic scope.
type compiler struct {
	schema          any
	r               *resolver
	formats         map[string]func(string) bool
	cache           map[uintptr]cachedSchema
	annotatingCache map[uintptr]cachedAnnotatingSchema
	dynamic         bool
	mu              sync.Mutex
}

func newCompiler(schema any, options compilerOptions) *compiler {
	return &compiler{
		schema: schema,
		r: newResolver(schema, resolverOptions{
			draft: options.draft, schemas: options.schemas, rejectUnresolved: options.rejectUnresolved,
		}),
		formats:         options.formats,
		cache:           make(map[uintptr]cachedSchema),
		annotatingCache: make(map[uintptr]cachedAnnotatingSchema),
	}
}

// The check of the root schema.
func (c *compiler) root() (compiledCheck, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	check, err := c.check(c.schema, defaultBase)
	if errors.Is(err, errDynamicScope) {
		c.dynamic = true
		c.cache = make(map[uintptr]cachedSchema)
		c.annotatingCache = make(map[uintptr]cachedAnnotatingSchema)
		check, err = c.check(c.schema, defaultBase)
	}
	return check, err
}

// The check of a schema, cached by object identity. Recursive references go
// through an indirection connected after the schema has compiled.
func (c *compiler) check(value any, base string) (compiledCheck, error) {
	if truth, ok := value.(bool); ok {
		if truth {
			return leaf(6, nil, always), nil
		}
		return plainCheck(never), nil
	}
	if !isSchemaObject(value) {
		return compiledCheck{}, fmt.Errorf("a schema must be an object or a boolean, got %T", value)
	}
	node := value.(map[string]any)
	identity := schemaID(node)
	if cached, found := c.cache[identity]; found {
		return *cached.check, nil
	}
	if check, ok := c.bareType(node); ok {
		return check, nil
	}
	slot := new(compiledCheck)
	*slot = indirect(slot)
	c.cache[identity] = cachedSchema{node: node, check: slot}
	check, err := c.build(node, c.r.baseFor(node, base))
	if err != nil {
		delete(c.cache, identity)
		return compiledCheck{}, err
	}
	*slot = check
	return check, nil
}

func (c *compiler) build(node map[string]any, base string) (compiledCheck, error) {
	if ref, ok := node["$ref"].(string); ok && c.r.draft == "7" {
		return c.ref(ref, base)
	}
	if c.r.draft != "7" && (hasKeyword(node, "unevaluatedProperties") || hasKeyword(node, "unevaluatedItems")) {
		check, err := c.annotating(node, base)
		if err != nil {
			return compiledCheck{}, err
		}
		return withFreshAnnotations(check), nil
	}
	check, err := c.plain(node, base)
	if err == nil && c.dynamic && c.r.isResource(node, base) {
		check = scopedCheck(base, check)
	}
	return check, err
}

// The shared check of a schema that is only a type (with annotations), or the
// true check for one with no assertion at all. The boolean reports whether such
// a check applies.
func (c *compiler) bareType(node map[string]any) (compiledCheck, bool) {
	for key := range node {
		if key != "type" {
			if _, neutral := neutralKeywords[key]; !neutral {
				return compiledCheck{}, false
			}
		}
	}
	value, found := node["type"]
	if !found || isUndefined(value) {
		return leaf(6, nil, always), true
	}
	name, ok := value.(string)
	if !ok {
		return compiledCheck{}, false
	}
	check, found := typeChecks[name]
	return check, found
}

// Every keyword of node, without tracking annotations.
func (c *compiler) plain(node map[string]any, base string) (compiledCheck, error) {
	var parts []compiledCheck
	value, typed := node["type"]
	typed = typed && !isUndefined(value)
	types, list := value.([]any)
	if typed && !list {
		types = []any{value}
	}
	var single string
	if len(types) == 1 {
		single, _ = types[0].(string)
	}
	typeInGroup := false
	if single == "bytes" {
		min, max := 0.0, math.Inf(1)
		if n, ok := numericValue(node["minLength"]); ok {
			min = n
		}
		if n, ok := numericValue(node["maxLength"]); ok {
			max = n
		}
		parts = append(parts, bytesCheck(min, max))
		typeInGroup = true
	} else {
		stringGroup, err := c.stringGroup(node, single == "string")
		if err != nil {
			return compiledCheck{}, err
		}
		if stringGroup.run != nil {
			parts = append(parts, stringGroup)
			typeInGroup = single == "string"
		}
		mode := 0
		switch single {
		case "number":
			mode = 1
		case "integer":
			mode = 2
		case "bigint":
			mode = 3
		}
		if numberGroup := c.numberGroup(node, mode); numberGroup.run != nil {
			parts = append(parts, numberGroup)
			typeInGroup = typeInGroup || mode != 0
		}
		object, err := c.objectGroup(node, base, single == "object")
		if err != nil {
			return compiledCheck{}, err
		}
		if object.run != nil {
			parts = append(parts, object)
			typeInGroup = typeInGroup || single == "object"
		}
		array, err := c.arrayGroup(node, base, single == "array")
		if err != nil {
			return compiledCheck{}, err
		}
		if array.run != nil {
			parts = append(parts, array)
			typeInGroup = typeInGroup || single == "array"
		}
	}
	if typed && !typeInGroup {
		parts = append([]compiledCheck{typeCheck(types)}, parts...)
	}
	parts, err := c.inPlace(node, base, parts)
	if err != nil {
		return compiledCheck{}, err
	}
	return and(parts), nil
}

// The keywords that apply to the value itself, whatever its type.
func (c *compiler) inPlace(node map[string]any, base string, parts []compiledCheck) ([]compiledCheck, error) {
	if values, ok := node["enum"].([]any); ok {
		parts = append(parts, enumCheck(values))
	}
	if value, found := node["const"]; found {
		parts = append(parts, constCheck(value))
	}
	if branches, ok := node["allOf"].([]any); ok {
		checks, err := c.compileBranches(branches, base)
		if err != nil {
			return nil, err
		}
		parts = append(parts, and(checks))
	}
	for _, keyword := range [...]string{"anyOf", "oneOf"} {
		if branches, ok := node[keyword].([]any); ok {
			check, err := c.union(branches, base, keyword == "oneOf")
			if err != nil {
				return nil, err
			}
			parts = append(parts, check)
		}
	}
	if hasKeyword(node, "not") {
		check, err := c.check(node["not"], base)
		if err != nil {
			return nil, err
		}
		parts = append(parts, notCheck(check))
	}
	if hasKeyword(node, "if") && (hasKeyword(node, "then") || hasKeyword(node, "else")) {
		then, otherwise := leaf(6, nil, always), leaf(6, nil, always)
		var err error
		if hasKeyword(node, "then") {
			then, err = c.check(node["then"], base)
			if err != nil {
				return nil, err
			}
		}
		if hasKeyword(node, "else") {
			otherwise, err = c.check(node["else"], base)
			if err != nil {
				return nil, err
			}
		}
		condition, err := c.check(node["if"], base)
		if err != nil {
			return nil, err
		}
		parts = append(parts, ifCheck(condition, then, otherwise))
	}
	if ref, ok := node["$ref"].(string); ok {
		check, err := c.ref(ref, base)
		if err != nil {
			return nil, err
		}
		parts = append(parts, check)
	}
	if ref, ok := node["$dynamicRef"].(string); ok && c.r.draft != "7" {
		check, err := c.dynamicRef(ref, base)
		if err != nil {
			return nil, err
		}
		parts = append(parts, check)
	}
	return parts, nil
}

func (c *compiler) compileBranches(branches []any, base string) ([]compiledCheck, error) {
	checks := make([]compiledCheck, 0, len(branches))
	for _, branch := range branches {
		check, err := c.check(branch, base)
		if err != nil {
			return nil, err
		}
		checks = append(checks, check)
	}
	return checks, nil
}

func (c *compiler) union(branches []any, base string, exactlyOne bool) (compiledCheck, error) {
	if check, ok := constUnion(branches, exactlyOne); ok {
		return check, nil
	}
	if check, err := c.tagged(branches, base); err != nil || check.run != nil {
		return check, err
	}
	checks, err := c.compileBranches(branches, base)
	if err != nil {
		return compiledCheck{}, err
	}
	if exactlyOne {
		return oneCheck(checks), nil
	}
	return anyOf(checks), nil
}

// anyOf/oneOf whose branches are all {const: primitive}: an enum. Distinct
// values make oneOf the same; duplicates retain their individual checks.
func constUnion(branches []any, exactlyOne bool) (compiledCheck, bool) {
	values := make([]any, 0, len(branches))
	for _, branch := range branches {
		if !isSchemaObject(branch) {
			return compiledCheck{}, false
		}
		node := branch.(map[string]any)
		value, found := node["const"]
		if !found || isObject(value) || isArray(value) || isBytes(value) {
			return compiledCheck{}, false
		}
		for key := range node {
			if _, neutral := neutralKeywords[key]; key != "const" && !neutral {
				return compiledCheck{}, false
			}
		}
		if exactlyOne {
			for _, previous := range values {
				x, xNumeric := numericValue(value)
				y, yNumeric := numericValue(previous)
				if deepEqual(value, previous) || xNumeric && yNumeric && math.IsNaN(x) && math.IsNaN(y) {
					return compiledCheck{}, false
				}
			}
		}
		values = append(values, value)
	}
	return enumCheck(values), true
}

// anyOf: its bare-type branches run as one type test, followed by the remaining
// branches in their original order.
func anyOf(checks []compiledCheck) compiledCheck {
	mask := 0
	var rest []compiledCheck
	for _, check := range checks {
		if check.typeBit != 0 {
			mask |= check.typeBit
		} else {
			rest = append(rest, check)
		}
	}
	if mask == 0 {
		return anyCheck(rest)
	}
	return maskedAnyCheck(mask, rest)
}

// anyOf/oneOf whose branches are all objects with a distinct required const on
// one key: the tag picks the branch.
func (c *compiler) tagged(branches []any, base string) (compiledCheck, error) {
	if len(branches) < 2 {
		return compiledCheck{}, nil
	}
	nodes := make([]map[string]any, 0, len(branches))
	for _, branch := range branches {
		if !isSchemaObject(branch) {
			return compiledCheck{}, nil
		}
		node := branch.(map[string]any)
		if ref, ok := node["$ref"].(string); ok && len(node) == 1 {
			target, err := c.r.resolve(ref, c.r.baseFor(node, base))
			if err != nil {
				return compiledCheck{}, err
			}
			if !isSchemaObject(target.node) {
				return compiledCheck{}, nil
			}
			node = target.node.(map[string]any)
		}
		nodes = append(nodes, node)
	}
	properties, _ := nodes[0]["properties"].(map[string]any)
	for _, key := range slices.Sorted(maps.Keys(properties)) {
		tags := make([]any, 0, len(nodes))
		for _, node := range nodes {
			tag, ok := tagOf(node, key)
			if !ok {
				break
			}
			duplicate := false
			for _, previous := range tags {
				x, xNumeric := numericValue(tag)
				y, yNumeric := numericValue(previous)
				if deepEqual(tag, previous) || xNumeric && yNumeric && math.IsNaN(x) && math.IsNaN(y) {
					duplicate = true
					break
				}
			}
			if duplicate {
				break
			}
			tags = append(tags, tag)
		}
		if len(tags) == len(nodes) {
			checks, err := c.compileBranches(branches, base)
			if err != nil {
				return compiledCheck{}, err
			}
			return taggedCheck(key, tags, checks), nil
		}
	}
	return compiledCheck{}, nil
}

// The tag of a tagged-union branch: a primitive const on a required property of
// an object schema.
func tagOf(node map[string]any, key string) (any, bool) {
	if node["type"] != "object" {
		return nil, false
	}
	required, _ := node["required"].([]any)
	if !slices.ContainsFunc(required, func(value any) bool { name, ok := value.(string); return ok && name == key }) {
		return nil, false
	}
	properties, _ := node["properties"].(map[string]any)
	tag, ok := properties[key].(map[string]any)
	if !ok {
		return nil, false
	}
	value, found := tag["const"]
	return value, found && !isUndefined(value) && !isObject(value) && !isArray(value) && !isBytes(value)
}

// The string assertions share one check. A zero check means no assertion applies.
func (c *compiler) stringGroup(node map[string]any, strict bool) (compiledCheck, error) {
	min, max := 0.0, math.Inf(1)
	if n, ok := numericValue(node["minLength"]); ok {
		min = n
	}
	if n, ok := numericValue(node["maxLength"]); ok {
		max = n
	}
	var re *compat.Regexp
	if pattern, ok := node["pattern"].(string); ok {
		var err error
		// Match depth can grow with valid input; do not impose a fixed stack cap.
		re, err = compat.Compile(pattern, regexp2.ECMAScript|regexp2.Unicode, regexp2.OptionMaxBacktrackingStackSize(-1))
		if err != nil {
			return compiledCheck{}, fmt.Errorf("pattern %q: %w", pattern, err)
		}
	}
	var format func(string) bool
	if name, ok := node["format"].(string); ok {
		format = c.formats[name]
	}
	if min == 0 && math.IsInf(max, 1) && re == nil && format == nil {
		return compiledCheck{}, nil
	}
	return stringCheck(strict, min, max, re, format), nil
}

// Numeric assertions accept ordinary numeric bounds and bigint bounds.
func (c *compiler) numberGroup(node map[string]any, mode int) compiledCheck {
	inf := math.Inf(1)
	bounds := [5]Bound{-inf, inf, -inf, inf, nil}
	found := false
	for i, keyword := range [...]string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"} {
		value := node[keyword]
		if n, ok := numericValue(value); ok {
			bounds[i], found = n, true
		} else if isBigInt(value) {
			bounds[i], found = value, true
		}
	}
	if !found {
		return compiledCheck{}
	}
	return numberCheck(mode, bounds[0], bounds[1], bounds[2], bounds[3], bounds[4])
}

// A schema with only the core object keywords gets one closure that also does
// the type guard.
func (c *compiler) objectGroup(node map[string]any, base string, strict bool) (compiledCheck, error) {
	onlyCore := true
	for _, keyword := range [...]string{"propertyNames", "minProperties", "maxProperties", "dependentRequired", "dependentSchemas", "dependencies"} {
		if hasKeyword(node, keyword) {
			onlyCore = false
			break
		}
	}
	guard := GuardNone
	if onlyCore {
		guard = GuardLoose
		if strict {
			guard = GuardStrict
		}
	}
	core, err := c.objectCore(node, base, guard)
	if err != nil || core.run != nil && onlyCore {
		return core, err
	}
	var parts []compiledCheck
	if core.run != nil {
		parts = append(parts, core)
	}
	if hasKeyword(node, "propertyNames") {
		check, err := c.check(node["propertyNames"], base)
		if err != nil {
			return compiledCheck{}, err
		}
		parts = append(parts, propertyNamesCheck(check))
	}
	min, hasMin := numericValue(node["minProperties"])
	max, hasMax := numericValue(node["maxProperties"])
	if hasMin || hasMax {
		if !hasMax {
			max = math.Inf(1)
		}
		parts = append(parts, propertyCountCheck(min, max))
	}
	var required []requiredDependency
	var schemas []schemaDependency
	for _, keyword := range [...]string{"dependentRequired", "dependentSchemas", "dependencies"} {
		if c.r.draft == "7" && keyword != "dependencies" {
			continue
		}
		entries, _ := node[keyword].(map[string]any)
		for _, key := range slices.Sorted(maps.Keys(entries)) {
			value := entries[key]
			list, isList := value.([]any)
			if keyword == "dependentRequired" || keyword == "dependencies" && isList {
				if isList {
					names := make([]string, 0, len(list))
					for _, name := range list {
						names = append(names, propertyKey(name))
					}
					required = append(required, requiredDependency{key: key, required: names})
				}
			} else {
				check, err := c.check(value, base)
				if err != nil {
					return compiledCheck{}, err
				}
				schemas = append(schemas, schemaDependency{key: key, check: check})
			}
		}
	}
	if len(required) > 0 {
		parts = append(parts, dependentRequiredCheck(required))
	}
	if len(schemas) > 0 {
		parts = append(parts, dependentSchemasCheck(schemas))
	}
	if len(parts) == 0 {
		if strict {
			return typeChecks["object"], nil
		}
		return compiledCheck{}, nil
	}
	return guardObject(and(parts), strict), nil
}

func (c *compiler) objectCore(node map[string]any, base string, guard Guard) (compiledCheck, error) {
	properties, _ := node["properties"].(map[string]any)
	patterns, _ := node["patternProperties"].(map[string]any)
	var required []string
	if list, ok := node["required"].([]any); ok {
		for _, value := range list {
			if name, ok := value.(string); ok {
				required = append(required, name)
			}
		}
	}
	additional := node["additionalProperties"]
	if len(properties)+len(required)+len(patterns) == 0 && (!hasKeyword(node, "additionalProperties") || additional == true) {
		return compiledCheck{}, nil
	}
	entries := make([]propertyEntry, 0, len(properties))
	requiredCount := 0
	for _, key := range slices.Sorted(maps.Keys(properties)) {
		check, err := c.check(properties[key], base)
		if err != nil {
			return compiledCheck{}, err
		}
		mustExist := slices.Contains(required, key)
		if mustExist {
			requiredCount++
		}
		entries = append(entries, propertyEntry{key: key, check: check, required: mustExist})
	}
	var extraRequired []string
	for _, key := range required {
		if _, found := properties[key]; !found {
			extraRequired = append(extraRequired, key)
		}
	}
	var pats []patternEntry
	for _, pattern := range slices.Sorted(maps.Keys(patterns)) {
		re, err := compat.Compile(pattern, regexp2.ECMAScript|regexp2.Unicode, regexp2.OptionMaxBacktrackingStackSize(-1))
		if err != nil {
			return compiledCheck{}, fmt.Errorf("patternProperties %q: %w", pattern, err)
		}
		check, err := c.check(patterns[pattern], base)
		if err != nil {
			return compiledCheck{}, err
		}
		pats = append(pats, patternEntry{pattern: re, check: check})
	}
	var extra compiledCheck
	if hasKeyword(node, "additionalProperties") && additional != true {
		var err error
		extra, err = c.check(additional, base)
		if err != nil {
			return compiledCheck{}, err
		}
	}
	if len(pats) == 0 && len(extraRequired) == 0 && (extra.run == nil || additional == false) {
		return declaredObject(entries, requiredCount, additional == false, guard), nil
	}
	return openObject(entries, extraRequired, pats, extra, guard), nil
}

func (c *compiler) arrayGroup(node map[string]any, base string, strict bool) (compiledCheck, error) {
	var prefix []any
	rest := node["items"]
	hasRest := hasKeyword(node, "items")
	if tuple, ok := rest.([]any); ok {
		prefix = tuple
		rest, hasRest = node["additionalItems"], hasKeyword(node, "additionalItems")
	} else if c.r.draft != "7" {
		prefix, _ = node["prefixItems"].([]any)
	}
	checks := make([]compiledCheck, 0, len(prefix))
	for _, value := range prefix {
		check, err := c.check(value, base)
		if err != nil {
			return compiledCheck{}, err
		}
		checks = append(checks, check)
	}
	var restCheck compiledCheck
	if hasRest && rest != true {
		var err error
		restCheck, err = c.check(rest, base)
		if err != nil {
			return compiledCheck{}, err
		}
	}
	min, hasMin := numericValue(node["minItems"])
	max, hasMax := numericValue(node["maxItems"])
	if !hasMax {
		max = math.Inf(1)
	}
	hasItems := len(checks) > 0 || restCheck.run != nil
	if !hasKeyword(node, "contains") && node["uniqueItems"] != true && hasItems {
		// The common array: one closure for the guard, the length bounds and the items.
		guard := GuardLoose
		if strict {
			guard = GuardStrict
		}
		return listCheck(guard, min, max, checks, restCheck), nil
	}
	var parts []compiledCheck
	if hasItems {
		parts = append(parts, itemsCheck(checks, restCheck))
	}
	if hasMin || hasMax {
		parts = append(parts, lengthCheck(min, max))
	}
	if hasKeyword(node, "contains") {
		check, err := c.check(node["contains"], base)
		if err != nil {
			return compiledCheck{}, err
		}
		minContains, maxContains := 1.0, math.Inf(1)
		if c.r.draft != "7" {
			if n, ok := numericValue(node["minContains"]); ok {
				minContains = n
			}
			if n, ok := numericValue(node["maxContains"]); ok {
				maxContains = n
			}
		}
		parts = append(parts, containsCheck(check, minContains, maxContains))
	}
	if node["uniqueItems"] == true {
		parts = append(parts, plainCheck(uniqueCheck))
	}
	if len(parts) == 0 {
		if strict {
			return typeChecks["array"], nil
		}
		return compiledCheck{}, nil
	}
	return guardArray(and(parts), strict), nil
}

// An explicit null is present; Undefined is equivalent to an omitted keyword.
func hasKeyword(node map[string]any, key string) bool {
	value, found := node[key]
	return found && !isUndefined(value)
}

// Dependency arrays name properties using string conversion.
func propertyKey(value any) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case undefinedValue:
		return "undefined"
	case map[string]any:
		return "[object Object]"
	case []any:
		parts := make([]string, len(value))
		for i, item := range value {
			if item != nil && !isUndefined(item) {
				parts[i] = propertyKey(item)
			}
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(value)
	}
}

func (c *compiler) ref(ref, base string) (compiledCheck, error) {
	target, err := c.r.resolve(ref, base)
	if err != nil {
		return compiledCheck{}, err
	}
	check, err := c.check(target.node, target.base)
	if err == nil && c.dynamic && target.base != base {
		check = scopedCheck(target.base, check)
	}
	return check, err
}

// The initial target and anchor name of a $dynamicRef that resolves dynamically,
// or an empty name for a plain ref.
func (c *compiler) bookended(ref, base string) (resolved, string, error) {
	initial, err := c.r.resolve(ref, base)
	if err != nil {
		return resolved{}, "", err
	}
	_, name, _ := strings.Cut(resolveURI(ref, base), "#")
	if name == "" || strings.HasPrefix(name, "/") || !isSchemaObject(initial.node) {
		return initial, "", nil
	}
	if anchor, ok := initial.node.(map[string]any)["$dynamicAnchor"].(string); !ok || anchor != name {
		return initial, "", nil
	}
	if !c.dynamic {
		return resolved{}, "", errDynamicScope
	}
	return initial, name, nil
}

// Resolved at run time against the dynamic scope. The compiler lock protects
// lazy caches and indexes; checks run after it is released. Compilation errors
// in a selected target panic.
func dynamicLookup[T any](c *compiler, name string, compile func(any, string) (T, error)) func(string) (T, bool) {
	type result struct {
		check T
		found bool
	}
	cache := make(map[string]result)
	return func(uri string) (T, bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if cached, ok := cache[uri]; ok {
			return cached.check, cached.found
		}
		var value result
		if target, ok := c.r.dynamicAnchors[uri+"#"+name]; ok {
			check, err := compile(target.node, target.base)
			if err != nil {
				panic(err)
			}
			value = result{check: check, found: true}
		}
		cache[uri] = value
		return value.check, value.found
	}
}

func (c *compiler) dynamicRef(ref, base string) (compiledCheck, error) {
	initial, name, err := c.bookended(ref, base)
	if err != nil {
		return compiledCheck{}, err
	}
	if name == "" {
		return c.ref(ref, base)
	}
	fallback, err := c.check(initial.node, initial.base)
	if err != nil {
		return compiledCheck{}, err
	}
	lookup := dynamicLookup(c, name, c.check)
	return compiledCheck{run: func(v any, scope *evaluationScope) bool {
		if scope != nil {
			for _, uri := range scope.resources {
				if check, ok := lookup(uri); ok {
					return check.test(v, scope)
				}
			}
		}
		return fallback.test(v, scope)
	}}, nil
}

// The annotating check of a schema evaluated in place, only below
// unevaluatedProperties/unevaluatedItems.
func (c *compiler) annotating(value any, base string) (annotatingCheck, error) {
	if truth, ok := value.(bool); ok {
		return func(any, *annotations, *evaluationScope) bool { return truth }, nil
	}
	if !isSchemaObject(value) {
		return nil, fmt.Errorf("a schema must be an object or a boolean, got %T", value)
	}
	node := value.(map[string]any)
	identity := schemaID(node)
	if cached, found := c.annotatingCache[identity]; found {
		return *cached.check, nil
	}
	slot := new(annotatingCheck)
	*slot = indirectAnnotating(slot)
	c.annotatingCache[identity] = cachedAnnotatingSchema{node: node, check: slot}
	check, err := c.buildAnnotating(node, c.r.baseFor(node, base))
	if err != nil {
		delete(c.annotatingCache, identity)
		return nil, err
	}
	*slot = check
	return check, nil
}

func (c *compiler) buildAnnotating(node map[string]any, base string) (annotatingCheck, error) {
	rest := make(map[string]any)
	annotates := false
	for key, value := range node {
		if _, ok := annotatingKeywords[key]; ok {
			annotates = true
		} else {
			rest[key] = value
		}
	}
	plain, err := c.plain(rest, base)
	if err != nil {
		return nil, err
	}
	var parts []annotatingCheck
	if annotates {
		parts, err = c.annotatingParts(node, base)
		if err != nil {
			return nil, err
		}
	}
	all := ignoreAnnotations(plain)
	if len(parts) > 0 {
		all = annotateAll(plain, parts)
	}
	if hasKeyword(node, "unevaluatedProperties") || hasKeyword(node, "unevaluatedItems") {
		all = isolateAnnotations(all)
	}
	if c.dynamic && c.r.isResource(node, base) {
		all = scopedAnnotating(base, all)
	}
	return all, nil
}

func (c *compiler) annotatingParts(node map[string]any, base string) ([]annotatingCheck, error) {
	var parts []annotatingCheck
	if hasKeyword(node, "properties") || hasKeyword(node, "patternProperties") || hasKeyword(node, "additionalProperties") {
		var props []propertyEntry
		properties, _ := node["properties"].(map[string]any)
		for _, key := range slices.Sorted(maps.Keys(properties)) {
			check, err := c.check(properties[key], base)
			if err != nil {
				return nil, err
			}
			props = append(props, propertyEntry{key: key, check: check})
		}
		var pats []patternEntry
		patterns, _ := node["patternProperties"].(map[string]any)
		for _, pattern := range slices.Sorted(maps.Keys(patterns)) {
			re, err := compat.Compile(pattern, regexp2.ECMAScript|regexp2.Unicode, regexp2.OptionMaxBacktrackingStackSize(-1))
			if err != nil {
				return nil, fmt.Errorf("patternProperties %q: %w", pattern, err)
			}
			check, err := c.check(patterns[pattern], base)
			if err != nil {
				return nil, err
			}
			pats = append(pats, patternEntry{pattern: re, check: check})
		}
		var extra compiledCheck
		if hasKeyword(node, "additionalProperties") {
			var err error
			extra, err = c.check(node["additionalProperties"], base)
			if err != nil {
				return nil, err
			}
		}
		parts = append(parts, annotateObject(props, pats, extra))
	}
	if hasKeyword(node, "prefixItems") || hasKeyword(node, "items") || hasKeyword(node, "contains") {
		prefix, ok := node["prefixItems"].([]any)
		if !ok {
			prefix, _ = node["items"].([]any)
		}
		rest, hasRest := node["items"], hasKeyword(node, "items")
		if _, tuple := rest.([]any); tuple {
			rest, hasRest = node["additionalItems"], hasKeyword(node, "additionalItems")
		}
		checks, err := c.compileBranches(prefix, base)
		if err != nil {
			return nil, err
		}
		var restCheck, contains compiledCheck
		if hasRest {
			restCheck, err = c.check(rest, base)
			if err != nil {
				return nil, err
			}
		}
		if hasKeyword(node, "contains") {
			contains, err = c.check(node["contains"], base)
			if err != nil {
				return nil, err
			}
		}
		minContains, maxContains := 1.0, math.Inf(1)
		if n, ok := numericValue(node["minContains"]); ok {
			minContains = n
		}
		if n, ok := numericValue(node["maxContains"]); ok {
			maxContains = n
		}
		parts = append(parts, annotateArray(checks, restCheck, contains, minContains, maxContains))
	}
	for _, keyword := range [...]string{"allOf", "anyOf", "oneOf"} {
		if branches, ok := node[keyword].([]any); ok {
			checks := make([]annotatingCheck, 0, len(branches))
			for _, branch := range branches {
				check, err := c.annotating(branch, base)
				if err != nil {
					return nil, err
				}
				checks = append(checks, check)
			}
			if keyword == "allOf" {
				parts = append(parts, checks...)
			} else {
				parts = append(parts, annotateAny(checks, keyword == "oneOf"))
			}
		}
	}
	if hasKeyword(node, "if") {
		var then, otherwise annotatingCheck
		var err error
		if hasKeyword(node, "then") {
			then, err = c.annotating(node["then"], base)
			if err != nil {
				return nil, err
			}
		}
		if hasKeyword(node, "else") {
			otherwise, err = c.annotating(node["else"], base)
			if err != nil {
				return nil, err
			}
		}
		condition, err := c.annotating(node["if"], base)
		if err != nil {
			return nil, err
		}
		parts = append(parts, annotateIf(condition, then, otherwise))
	}
	if ref, ok := node["$ref"].(string); ok {
		check, err := c.annotatingRef(ref, base)
		if err != nil {
			return nil, err
		}
		parts = append(parts, check)
	}
	if ref, ok := node["$dynamicRef"].(string); ok {
		check, err := c.annotatingDynamicRef(ref, base)
		if err != nil {
			return nil, err
		}
		parts = append(parts, check)
	}
	dependencies, _ := node["dependentSchemas"].(map[string]any)
	for _, key := range slices.Sorted(maps.Keys(dependencies)) {
		check, err := c.annotating(dependencies[key], base)
		if err != nil {
			return nil, err
		}
		parts = append(parts, annotateDependent(key, check))
	}
	for _, keyword := range [...]string{"unevaluatedProperties", "unevaluatedItems"} {
		if !hasKeyword(node, keyword) {
			continue
		}
		check, err := c.check(node[keyword], base)
		if err != nil {
			return nil, err
		}
		if keyword == "unevaluatedProperties" {
			parts = append(parts, unevaluatedProperties(check))
		} else {
			parts = append(parts, unevaluatedItems(check))
		}
	}
	return parts, nil
}

func (c *compiler) annotatingRef(ref, base string) (annotatingCheck, error) {
	target, err := c.r.resolve(ref, base)
	if err != nil {
		return nil, err
	}
	check, err := c.annotating(target.node, target.base)
	if err == nil && c.dynamic && target.base != base {
		check = scopedAnnotating(target.base, check)
	}
	return check, err
}

func (c *compiler) annotatingDynamicRef(ref, base string) (annotatingCheck, error) {
	initial, name, err := c.bookended(ref, base)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return c.annotatingRef(ref, base)
	}
	fallback, err := c.annotating(initial.node, initial.base)
	if err != nil {
		return nil, err
	}
	lookup := dynamicLookup(c, name, c.annotating)
	return func(v any, a *annotations, scope *evaluationScope) bool {
		if scope != nil {
			for _, uri := range scope.resources {
				if check, ok := lookup(uri); ok {
					return check(v, a, scope)
				}
			}
		}
		return fallback(v, a, scope)
	}, nil
}

// The keywords an annotating check handles itself; every other keyword compiles
// into its plain part.
var annotatingKeywords = map[string]struct{}{
	"properties": {}, "patternProperties": {}, "additionalProperties": {},
	"prefixItems": {}, "items": {}, "additionalItems": {}, "contains": {},
	"minContains": {}, "maxContains": {}, "allOf": {}, "anyOf": {}, "oneOf": {},
	"if": {}, "then": {}, "else": {}, "$ref": {}, "$dynamicRef": {},
	"dependentSchemas": {}, "unevaluatedProperties": {}, "unevaluatedItems": {},
}

// Keywords that never affect a check.
var neutralKeywords = map[string]struct{}{
	"$schema": {}, "$comment": {}, "title": {}, "description": {},
	"default": {}, "examples": {}, "deprecated": {}, "readOnly": {}, "writeOnly": {},
	"$defs": {}, "definitions": {}, "contentEncoding": {}, "contentMediaType": {},
	"minContains": {}, "maxContains": {}, "$vocabulary": {},
}
