package formats

import "github.com/dlclark/regexp2/v2"

// Regex reports whether value compiles in ECMAScript Unicode mode. Syntax
// acceptance follows regexp2, as it does for schema pattern keywords.
func Regex(value string) bool {
	_, err := regexp2.Compile(value, regexp2.ECMAScript|regexp2.Unicode, regexp2.OptionMaxBacktrackingStackSize(-1))
	return err == nil
}
