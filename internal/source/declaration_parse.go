package source

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Parse reads the declaration in text. file is how errors name it. What it returns with a nil error is safe to call:
// every refusal that does not depend on a parameter's value is made here.
func Parse(file string, text []byte) (*Declaration, error) {
	if len(text) > MaxDeclaration {
		return nil, Errors{{File: file, Msg: fmt.Sprintf("%d bytes: a declaration is at most %d", len(text), MaxDeclaration)}}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(text, &doc); err != nil {
		return nil, Errors{{File: file, Msg: err.Error()}}
	}
	if len(doc.Content) == 0 {
		return nil, Errors{{File: file, Msg: "the file is empty"}}
	}
	r := &reader{file: file}
	d := &Declaration{}
	r.declaration(d, doc.Content[0])
	if len(r.errs) > 0 {
		sort.SliceStable(r.errs, func(i, j int) bool { return r.errs[i].Line < r.errs[j].Line })
		return nil, r.errs
	}
	return d, nil
}

type reader struct {
	file string
	errs Errors
}

func (r *reader) errf(n *yaml.Node, key, format string, args ...any) {
	r.errs = append(r.errs, Error{File: r.file, Line: n.Line, Key: key, Msg: fmt.Sprintf(format, args...)})
}

type entry struct {
	key  string
	path string // the key's dotted path from the root
	k, v *yaml.Node
}

func join(key, name string) string {
	if key == "" {
		return name
	}
	return key + "." + name
}

// entries are a mapping's keys and values in file order. With allowed, a
// key outside it is an error; a key YAML reads as a boolean, a non-string
// key and a repeated key are errors always, and such an entry is left out.
func (r *reader) entries(n *yaml.Node, key string, allowed ...string) []entry {
	if n.Kind != yaml.MappingNode {
		r.errf(n, key, "want a mapping (key: value), got %s", describe(n))
		return nil
	}
	var out []entry
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		p := join(key, k.Value)
		switch {
		case booleanLike(k):
			r.errf(k, p, "YAML reads the bare key %s as a boolean, not a name: rename it, or quote it (%q)", k.Value, k.Value)
		case k.Kind != yaml.ScalarNode || k.Tag != "!!str":
			r.errf(k, p, "a key must be a string, got %s", describe(k))
		case seen[k.Value]:
			r.errf(k, p, "given twice")
		case len(allowed) > 0 && !slices.Contains(allowed, k.Value):
			r.errf(k, p, "unknown key (known here: %s)", strings.Join(allowed, ", "))
		default:
			seen[k.Value] = true
			out = append(out, entry{key: k.Value, path: p, k: k, v: v})
		}
	}
	return out
}

func describe(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a list"
	case yaml.AliasNode:
		return "an alias (not read in a declaration)"
	case yaml.ScalarNode:
		if n.Tag == "!!null" {
			return "nothing"
		}
		if n.Tag == "!!str" {
			return strconv.Quote(n.Value)
		}
		return strings.TrimPrefix(n.Tag, "!!") + " " + n.Value
	}
	return "something else"
}

func (r *reader) str(n *yaml.Node, key string) (string, bool) {
	if booleanLike(n) {
		r.errf(n, key, "YAML reads the bare word %s as a boolean, not a string: quote it (%q)", n.Value, n.Value)
		return "", false
	}
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" || n.Value == "" {
		r.errf(n, key, "want a string, got %s", describe(n))
		return "", false
	}
	return n.Value, true
}

func (r *reader) boolean(n *yaml.Node, key string) (value, ok bool) {
	if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" || (n.Value != "true" && n.Value != "false") {
		r.errf(n, key, "want true or false, got %s", describe(n))
		return false, false
	}
	return n.Value == "true", true
}

// count reads a whole number from 1 to most; most below 1 reads one with
// no bound above, for a limit the file sets and nothing else holds.
func (r *reader) count(n *yaml.Node, key string, most int) (int, bool) {
	v, err := strconv.Atoi(n.Value)
	if n.Kind != yaml.ScalarNode || n.Tag != "!!int" || err != nil || v < 1 || (most > 0 && v > most) {
		if most > 0 {
			r.errf(n, key, "want a whole number from 1 to %d, got %s", most, describe(n))
		} else {
			r.errf(n, key, "want a whole number from 1, got %s", describe(n))
		}
		return 0, false
	}
	return v, true
}

func (r *reader) declaration(d *Declaration, root *yaml.Node) {
	var calls, check, notify, hear, base, bases, baseTemplate, credential, idempotency, hook, git *entry
	named := false
	for _, e := range r.entries(root, "", "name", "about", "base", "bases", "base_template", "credential", "check", "idempotency", "calls", "notify", "hear", "limits", "thread", "link", "hook", "identity", "events", "git") {
		switch e.key {
		case "name":
			named = true
			name, ok := r.str(e.v, e.path)
			switch {
			case !ok:
			case !validName.MatchString(name):
				r.errf(e.v, e.path, "name %q: use lowercase letters, digits, - and _, starting with a letter or digit", name)
			case slices.Contains(BuiltIn, name):
				r.errf(e.v, e.path, "%s is a source yardr has built in (%s): a declared source takes another name", name, strings.Join(BuiltIn, ", "))
			default:
				d.Name = name
			}
		case "about":
			d.About, _ = r.str(e.v, e.path)
		case "base":
			base = &e
			if s, ok := r.str(e.v, e.path); ok {
				d.Base = r.base(e.v, e.path, s)
			}
		case "bases":
			bases = &e
			d.Bases = r.bases(e)
		case "base_template":
			baseTemplate = &e
			d.BaseTemplate = r.baseTemplate(e)
		case "credential":
			credential = &e
			d.Credential = r.credential(e)
		case "check":
			check = &e
		case "idempotency":
			idempotency = &e
		case "calls":
			calls = &e
		case "notify":
			notify = &e
		case "hear":
			hear = &e
		case "limits":
			d.Limits = r.limits(e)
		case "thread":
			d.Thread = r.thread(e)
		case "link":
			d.Link = r.link(e)
		case "hook":
			hook = &e
			d.Hook = r.hook(e)
		case "identity":
			d.Identity = r.identity(e)
		case "events":
			d.Events = r.events(e)
		case "git":
			git = &e
			d.Git = r.git(e)
		}
	}
	if root.Kind != yaml.MappingNode {
		return
	}
	if !named {
		r.errf(root, "name", "missing: what the source is called in this yard")
	}
	if calls == nil && (d.Limits != nil || (d.Hook != nil && d.Hook.Register == nil)) {
		// A source that is its limits command and/or what it sends in,
		// and nothing else: there is no request, so nothing a request
		// needs has a meaning. Hearing is not one of those (it keeps
		// deliveries by who speaks, needing no call), so the hear key
		// is read and hearing by address refused there with its reason.
		alone := "limits alone"
		if d.Hook != nil && d.Limits != nil {
			alone = "hook and limits alone"
		} else if d.Hook != nil {
			alone = "hook alone"
		}
		for _, e := range []*entry{base, bases, baseTemplate, credential, check, idempotency, notify} {
			if e != nil {
				r.errf(e.k, e.path, "is for the calls of a source, and this file declares none (it has %s)", alone)
			}
		}
		if git != nil {
			r.errf(git.k, git.path, "carries the source's token to git, and this file declares no credential (it has %s)", alone)
		}
		d.Base, d.Bases, d.BaseTemplate, d.Credential = "", nil, "", nil
		if hear != nil {
			d.Hear = r.hear(d, *hear, alone)
		}
		return
	}
	// A credential that is the address, and nothing else, is a giving alone (the routine's URL, read by an adapter): no call to make, and every key of a call beside it falls through to the refusal below.
	if calls == nil && d.Credential != nil && d.Credential.URL && d.Limits == nil && d.Hook == nil && d.Thread == nil && d.Link == nil && d.Identity == nil && d.Events == nil && hear == nil && git == nil && base == nil && bases == nil && baseTemplate == nil && check == nil && idempotency == nil && notify == nil {
		d.Base, d.Bases, d.BaseTemplate = "", nil, ""
		return
	}
	// A credential that is the address is every call's address, so it
	// takes no base beside it, and without one the file needs a base.
	address := d.Credential != nil && d.Credential.URL
	switch {
	case base != nil && address:
		r.errf(base.k, base.path, "the credential is the address every call goes to (credential.url: true), so the file names no base beside it")
	case bases != nil && address:
		r.errf(bases.k, bases.path, "the credential is the address every call goes to (credential.url: true), so the file names no bases beside it")
	case baseTemplate != nil && address:
		r.errf(baseTemplate.k, baseTemplate.path, "the credential is the address every call goes to (credential.url: true), so the file names no base_template beside it")
	case base == nil && !address:
		r.errf(root, "base", "missing: the address every call's path is under (https://…), or credential.url: true where the credential is the address itself")
	}
	// The git helper carries the source's token, so it stands beside the
	// credential that names it, and never beside one that is an address.
	if git != nil {
		switch {
		case d.Credential == nil:
			r.errf(git.k, git.path, "carries the source's token to git, and the file names no credential")
		case address:
			r.errf(git.k, git.path, "carries the source's token to git, and the credential is the address every call goes to (credential.url: true), not a token")
		}
	}
	if calls == nil {
		r.errf(root, "calls", "missing: a source is the calls it declares, or its limits command, or what it sends in (hook)")
		return
	}
	for _, e := range r.entries(calls.v, calls.path) {
		switch {
		case !validName.MatchString(e.key):
			r.errf(e.k, e.path, "name %q: use lowercase letters, digits, - and _, starting with a letter or digit", e.key)
		// The word a grant uses for every read call of a source.
		case e.key == Read:
			r.errf(e.k, e.path, "a call cannot be named %s: the word stands for every read call of a source", Read)
		default:
			inPath := d.Credential != nil && d.Credential.InPath
			d.Calls = append(d.Calls, r.call(e, d.Credential != nil, inPath, address))
		}
	}
	if calls.v.Kind == yaml.MappingNode && len(calls.v.Content) == 0 {
		r.errf(calls.v, calls.path, "no call: a source is the calls it declares")
	}
	if check != nil {
		name, ok := r.str(check.v, check.path)
		c, found := d.Call(name)
		switch {
		case !ok:
		case !found:
			r.errf(check.v, check.path, "%s is no call of this file", name)
		case c.Kind != Read:
			r.errf(check.v, check.path, "%s is an update: the check is a read call, made to see that the source answers", name)
		case len(c.Build) > 0:
			r.errf(check.v, check.path, "%s names build: source check and yardr doctor send the request's template and run no build command", name)
		case slices.ContainsFunc(c.Params, func(p Param) bool { return !p.Optional }):
			r.errf(check.v, check.path, "%s needs a parameter: the check is a call without any", name)
		default:
			d.Check = name
		}
	}
	if idempotency != nil {
		d.Idempotency = r.idempotency(d, *idempotency)
	}
	if notify != nil {
		d.Notify = r.notify(d, *notify)
	}
	if hear != nil {
		d.Hear = r.hear(d, *hear, "")
	}
	if hook != nil && d.Hook != nil && d.Hook.Register != nil {
		r.hookRegisterCheck(d, *hook)
	}
}

// hookRegisterCheck holds a hook's register to the calls as read: the
// call is there and is an update, its URL, secret and asked parameters
// are its own and take text, and nothing it requires is left unfilled.
// What would fail at every registration is refused here.
func (r *reader) hookRegisterCheck(d *Declaration, hookE entry) {
	reg := d.Hook.Register
	base := join(hookE.path, "register")
	c, found := d.Call(reg.Call)
	if reg.Call == "" {
		return // hookRegister named the missing call
	}
	if !found {
		r.errf(hookE.v, join(base, "call"), "%s is no call of this file", reg.Call)
		return
	}
	if c.Kind != Update {
		r.errf(hookE.v, join(base, "call"), "%s is a %s call: registering a webhook sends something, so it is an %s", reg.Call, c.Kind, Update)
		return
	}
	if len(c.Build) > 0 {
		r.errf(hookE.v, join(base, "call"), "%s names build: registering a webhook sends the request's template and runs no build command", reg.Call)
		return
	}
	fills := map[string]string{}
	param := func(name, key string) {
		if name == "" {
			return
		}
		p, ok := c.param(name)
		switch {
		case !ok:
			r.errf(hookE.v, key, "%s is no parameter of the call %s (it takes: %s)", name, c.Name, or(strings.Join(c.names(), ", "), "none"))
		case p.Type != "string":
			r.errf(hookE.v, key, "the parameter %s is %s: what the yard gives is text, so it is a string", name, p.Type)
		case fills[name] != "":
			r.errf(hookE.v, key, "the parameter %s is filled by %s already", name, fills[name])
		case !c.usesParam(name):
			r.errf(hookE.v, key, "the call %s does not send parameter %s in its path, query or body", c.Name, name)
		default:
			fills[name] = key
		}
	}
	param(reg.URL, join(base, "url"))
	param(reg.Secret, join(base, "secret"))
	for _, name := range reg.Ask {
		p, ok := c.param(name)
		switch {
		case !ok:
			r.errf(hookE.v, join(base, "ask"), "%s is no parameter of the call %s (it takes: %s)", name, c.Name, or(strings.Join(c.names(), ", "), "none"))
		case fills[name] != "":
			r.errf(hookE.v, join(base, "ask"), "the parameter %s is filled by %s already: the card asks for what the yard has not", name, fills[name])
		case p.Optional:
			r.errf(hookE.v, join(base, "ask"), "the parameter %s is optional: the card asks for a required parameter", name)
		case p.Type != "string" || p.Pattern == "":
			r.errf(hookE.v, join(base, "ask"), "the parameter %s is asked for in the clear: it is a string with a pattern, so the card takes an id and no free text", name)
		case !c.usesParam(name):
			r.errf(hookE.v, join(base, "ask"), "the call %s does not send parameter %s in its path, query or body", c.Name, name)
		default:
			fills[name] = join(base, "ask")
		}
	}
	for _, p := range c.Params {
		if !p.Optional && fills[p.Name] == "" {
			r.errf(hookE.v, base, "the call %s needs the parameter %s, and nothing here fills it (url and secret are what the yard has, and ask names the rest)", c.Name, p.Name)
		}
	}
	if reg.Existing != nil {
		r.hookExistingCheck(d, hookE, base)
	}
}

// hookExistingCheck holds a register's check to the calls as read: the
// call is there and is a read, and every parameter it requires is the
// public URL's or one the card asks for, filled with the same values as
// the register call. The secret is never sent with the check: a check
// lists what stands, and takes no secret.
func (r *reader) hookExistingCheck(d *Declaration, hookE entry, base string) {
	reg := d.Hook.Register
	ex := reg.Existing
	base = join(base, "existing")
	c, found := d.Call(ex.Call)
	if ex.Call == "" {
		return // hookExisting named the missing call
	}
	if !found {
		r.errf(hookE.v, join(base, "call"), "%s is no call of this file", ex.Call)
		return
	}
	if c.Kind != Read {
		r.errf(hookE.v, join(base, "call"), "%s is an update: the check is a read call, made to list what stands", ex.Call)
		return
	}
	if len(c.Build) > 0 {
		r.errf(hookE.v, join(base, "call"), "%s names build: the check sends the request's template and runs no build command", ex.Call)
		return
	}
	if len(c.Run) > 0 {
		r.errf(hookE.v, join(base, "call"), "%s names run: the check sends the request a call declares, and a call that names run declares none", ex.Call)
		return
	}
	for _, p := range c.Params {
		if p.Optional || p.Name == reg.URL || slices.Contains(reg.Ask, p.Name) {
			continue
		}
		r.errf(hookE.v, base, "the call %s needs the parameter %s, and nothing here fills it (the check is sent the URL and the asked values, never the secret)", c.Name, p.Name)
	}
}

// ownHeaders are the headers Do sets on every request itself.
var ownHeaders = []string{"Accept", "Content-Type", "User-Agent"}

// idempotency reads the name of the header an update's key goes in. What
// it refuses is a header that is another's already, the credential's above
// all, and a file whose calls are all reads, which no key is sent with.
func (r *reader) idempotency(d *Declaration, e entry) string {
	name, ok := r.str(e.v, e.path)
	if !ok {
		return ""
	}
	credential := ""
	if d.Credential != nil {
		credential, _, _ = strings.Cut(d.Credential.Header, ":")
	}
	switch {
	case !headerName.MatchString(name):
		r.errf(e.v, e.path, "%q is no name of a header: this is the header the service takes an update's key in (Idempotency-Key)", name)
	case credential != "" && strings.EqualFold(name, credential):
		r.errf(e.v, e.path, "%s is the header the credential is sent in", name)
	case slices.ContainsFunc(ownHeaders, func(h string) bool { return strings.EqualFold(h, name) }):
		r.errf(e.v, e.path, "%s is a header every call is sent with already (%s)", name, strings.Join(ownHeaders, ", "))
	case !slices.ContainsFunc(d.Calls, func(c Call) bool { return c.Kind == Update }):
		r.errf(e.v, e.path, "the file has no %s call, and a key is sent with those alone", Update)
	default:
		return name
	}
	return ""
}

// command reads one command of a declaration: the program and its
// arguments, a list of strings run without a shell, so each word is one
// element. example shows the list's shape in an error (`"{provider}",
// --json`). A program with no / at its start is a file beside the
// declaration's file; one with a '.' or '..' segment would leave it.
func (r *reader) command(f entry, example string) []string {
	if f.v.Kind != yaml.SequenceNode {
		r.errf(f.v, f.path, "want a list of strings ([program, %s]), got %s: the command is run without a shell, so each word is one element", example, describe(f.v))
		return nil
	}
	var out []string
	whole := true
	for _, c := range f.v.Content {
		s, ok := r.str(c, f.path)
		whole = whole && ok
		out = append(out, s)
	}
	switch {
	case !whole:
		return nil
	case len(out) == 0:
		r.errf(f.v, f.path, "an empty list: want the program and its arguments")
		return nil
	case hasDots(out[0]):
		r.errf(f.v, f.path, "the program %q has a '.' or '..' segment: one with no / at its start is a file beside the declaration's file, and stays under it", out[0])
		return nil
	}
	return out
}

// The ways a text fails to be an address calls go to, as checkAddress finds them.
type addressFault int

const (
	addressOK     addressFault = iota
	addressNone                // no host, or no address at all
	addressUser                // a user or password in it
	addressQuery               // a query or a fragment
	addressPlain               // plain http to a machine that is not this one
	addressScheme              // a scheme but https and http
	addressDots                // a '..', '//' or '\' in its path
)

// checkAddress holds s to what the address of calls must be: https, or
// plain http to this machine for a stand-in, with no user, query or
// fragment and no way up out of its path. A base in a file is held to it
// when the file is read (reader.base), and a credential that is the
// address when a call is made (doKeyed), since the file cannot show it.
// The URL is s as parsed, nil where s did not parse.
func checkAddress(s string) (*url.URL, addressFault) {
	u, err := url.Parse(s)
	switch {
	case err != nil || u.Host == "" || u.Opaque != "":
		return u, addressNone
	case u.User != nil:
		return u, addressUser
	case u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(s, "?#"):
		return u, addressQuery
	case u.Scheme == "http" && !LoopbackHost(u.Hostname()):
		return u, addressPlain
	case u.Scheme != "https" && u.Scheme != "http":
		return u, addressScheme
	case hasDots(u.Path) || strings.Contains(u.Path, "//") || strings.Contains(s, `\`):
		return u, addressDots
	}
	return u, addressOK
}

// AddressWords says what is wrong with s as the address calls go to,
// "" when it is an acceptable one. It is checkAddress's fault in words,
// for a credential that is the address where it is given: an error may
// not repeat the value.
func AddressWords(s string) string {
	_, fault := checkAddress(s)
	if fault == addressOK {
		return ""
	}
	return fault.words()
}

// words says what is wrong with an address and nothing of the address
// itself, for a credential that is one: an error may not repeat it.
func (f addressFault) words() string {
	switch f {
	case addressNone:
		return "is no address: want https://host/path"
	case addressUser:
		return "has a user or password in it"
	case addressQuery:
		return "has a query or a fragment"
	case addressPlain:
		return "is plain http to a machine that is not this one: a call goes over https, and plain http is for a stand-in on this machine alone"
	case addressScheme:
		return "is not https://"
	case addressDots:
		return "has a '..', '//' or '\\' in its path"
	}
	return "is no address"
}

// base reads the address calls are under. Anything but https is refused,
// since the credential goes with every call; plain http is for a stand-in
// on this machine alone. Do holds a call to that again when it connects.
func (r *reader) base(n *yaml.Node, key, s string) string {
	u, fault := checkAddress(s)
	switch fault {
	case addressNone:
		r.errf(n, key, "%q is no address: want https://host/path", s)
	case addressUser:
		r.errf(n, key, "an address with a user or password in it: the credential is named under credential, never written here")
	case addressQuery:
		r.errf(n, key, "an address with a query or a fragment: a call's query is under its request")
	case addressPlain:
		r.errf(n, key, "http:// is for a stand-in on this machine only (127.0.0.1, [::1], localhost): %s is not, so it is https:// or nothing", u.Hostname())
	case addressScheme:
		r.errf(n, key, "%s:// is not https://", u.Scheme)
	case addressDots:
		r.errf(n, key, "the path of an address has no '..', '//' or '\\'")
	default:
		return strings.TrimRight(s, "/")
	}
	return ""
}

// bases reads the regions' bases beside base: each name, and each URL
// held to what a base is (reader.base). A URL that fails is refused and
// left out, like a base that fails.
func (r *reader) bases(e entry) map[string]string {
	var out map[string]string
	for _, f := range r.entries(e.v, e.path) {
		if !validName.MatchString(f.key) {
			r.errf(f.k, f.path, "name %q: use lowercase letters, digits, - and _, starting with a letter or digit", f.key)
			continue
		}
		s, ok := r.str(f.v, f.path)
		if !ok {
			continue
		}
		if u := r.base(f.v, f.path, s); u != "" {
			if out == nil {
				out = map[string]string{}
			}
			out[f.key] = u
		}
	}
	if e.v.Kind == yaml.MappingNode && len(e.v.Content) == 0 {
		r.errf(e.v, e.path, "no base: bases names the regions' bases beside base, or is left out")
	}
	return out
}

// LoopbackHost reports whether host, as written, is this machine's
// loopback: an address of it, or the name every system gives it. It is the
// one rule for where plain http may go: a declaration's base (checkAddress)
// and a pack fetched by URL (pack.URLFS) both hold to it.
func LoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// hasDots reports whether a segment of path is "." or "..".
func hasDots(path string) bool {
	return slices.ContainsFunc(strings.Split(path, "/"), func(s string) bool { return s == "." || s == ".." })
}

func (r *reader) credential(e entry) *Credential {
	c, has := &Credential{}, false
	var update, mint, mintCommand, header, inPath, login *entry
	for _, f := range r.entries(e.v, e.path, "env", "update_env", "header", "mint", "mint_command", "in_path", "url", "login") {
		switch f.key {
		case "env":
			has = true
			if s, ok := r.str(f.v, f.path); ok && !paramName.MatchString(s) {
				r.errf(f.v, f.path, "%q is no name of a variable: this is the name the credential is under in the source's env file, never the credential", s)
			} else if ok {
				c.Env = s
			}
		case "update_env":
			update = &f
			if s, ok := r.str(f.v, f.path); ok && !paramName.MatchString(s) {
				r.errf(f.v, f.path, "%q is no name of a variable: this is the name the credential of an update call is under in the source's env file, never the credential", s)
			} else if ok {
				c.UpdateEnv = s
			}
		case "mint":
			mint = &f
			c.Mint, c.NotSecret, c.Bound = r.mintNames(f)
		case "mint_command":
			mintCommand = &f
			c.MintCommand = r.command(f, "argument")
			// Relative alone, as a build's: the program that runs with
			// a person's login is one the yard copies and hashes, so the
			// card that adds it shows its digest and a later change is
			// shown again. An absolute one is the operator's to set on
			// the source, never a file's to name.
			if len(c.MintCommand) > 0 && strings.HasPrefix(c.MintCommand[0], "/") {
				r.errf(f.v, f.path, "the program %q is absolute: a mint_command a declaration brings is a file beside the declaration's file that the yard copies and hashes; the operator's own is set on the source (yardr source set <name> mint_command=<program>)", c.MintCommand[0])
				c.MintCommand = nil
			}
		case "in_path":
			inPath = &f
			if v, ok := r.boolean(f.v, f.path); ok {
				c.InPath = v
			}
		case "url":
			if v, ok := r.boolean(f.v, f.path); ok {
				c.URL = v
			}
		case "login":
			login = &f
			c.Login = r.credentialLogin(f)
		case "header":
			header = &f
			s, ok := r.str(f.v, f.path)
			if !ok {
				continue
			}
			name, value, cut := strings.Cut(s, ":")
			marks := mark.FindAllString(value, -1)
			switch {
			case !cut || !headerName.MatchString(name) || strings.TrimSpace(value) == "":
				r.errf(f.v, f.path, `want "Name: text %s", got %q`, CredentialMark, s)
			case strings.ContainsFunc(value, func(c rune) bool { return c < ' ' || c == 0x7f }):
				r.errf(f.v, f.path, "a header is one line")
			case len(marks) != 1 || marks[0] != CredentialMark:
				r.errf(f.v, f.path, "a header is fixed text and %s, once: a parameter never reaches a header", CredentialMark)
			default:
				c.Header = name + ": " + strings.TrimSpace(value)
			}
		}
	}
	if e.v.Kind == yaml.MappingNode && !has {
		r.errf(e.v, join(e.path, "env"), "missing: the name of the variable the credential is in")
	}
	// A credential that is the address goes where the call goes and
	// nowhere else: no header, path, mint or second variable beside it.
	if c.URL {
		for _, no := range []struct {
			e    *entry
			what string
		}{{header, "sent in a header"}, {inPath, "put in a path"}, {mint, "minted"}, {update, "given a second variable for updates"}, {login, "given sign-in steps"}} {
			if no.e != nil && (no.e != inPath || c.InPath) {
				r.errf(no.e.k, no.e.path, "the credential is the address every call goes to (url: true), and is not %s", no.what)
			}
		}
	}
	// The same name twice says there are two credentials where there is one.
	if update != nil && c.UpdateEnv != "" && c.UpdateEnv == c.Env {
		r.errf(update.v, update.path, "%s is env already: update_env is for a second credential, which the update calls are sent with. Leave it out for a source with one", c.Env)
		c.UpdateEnv = ""
	}
	// A mint's name cannot be the variable the minted token is handed
	// under: the mint's inputs are beside the credential, never it.
	if mint != nil {
		for _, m := range c.Mint {
			switch m {
			case c.Env:
				r.errf(mint.v, mint.path, "%s is env already: mint names the variables the token is minted from, beside the variable the token is handed under", m)
			case c.UpdateEnv:
				r.errf(mint.v, mint.path, "%s is update_env already: mint names the variables the token is minted from, beside the variables a call is sent with", m)
			}
		}
	}
	if mintCommand != nil && len(c.MintCommand) > 0 && len(c.Mint) == 0 {
		r.errf(mintCommand.v, mintCommand.path, "names a command to mint with, and the credential names no mint: the names it mints from go under credential.mint")
		c.MintCommand = nil
	}
	return c
}

// credentialLogin reads a credential's sign-in steps: the shell lines
// that make the login, in order, and the one line that says what to
// paste where. Each is the pack's words for a person to run and read on
// their own machine, so each stands on one line, and the yard shows
// them and never runs them: a line with a break in it could forge
// another line of the card that shows it.
func (r *reader) credentialLogin(e entry) *CredentialLogin {
	login := &CredentialLogin{}
	var run, paste *entry
	for _, f := range r.entries(e.v, e.path, "run", "paste") {
		switch f.key {
		case "run":
			run = &f
			if f.v.Kind != yaml.SequenceNode || len(f.v.Content) == 0 {
				r.errf(f.v, f.path, "want a list of one or more shell lines, in order, got %s", describe(f.v))
				continue
			}
			for _, c := range f.v.Content {
				s, ok := r.str(c, f.path)
				if !ok {
					continue
				}
				if strings.TrimSpace(s) == "" || !oneLine(s) {
					r.errf(c, f.path, "a sign-in line is one non-blank line: it is shown on a card, never run by the yard")
					continue
				}
				login.Run = append(login.Run, s)
			}
		case "paste":
			paste = &f
			if s, ok := r.str(f.v, f.path); ok {
				if strings.TrimSpace(s) == "" || !oneLine(s) {
					r.errf(f.v, f.path, "what to paste is one non-blank line: it is shown on a card")
				} else {
					login.Paste = s
				}
			}
		}
	}
	if e.v.Kind == yaml.MappingNode {
		if run == nil {
			r.errf(e.v, join(e.path, "run"), "missing: the shell lines that make the login, in order")
		}
		if paste == nil {
			r.errf(e.v, join(e.path, "paste"), "missing: what to paste where, in one line")
		}
	}
	if len(login.Run) == 0 || login.Paste == "" {
		return nil
	}
	return login
}

// oneLine reports whether s stands on one line: no break, tab or other
// control in it, as a header is held to (credential's header above).
func oneLine(s string) bool {
	return !strings.ContainsFunc(s, func(c rune) bool { return c < ' ' || c == 0x7f })
}

// mintNames reads the names of the variables that together select a minted
// credential: a list of one or more, each a name of a variable, never a
// value, or a name with secret false (no secret) or bound true (one holder)
// beside it. env stays required beside them: it is the variable the minted
// token is handed under. It answers the names, the no-secret ones, and the
// bound ones.
func (r *reader) mintNames(e entry) (mint, notSecret, bound []string) {
	if e.v.Kind == yaml.SequenceNode && len(e.v.Content) == 0 {
		r.errf(e.v, e.path, "missing: the names of the variables that together select a minted credential")
		return nil, nil, nil
	}
	if e.v.Kind != yaml.SequenceNode {
		r.errf(e.v, e.path, "want a list ([a, b]), got %s", describe(e.v))
		return nil, nil, nil
	}
	add := func(n *yaml.Node, name string, secret, isBound bool) {
		if !paramName.MatchString(name) {
			r.errf(n, e.path, "%q is no name of a variable: these are the names the mint's inputs are under in the source's env file, never the values", name)
			return
		}
		if slices.Contains(mint, name) {
			r.errf(n, e.path, "%s is listed twice", name)
			return
		}
		mint = append(mint, name)
		if !secret {
			notSecret = append(notSecret, name)
		}
		if isBound {
			bound = append(bound, name)
		}
	}
	for _, c := range e.v.Content {
		if c.Kind == yaml.MappingNode {
			var name string
			var hasName bool
			secret, isBound := true, false
			for _, f := range r.entries(c, e.path, "name", "secret", "bound") {
				switch f.key {
				case "name":
					hasName = true
					if s, ok := r.str(f.v, f.path); ok {
						name = s
					}
				case "secret":
					if v, ok := r.boolean(f.v, f.path); ok {
						secret = v
					}
				case "bound":
					if v, ok := r.boolean(f.v, f.path); ok {
						isBound = v
					}
				}
			}
			if !hasName {
				r.errf(c, e.path, "missing: the name the mint's input is under in the source's env file")
				continue
			}
			if name == "" {
				continue
			}
			add(c, name, secret, isBound)
			continue
		}
		s, ok := r.str(c, e.path)
		if !ok {
			continue
		}
		add(c, s, true, false)
	}
	return mint, notSecret, bound
}
