package schema

// A compiled boolean check.
type Check func(value any) bool

// One failure of a value: where it is (a JSON Pointer, "" for the value itself),
// the failing keyword and a message.
type ValidationError struct {
	Path    string `json:"path"`
	Keyword string `json:"keyword"`
	Message string `json:"message"`
}

// Undefined represents an undefined value, distinct from JSON null (nil).
var Undefined = undefinedValue{}

type undefinedValue struct{}
