package pack

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// A pack says who published it, and where, with two optional keys of
// pack.yaml (pack API 5): author and id. Both are the pack's own words, kept
// as written and held to their form alone. Nothing checks that the pack came
// from that author or is served at that address, so whatever shows them says
// whose words they are, and nothing decides by them.

// author reads the author key: the name of whoever published the pack, a
// display string. It is printed on one line beside the pack's name (pack
// show), so a blank one says nothing, and a character a terminal would act on
// (a line break, an escape) is refused, as an env value's is, not printed.
func (r *reader) author(e entry) string {
	if n := e.v; n.Kind == yaml.ScalarNode && n.Tag == "!!str" && strings.TrimSpace(n.Value) == "" {
		r.errf(n, e.path, "empty: name who published the pack, or leave author out for a pack that says none")
		return ""
	}
	s, ok := r.str(e.v, e.path)
	if !ok {
		return ""
	}
	if i := strings.IndexFunc(s, func(c rune) bool { return !unicode.IsPrint(c) }); i >= 0 {
		c, _ := utf8.DecodeRuneInString(s[i:])
		r.errf(e.v, e.path, "the author holds %U, which is not printable: it is shown on one line beside the pack's name", c)
		return ""
	}
	return s
}

// publishedAt reads the id key: the address the pack is published at, an
// https URL with a host. It carries no user or password, which would be
// published with it, and no query or fragment, as a declaration's base
// carries none. It is ASCII with no space, as a URL is, so that what an
// operator reads is the address and not a look-alike: a host with letters
// beyond ASCII is written in its xn-- form. There is no registry to hold it
// to, and two packs may say one address.
func (r *reader) publishedAt(e entry) string {
	s, ok := r.str(e.v, e.path)
	if !ok {
		return ""
	}
	// url.Parse takes a space in a path and a letter beyond ASCII in a host
	// name, so the character is looked for first.
	if i := strings.IndexFunc(s, func(c rune) bool { return c <= ' ' || c >= 0x7f }); i >= 0 {
		c, _ := utf8.DecodeRuneInString(s[i:])
		r.errf(e.v, e.path, "the id holds %q (%U), which a URL does not: write it in ASCII, a host name with letters beyond ASCII in its xn-- form and the rest percent-encoded", c, c)
		return ""
	}
	u, err := url.Parse(s)
	switch {
	case err != nil:
		r.errf(e.v, e.path, "no address: want https://host/path")
	case u.Scheme == "":
		r.errf(e.v, e.path, "no scheme: want https://host/path")
	case u.Scheme != "https":
		r.errf(e.v, e.path, "%s:// is not https://: an id is the address the pack is published at, over https", u.Scheme)
	case u.Hostname() == "":
		r.errf(e.v, e.path, "no host: want https://host/path")
	case u.User != nil:
		// Said without the address: the user may be the credential itself.
		r.errf(e.v, e.path, "an address with a user or password in it: an id is published, so it carries no credential")
	case u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(s, "?#"):
		r.errf(e.v, e.path, "an address with a query or a fragment: an id is the https://host/path the pack is published at")
	default:
		return s
	}
	return ""
}
