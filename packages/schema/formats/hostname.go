package formats

import (
	"strings"
	"unicode/utf16"

	"golang.org/x/net/idna"
	"golang.org/x/text/unicode/norm"
)

// Hostname reports whether value is an ASCII hostname with LDH or valid
// Punycode labels. A trailing dot is rejected.
func Hostname(value string) bool {
	if len(value) == 0 || len(value) > 253 || strings.HasSuffix(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if punyLabel(label) {
			continue
		}
		if label[0] == '-' || label[len(label)-1] == '-' || len(label) >= 4 && label[2:4] == "--" {
			return false
		}
		for i := range len(label) {
			b := label[i]
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-') {
				return false
			}
		}
	}
	return true
}

// IDNHostname reports whether value is an internationalized hostname after
// fullwidth mapping, NFC normalization, ignored-character removal, and dot mapping.
func IDNHostname(value string) bool {
	if value == "" || strings.Contains(value, " ") {
		return false
	}
	value = strings.Map(func(r rune) rune {
		if r >= 0xff01 && r <= 0xff5e {
			return r - 0xfee0
		}
		return r
	}, value)
	value = norm.NFC.String(value)
	value = strings.Map(func(r rune) rune {
		switch {
		case r == 0xad || r == 0x34f || r >= 0x180b && r <= 0x180d || r == 0x200b || r >= 0xfe00 && r <= 0xfe0f || r >= 0xe0100 && r <= 0xe01ef:
			return -1
		case r == 0x3002 || r == 0xff0e || r == 0xff61:
			return '.'
		}
		return r
	}, value)
	if utf16Length(value) > 253 {
		return false
	}
	labels := strings.Split(value, ".")
	bidi := false
	for _, label := range labels {
		text := label
		if acePrefixed(label) {
			if decoded, err := idna.Punycode.ToUnicode("xn--" + strings.ToLower(label[4:])); err == nil {
				text = decoded
			} else {
				continue
			}
		}
		if hasRTL(text) {
			bidi = true
			break
		}
	}
	for _, label := range labels {
		n := utf16Length(label)
		if n == 0 || n > 63 || !(punyLabel(label) || unicodeLabel(label)) || bidi && !satisfiesBidi(label) {
			return false
		}
	}
	return true
}

func utf16Length(value string) int {
	n := 0
	for _, r := range value {
		n += utf16.RuneLen(r)
	}
	return n
}

func acePrefixed(value string) bool {
	return len(value) >= 4 && (value[0] == 'x' || value[0] == 'X') && (value[1] == 'n' || value[1] == 'N') && value[2:4] == "--"
}

func punyLabel(value string) bool {
	if !acePrefixed(value) {
		return false
	}
	body := strings.ToLower(value[4:])
	if strings.LastIndexByte(body, '-') == 0 {
		return false
	}
	// Punycode payloads contain only ASCII; the raw profile also accepts
	// non-ASCII basic characters before its delimiter.
	for i := range len(body) {
		if body[i] >= 128 {
			return false
		}
	}
	decoded, err := idna.Punycode.ToUnicode("xn--" + body)
	if err != nil {
		return false
	}
	nonASCII := false
	for _, r := range decoded {
		if r >= 128 {
			nonASCII = true
			break
		}
	}
	return nonASCII && unicodeLabel(decoded)
}
