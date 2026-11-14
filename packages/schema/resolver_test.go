package schema

import "testing"

func TestDocumentOf(t *testing.T) {
	for _, tc := range []struct {
		uri  string
		want string
	}{
		{"", ""},
		{"https://example.com/root.json", "https://example.com/root.json"},
		{"https://example.com/root.json#", "https://example.com/root.json"},
		{"https://example.com/root.json#/$defs/value", "https://example.com/root.json"},
		{"https://example.com/root.json?name=%23#first#second", "https://example.com/root.json?name=%23"},
		{"urn:example:schema#anchor", "urn:example:schema"},
		{"relative/root.json#anchor", "relative/root.json"},
		{"#anchor", ""},
	} {
		if got := documentOf(tc.uri); got != tc.want {
			t.Errorf("documentOf(%q) = %q, want %q", tc.uri, got, tc.want)
		}
	}
}

func TestResolveURI(t *testing.T) {
	for _, tc := range []struct {
		name string
		ref  string
		base string
		want string
	}{
		{"sibling", "child.json", "https://example.com/schemas/root.json", "https://example.com/schemas/child.json"},
		{"parent", "../shared.json#/$defs/value", "https://example.com/schemas/root.json", "https://example.com/shared.json#/$defs/value"},
		{"root path", "/shared.json", "https://example.com/schemas/root.json", "https://example.com/shared.json"},
		{"dot segments", "./a/../child.json", "https://example.com/schemas/root.json", "https://example.com/schemas/child.json"},
		{"absolute", "https://other.example/root.json", "https://example.com/root.json", "https://other.example/root.json"},
		{"network path", "//other.example/root.json", "https://example.com/root.json", "https://other.example/root.json"},
		{"pointer", "#/$defs/value", "https://example.com/root.json#old", "https://example.com/root.json#/$defs/value"},
		{"anchor", "#name", "https://example.com/root.json", "https://example.com/root.json#name"},
		{"query", "?version=2", "https://example.com/root.json?version=1#old", "https://example.com/root.json?version=2"},
		{"empty query", "?", "https://example.com/root.json?old#anchor", "https://example.com/root.json?"},
		{"empty ref", "", "https://example.com/root.json?version=1#old", "https://example.com/root.json?version=1"},
		{"empty fragment", "#", "https://example.com/root.json#old", "https://example.com/root.json#"},
		{"empty fragment on path", "child.json#", "https://example.com/root.json", "https://example.com/child.json#"},
		{"fragment ending in hash", "#name#", "https://example.com/root.json", "https://example.com/root.json#name#"},
		{"literal and encoded fragment hashes", "#first%23second#third", "https://example.com/root.json", "https://example.com/root.json#first%23second#third"},
		{"encoded hash", "child%23name.json#value", "https://example.com/root.json", "https://example.com/child%23name.json#value"},
		{"unicode fragment", "#中文", "https://example.com/root.json", "https://example.com/root.json#%E4%B8%AD%E6%96%87"},
		{"urn fragment", "#/$defs/value", "urn:example:schema#old", "urn:example:schema#/$defs/value"},
		{"urn empty fragment", "#", "urn:example:schema#old", "urn:example:schema#"},
		{"urn relative", "child.json", "urn:example:schema", "child.json"},
		{"urn query", "?version=2", "urn:example:schema", "?version=2"},
		{"urn empty ref", "", "urn:example:schema", ""},
		{"urn absolute", "https://other.example/root.json", "urn:example:schema", "https://other.example/root.json"},
		{"hierarchical urn", "../child", "urn:/schemas/root", "urn:/child"},
		{"relative base fragment", "#x", "relative/root.json", "relative/root.json#x"},
		{"invalid base fragment", "#new", "https://[invalid", "https://[invalid#new"},
		{"invalid base relative", "child.json", "https://[invalid", "child.json"},
		{"invalid base absolute", "https://example.com/root.json", "invalid base", "https://example.com/root.json"},
		{"invalid ref", "http://[invalid", "https://example.com/root.json", "http://[invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveURI(tc.ref, tc.base); got != tc.want {
				t.Fatalf("resolveURI(%q, %q) = %q, want %q", tc.ref, tc.base, got, tc.want)
			}
		})
	}
}
