package schema

import (
	"net/url"
	"strings"
)

// uri without its fragment.
func documentOf(uri string) string {
	document, _, _ := strings.Cut(uri, "#")
	return document
}

// ref resolved against base. URNs and other bases a URL parser rejects take
// fragment-only references.
func resolveURI(ref, base string) string {
	// Replace the base fragment rather than inheriting it.
	baseURL, baseErr := url.Parse(documentOf(base))
	refURL, refErr := url.Parse(ref)
	if baseErr != nil || !baseURL.IsAbs() || refErr != nil ||
		(baseURL.Opaque != "" && !refURL.IsAbs() && !strings.HasPrefix(ref, "#")) {
		if strings.HasPrefix(ref, "#") {
			return documentOf(base) + ref
		}
		return ref
	}
	resolved := baseURL.ResolveReference(refURL)
	if !refURL.IsAbs() && resolved.Host == "" {
		resolved.OmitHost = baseURL.OmitHost
	}
	_, fragment, found := strings.Cut(ref, "#")
	if !found {
		return resolved.String()
	}
	// Preserve the explicit # delimiters, escaping only the text between them.
	parts := strings.Split(fragment, "#")
	for i, part := range parts {
		u, _ := url.Parse("#" + part) // refURL already validated these escapes.
		parts[i] = u.EscapedFragment()
	}
	return documentOf(resolved.String()) + "#" + strings.Join(parts, "#")
}
