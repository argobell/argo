package schema

import "strings"

// CompileOptions controls schema compilation. The zero value uses draft 2020-12,
// treats formats as annotations, and reports unresolved references as errors.
type CompileOptions struct {
	// Draft is "2020-12" (default) or "7".
	Draft string
	// Formats maps format names to checks. Unknown formats remain annotations.
	Formats map[string]func(string) bool
	// Schemas registers schema documents by URI for references to other documents.
	Schemas map[string]any
	// RejectUnresolved compiles unresolved references as false schemas instead
	// of returning a compilation error. Only values at those references fail.
	RejectUnresolved bool
}

// A compiled boolean check.
type Check func(value any) bool

// One failure of a value: where it is (a JSON Pointer, "" for the value itself),
// the failing keyword and a message.
type ValidationError struct {
	Path    string `json:"path"`
	Keyword string `json:"keyword"`
	Message string `json:"message"`
}

// ValidationErrors is the list of failures returned by Validate and Parse.
// Use errors.As to retrieve the individual paths, keywords, and messages.
type ValidationErrors []ValidationError

func (errors ValidationErrors) Error() string {
	var message strings.Builder
	for i, failure := range errors {
		if i > 0 {
			message.WriteString("; ")
		}
		if failure.Path != "" {
			message.WriteString(failure.Path)
			message.WriteString(": ")
		}
		message.WriteString(failure.Message)
		message.WriteString(" (")
		message.WriteString(failure.Keyword)
		message.WriteByte(')')
	}
	return message.String()
}

// Undefined represents an undefined value, distinct from JSON null (nil).
var Undefined = undefinedValue{}

type undefinedValue struct{}
