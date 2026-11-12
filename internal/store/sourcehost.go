package store

import "yardr.dev/yardr/internal/source"

// Resolved is d with the source's own settings in place of what the
// declaration says by itself: the region base the base setting names, or the
// host the host setting fills the template with (the two are never set
// together). It is what every call of the source is made against and what its
// credential is paired with (pairCredential, CredentialHeld), so a place that
// needs where the calls go asks this and never reads the declaration's base:
// a call would then go to the base while the operator had said a host.
func (s Source) Resolved(d *source.Declaration) *source.Declaration {
	return d.WithBase(s.Base).WithHost(s.Host)
}

// where is what the source's settings say of where its calls go, as a run ask
// holds it (sourceFingerprint): the base's name, or the host where one is
// typed. A source with none reads as it did before the host was a setting, so
// no card that was written before it is made stale by it.
func (s Source) where() string {
	if s.Host != "" {
		return "host=" + s.Host
	}
	return s.Base
}
