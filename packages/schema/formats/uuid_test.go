package formats

import "testing"

func TestUUID(t *testing.T) {
	checkFormat(t, UUID, []string{"00000000-0000-0000-0000-000000000000", "6ba7b810-9dad-11d1-80b4-00c04fd430c8", "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF"}, []string{"", "urn:uuid:6ba7b810-9dad-11d1-80b4-00c04fd430c8", "{6ba7b810-9dad-11d1-80b4-00c04fd430c8}", "6ba7b8109dad11d180b400c04fd430c8", "6ba7b810-9dad-11d1-80b4-00c04fd430c", "6ba7b810-9dad-11d1-80b4-00c04fd430cg", "00000000-0000-0000-0000-000000000000\n"})
}
