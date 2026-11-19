package formats

import (
	"strings"
	"unicode"

	"golang.org/x/net/idna"
)

func bidiClass(r rune) byte {
	switch {
	case r >= '0' && r <= '9' || r >= 0x6f0 && r <= 0x6f9:
		return 'E'
	case r >= 0x660 && r <= 0x669:
		return 'A'
	case unicode.Is(unicode.Mn, r):
		return 'N'
	case unicode.Is(unicode.Hebrew, r):
		return 'R'
	case unicode.In(r, unicode.Arabic, unicode.Syriac, unicode.Thaana, unicode.Mandaic):
		return 'B'
	case unicode.IsLetter(r):
		return 'L'
	default:
		return 'O'
	}
}

func hasRTL(value string) bool {
	for _, r := range value {
		switch bidiClass(r) {
		case 'R', 'B', 'A':
			return true
		}
	}
	return false
}

// The direction rule applies to every label when the hostname contains RTL text.
func satisfiesBidi(value string) bool {
	rtl, first, en, an := false, true, false, false
	for _, r := range value {
		class := bidiClass(r)
		if first {
			if class != 'L' && class != 'R' && class != 'B' {
				return false
			}
			rtl = class == 'R' || class == 'B'
			first = false
		}
		if rtl {
			if class == 'L' {
				return false
			}
		} else if class == 'R' || class == 'B' || class == 'A' {
			return false
		}
		en = en || class == 'E'
		an = an || class == 'A'
	}
	return !(rtl && en && an)
}

func unicodeLabel(value string) bool {
	chars := []rune(value)
	if len(chars) == 0 || chars[0] == '-' || chars[len(chars)-1] == '-' || len(chars) >= 4 && chars[2] == '-' && chars[3] == '-' {
		return false
	}
	if unicode.IsMark(chars[0]) {
		return false
	}
	nonASCII := false
	for _, r := range chars {
		if r >= 128 {
			nonASCII = true
			break
		}
	}
	if nonASCII {
		encoded, err := idna.Punycode.ToASCII(value)
		if err != nil || len(encoded) > 63 {
			return false
		}
	}
	if hasRTL(value) && !satisfiesBidi(value) {
		return false
	}
	japanese, middleDot := false, false
	for i, r := range chars {
		switch r {
		case 0x640, 0x7fa, 0x302e, 0x302f, 0x3031, 0x3032, 0x3033, 0x3034, 0x3035, 0x303b:
			return false
		}
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.In(r, unicode.Mn, unicode.Mc) || strings.ContainsRune("-+.,:/\u00b7\u0375\u05f3\u05f4\u200c\u200d\u30fb\u00df\u03c2\u06fd\u06fe\u0f0b\u3007", r)) {
			return false
		}
		japanese = japanese || unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han)
		switch r {
		case 0xb7:
			if i == 0 || i+1 == len(chars) || chars[i-1] != 'l' || chars[i+1] != 'l' {
				return false
			}
		case 0x375:
			if i+1 == len(chars) || !unicode.Is(unicode.Greek, chars[i+1]) {
				return false
			}
		case 0x5f3, 0x5f4:
			if i == 0 || !unicode.Is(unicode.Hebrew, chars[i-1]) {
				return false
			}
		case 0x200c:
			if i == 0 || chars[i-1] < 128 && !virama(chars[i-1]) {
				return false
			}
		case 0x200d:
			if i == 0 || !virama(chars[i-1]) {
				return false
			}
		case 0x30fb:
			middleDot = true
		}
	}
	return !middleDot || japanese
}

func virama(r rune) bool {
	switch r {
	case 0x094d, 0x09cd, 0x0a4d, 0x0acd, 0x0b4d, 0x0bcd, 0x0c4d, 0x0ccd, 0x0d3b, 0x0d3c, 0x0d4d, 0x0dca, 0x1b44, 0x1baa, 0x1bab, 0xa9c0, 0x11046, 0x1107f, 0x110b9, 0x11133, 0x11134, 0x111c0, 0x11235, 0x1134d, 0x11442, 0x114c2, 0x115bf, 0x1163f, 0x116b6, 0x11c3f, 0x11d44, 0x11d45:
		return true
	default:
		return false
	}
}
