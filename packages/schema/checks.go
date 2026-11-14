package schema

import (
	"bytes"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

func always(any) bool     { return true }
func never(any) bool      { return false }
func isString(v any) bool { _, ok := v.(string); return ok }
func isNumber(v any) bool {
	n, ok := numericValue(v)
	return ok && !math.IsNaN(n) && !math.IsInf(n, 0)
}
func isInteger(v any) bool {
	n, ok := numericValue(v)
	return ok && !math.IsInf(n, 0) && math.Trunc(n) == n
}
func isBoolean(v any) bool   { _, ok := v.(bool); return ok }
func isNull(v any) bool      { return v == nil }
func isArray(v any) bool     { _, ok := v.([]any); return ok }
func isBytes(v any) bool     { _, ok := v.([]byte); return ok }
func isBigInt(v any) bool    { n, ok := v.(*big.Int); return ok && n != nil }
func isUndefined(v any) bool { _, ok := v.(undefinedValue); return ok }

// Whether v is a JSON object: not null, an array or bytes.
func isObject(v any) bool { _, ok := v.(map[string]any); return ok }

// A leaf check as a code and its argument, which object and array loops run
// by leafOk instead of calling the check. The zero value has no leaf form.
type Leaf struct {
	Code int
	Arg  any
}

// Compiled checks carry their leaf and bare-type information explicitly.
// No function identity or global metadata registry is needed.
type compiledCheck struct {
	run     func(any, *evaluationScope) bool
	leaf    Leaf
	typeBit int
}

// Dynamic scope belongs to one evaluation, never to a compiled check.
type evaluationScope struct {
	resources []string
}

func (c compiledCheck) check(v any) bool { return c.run(v, nil) }

func plainCheck(check Check) compiledCheck {
	return compiledCheck{run: func(v any, _ *evaluationScope) bool { return check(v) }}
}

// Container loops execute leaf checks directly, falling back to the closure.
func (c compiledCheck) test(v any, scope *evaluationScope) bool {
	if c.leaf.Code != 0 {
		return leafOk(c.leaf.Code, v, c.leaf.Arg)
	}
	return c.run(v, scope)
}

func leaf(code int, arg any, check Check) compiledCheck {
	c := plainCheck(check)
	c.leaf = Leaf{Code: code, Arg: arg}
	return c
}

// The leaf form of check; Code is zero when it is not a leaf.
func leafOf(check compiledCheck) Leaf { return check.leaf }

// Codes 1-6 are the bare types and true; the others read their argument.
func leafOk(code int, x, arg any) bool {
	switch code {
	case 1:
		return isString(x)
	case 2:
		return isNumber(x)
	case 3:
		return isInteger(x)
	case 4:
		return isBoolean(x)
	case 5:
		return isNull(x)
	case 7:
		return deepEqual(x, arg)
	case 8:
		for _, value := range arg.([]any) {
			if deepEqual(x, value) {
				return true
			}
		}
		return false
	case 9:
		return typeBits(x)&arg.(int) != 0
	case 10, 11:
		n, ok := numericValue(x)
		return ok && isNumber(n) && (code != 10 || math.Trunc(n) == n) && inBounds(n, arg.(Bounds))
	case 12:
		s, ok := x.(string)
		b := arg.(Bounds)
		return ok && lengthOk(s, b.Min, b.Max)
	case 13:
		s, ok := x.(string)
		return ok && arg.(*regexp.Regexp).MatchString(s)
	default:
		return true
	}
}

const (
	stringBit = 1 << iota
	numberBit
	integerBit
	booleanBit
	nullBit
	arrayBit
	objectBit
	bytesBit
	bigintBit
	undefinedBit
)

// The type bits of v: an integer has the bits of number and integer;
// NaN and Infinity have neither.
func typeBits(v any) int {
	if n, ok := numericValue(v); ok {
		if !isNumber(n) {
			return 0
		}
		if math.Trunc(n) == n {
			return numberBit | integerBit
		}
		return numberBit
	}
	switch v.(type) {
	case string:
		return stringBit
	case bool:
		return booleanBit
	case nil:
		return nullBit
	case []any:
		return arrayBit
	case map[string]any:
		return objectBit
	case []byte:
		return bytesBit
	case *big.Int:
		if isBigInt(v) {
			return bigintBit
		}
	case undefinedValue:
		return undefinedBit
	}
	return 0
}

// The check of a type name. bytes, bigint and undefined extend JSON Schema
// for values that never pass through JSON. A number is finite, as in TypeBox.
var typeChecks = map[string]compiledCheck{
	"string":    {run: plainCheck(isString).run, leaf: Leaf{Code: 1}, typeBit: stringBit},
	"number":    {run: plainCheck(isNumber).run, leaf: Leaf{Code: 2}, typeBit: numberBit},
	"integer":   {run: plainCheck(isInteger).run, leaf: Leaf{Code: 3}, typeBit: integerBit},
	"boolean":   {run: plainCheck(isBoolean).run, leaf: Leaf{Code: 4}, typeBit: booleanBit},
	"null":      {run: plainCheck(isNull).run, leaf: Leaf{Code: 5}, typeBit: nullBit},
	"array":     {run: plainCheck(isArray).run, typeBit: arrayBit},
	"object":    {run: plainCheck(isObject).run, typeBit: objectBit},
	"bytes":     {run: plainCheck(isBytes).run, typeBit: bytesBit},
	"bigint":    {run: plainCheck(isBigInt).run, typeBit: bigintBit},
	"undefined": {run: plainCheck(isUndefined).run, typeBit: undefinedBit},
}

// The bits of the named types; unknown names add none.
func typeMask(types []any) int {
	mask := 0
	for _, value := range types {
		if name, ok := value.(string); ok {
			mask |= typeChecks[name].typeBit
		}
	}
	return mask
}

func maskCheck(mask int) compiledCheck {
	return leaf(9, mask, func(v any) bool { return typeBits(v)&mask != 0 })
}

// Any of the named types.
func typeCheck(types []any) compiledCheck {
	if len(types) == 1 {
		if name, ok := types[0].(string); ok {
			if check, found := typeChecks[name]; found {
				return check
			}
		}
		return plainCheck(never)
	}
	return maskCheck(typeMask(types))
}

// anyOf whose bare-type branches have the types of mask: those by one type
// test, the others by their checks.
func maskedAnyCheck(mask int, checks []compiledCheck) compiledCheck {
	if len(checks) == 0 {
		return maskCheck(mask)
	}
	rest := checks[0]
	if len(checks) > 1 {
		rest = anyCheck(checks)
	}
	return compiledCheck{run: func(v any, scope *evaluationScope) bool {
		return typeBits(v)&mask != 0 || rest.run(v, scope)
	}}
}

// A byte slice within the length bounds.
func bytesCheck(min, max float64) compiledCheck {
	return plainCheck(func(v any) bool {
		data, ok := v.([]byte)
		return ok && float64(len(data)) >= min && float64(len(data)) <= max
	})
}

// The string keywords: length bounds, pattern and format.
func stringCheck(strict bool, min, max float64, re *regexp.Regexp, format func(string) bool) compiledCheck {
	if re != nil && min == 0 && math.IsInf(max, 1) && format == nil {
		if strict {
			return leaf(13, re, func(v any) bool {
				s, ok := v.(string)
				return ok && re.MatchString(s)
			})
		}
		return plainCheck(func(v any) bool {
			s, ok := v.(string)
			return !ok || re.MatchString(s)
		})
	}
	if re == nil && format == nil {
		check := func(v any) bool {
			s, ok := v.(string)
			if !ok {
				return !strict
			}
			return lengthOk(s, min, max)
		}
		if strict {
			return leaf(12, Bounds{Min: min, Max: max}, check)
		}
		return plainCheck(check)
	}
	return plainCheck(func(v any) bool {
		s, ok := v.(string)
		if !ok {
			return !strict
		}
		return lengthOk(s, min, max) && (re == nil || re.MatchString(s)) && (format == nil || format(s))
	})
}

// A number bound: a Go numeric value or *big.Int (TypeBox's Type.BigInt
// writes its bounds as bigints). The accepted types are checked at use sites.
type Bound = any

// The number keywords, for numbers and bigints alike. Bounds of either kind
// compare with values of either kind. Mode: 0 passes other types, 1 requires
// a number, 2 an integer, 3 a bigint. A nil multipleOf is absent.
func numberCheck(mode int, min, max, xmin, xmax, multipleOf Bound) compiledCheck {
	lower, minPlain := numericValue(min)
	upper, maxPlain := numericValue(max)
	xlower, xminPlain := numericValue(xmin)
	xupper, xmaxPlain := numericValue(xmax)
	plain := minPlain && maxPlain && xminPlain && xmaxPlain
	bounds := Bounds{lower, upper, xlower, xupper}
	mixed := [...]struct {
		value  Bound
		lower  bool
		strict bool
	}{{min, true, false}, {max, false, false}, {xmin, true, true}, {xmax, false, true}}
	check := func(v any) bool {
		n, numeric := numericValue(v)
		bigint := isBigInt(v)
		if !numeric && !bigint {
			return mode == 0
		}
		if mode == 3 {
			if !bigint {
				return false
			}
		} else if mode != 0 && (!numeric || !isNumber(n)) {
			return false
		}
		if mode == 2 && math.Trunc(n) != n {
			return false
		}
		if plain && numeric {
			return inBounds(n, bounds) && (multipleOf == nil || isMultiple(v, multipleOf))
		}
		for _, bound := range mixed {
			order, ordered := compareBound(v, bound.value)
			if !ordered || bound.lower && order < 0 || !bound.lower && order > 0 || bound.strict && order == 0 {
				return false
			}
		}
		return multipleOf == nil || isMultiple(v, multipleOf)
	}
	if mode == 0 || mode == 3 || multipleOf != nil || !plain {
		return plainCheck(check)
	}
	code := 11
	if mode == 2 {
		code = 10
	}
	return leaf(code, bounds, check)
}

// Whether v is a multiple of d, exactly for decimals. With a bigint on either
// side, use exact integer arithmetic; a bigint is no multiple of zero or a fraction.
func isMultiple(v, d Bound) bool {
	xbig, xIsBig := v.(*big.Int)
	ybig, yIsBig := d.(*big.Int)
	x, xNumeric := numericValue(v)
	y, yNumeric := numericValue(d)
	if xIsBig || yIsBig {
		if xIsBig {
			if xbig == nil {
				return false
			}
		} else {
			if !xNumeric || !isInteger(x) {
				return false
			}
			xbig, _ = big.NewFloat(x).Int(nil)
		}
		if yIsBig {
			if ybig == nil {
				return false
			}
		} else {
			if !yNumeric || !isInteger(y) {
				return false
			}
			ybig, _ = big.NewFloat(y).Int(nil)
		}
		return ybig.Sign() != 0 && new(big.Int).Rem(xbig, ybig).Sign() == 0
	}
	if !xNumeric || !yNumeric {
		return false
	}
	q := x / y
	if isInteger(q) {
		return true
	}
	if !isNumber(q) {
		return math.Mod(x, y) == 0
	}
	xc, xe := decimal(x)
	yc, ye := decimal(y)
	if xe >= ye {
		xc.Mul(xc, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(xe-ye)), nil))
	} else {
		yc.Mul(yc, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(ye-xe)), nil))
	}
	return xc.Rem(xc, yc).Sign() == 0
}

// Native numeric values are converted to float64, including NaN and Infinity.
func numericValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case uintptr:
		return float64(n), true
	default:
		return 0, false
	}
}

// Compare mixed number/bigint bounds without rounding the bigint to float64.
// NaN is unordered, matching JS relational comparisons.
func compareBound(a, b Bound) (int, bool) {
	xbig, xIsBig := a.(*big.Int)
	ybig, yIsBig := b.(*big.Int)
	x, xNumeric := numericValue(a)
	y, yNumeric := numericValue(b)
	if xIsBig && yIsBig {
		return xbig.Cmp(ybig), true
	}
	if xIsBig {
		if !yNumeric || math.IsNaN(y) {
			return 0, false
		}
		return new(big.Float).SetInt(xbig).Cmp(big.NewFloat(y)), true
	}
	if yIsBig {
		if !xNumeric || math.IsNaN(x) {
			return 0, false
		}
		return big.NewFloat(x).Cmp(new(big.Float).SetInt(ybig)), true
	}
	if !xNumeric || !yNumeric || math.IsNaN(x) || math.IsNaN(y) {
		return 0, false
	}
	if x < y {
		return -1, true
	}
	if x > y {
		return 1, true
	}
	return 0, true
}

// const.
func constCheck(value any) compiledCheck {
	check := func(v any) bool { return deepEqual(v, value) }
	if isArray(value) || isObject(value) || isBytes(value) {
		return plainCheck(check)
	}
	return leaf(7, value, check)
}

// enum: up to 8 primitives by a scan, more by a set, objects and arrays by deep equality.
func enumCheck(values []any) compiledCheck {
	var primitives, objects []any
	for _, value := range values {
		if isArray(value) || isObject(value) || isBytes(value) {
			objects = append(objects, value)
		} else {
			primitives = append(primitives, value)
		}
	}
	if len(primitives) == len(values) && len(primitives) <= 8 {
		return leaf(8, primitives, func(v any) bool {
			for _, value := range primitives {
				if deepEqual(v, value) {
					return true
				}
			}
			return false
		})
	}
	// Canonical keys preserve JS Set equality across Go numeric widths and
	// bigint pointers. A Set also matches NaN, unlike the small enum scan.
	key := func(v any) (any, bool) {
		if n, ok := numericValue(v); ok {
			if math.IsNaN(n) {
				return struct{}{}, true
			}
			return n, true
		}
		switch x := v.(type) {
		case nil, string, bool, undefinedValue:
			return v, true
		case *big.Int:
			return struct{ bigint string }{x.String()}, true
		default:
			return nil, false
		}
	}
	set := make(map[any]struct{}, len(primitives))
	for _, value := range primitives {
		if k, ok := key(value); ok {
			set[k] = struct{}{}
		}
	}
	return plainCheck(func(v any) bool {
		if k, ok := key(v); ok {
			_, found := set[k]
			return found
		}
		for _, object := range objects {
			if deepEqual(v, object) {
				return true
			}
		}
		return false
	})
}

// All of parts; two and three parts get closures without a loop.
func and(parts []compiledCheck) compiledCheck {
	switch len(parts) {
	case 0:
		return leaf(6, nil, always)
	case 1:
		return parts[0]
	case 2:
		a, b := parts[0], parts[1]
		return compiledCheck{run: func(v any, scope *evaluationScope) bool { return a.run(v, scope) && b.run(v, scope) }}
	case 3:
		a, b, c := parts[0], parts[1], parts[2]
		return compiledCheck{run: func(v any, scope *evaluationScope) bool {
			return a.run(v, scope) && b.run(v, scope) && c.run(v, scope)
		}}
	default:
		return compiledCheck{run: func(v any, scope *evaluationScope) bool {
			for _, check := range parts {
				if !check.run(v, scope) {
					return false
				}
			}
			return true
		}}
	}
}

// anyOf.
func anyCheck(checks []compiledCheck) compiledCheck {
	return compiledCheck{run: func(v any, scope *evaluationScope) bool {
		for _, check := range checks {
			if check.run(v, scope) {
				return true
			}
		}
		return false
	}}
}

// oneOf.
func oneCheck(checks []compiledCheck) compiledCheck {
	return compiledCheck{run: func(v any, scope *evaluationScope) bool {
		n := 0
		for _, check := range checks {
			if check.run(v, scope) {
				n++
				if n > 1 {
					return false
				}
			}
		}
		return n == 1
	}}
}

// not.
func notCheck(check compiledCheck) compiledCheck {
	return compiledCheck{run: func(v any, scope *evaluationScope) bool { return !check.run(v, scope) }}
}

// if, then and else.
func ifCheck(cond, then, otherwise compiledCheck) compiledCheck {
	return compiledCheck{run: func(v any, scope *evaluationScope) bool {
		if cond.run(v, scope) {
			return then.run(v, scope)
		}
		return otherwise.run(v, scope)
	}}
}

// A check whose implementation is set later: the indirection a recursive reference goes through.
func indirect(slot *compiledCheck) compiledCheck {
	return compiledCheck{run: func(v any, scope *evaluationScope) bool { return slot.run(v, scope) }}
}

// Whether a combined object or array check also checks the type:
// not at all, strictly, or passing other types.
type Guard uint8

const (
	GuardNone Guard = iota
	GuardStrict
	GuardLoose
)

// Object entries keep checks and required markers in named fields.
// Go maps have no inherited keys.
type propertyEntry struct {
	key      string
	check    compiledCheck
	required bool
}

type patternEntry struct {
	pattern *regexp.Regexp
	check   compiledCheck
}

type requiredDependency struct {
	key      string
	required []string
}

type schemaDependency struct {
	key   string
	check compiledCheck
}

// inner for objects; other values pass unless strict.
func guardObject(inner compiledCheck, strict bool) compiledCheck {
	return compiledCheck{run: func(v any, scope *evaluationScope) bool {
		if !isObject(v) {
			return !strict
		}
		return inner.run(v, scope)
	}}
}

// inner for arrays; other values pass unless strict.
func guardArray(inner compiledCheck, strict bool) compiledCheck {
	return compiledCheck{run: func(v any, scope *evaluationScope) bool {
		if !isArray(v) {
			return !strict
		}
		return inner.run(v, scope)
	}}
}

// An object with declared properties; closed rejects any other key. An optional
// property set to Undefined counts as absent. Required properties may accept it.
func declaredObject(entries []propertyEntry, requiredCount int, closed bool, guard Guard) compiledCheck {
	index := make(map[string]propertyEntry, len(entries))
	for _, entry := range entries {
		index[entry.key] = entry
	}
	return compiledCheck{run: func(v any, scope *evaluationScope) bool {
		if guard != GuardNone && !isObject(v) {
			return guard == GuardLoose
		}
		object := v.(map[string]any)
		required := 0
		for key, value := range object {
			entry, found := index[key]
			if !found {
				if closed {
					return false
				}
				continue
			}
			if isUndefined(value) && !entry.required {
				continue
			}
			if !entry.check.test(value, scope) {
				return false
			}
			if entry.required {
				required++
			}
		}
		return required == requiredCount
	}}
}

// An object with declared properties, required keys not declared,
// pattern properties and additional properties.
func openObject(entries []propertyEntry, extraRequired []string, patterns []patternEntry, additional compiledCheck, guard Guard) compiledCheck {
	known := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		known[entry.key] = struct{}{}
	}
	return compiledCheck{run: func(v any, scope *evaluationScope) bool {
		if guard != GuardNone && !isObject(v) {
			return guard == GuardLoose
		}
		object := v.(map[string]any)
		for _, entry := range entries {
			value, found := object[entry.key]
			if !found || isUndefined(value) && !entry.required {
				if entry.required {
					return false
				}
				continue
			}
			if !entry.check.test(value, scope) {
				return false
			}
		}
		for _, key := range extraRequired {
			if _, found := object[key]; !found {
				return false
			}
		}
		if len(patterns) == 0 && additional.run == nil {
			return true
		}
		for key, value := range object {
			_, matched := known[key]
			for _, pattern := range patterns {
				if pattern.pattern.MatchString(key) {
					matched = true
					if !pattern.check.run(value, scope) {
						return false
					}
				}
			}
			if !matched && additional.run != nil && !additional.run(value, scope) {
				return false
			}
		}
		return true
	}}
}

// Every property name passes check (v is an object).
func propertyNamesCheck(check compiledCheck) compiledCheck {
	return compiledCheck{run: func(v any, scope *evaluationScope) bool {
		for key := range v.(map[string]any) {
			if !check.run(key, scope) {
				return false
			}
		}
		return true
	}}
}

// minProperties and maxProperties (v is an object).
func propertyCountCheck(min, max float64) compiledCheck {
	return plainCheck(func(v any) bool {
		n := float64(len(v.(map[string]any)))
		return n >= min && n <= max
	})
}

// dependentRequired (v is an object).
func dependentRequiredCheck(entries []requiredDependency) compiledCheck {
	return plainCheck(func(v any) bool {
		object := v.(map[string]any)
		for _, entry := range entries {
			if _, found := object[entry.key]; found {
				for _, key := range entry.required {
					if _, found := object[key]; !found {
						return false
					}
				}
			}
		}
		return true
	})
}

// dependentSchemas (v is an object).
func dependentSchemasCheck(entries []schemaDependency) compiledCheck {
	return compiledCheck{run: func(v any, scope *evaluationScope) bool {
		object := v.(map[string]any)
		for _, entry := range entries {
			if _, found := object[entry.key]; found && !entry.check.run(v, scope) {
				return false
			}
		}
		return true
	}}
}

// The common array: guard, length bounds, prefix items and the items after them,
// in one closure. A zero compiledCheck means no rest check; its zero Leaf is NO_LEAF.
func listCheck(guard Guard, min, max float64, prefix []compiledCheck, rest compiledCheck) compiledCheck {
	return compiledCheck{run: func(v any, scope *evaluationScope) bool {
		list, ok := v.([]any)
		if !ok {
			return guard == GuardLoose
		}
		length := float64(len(list))
		if length < min || length > max {
			return false
		}
		for i := 0; i < len(list) && i < len(prefix); i++ {
			if !prefix[i].run(list[i], scope) {
				return false
			}
		}
		if rest.run != nil {
			for i := len(prefix); i < len(list); i++ {
				if !rest.test(list[i], scope) {
					return false
				}
			}
		}
		return true
	}}
}

// Prefix items and the items after them (v is an array).
func itemsCheck(prefix []compiledCheck, rest compiledCheck) compiledCheck {
	return listCheck(GuardNone, 0, math.Inf(1), prefix, rest)
}

// minItems and maxItems (v is an array).
func lengthCheck(min, max float64) compiledCheck {
	return plainCheck(func(v any) bool {
		n := float64(len(v.([]any)))
		return n >= min && n <= max
	})
}

// contains with its bounds (v is an array).
func containsCheck(check compiledCheck, min, max float64) compiledCheck {
	return compiledCheck{run: func(v any, scope *evaluationScope) bool {
		n := 0
		for _, value := range v.([]any) {
			if check.run(value, scope) {
				n++
				if float64(n) > max {
					return false
				}
			}
		}
		return float64(n) >= min
	}}
}

// uniqueItems (v is an array).
func uniqueCheck(v any) bool {
	list := v.([]any)
	for i := 1; i < len(list); i++ {
		for j := 0; j < i; j++ {
			if deepEqual(list[i], list[j]) {
				return false
			}
		}
	}
	return true
}

// A union of objects tagged by a const on key: the tag selects the one branch
// that can match. Tags are primitives, as selected by the compiler.
func taggedCheck(key string, tags []any, checks []compiledCheck) compiledCheck {
	return compiledCheck{run: func(v any, scope *evaluationScope) bool {
		object, ok := v.(map[string]any)
		if !ok {
			return false
		}
		tag, found := object[key]
		if !found {
			tag = Undefined
		}
		for i, value := range tags {
			if deepEqual(value, tag) {
				return checks[i].run(v, scope)
			}
		}
		return false
	}}
}

// check with uri pushed onto the dynamic scope while it runs (only for schemas
// with $dynamicRef). Allocate scope at the first resource, once per evaluation.
func scopedCheck(uri string, check compiledCheck) compiledCheck {
	return compiledCheck{run: func(v any, scope *evaluationScope) bool {
		if scope == nil {
			scope = &evaluationScope{}
		}
		length := len(scope.resources)
		scope.resources = append(scope.resources, uri)
		defer func() { scope.resources = scope.resources[:length] }()
		return check.run(v, scope)
	}}
}

// Bounds of a number or a string length leaf.
type Bounds struct {
	Min  float64
	Max  float64
	Xmin float64
	Xmax float64
}

func inBounds(x float64, b Bounds) bool {
	return x >= b.Min && x <= b.Max && x > b.Xmin && x < b.Xmax
}

// The number of Unicode code points of s: JSON Schema string lengths count code points.
func codePoints(s string) int { return utf8.RuneCountInString(s) }

func lengthOk(s string, min, max float64) bool {
	n := float64(codePoints(s))
	if n < min {
		return false
	}
	return n <= max
}

func decimal(x float64) (*big.Int, int) {
	x = math.Abs(x)
	// Match JS number.toString's fixed and exponential notation thresholds.
	format := byte('e')
	if x == 0 || (x >= 1e-6 && x < 1e21) {
		format = 'f'
	}
	mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(x, format, -1, 64), "e")
	e, _ := strconv.Atoi(exponent)
	if point := strings.IndexByte(mantissa, '.'); point >= 0 {
		e -= len(mantissa) - point - 1
		mantissa = mantissa[:point] + mantissa[point+1:]
	}
	coefficient, _ := new(big.Int).SetString(mantissa, 10)
	return coefficient, e
}

// JSON equality, as const, enum and uniqueItems compare: the same primitives,
// arrays with equal items in order, objects with the same keys and equal values
// (key order does not matter).
func deepEqual(a, b any) bool {
	if x, ok := numericValue(a); ok {
		y, ok := numericValue(b)
		return ok && x == y
	}
	switch x := a.(type) {
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case undefinedValue:
		return isUndefined(b)
	case *big.Int:
		y, ok := b.(*big.Int)
		return ok && x != nil && y != nil && x.Cmp(y) == 0
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		if reflect.ValueOf(x).Pointer() == reflect.ValueOf(y).Pointer() {
			return true
		}
		for i, value := range x {
			if !deepEqual(value, y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		if _, ok := b.([]byte); ok {
			return deepEqual(b, a)
		}
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		if reflect.ValueOf(x).UnsafePointer() == reflect.ValueOf(y).UnsafePointer() {
			return true
		}
		for key, value := range x {
			other, found := y[key]
			if !found || !deepEqual(value, other) {
				return false
			}
		}
		return true
	case []byte:
		switch y := b.(type) {
		case []byte:
			return bytes.Equal(x, y)
		case map[string]any:
			// Byte slices compare with objects through their numeric property keys.
			if len(x) != len(y) {
				return false
			}
			for i, value := range x {
				other, found := y[strconv.Itoa(i)]
				if !found || !deepEqual(value, other) {
					return false
				}
			}
			return true
		}
	}
	return false
}
