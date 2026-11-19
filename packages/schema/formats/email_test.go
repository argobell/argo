package formats

import "testing"

func TestEmail(t *testing.T) {
	checkFormat(t, Email, []string{"a@b", "user+tag@example.com", "A@EXAMPLE.COM", "\"a b\"@example.com", "\"a\\\"b\"@example.com", "a@[127.0.0.1]", "a@[IPv6:::1]"}, []string{"", "a", "@example.com", "a@", "a..b@example.com", ".a@example.com", "a.@example.com", "a@-example.com", "a@example-.com", "a@example.com.", "é@example.com", "a@bücher.de", "a@[256.0.0.1]", "a@example.com\n"})
}
func TestEmailASCIICase(t *testing.T) {
	checkFormat(t, Email, []string{"A@EXAMPLE.COM", "\"K\"@example.com"}, []string{"a@K.com", "a@ſ.com", "K@example.com"})
}

func TestIDNEmail(t *testing.T) {
	checkFormat(t, IDNEmail, []string{"é@bücher.de", "e\u0301@bu\u0308cher.de", "用户@例子.广告", "a@example.com", "\"a b\"@example.com"}, []string{"", "a@-example.com", "a@example-.com", "a@a..b", "a@😀.com", "a@[127.0.0.1]", "a..b@example.com", "a@bücher.de\n"})
	if IDNEmail("e\u0301@bu\u0308cher.de") != IDNEmail("é@bücher.de") {
		t.Fatal("canonically equivalent addresses differ")
	}
}
