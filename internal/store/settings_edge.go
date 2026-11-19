package store

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
)

// CheckEdgeURL refuses an address that is no edge's: one hook_public_url
// would not take, and one with a path, since the edge answers at fixed paths
// (/edge/key, /edge/enrol) that a signature covers. Plain http is for a
// stand-in on this machine: the key of an edge is fetched before anything
// is signed, and over http it would be anyone's.
func CheckEdgeURL(v string) error {
	if err := checkPublicURL("edge address", v, "https://my.yardr.dev", ""); err != nil {
		return err
	}
	if u, _ := url.Parse(v); u.Path != "" {
		return fmt.Errorf("edge address %q: the edge's origin alone, such as https://my.yardr.dev: its calls are at paths of their own under it", v)
	}
	return nil
}

// Edge is the value of the edge setting. It is one setting so that the key
// an edge was pinned by, and the names it gave this yard, cannot change
// apart from the address they were got at.
type Edge struct {
	// URL is the edge's origin (CheckEdgeURL).
	URL string `json:"url"`
	// Key is the edge's public key (Public) as this yard pinned it
	// when it connected. It is never taken from the edge again: a different
	// key at the same address is a different edge.
	Key string `json:"key"`
	// Handle is the account's at the edge, and Name this yard's among its
	// yards, which its hooks are reached under.
	Handle string `json:"handle"`
	Name   string `json:"name,omitempty"`
	// HookPublicURL is the hook_public_url that connecting replaced, which
	// disconnecting puts back; "" when there was none.
	HookPublicURL string `json:"hook_public_url,omitempty"`
}

// edgeHandle is what an edge may call an account or one of its yards: one
// segment of a path, in lower case.
var edgeHandle = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// edgeNameRule is the rule edgeHandle holds, as a refusal states it.
const edgeNameRule = "lower case letters, digits and '-', which the edge's address takes as one segment of a path"

// CheckEdgeName refuses a name the edge does not take for a yard: what
// yardr edge connect --as proposes is validated with it before it is sent.
func CheckEdgeName(name string) error {
	if !edgeHandle.MatchString(name) {
		return fmt.Errorf("name %q: %s", name, edgeNameRule)
	}
	return nil
}

// HooksURL is the address the edge takes this yard's webhooks at, which is
// what hook_public_url is while connected: a hook's URL is it plus /<name>.
func (e Edge) HooksURL() string { return e.URL + "/hooks/" + e.Handle + "/" + e.Name }

// HandleName is this yard as the edge tells its account's yards apart:
// handle/name, the spelling a device pairs with and calls with
// (X-Edge-Yard). A value that holds no name reads as named as its account
// (ParseEdge), so this is always two segments where an edge setting
// stands.
func (e Edge) HandleName() string { return e.Handle + "/" + e.Name }

// String is the edge as the setting holds it.
func (e Edge) String() string {
	b, _ := json.Marshal(e) // strings only
	return string(b)
}

// ParseEdge reads the value of the edge setting as String writes it, and
// refuses one that holds what connecting would not have written: an address
// that is no edge's, a key that is no yard's, a handle or name that is no
// path segment, a hook_public_url that setting would not take. A value
// written before yards had names holds no name, and reads as named as its
// account, which is the yard an edge with one yard per account meant.
func ParseEdge(v string) (Edge, error) {
	var e Edge
	if err := json.Unmarshal([]byte(v), &e); err != nil {
		return Edge{}, fmt.Errorf("%s: not what yardr edge connect writes (%v)", EdgeSetting, err)
	}
	if err := CheckEdgeURL(e.URL); err != nil {
		return Edge{}, fmt.Errorf("%s: %w", EdgeSetting, err)
	}
	if _, err := ParsePublic(e.Key); err != nil {
		return Edge{}, fmt.Errorf("%s: the edge's key: %w", EdgeSetting, err)
	}
	if !edgeHandle.MatchString(e.Handle) {
		return Edge{}, fmt.Errorf("%s: handle %q: %s", EdgeSetting, e.Handle, edgeNameRule)
	}
	if e.Name == "" {
		e.Name = e.Handle
	} else if !edgeHandle.MatchString(e.Name) {
		return Edge{}, fmt.Errorf("%s: name %q: %s", EdgeSetting, e.Name, edgeNameRule)
	}
	if e.HookPublicURL != "" {
		if err := CheckHookPublicURL(e.HookPublicURL); err != nil {
			return Edge{}, fmt.Errorf("%s: the hook_public_url to put back: %w", EdgeSetting, err)
		}
	}
	return e, nil
}

// CheckEdge is the edge setting's check.
func CheckEdge(v string) error {
	_, err := ParseEdge(v)
	return err
}

// EdgeOf is the edge a yard's settings name, and false when it is
// connected to none.
func EdgeOf(settings map[string]string) (Edge, bool, error) {
	v := settings[EdgeSetting]
	if v == "" {
		return Edge{}, false, nil
	}
	e, err := ParseEdge(v)
	return e, err == nil, err
}
