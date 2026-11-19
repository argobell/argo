package formats

import "testing"

func TestRegex(t *testing.T) {
	checkFormat(t, Regex, []string{"", "^a+$", "(a+)\\1", "(?<name>ab)\\k<name>", "(?<=a)b", "\\p{Script=Greek}+", "^.$", "[a-z]{1,3}"}, []string{"(", "[", "a{3,2}", "\\", "\\141", "(?<bad", "*a"})
}
func TestRegexEngineSyntax(t *testing.T) {
	// The engine accepts these extensions to JavaScript's Unicode regex syntax.
	for _, pattern := range []string{`\a`, `(?#comment)a`, `(?i)abc`, `{v:0}`} {
		if !Regex(pattern) {
			t.Errorf("engine syntax %q unexpectedly rejected", pattern)
		}
	}
}
