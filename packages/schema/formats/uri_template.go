package formats

var uRITemplatePattern = mustPattern("^(?:(?:[^\\x00-\\x20\"<>%\\\\^`{|}\\x7f]|%[0-9a-fA-F]{2})|\\{[+#./;?&=,!@|]?(?:[a-zA-Z0-9_]|%[0-9a-fA-F]{2})+(?:\\.(?:[a-zA-Z0-9_]|%[0-9a-fA-F]{2})+)*(?::[1-9]\\d{0,3}|\\*)?(?:,(?:[a-zA-Z0-9_]|%[0-9a-fA-F]{2})+(?:\\.(?:[a-zA-Z0-9_]|%[0-9a-fA-F]{2})+)*(?::[1-9]\\d{0,3}|\\*)?)*\\})*$")

// URITemplate reports whether value is an RFC 6570 URI template.
func URITemplate(value string) bool { return uRITemplatePattern.MatchString(value) }
