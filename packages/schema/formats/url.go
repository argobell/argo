package formats

import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

var ipvFuturePattern = mustPattern(`\[[vV][0-9a-fA-F]+\.[^\]]+\]`)
var malformedIRIScheme = mustPattern(`^[a-zA-Z][a-zA-Z0-9+\-.]*//`)

// URL reports whether value is an absolute URL. It uses net/url with the common
// special-scheme parsing rules; acceptance can differ from WHATWG URL parsing.
func URL(value string) bool { return parseURL(value, false) }

// IRI reports whether value is an absolute internationalized resource identifier.
// IPvFuture literals in inputs shorter than 2048 UTF-16 units are narrowed to
// an IPv6 loopback address before URL parsing.
func IRI(value string) bool {
	for _, r := range value {
		if r <= 0x20 || strings.ContainsRune("<>^`{|}\\", r) {
			return false
		}
	}
	if !validPercent(value) {
		return false
	}
	if utf16Length(value) < 2048 {
		if indexes := ipvFuturePattern.FindStringIndex(value); indexes != nil {
			value = value[:indexes[0]] + "[::1]" + value[indexes[1]:]
		}
	}
	return parseURL(value, false)
}

// IRIReference reports whether value is an IRI reference resolved against an
// HTTP base URL. Raw whitespace, controls, backslashes, and malformed escapes
// are rejected before parsing.
func IRIReference(value string) bool {
	for _, r := range value {
		if r <= 0x20 || r == 0x7f || r == '\\' {
			return false
		}
	}
	return validPercent(value) && !malformedIRIScheme.MatchString(value) && parseURL(value, true)
}

func validPercent(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] != '%' {
			continue
		}
		if i+2 >= len(value) {
			return false
		}
		for _, b := range []byte{value[i+1], value[i+2]} {
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F') {
				return false
			}
		}
		i += 2
	}
	return true
}

func parseURL(value string, relative bool) bool {
	// WHATWG preprocessing removes ASCII tabs/newlines and trims C0 plus spaces.
	value = strings.Trim(value, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f ")
	value = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(value)
	scheme := ""
	if i := strings.IndexByte(value, ':'); i > 0 {
		candidate := value[:i]
		valid := true
		for j := range len(candidate) {
			b := candidate[j]
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || j > 0 && (b >= '0' && b <= '9' || b == '+' || b == '-' || b == '.')) {
				valid = false
				break
			}
		}
		if valid {
			scheme = strings.ToLower(candidate)
		}
	}
	special := scheme == "http" || scheme == "https" || scheme == "ftp" || scheme == "ws" || scheme == "wss" || scheme == "file" || scheme == "" && relative
	if scheme == "" && !relative {
		return false
	}
	if special {
		value = strings.ReplaceAll(value, "\\", "/")
		if scheme != "" && scheme != "file" {
			i := strings.IndexByte(value, ':')
			value = value[:i+1] + "//" + strings.TrimLeft(value[i+1:], "/")
		}
	}
	if scheme == "" && relative && !strings.HasPrefix(value, "//") {
		value = "http://example.com/" + strings.TrimLeft(value, "/")
	}
	// Decode special hosts before net/url parses them. Opaque hosts are checked
	// separately because net/url rejects percent-encoded ASCII in hostnames.
	if start := strings.Index(value, "//"); start >= 0 && (start == 0 || start == len(scheme)+1 || scheme == "" && relative) {
		end := len(value)
		if i := strings.IndexAny(value[start+2:], "/?#"); i >= 0 {
			end = start + 2 + i
		}
		authority := value[start+2 : end]
		hostStart := strings.LastIndexByte(authority, '@') + 1
		hostPort := authority[hostStart:]
		if hostPort != "" && hostPort[0] != '[' {
			hostEnd := len(hostPort)
			if i := strings.LastIndexByte(hostPort, ':'); i >= 0 {
				hostEnd = i
			}
			host := hostPort[:hostEnd]
			if special {
				decoded, err := url.PathUnescape(host)
				if err != nil {
					return false
				}
				for _, r := range decoded {
					if r <= 0x20 || r == 0x7f || strings.ContainsRune("#%/:<>?@[\\\\]^|", r) {
						return false
					}
				}
				authority = authority[:hostStart] + decoded + hostPort[hostEnd:]
			} else {
				for _, r := range host {
					if r <= 0x20 || strings.ContainsRune("#/:<>?@[\\\\]^|", r) {
						return false
					}
				}
				if host == "" && hostPort != "" {
					return false
				}
				authority = authority[:hostStart] + "example.com" + hostPort[hostEnd:]
			}
			value = value[:start+2] + authority + value[end:]
		}
	}
	// Percent escapes in paths and queries need not be well formed for URL.
	// Replacing them here avoids net/url rejecting otherwise parseable URLs.
	pathStart := 0
	if i := strings.Index(value, "//"); i >= 0 && (i == 0 || i == len(scheme)+1) {
		pathStart = len(value)
		if j := strings.IndexAny(value[i+2:], "/?#"); j >= 0 {
			pathStart = i + 2 + j
		}
	}
	value = value[:pathStart] + strings.ReplaceAll(value[pathStart:], "%", "%25")
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if !relative && parsed.Scheme == "" {
		return false
	}
	if scheme == "file" && parsed.User != nil {
		return false
	}
	if parsed.Host == "" {
		if special && scheme != "file" && (scheme != "" || strings.HasPrefix(value, "//")) {
			return false
		}
		return true
	}
	host := parsed.Hostname()
	if strings.Contains(host, ":") {
		if strings.Contains(host, "%") {
			return false
		}
		addr, err := netip.ParseAddr(host)
		if err != nil || !addr.Is6() {
			return false
		}
	}
	if special {
		if host == "" {
			return false
		}
		if !strings.Contains(host, ":") {
			for _, r := range host {
				if r <= 0x20 || strings.ContainsRune("#%/:<>?@[\\]^|", r) {
					return false
				}
			}
			labels := strings.Split(strings.TrimSuffix(host, "."), ".")
			last := labels[len(labels)-1]
			numeric := last != ""
			for i := range len(last) {
				if last[i] < '0' || last[i] > '9' {
					numeric = false
					break
				}
			}
			if len(last) >= 2 && strings.EqualFold(last[:2], "0x") {
				_, err := strconv.ParseUint(last[2:], 16, 32)
				numeric = last == "0x" || last == "0X" || err == nil
			}
			if numeric && !urlIPv4(labels) {
				return false
			}
		}
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n > 65535 {
			return false
		}
		if scheme == "file" {
			return false
		}
	}
	return true
}

// URL hosts accept one to four numeric components and hexadecimal/octal forms.
func urlIPv4(parts []string) bool {
	if len(parts) > 4 {
		return false
	}
	for i, part := range parts {
		if part == "" {
			return false
		}
		base := 10
		if len(part) >= 2 && strings.EqualFold(part[:2], "0x") {
			base = 16
			part = part[2:]
		} else if len(part) > 1 && part[0] == '0' {
			base = 8
			part = part[1:]
		}
		var n uint64
		if part != "" {
			var err error
			n, err = strconv.ParseUint(part, base, 32)
			if err != nil {
				return false
			}
		}
		if i < len(parts)-1 && n > 255 || i == len(parts)-1 && n >= uint64(1)<<uint(8*(5-len(parts))) {
			return false
		}
	}
	return true
}
