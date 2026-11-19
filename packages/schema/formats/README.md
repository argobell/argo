# Optional format checks

Pass the built-in checks to schema compilation:

```go
import (
    "github.com/argobell/argo/packages/schema"
    "github.com/argobell/argo/packages/schema/formats"
)

validator, err := schema.Compile(document, &schema.CompileOptions{
    Formats: formats.All(),
})
```

`All()` returns a new map of all 21 supported formats. Callers can change it without affecting other validators. To enable selected formats, provide a map such as `map[string]func(string) bool{"email": formats.Email}`. Without registered checks, the validator treats `format` as an annotation.

The checks cover date, time, date-time, duration, email, idn-email, hostname, idn-hostname, ipv4, ipv6, uri, uri-reference, iri, iri-reference, uri-template, url, json-pointer, relative-json-pointer, json-pointer-uri-fragment, regex, and uuid. Checks may be used concurrently.

`Time` requires a timezone and validates leap seconds relative to UTC. Internationalized email and hostnames use NFC normalization. Hostnames use `golang.org/x/net/idna` for raw Punycode conversion while retaining the existing label, contextual-character, encoded-length, and Bidi rules. The package does not apply a stricter registration or lookup profile to these checks. IP checks use `net/netip`; IPv6 zone identifiers are rejected. UUID checks accept the hexadecimal shape without restricting the version or variant.

## Compatibility boundaries

`Regex` uses regexp2 in ECMAScript + Unicode mode, consistent with schema `pattern` checks. regexp2 accepts some constructs JavaScript rejects, including `\a`, global inline flags such as `(?i)`, comment groups, and some unescaped braces. A successful `Regex` check means the pattern compiles in this package's engine.

`URI` and `URIReference` use RFC 3986 matching rules. `URL`, `IRI`, and `IRIReference` use Go's `net/url` with handling for common special schemes, escaped hostnames, numeric IPv4 hosts, and relative references. They do not implement WHATWG's full UTS #46 host processing. For example, `http://a\u00a0b` and `http://a\u200db` (with the actual Unicode characters) are accepted here and rejected by WHATWG URL parsing. Use the dedicated hostname checks when hostname validation is required.
