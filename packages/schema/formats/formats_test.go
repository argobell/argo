package formats

import (
	"sync"
	"testing"
)

func checkFormat(t *testing.T, check func(string) bool, good, bad []string) {
	t.Helper()
	for _, value := range good {
		if !check(value) {
			t.Errorf("valid value %q was rejected", value)
		}
	}
	for _, value := range bad {
		if check(value) {
			t.Errorf("invalid value %q was accepted", value)
		}
	}
}

func TestAll(t *testing.T) {
	names := []string{"date", "date-time", "duration", "email", "hostname", "idn-email", "idn-hostname", "ipv4", "ipv6", "iri", "iri-reference", "json-pointer", "json-pointer-uri-fragment", "regex", "relative-json-pointer", "time", "uri", "uri-reference", "uri-template", "url", "uuid"}
	checks := All()
	if len(checks) != len(names) {
		t.Fatalf("registered %d formats, want %d", len(checks), len(names))
	}
	for _, name := range names {
		if checks[name] == nil {
			t.Errorf("missing check for %s", name)
		}
	}
	checks["custom"] = func(string) bool { return true }
	checks["date"] = nil
	delete(checks, "time")
	again := All()
	if again["date"] == nil || again["time"] == nil || again["custom"] != nil {
		t.Fatal("format maps share caller mutations")
	}
}

func TestFormatsConcurrent(t *testing.T) {
	examples := map[string][2]string{
		"date": {"2024-02-29", "2023-02-29"}, "time": {"23:59:60Z", "12:00:60Z"}, "date-time": {"2024-02-29T12:30:00Z", "2024-02-30T12:30:00Z"}, "duration": {"P1Y2M3DT4H5M6S", "P"},
		"email": {"a@example.com", "a..b@example.com"}, "idn-email": {"é@bücher.de", "a@-example.com"}, "hostname": {"xn--bcher-kva.example", "a..b"}, "idn-hostname": {"bücher.example", "a😀.com"},
		"ipv4": {"192.0.2.1", "256.0.0.1"}, "ipv6": {"::ffff:192.0.2.1", "fe80::1%eth0"}, "uri": {"https://example.com/a", "https://example.com/%ZZ"}, "uri-reference": {"../a?b#c", "a b"},
		"iri": {"https://例え.テスト/道", "https://example.com/a b"}, "iri-reference": {"道/../a", "httpx//example.com"}, "url": {"http://example.com/a b", "http://[bad]"},
		"uri-template": {"https://example.com/{path*}", "{var:0}"}, "json-pointer": {"/a~1b", "/a~2b"}, "relative-json-pointer": {"2/a~1b", "02/a"}, "json-pointer-uri-fragment": {"#/a%20b", "#/a b"},
		"regex": {"(?<=a)b", "("}, "uuid": {"6ba7b810-9dad-11d1-80b4-00c04fd430c8", "6ba7b810-9dad-11d1-80b4-00c04fd430c"},
	}
	checks := All()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 30 {
				for name, pair := range examples {
					if !checks[name](pair[0]) || checks[name](pair[1]) {
						t.Errorf("concurrent %s check differs", name)
						return
					}
				}
			}
		})
	}
	wg.Wait()
	if len(examples) != len(checks) {
		t.Fatal("concurrency examples do not cover every format")
	}
}

func TestUTF16Length(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{{"", 0}, {"a", 1}, {"汉字", 2}, {"😀", 2}, {"a😀b", 4}} {
		if got := utf16Length(tc.value); got != tc.want {
			t.Errorf("UTF-16 length of %q = %d, want %d", tc.value, got, tc.want)
		}
	}
}
