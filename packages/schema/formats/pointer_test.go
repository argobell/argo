package formats

import "testing"

func TestJSONPointer(t *testing.T) {
	checkFormat(t, JSONPointer, []string{"", "/", "/a/b", "/a~1b/~0c", "/a b", "/😀", "/a\nb"}, []string{"a", "#/a", "/~", "/~2", "/~01~"})
}
func TestRelativeJSONPointer(t *testing.T) {
	checkFormat(t, RelativeJSONPointer, []string{"0", "0#", "1/a", "12/a~1b", "123/😀", "1/"}, []string{"", "01", "01/a", "-1/a", "+1/a", "1#/a", "1/~2", "1#x"})
}
func TestJSONPointerURIFragmentASCIICase(t *testing.T) {
	checkFormat(t, JSONPointerURIFragment, []string{"#/ABC/%ab"}, []string{"#/K", "#/ſ"})
}

func TestJSONPointerURIFragment(t *testing.T) {
	checkFormat(t, JSONPointerURIFragment, []string{"#", "#/", "#/a~1b/~0c", "#/a%20b", "#/%F0%9F%98%80", "#/$&'()*+,;:=@"}, []string{"", "/a", "#/a b", "#/😀", "#/~2", "#/%GG", "#/%2", "#/a?b", "#/a#b"})
}
