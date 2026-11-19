// Package formats provides optional JSON Schema format checks. Pass All() or a
// selection of the exported functions to schema.CompileOptions.Formats.
package formats

import (
	"github.com/dlclark/regexp2/v2"
	"github.com/dlclark/regexp2/v2/compat"
)

// All returns a new map containing all 21 built-in formats. The caller may add,
// replace, or remove entries without affecting another caller.
func All() map[string]func(string) bool {
	return map[string]func(string) bool{
		"date": Date, "time": Time, "date-time": DateTime, "duration": Duration,
		"email": Email, "idn-email": IDNEmail, "hostname": Hostname, "idn-hostname": IDNHostname,
		"ipv4": IPv4, "ipv6": IPv6, "uri": URI, "uri-reference": URIReference,
		"iri": IRI, "iri-reference": IRIReference, "url": URL, "uri-template": URITemplate,
		"json-pointer": JSONPointer, "relative-json-pointer": RelativeJSONPointer,
		"json-pointer-uri-fragment": JSONPointerURIFragment, "regex": Regex, "uuid": UUID,
	}
}

// Patterns are compiled once and shared by concurrent checks.
func mustPattern(pattern string) *compat.Regexp {
	return compat.MustCompile(pattern, regexp2.ECMAScript|regexp2.Unicode, regexp2.OptionMaxBacktrackingStackSize(-1))
}
