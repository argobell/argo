package schema

// A compiled boolean check.
type Check func(value any) bool

// Undefined represents an undefined value, distinct from JSON null (nil).
var Undefined = undefinedValue{}

type undefinedValue struct{}
