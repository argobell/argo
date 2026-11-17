package schema

import (
	"reflect"
	"slices"
	"testing"
)

func TestAnnotationsMerge(t *testing.T) {
	a := annotations{items: 2}
	a.addProp("a")
	a.addIndex(4)
	b := annotations{allProps: true, allItems: true, items: 1}
	b.addProp("b")
	b.addIndex(3)
	a.merge(&b)
	if !a.allProps || !a.allItems || a.items != 2 || len(a.props) != 2 || len(a.indexes) != 2 {
		t.Fatalf("annotations were not combined: %+v", a)
	}
	// Merging must not alias the source sets: later branch evaluations are independent.
	b.addProp("c")
	b.addIndex(5)
	if len(a.props) != 2 || len(a.indexes) != 2 {
		t.Fatal("merged annotations share mutable sets")
	}
	b.items = 6
	a.merge(&b)
	if a.items != 6 {
		t.Fatal("the longest evaluated prefix must win")
	}
}

func TestAnnotationsBranchIsolation(t *testing.T) {
	failed := func(_ any, a *annotations, _ *evaluationScope) bool {
		a.addProp("failed")
		a.addIndex(3)
		a.allProps, a.allItems = true, true
		return false
	}
	passed := func(_ any, a *annotations, _ *evaluationScope) bool { a.addProp("passed"); return true }
	var a annotations
	if !annotateAny([]annotatingCheck{failed, passed}, false)(nil, &a, nil) || len(a.props) != 1 || a.allProps || a.allItems || len(a.indexes) != 0 {
		t.Fatalf("a failed alternative leaked annotations: %+v", a)
	}
	before := annotations{props: map[string]struct{}{"passed": {}}}
	if isolateAnnotations(failed)(nil, &a, nil) || !reflect.DeepEqual(a, before) {
		t.Fatal("an isolated failed check changed its parent annotations")
	}
	if !annotateIf(failed, nil, passed)(nil, &a, nil) || !reflect.DeepEqual(a, before) {
		t.Fatal("a failed condition contributed annotations")
	}
}

func TestScopedAnnotating(t *testing.T) {
	check := scopedAnnotating("inner", func(_ any, _ *annotations, scope *evaluationScope) bool {
		if !slices.Equal(scope.resources, []string{"outer", "inner"}) {
			t.Fatalf("unexpected scope: %v", scope.resources)
		}
		return false
	})
	scope := &evaluationScope{resources: []string{"outer"}}
	if check(nil, new(annotations), scope) || !slices.Equal(scope.resources, []string{"outer"}) {
		t.Fatal("a rejected check did not restore the scope")
	}
	panicCheck := scopedAnnotating("inner", func(any, *annotations, *evaluationScope) bool { panic("stop") })
	func() {
		defer func() {
			if recover() != "stop" {
				t.Fatal("expected the check's panic")
			}
		}()
		panicCheck(nil, new(annotations), scope)
	}()
	if !slices.Equal(scope.resources, []string{"outer"}) {
		t.Fatal("a panicking check did not restore the scope")
	}
	fresh := scopedAnnotating("root", func(_ any, _ *annotations, scope *evaluationScope) bool {
		return slices.Equal(scope.resources, []string{"root"})
	})
	for range 2 {
		if !fresh(nil, new(annotations), nil) {
			t.Fatal("evaluations must start with independent scopes")
		}
	}
}
