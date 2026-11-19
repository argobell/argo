package formats

var jSONPointerPattern = mustPattern("^(?:/(?:[^~/]|~0|~1)*)*$")

// JSONPointer reports whether value is a JSON Pointer.
func JSONPointer(value string) bool { return jSONPointerPattern.MatchString(value) }

var relativeJSONPointerPattern = mustPattern("^(?:0|[1-9][0-9]*)(?:#|(?:/(?:[^~/]|~0|~1)*)*)$")

// RelativeJSONPointer reports whether value is a relative JSON Pointer.
func RelativeJSONPointer(value string) bool { return relativeJSONPointerPattern.MatchString(value) }

var jSONPointerURIFragmentPattern = mustPattern("^#(?:/(?:[a-zA-Z0-9_\\-.!$&'()*+,;:=@]|%[0-9a-fA-F]{2}|~0|~1)*)*$")

// JSONPointerURIFragment reports whether value is a JSON Pointer URI fragment.
func JSONPointerURIFragment(value string) bool {
	return jSONPointerURIFragmentPattern.MatchString(value)
}
