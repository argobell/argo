package formats

import "testing"

func BenchmarkIP(b *testing.B) {
	for _, tc := range []struct {
		name   string
		check  func(string) bool
		values []string
	}{
		{"IPv4", IPv4, []string{"192.0.2.1", "255.255.255.255", "256.1.1.1", "01.2.3.4"}},
		{"IPv6", IPv6, []string{"2001:db8::1", "::ffff:192.0.2.1", "1:2:3:4:5:6:7:8", "2001::db8::1"}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for _, value := range tc.values {
					tc.check(value)
				}
			}
		})
	}
}

func TestIPv4(t *testing.T) {
	checkFormat(t, IPv4, []string{"0.0.0.0", "127.0.0.1", "192.0.2.1", "255.255.255.255"}, []string{"", "1", "1.2.3", "1.2.3.4.5", "256.0.0.1", "01.2.3.4", "1.2.3.-1", "1.2.3.4/24", " 1.2.3.4", "1.2.3.4\n", "١.٢.٣.٤"})
}
func TestIPv6(t *testing.T) {
	checkFormat(t, IPv6, []string{"::", "::1", "2001:db8::1", "2001:DB8:0:0:0:0:0:1", "::ffff:192.0.2.1", "::192.0.2.1", "::ffff:0:192.0.2.1", "2001:db8:1:2:3:4:192.0.2.1"}, []string{"", "1.2.3.4", "[::1]", "fe80::1%eth0", "fe80::1%25eth0", "::1%", "::ffff:192.0.2.1%zone", "::1/128", "2001::db8::1", "12345::1", "1:2:3:4:5:6:7", "1:2:3:4:5:6:7:8:9", "::ffff:256.0.0.1", "::1\n"})
}
