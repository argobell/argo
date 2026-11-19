package formats

import "testing"

func TestURL(t *testing.T) {
	checkFormat(t, URL, []string{"https://example.com/a", "https://例え.テスト/道", "http:example.com", "http:/example.com", "http:\\example.com", "https:///example.com", "file:///C:/x", "file:", "custom:", "mailto:a@example.com", "http://a/a b", "http://a/%GG", "http://a/#%GG", "http://a:65535", "http://0x7f.1", "http://127.1", "http://0xGG", "http://a%2eb", "custom://a%20b", "custom://a\x7fb", " http://a/\n", "http://[::1]"}, []string{"", "/a", "//a", "http://", "http://[bad]", "http://[127.0.0.1]", "http://[fe80::1%25eth0]", "custom://[fe80::1%25eth0]", "http://a b", "http://a:65536", "http://a:-1", "http://a:+1", "http://256.0.0.1", "http://foo.1", "http://a%20b", "http://a%ZZ", "file://a:80", "file://user@a/path", "custom://:80", "http://0x1.2.3.4.5"})
}
func TestURLNumericHosts(t *testing.T) {
	checkFormat(t, URL, []string{"http://0x", "http://0x.1", "http://0177.1", "http://4294967295"}, []string{"http://.1", "http://1..2", "http://08", "http://4294967296", "http://1.16777216", "http://1.2.65536", "http://1.2.3.256"})
}

func TestURLStandardLibraryBoundary(t *testing.T) {
	// Host IDNA mapping follows net/url rather than WHATWG's UTS #46 processing.
	// These hostnames are accepted here; WHATWG rejects them.
	for _, value := range []string{"http://a\u00a0b", "http://a\u200db"} {
		if !URL(value) || !IRI(value) || !IRIReference(value) {
			t.Errorf("standard-library host behavior changed for %q", value)
		}
	}
}
