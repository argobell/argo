package formats

var uUIDPattern = mustPattern("^[0-9a-fA-F]{8}-(?:[0-9a-fA-F]{4}-){3}[0-9a-fA-F]{12}$")

// UUID reports whether value is a UUID, without restricting version or variant.
func UUID(value string) bool { return uUIDPattern.MatchString(value) }
