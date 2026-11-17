package schema

// Annotation tracking for unevaluatedProperties and unevaluatedItems: annotating
// checks record which properties and items a schema evaluated. Only schemas that
// use those keywords compile into these closures.

// What a schema evaluated: property names (or all of them), the evaluated item
// prefix, and items matched by contains. The zero value has no annotations.
type annotations struct {
	props    map[string]struct{}
	allProps bool
	items    int
	allItems bool
	indexes  map[int]struct{}
}

// A check that records what it evaluated into a when it passes.
type annotatingCheck func(any, *annotations, *evaluationScope) bool

func (a *annotations) addProp(key string) {
	if a.props == nil {
		a.props = make(map[string]struct{})
	}
	a.props[key] = struct{}{}
}

func (a *annotations) addIndex(index int) {
	if a.indexes == nil {
		a.indexes = make(map[int]struct{})
	}
	a.indexes[index] = struct{}{}
}

func (a *annotations) merge(b *annotations) {
	for key := range b.props {
		a.addProp(key)
	}
	a.allProps = a.allProps || b.allProps
	if b.items > a.items {
		a.items = b.items
	}
	a.allItems = a.allItems || b.allItems
	for index := range b.indexes {
		a.addIndex(index)
	}
}

// The indirection a recursive reference goes through.
func indirectAnnotating(slot *annotatingCheck) annotatingCheck {
	return func(v any, a *annotations, scope *evaluationScope) bool { return (*slot)(v, a, scope) }
}

// A plain check in annotating position: it evaluates nothing that unevaluated* sees.
func ignoreAnnotations(check compiledCheck) annotatingCheck {
	return func(v any, _ *annotations, scope *evaluationScope) bool { return check.test(v, scope) }
}

// An annotating check run as a plain check, with fresh annotations.
func withFreshAnnotations(check annotatingCheck) compiledCheck {
	return compiledCheck{run: func(v any, scope *evaluationScope) bool {
		return check(v, new(annotations), scope)
	}}
}

// check with uri pushed onto the dynamic scope while it runs.
func scopedAnnotating(uri string, check annotatingCheck) annotatingCheck {
	return func(v any, a *annotations, scope *evaluationScope) bool {
		if scope == nil {
			scope = &evaluationScope{}
		}
		length := len(scope.resources)
		scope.resources = append(scope.resources, uri)
		defer func() { scope.resources = scope.resources[:length] }()
		return check(v, a, scope)
	}
}

// unevaluated* sees only the annotations of its own schema, never those of its
// siblings in the parent.
func isolateAnnotations(check annotatingCheck) annotatingCheck {
	return func(v any, a *annotations, scope *evaluationScope) bool {
		var b annotations
		if !check(v, &b, scope) {
			return false
		}
		a.merge(&b)
		return true
	}
}

// The non-annotating keywords, then each annotating part in order.
func annotateAll(plain compiledCheck, parts []annotatingCheck) annotatingCheck {
	return func(v any, a *annotations, scope *evaluationScope) bool {
		if !plain.test(v, scope) {
			return false
		}
		for _, part := range parts {
			if !part(v, a, scope) {
				return false
			}
		}
		return true
	}
}

// properties, patternProperties and additionalProperties.
func annotateObject(props []propertyEntry, patterns []patternEntry, additional compiledCheck) annotatingCheck {
	declared := make(map[string]struct{}, len(props))
	for _, prop := range props {
		declared[prop.key] = struct{}{}
	}
	return func(v any, a *annotations, scope *evaluationScope) bool {
		object, ok := v.(map[string]any)
		if !ok || object == nil {
			return true
		}
		var seen []string
		for _, prop := range props {
			value, found := object[prop.key]
			if !found {
				continue
			}
			if !prop.check.test(value, scope) {
				return false
			}
			seen = append(seen, prop.key)
		}
		for key, value := range object {
			_, matched := declared[key]
			for _, pattern := range patterns {
				if pattern.pattern.MatchString(key) {
					if !pattern.check.test(value, scope) {
						return false
					}
					matched = true
					seen = append(seen, key)
				}
			}
			if !matched && additional.run != nil {
				if !additional.test(value, scope) {
					return false
				}
				seen = append(seen, key)
			}
		}
		for _, key := range seen {
			a.addProp(key)
		}
		return true
	}
}

// prefixItems/items/additionalItems and contains with its bounds.
func annotateArray(prefix []compiledCheck, rest, contains compiledCheck, minContains, maxContains float64) annotatingCheck {
	return func(v any, a *annotations, scope *evaluationScope) bool {
		array, ok := v.([]any)
		if !ok {
			return true
		}
		m := min(len(array), len(prefix))
		for i := range m {
			if !prefix[i].test(array[i], scope) {
				return false
			}
		}
		if rest.run != nil {
			for i := len(prefix); i < len(array); i++ {
				if !rest.test(array[i], scope) {
					return false
				}
			}
			a.allItems = true
		}
		if m > a.items {
			a.items = m
		}
		if contains.run != nil {
			n := 0.0
			for i, value := range array {
				if contains.test(value, scope) {
					n++
					a.addIndex(i)
				}
			}
			if n < minContains || n > maxContains {
				return false
			}
		}
		return true
	}
}

// anyOf (or oneOf when exactlyOne): every passing branch contributes its annotations.
func annotateAny(branches []annotatingCheck, exactlyOne bool) annotatingCheck {
	return func(v any, a *annotations, scope *evaluationScope) bool {
		n := 0
		for _, branch := range branches {
			var b annotations
			if branch(v, &b, scope) {
				n++
				a.merge(&b)
			}
		}
		if exactlyOne {
			return n == 1
		}
		return n > 0
	}
}

// if, then and else: a passing if contributes its annotations even without then.
func annotateIf(condition, then, otherwise annotatingCheck) annotatingCheck {
	return func(v any, a *annotations, scope *evaluationScope) bool {
		var b annotations
		if condition(v, &b, scope) {
			a.merge(&b)
			return then == nil || then(v, a, scope)
		}
		return otherwise == nil || otherwise(v, a, scope)
	}
}

// One dependentSchemas entry.
func annotateDependent(key string, check annotatingCheck) annotatingCheck {
	return func(v any, a *annotations, scope *evaluationScope) bool {
		object, ok := v.(map[string]any)
		if !ok {
			return true
		}
		_, found := object[key]
		return !found || check(v, a, scope)
	}
}

// unevaluatedProperties: every property no other keyword evaluated passes check.
func unevaluatedProperties(check compiledCheck) annotatingCheck {
	return func(v any, a *annotations, scope *evaluationScope) bool {
		object, ok := v.(map[string]any)
		if !ok || a.allProps {
			return true
		}
		for key, value := range object {
			if _, found := a.props[key]; !found && !check.test(value, scope) {
				return false
			}
		}
		a.allProps = true
		return true
	}
}

// unevaluatedItems: every item no other keyword evaluated passes check.
func unevaluatedItems(check compiledCheck) annotatingCheck {
	return func(v any, a *annotations, scope *evaluationScope) bool {
		array, ok := v.([]any)
		if !ok || a.allItems {
			return true
		}
		for i := a.items; i < len(array); i++ {
			if _, found := a.indexes[i]; !found && !check.test(array[i], scope) {
				return false
			}
		}
		a.allItems = true
		return true
	}
}
