package formats

import "testing"

func TestURITemplateASCIICase(t *testing.T) {
	checkFormat(t, URITemplate, []string{"{ABC}", "K/{var}"}, []string{"{K}", "{ſ}"})
}

func TestURITemplate(t *testing.T) {
	checkFormat(t, URITemplate, []string{"", "https://example.com/{path}", "{+path}", "{?a,b}", "{var:12}", "{var*}", "{a.b,c_d}", "/{a%20b}", "道/{var}"}, []string{"{", "}", "{}", "{var:0}", "{var:10000}", "{var:01}", "{a,,b}", "{%GG}", "a b", "a%GG", "{a{b}}"})
}
