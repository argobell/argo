package formats

import (
	"strings"
	"testing"
)

func TestPunycode(t *testing.T) {
	for _, label := range []string{"xn--bcher-kva", "XN--BCHER-KVA", "xn--maana-pta", "xn--r8jz45g", "xn--zckzah", "xn--e1afmkfd", "xn--mgbh0fb"} {
		if !Hostname(label) || !IDNHostname(label) {
			t.Errorf("valid Punycode label %q was rejected", label)
		}
	}
	for _, label := range []string{"xn--bücher-kva", "xn--a!", "xn--0", "xn--" + strings.Repeat("z", 1000), "xn--ib9b", "xn--abc-", "xn--", "xn---9uc"} {
		if Hostname(label) || IDNHostname(label) {
			t.Errorf("invalid Punycode label %q was accepted", label)
		}
	}
	if !IDNHostname(strings.Repeat("é", 57)) || IDNHostname(strings.Repeat("é", 58)) {
		t.Fatal("incorrect maximum A-label length")
	}
	// Raw Punycode conversion must leave the existing label rules in charge.
	for _, label := range []string{"a+b", "ا\u200cب"} {
		if !IDNHostname(label) {
			t.Errorf("raw Punycode conversion applied stricter IDNA rules to %q", label)
		}
	}
}
func TestBidi(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{{"abc", true}, {"א", true}, {"ا١", true}, {"ا۱", true}, {"ا1١", false}, {"1א", false}, {"אa", false}, {"aא", false}, {"", true}, {"a-b", true}} {
		if got := satisfiesBidi(tc.value); got != tc.want {
			t.Errorf("Bidi(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
	for _, tc := range []struct {
		value string
		want  bool
	}{{"abc", false}, {"א", true}, {"ا", true}, {"١", true}, {"۱", false}} {
		if got := hasRTL(tc.value); got != tc.want {
			t.Errorf("RTL(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
	for _, r := range []rune{0x94d, 0x11046, 0x11d45} {
		if !virama(r) {
			t.Errorf("virama U+%04X rejected", r)
		}
	}
	if virama('a') {
		t.Fatal("ordinary letter treated as a virama")
	}
}
