package formats

import "net/netip"

// IPv4 reports whether value is an IPv4 address.
func IPv4(value string) bool {
	addr, err := netip.ParseAddr(value)
	return err == nil && addr.Is4()
}

// IPv6 reports whether value is an IPv6 address without a zone identifier.
func IPv6(value string) bool {
	addr, err := netip.ParseAddr(value)
	return err == nil && addr.Is6() && addr.Zone() == ""
}
