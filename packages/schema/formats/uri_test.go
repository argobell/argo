package formats

import (
	"strings"
	"testing"
)

func TestURI(t *testing.T) {
	checkFormat(t, URI, []string{"https://example.com/a?b#c", "HTTP://user:pass@example.com:80/a", "urn:isbn:0451450523", "mailto:a@example.com", "custom:", "http://[::1]/", "http://[v1.a]/", "http://", "file:///a", "custom:/a", "custom:a/b", "https://a/%20"}, []string{"", "/a", "//example.com", "1scheme:a", "https://例え.テスト/道", "https://example.com/a b", "https://example.com/%ZZ", "http://[bad]", "https://a/\\b"})
}
func TestURIASCIICase(t *testing.T) {
	for _, check := range []func(string) bool{URI, URIReference} {
		checkFormat(t, check, []string{"HTTP://EXAMPLE.COM/%AB"}, []string{"http://K.com", "http://a/ſ"})
	}
}

func TestURIReference(t *testing.T) {
	checkFormat(t, URIReference, []string{"", "/a", "../a", "//example.com/a", "?query", "#fragment", "a/b", "https://example.com/a", "//[v1.a]/"}, []string{"a b", "a%ZZ", "1:a", "//[bad]", "/道", "a\\b"})
}
func TestIRI(t *testing.T) {
	checkFormat(t, IRI, []string{"https://例え.テスト/道", "https://bücher.de/a", "urn:example:道", "http://[v1.a]/", "http://0xGG", "http://a%2eb/"}, []string{"", "道", "https://example.com/a b", "https://example.com/%GG", "https://example.com/\\b", "https://example.com/<b>", "http://[bad]", "http://256.1.1.1", "http://[v1.a]/" + strings.Repeat("a", 2048)})
}
func TestIRIReference(t *testing.T) {
	checkFormat(t, IRIReference, []string{"", "道/路", "/道", "?名=值", "#片段", "00:00:00", "../a", "https://例え.テスト/道", "[::1]"}, []string{"a b", "a\x7fb", "a\\b", "a%GG", "httpx//example.com", "http://[bad]", "http://[v1.a]/"})
}
