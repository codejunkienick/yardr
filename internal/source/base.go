package source

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// What chooses where a source's calls go beside the base its file names: the
// regions' bases a setting picks from (bases), the address a host the
// operator types fills (base_template), for a service the operator may run on
// a machine of their own, and the address a site the operator types fills
// (path_template), one path segment of a service that scopes its calls by
// site. The file says every address a credential may go to, but for the host
// of the second and the segment of the third, which are the operator's word alone.

// HostMark is where the operator's host goes in a base_template, as
// CredentialMark is where the credential's value goes in a request.
const HostMark = "{host}"

// SiteMark is where the operator's site goes in a path_template: one path
// segment of the address, never the host.
const SiteMark = "{site}"

// standInHost is the host a template is held to the checks of an address with
// when its file is read: a name no machine has.
const standInHost = "host.example"

// standInSite is the segment a path template is held to the checks of an
// address with when its file is read: a segment no site has.
const standInSite = "site"

// bareSite is what a site is: one path segment, letters, digits and the
// punctuation a segment carries, and nothing else. A slash, a query or a
// fragment in it would make it more than one segment, which is the
// declaration's to say, never the site's.
var bareSite = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// bareHost is what a host is: a name, an IPv4 address or an IPv6 one in
// brackets, and a port after it where the machine has one. A scheme, a path, a
// user, a query and a fragment are the template's to say, never the host's.
var bareHost = regexp.MustCompile(`^(?:\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9_](?:[A-Za-z0-9_.-]*[A-Za-z0-9_])?)(?::[0-9]{1,5})?$`)

// BaseURL is the address the source's calls go to when its base setting names
// name: the base for "", the named one of bases. ok is false for a name the
// file declares none of: no free-text base is accepted from anyone, anywhere,
// so the credential goes only to a host the file declares.
func (d *Declaration) BaseURL(name string) (url string, ok bool) {
	if name == "" {
		return d.Base, true
	}
	u, ok := d.Bases[name]
	return u, ok
}

// WithBase is d with the base setting name in place of its own: what a call of a source set to name is made against.
// "" and a name the file declares none of are d itself: the setting names nothing under a declaration that lost it,
// and the call goes to the default base.
func (d *Declaration) WithBase(name string) *Declaration {
	u, ok := d.BaseURL(name)
	if !ok || u == d.Base {
		return d
	}
	c := *d
	c.Base = u
	return &c
}

// BaseNames are the names the file declares bases under, in order: what a refusal of an unknown one lists.
func (d *Declaration) BaseNames() []string {
	return slices.Sorted(maps.Keys(d.Bases))
}

// CheckBase holds value to the file's bases: "" is the base itself, and anything else names one of bases.
func (d *Declaration) CheckBase(value string) error {
	if value == "" {
		return nil
	}
	if len(d.Bases) == 0 {
		return fmt.Errorf("source %s: base=%q: the declaration names no bases, so there is nothing a base would choose", d.Name, value)
	}
	if _, ok := d.Bases[value]; !ok {
		return fmt.Errorf("source %s: base=%q: the declaration has no such base (it has: %s)", d.Name, value, strings.Join(d.BaseNames(), ", "))
	}
	return nil
}

// baseTemplate reads the address a host fills, beside base: https://{host} and
// the path every call is under. The mark stands once, in the host's place and
// with nothing beside it, so that what the operator types decides which
// machine the calls go to and the file decides the rest, the scheme and the
// path. With a stand-in host in it the template is held to what a base is
// (checkAddress), and CheckHost holds the address a typed host makes to the
// same again.
func (r *reader) baseTemplate(e entry) string {
	s, ok := r.str(e.v, e.path)
	if !ok {
		return ""
	}
	rest, atStart := strings.CutPrefix(s, "https://"+HostMark)
	switch {
	case strings.Count(s, HostMark) != 1:
		r.errf(e.v, e.path, "want %s once, where the host goes: https://%s/path", HostMark, HostMark)
	case !atStart:
		r.errf(e.v, e.path, "%s stands right after https://, as the whole host: https://%s/path", HostMark, HostMark)
	case rest != "" && rest[0] != '/':
		r.errf(e.v, e.path, "%s is the whole host, so nothing but a / follows it: a port is the operator's to type with the host", HostMark)
	default:
		if _, fault := checkAddress(strings.Replace(s, HostMark, standInHost, 1)); fault != addressOK {
			r.errf(e.v, e.path, "the template with a host in it %s", fault.words())
			return ""
		}
		return strings.TrimRight(s, "/")
	}
	return ""
}

// fillHost is the address the template makes with host, and why there is none.
// The address is held to what a base is first (checkAddress), so that a host
// that makes one the checks refuse is refused in their words; what passes them
// and is still more than a host, a path of its own, is refused after.
func (d *Declaration) fillHost(host string) (string, error) {
	if strings.Contains(host, "://") {
		return "", errors.New("a host has no scheme: the template says it (" + d.BaseTemplate + ")")
	}
	filled := strings.Replace(d.BaseTemplate, HostMark, host, 1)
	u, fault := checkAddress(filled)
	if fault != addressOK {
		return "", errors.New("the address it makes " + fault.words())
	}
	if !bareHost.MatchString(host) {
		return "", errors.New("a host is a name or an address with an optional port (crm.example.net, 192.168.1.20:8443) and nothing else: the path is the declaration's")
	}
	if port := u.Port(); port != "" {
		if n, _ := strconv.Atoi(port); n < 1 || n > 65535 {
			return "", errors.New("the port is a number from 1 to 65535")
		}
	}
	return filled, nil
}

// CheckHost holds value to the declaration's template: "" is the base itself,
// and anything else is a host the template is filled with, held to what a base
// is again with the host in it.
func (d *Declaration) CheckHost(value string) error {
	if value == "" {
		return nil
	}
	if d.BaseTemplate == "" {
		return fmt.Errorf("source %s: host=%q: the declaration names no base_template, so there is nothing a host would fill", d.Name, value)
	}
	if _, err := d.fillHost(value); err != nil {
		return fmt.Errorf("source %s: host=%q: %w", d.Name, value, err)
	}
	return nil
}

// WithHost is d with the template filled with host in place of its base: what a
// call of a source set to host is made against. "" is d itself, and so is a
// declaration that has no template: the setting names nothing under a
// declaration that lost it, and the call goes to the base, which a credential
// given for the host is then held from (store.Source.CredentialHeld). A host
// that makes an address the checks refuse, which CheckHost refused when it was
// set, leaves the declaration no address at all, since a call that cannot go
// where the operator said it goes must go nowhere.
func (d *Declaration) WithHost(host string) *Declaration {
	if host == "" || d.BaseTemplate == "" {
		return d
	}
	c := *d
	c.Base, _ = d.fillHost(host)
	return &c
}

// pathTemplate reads the address a site fills, beside base: the host the file
// names and a path with one segment the operator's site fills, as
// https://api.example.com/ex/{site}/wiki. The mark stands once, alone as one
// path segment and never as the host, so that what the operator types decides
// which site the calls are scoped to and the file decides the rest, the host
// and the other segments. With a stand-in site in it the template is held to
// what a base is (checkAddress), as a base_template is with a stand-in host,
// and CheckSite holds the address a typed site makes to the same again.
func (r *reader) pathTemplate(e entry) string {
	s, ok := r.str(e.v, e.path)
	if !ok {
		return ""
	}
	at := strings.Index(s, SiteMark)
	switch {
	case strings.Count(s, SiteMark) != 1:
		r.errf(e.v, e.path, "want %s once, where one path segment goes: https://host/%s/path", SiteMark, SiteMark)
	case strings.HasPrefix(s, "https://"+SiteMark):
		r.errf(e.v, e.path, "%s is one path segment, not the host: the host is the file's to name (https://host/%s/path)", SiteMark, SiteMark)
	case at <= 0 || s[at-1] != '/' || at+len(SiteMark) < len(s) && s[at+len(SiteMark)] != '/':
		r.errf(e.v, e.path, "%s stands alone as one path segment, with a / on each side of it: https://host/%s/path", SiteMark, SiteMark)
	default:
		if _, fault := checkAddress(strings.Replace(s, SiteMark, standInSite, 1)); fault != addressOK {
			r.errf(e.v, e.path, "the template with a site in it %s", fault.words())
			return ""
		}
		return strings.TrimRight(s, "/")
	}
	return ""
}

// fillSite is the address the path template makes with site, and why there is
// none. The address is held to what a base is first (checkAddress), as
// fillHost holds a filled host template, so that a site that makes one the
// checks refuse is refused in their words; what passes them and is still more
// than one segment is refused after.
func (d *Declaration) fillSite(site string) (string, error) {
	filled := strings.Replace(d.PathTemplate, SiteMark, site, 1)
	if _, fault := checkAddress(filled); fault != addressOK {
		return "", errors.New("the address it makes " + fault.words())
	}
	if !bareSite.MatchString(site) || site == "." || site == ".." {
		return "", errors.New("a site is one path segment (a name or an id, as cloud-1) and nothing else: the rest of the path is the declaration's")
	}
	return filled, nil
}

// CheckSite holds value to the declaration's path template: "" is the base
// itself, and anything else is a site the template is filled with, held to
// what a base is again with the site in it.
func (d *Declaration) CheckSite(value string) error {
	if value == "" {
		return nil
	}
	if d.PathTemplate == "" {
		return fmt.Errorf("source %s: site=%q: the declaration names no path_template, so there is nothing a site would fill", d.Name, value)
	}
	if _, err := d.fillSite(value); err != nil {
		return fmt.Errorf("source %s: site=%q: %w", d.Name, value, err)
	}
	return nil
}

// WithSite is d with the path template filled with site in place of its base:
// what a call of a source set to site is made against. It is chained last by
// store.Source.Resolved, so a site wins over a base or a host a stale record
// still names; the paths that set a setting keep the three exclusive, so in
// practice only one stands. "" is d itself, and so is a declaration that has
// no path template: the setting names nothing under a declaration that lost
// it, and the call goes to the base, which a credential given for the site is
// then held from (store.Source.CredentialHeld). A site that makes an address
// the checks refuse, which CheckSite refused when it was set, leaves the
// declaration no address at all, as WithHost leaves a refused host none.
func (d *Declaration) WithSite(site string) *Declaration {
	if site == "" || d.PathTemplate == "" {
		return d
	}
	c := *d
	c.Base, _ = d.fillSite(site)
	c.filledSite = site
	return &c
}

// FilledSite is the site segment WithSite filled into this declaration's
// base, "" where none was filled: what a credential is keyed to beside the
// host (store's sourceHost), never read from the declaration's own base, whose
// default segment is an example the file names and no operator typed.
func (d *Declaration) FilledSite() string { return d.filledSite }
