package formats

import "golang.org/x/text/unicode/norm"

var emailPattern = mustPattern("^(?:[a-zA-Z0-9!#$%&'*+/=?^_`{|}~-]+(?:\\.[a-zA-Z0-9!#$%&'*+/=?^_`{|}~-]+)*|\"(?:[^\"\\\\]|\\\\[\\x20-\\x7e])*\")@(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*|\\[(?:[iI][pP][vV]6:[a-fA-F0-9:]+|(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])(?:\\.(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3})\\])$")

// Email reports whether value is an email address.
func Email(value string) bool { return emailPattern.MatchString(value) }

var iDNEmailPattern = mustPattern("^(?:[A-Za-zA-Z0-9!#$%&'*+/=?^_`{|}~\\u{0080}-\\u{10FFFF}-]+(?:\\.[A-Za-zA-Z0-9!#$%&'*+/=?^_`{|}~\\u{0080}-\\u{10FFFF}-]+)*|\"(?:[^\"\\\\]|\\\\.)*\")@[\\p{L}\\p{N}](?:[\\p{L}\\p{N}-]{0,62})(?<!-)(?:\\.[\\p{L}\\p{N}](?:[\\p{L}\\p{N}-]{0,62})(?<!-))*$")

// IDNEmail reports whether value is an internationalized email address after NFC normalization.
func IDNEmail(value string) bool { return iDNEmailPattern.MatchString(norm.NFC.String(value)) }
