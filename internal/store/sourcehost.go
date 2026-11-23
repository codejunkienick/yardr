package store

import "yardr.dev/yardr/internal/source"

// Resolved is d with the source's own settings in place of what the
// declaration says by itself: the region base the base setting names, the
// host the host setting fills the base template with, or the site the site
// setting fills the path template with, chained in that order so the site
// wins last (the three are never set together: every path that sets one
// clears the others, so the precedence is for a stale record alone). It is
// what every call of the source is made against and what its credential is
// paired with (pairCredential, CredentialHeld), so a place that needs where
// the calls go asks this and never reads the declaration's base: a call would
// then go to the base while the operator had said a host or a site.
func (s Source) Resolved(d *source.Declaration) *source.Declaration {
	return d.WithBase(s.Base).WithHost(s.Host).WithSite(s.Site)
}

// where is what the source's settings say of where its calls go, as a run ask
// holds it (sourceFingerprint): the base's name, the host where one is typed,
// or the site where one is typed. A source with none reads as it did before
// the host was a setting, so no card that was written before it is made stale
// by it; one with a site, which no older card could name, reads site=, so a
// card written before the site was set is stale by it, as it should be.
func (s Source) where() string {
	if s.Site != "" {
		return "site=" + s.Site
	}
	if s.Host != "" {
		return "host=" + s.Host
	}
	return s.Base
}
