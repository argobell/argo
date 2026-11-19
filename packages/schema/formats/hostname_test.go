package formats

import (
	"strings"
	"testing"
)

func BenchmarkHostname(b *testing.B) {
	for _, tc := range []struct {
		name, value string
		check       func(string) bool
	}{
		{"ASCII", "example.com", Hostname},
		{"Punycode", "xn--bcher-kva.example", Hostname},
		{"Unicode", "bücher.example", IDNHostname},
		{"long Unicode", strings.Repeat("é", 57), IDNHostname},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				tc.check(tc.value)
			}
		})
	}
}

func TestHostname(t *testing.T) {
	checkFormat(t, Hostname, []string{"localhost", "example.com", "EXAMPLE.COM", "a1-b2.example", "1.2.3.4", "xn--bcher-kva.example", strings.Repeat("a", 63) + ".com", strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)}, []string{"", "example.com.", "a..b", "-a", "a-", "ab--cd", "a_b", "a+b", "bücher.de", "xn--", "xn--a", "xn---9uc", "xn--abc-", "xn--0", "a\n", strings.Repeat("a", 64) + ".com", strings.Repeat("a.", 127) + "a"})
}
func TestIDNHostname(t *testing.T) {
	checkFormat(t, IDNHostname, []string{"bücher.de", "bu\u0308cher.de", "日本語.jp", "مثال.إختبار", "xn--bcher-kva.example", "ａｂｃ。ｅｘａｍｐｌｅ", "a\u00adb.example", "a\u200bb.example", "l·l.cat", "カ・ナ.jp", "日・本.jp", "क्‍ष.example", "ا‌ب.example", "א׳.example", "͵α.example", "ß.de", strings.Repeat("é", 57)}, []string{"", "a b", "example.com.", "a..b", "a😀.com", "-a", "a-", "ab--cd", "\u0301a", "a·a", "L·L", "͵a", "a׳", "a‍b", "a‌b", "a・b", "ا1١", "אa", "aא", "١", "\u0640", "\u07fa", strings.Repeat("é", 58), strings.Repeat("a", 64), strings.Repeat("a.", 127) + "a"})
}
