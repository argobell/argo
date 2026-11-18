package schema

// Compile turns a schema into a Validator. Check is the fast boolean path;
// Errors explains failures, and CompileCoercing also enables conversion.

import (
	"fmt"
	"maps"
	"sync"
)

// Validator checks values against a compiled schema. Its methods may be called
// concurrently and from format callbacks. The schema and registered documents
// must remain unchanged after compilation; format checks must support the same
// concurrent and reentrant use as their caller.
type Validator struct {
	check     Check
	compiler  func() *compiler
	explainer func() *explainer
}

// Compile compiles a boolean or map[string]any schema. Pass nil options for the
// defaults. It returns an error for malformed schemas and, by default, unresolved
// references. RejectUnresolved makes unresolved references reject their values.
// Compilation reads the schema without modifying it. Option maps are copied;
// registered schema documents are retained.
func Compile(schema any, options *CompileOptions) (*Validator, error) {
	var config compilerOptions
	if options != nil {
		if options.Draft != "" && options.Draft != "2020-12" && options.Draft != "7" {
			return nil, fmt.Errorf("unsupported schema draft %q", options.Draft)
		}
		config = compilerOptions{
			draft:            options.Draft,
			formats:          maps.Clone(options.Formats),
			schemas:          maps.Clone(options.Schemas),
			rejectUnresolved: options.RejectUnresolved,
		}
	}
	c := newCompiler(schema, config)
	check, err := c.root()
	if err != nil {
		return nil, err
	}
	// Ordinary checks can release compilation caches. Explanations and conversion
	// share a compiler prepared only when one of them is first needed.
	prepareCompiler := sync.OnceValue(func() *compiler {
		c := newCompiler(schema, config)
		if _, err := c.root(); err != nil {
			panic(err)
		}
		return c
	})
	prepareExplainer := sync.OnceValue(func() *explainer {
		e, err := newExplainer(prepareCompiler(), schema)
		if err != nil {
			panic(err)
		}
		return e
	})
	return &Validator{check: check.check, compiler: prepareCompiler, explainer: prepareExplainer}, nil
}

// Check reports whether value is valid, without coercion. Its method value may
// be passed around as a Check independently of the original variable.
func (v *Validator) Check(value any) bool {
	return v.check(value)
}

// Errors returns one error per failing keyword, or an empty slice for a valid
// value. The explainer is built on the first failure and reused.
func (v *Validator) Errors(value any) []ValidationError {
	if v.check(value) {
		return []ValidationError{}
	}
	return v.explainer().errors(value)
}

// Validate checks value without coercion or modification. It returns nil for a
// valid value and ValidationErrors for an invalid value.
func (v *Validator) Validate(value any) error {
	failures := v.Errors(value)
	if len(failures) == 0 {
		return nil
	}
	return ValidationErrors(failures)
}

// CoercingValidator also converts loosely typed input towards the schema.
// Check, Errors, and Validate retain their behavior without coercion.
type CoercingValidator struct {
	*Validator
	coercer func() *coercer
}

// CompileCoercing compiles a schema with the same options as Compile and adds
// Coerce and Parse. Conversion rules are prepared on their first use.
func CompileCoercing(schema any, options *CompileOptions) (*CoercingValidator, error) {
	v, err := Compile(schema, options)
	if err != nil {
		return nil, err
	}
	return &CoercingValidator{
		Validator: v,
		coercer:   sync.OnceValue(func() *coercer { return newCoercer(v.compiler(), schema) }),
	}, nil
}

// Coerce converts numeric strings, booleans, arrays, optional nulls, and union
// members towards the schema. Only changed paths are copied; value is never
// modified and is returned itself when unchanged. Unconvertible values stay as
// they are for a subsequent check to report.
func (v *CoercingValidator) Coerce(value any) any {
	return v.coercer().coerce(value)
}

// Parse coerces value, then validates it. It returns the converted value on
// success, or nil and ValidationErrors on failure.
func (v *CoercingValidator) Parse(value any) (any, error) {
	value = v.Coerce(value)
	if err := v.Validate(value); err != nil {
		return nil, err
	}
	return value, nil
}
