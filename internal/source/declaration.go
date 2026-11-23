// Package source reads the declaration of a service outside the yard, one
// YAML file that names the service's address, the credential it takes and
// the requests the yard may make of it, and makes one such request (Do).
// A service is added by its file and no Go: what the file cannot say (a
// request that must be signed, an answer in pages, anything but JSON or a
// file upload over HTTPS) is not a declared source.
//
// One thing a file can say is no request: a command the yard runs. The
// command that tells what a harness kind's subscription still carries (the
// key limits) is the one that is run today; internal/limits runs it. A file
// with that key may have no calls at all. The thread and link keys say how
// the yard speaks in the source's threads and how its issues are a bead's
// link — thread's by one command per thing the yard does there, run by
// internal/hooks. The hook key says what the service sends in: how the
// core verifies its webhooks, and the command that turns a verified body
// into the envelope. The events key says what the source sends in about
// pull requests and CI, and the commands that read them back: the daemon
// judges CI runs against the pull request's head, and reads the pull
// request and its CI live when no event told what it needs. The git key
// names the credential helper git runs for the source's host, for a depot
// whose origin is there: the yard copies it with the declaration's other
// command files and never runs it.
//
// The file holds no secret. It names the variable the credential is in, and
// the value is read from a file of the yard's at each call, so a declaration
// can be kept in the store, shown and copied. A service whose address is its
// secret, a webhook URL, names the variable that holds the address
// (credential.url), and the file names no base.
package source

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// BuiltIn are the names a declaration may not take: the sources yardr has in Go (hooks.Adapters), but for one whose pack has taken the name while its Go adapter is still compiled in, its own name for itself (store.SourceYardr), and its name for the yards it is linked with (store.SourcePeer); the pack's source then wins every lookup when the yard has it, and a test in hooks holds this list against the adapters.
var BuiltIn = []string{"peer", "token", "yardr"}

// MaxDeclaration bounds a declaration file: it is a list of requests, read whole at every use.
const MaxDeclaration = 256 << 10

// The kinds of a call: an update changes something in the service; a read does not, whatever its method (a search by POST, GraphQL).
const (
	Read   = "read"
	Update = "update"
)

// CredentialMark is where the credential's value goes: in the credential's header, or as a whole leaf of a body; never in a path or a query, which end up in the logs of every proxy and server on the way.
const CredentialMark = "{credential}"

// Declaration is a source as its file declares it.
type Declaration struct {
	Name  string `json:"name"`
	About string `json:"about,omitempty"`
	// Base is where every call's path is under, without a trailing slash.
	// Empty for a source that declares no call, which has a Limits, and
	// for one whose credential is the address (Credential.URL).
	Base string `json:"base"`
	// Bases names the regions' bases beside Base, which stays the
	// default: name -> URL, without a trailing slash. A source setting
	// base names one of them, "" for Base; each is held to Base's
	// checks. Empty for a service that runs in one region.
	Bases map[string]string `json:"bases,omitempty"`
	// BaseTemplate is the address a host the operator types fills, for a
	// service they may run on a machine of their own: https://{host} and
	// the path every call's path is under, without a trailing slash. Base
	// stays the default; a source set to a host sends its calls to the
	// filled address instead (WithHost). Empty for a service that is only
	// where Base says.
	BaseTemplate string `json:"base_template,omitempty"`
	// PathTemplate is the address a site the operator types fills, one path
	// segment of it, for a service that scopes its calls by site:
	// https://host/path/{site}/rest, without a trailing slash. Base stays
	// the default; a source set to a site sends its calls to the filled
	// address instead (WithSite), and its credential is keyed to the host
	// and that segment together, so one site's credential is never sent to
	// another's. Empty for a service that scopes nothing by site.
	PathTemplate string      `json:"path_template,omitempty"`
	Credential   *Credential `json:"credential,omitempty"`
	// Check names the call `yardr source check` makes: a read call that
	// needs no parameter, sent as a request or answered by its run command
	// (DoCheck). Empty when the file names none.
	Check string `json:"check,omitempty"`
	// Idempotency is the name of the header the service takes a key in, by
	// which it tells an update it was sent before from a new one; "" for a
	// service that takes none. Whoever makes an update call gives the key
	// (DoKeyed): the same for a call run again after one whose outcome was
	// lost, so that it writes once.
	Idempotency string `json:"idempotency,omitempty"`
	Calls       []Call `json:"calls"` // in file order
	// Notify is the call the yard makes to tell a person outside the yard,
	// nil for a source that cannot: the capability a user names a source
	// for (yardr user set <name> notify=<source>).
	Notify *Notify `json:"notify,omitempty"`
	// Hear is which deliveries of the source the yard keeps, nil for a
	// source that keeps every delivery it verifies: one whose envelope
	// field holds an address a user's env file sets.
	Hear *Hear `json:"hear,omitempty"`
	// Limits is the command that says what a harness kind's subscription
	// still carries, nil for a source that cannot: see internal/limits.
	Limits *Limits `json:"limits,omitempty"`
	// Thread is how the yard speaks in the source's threads: a command per
	// thing it does there, nil for a source the yard does not speak in.
	Thread *Thread `json:"thread,omitempty"`
	// Link is the source's issues as a bead's link, nil for a source whose
	// issues are none.
	Link *Link `json:"link,omitempty"`
	// Identity is who a crew member is in the source, as the declaration
	// says it, nil for a source whose keys keep their case.
	Identity *Identity `json:"identity,omitempty"`
	// Hook is what the source sends in: how the core verifies its
	// webhooks and the command that turns a verified body into the
	// envelope, nil for a source the yard hears nothing from.
	Hook *Hook `json:"hook,omitempty"`
	// Events is what the source sends in about pull requests and CI, and
	// the commands that read them back, nil for a source without either.
	Events *Events `json:"events,omitempty"`
	// Git is what git uses to reach the source's repositories, nil for a
	// source whose repositories git reaches without the yard: the
	// credential helper git runs, which the yard copies and never runs.
	Git *Git `json:"git,omitempty"`
	// Needs are the programs the source's commands need on the machine
	// the yard runs on, in file order: each names the program, the
	// variable that may name another copy of it, and the install that
	// gets it. The yard reports what is missing and installs nothing
	// by itself; yardr pack install runs an install, in a terminal or
	// on a run card the operator approves.
	Needs []Need `json:"needs,omitempty"`

	// filledSite is the segment WithSite filled into Base, "" where none
	// was: it is the site a credential of this copy is keyed to, and is
	// never in the file, so it is no part of the declaration as read.
	filledSite string
}

// Need is one program a declaration needs on the machine: the program
// a command of the source runs (gws for the Google sources), the
// variable that may name another path to it, checked first, and the
// install that gets it, an argv run without a shell like a mint
// command. An install whose program is a file beside the declaration
// is carried and shown whole, as every command file is; an absolute
// one is the operator's own and stays in a terminal.
type Need struct {
	Program string   `json:"program"`
	Env     string   `json:"env,omitempty"`
	Install []string `json:"install"`
}

// Credential is where a source's credential comes from and where it goes.
type Credential struct {
	// Env is the name of the variable that holds it (never the value).
	Env string `json:"env"`
	// Header is the header it is sent in, "Name: text {credential}", for a
	// service that takes it there. One that takes it in the body names it
	// as a leaf of the call's body instead.
	Header string `json:"header,omitempty"`
	// UpdateEnv is the name of the variable an update call is sent with,
	// in the same file, so reads can run on a read-only credential.
	// Empty for a source with one credential, used for updates too.
	UpdateEnv string `json:"update_env,omitempty"`
	// Mint are the names of the variables that together select a minted
	// credential: when the acting env sets every one, the core runs the
	// source's mint_command and hands what it mints to the call under Env.
	// Names only, never values. Empty where the credential is used as set.
	Mint []string `json:"mint,omitempty"`
	// NotSecret are the mint's inputs that are no secret (an account's
	// address, not a key): a secret ask narrowed to these alone is asked
	// in the clear, with concealed false. A name the file marks with
	// secret false beside it; every other variable of the credential is a
	// secret, and a static token always is.
	NotSecret []string `json:"not_secret,omitempty"`
	// Bound are the mint's inputs that belong to one holder (an account's
	// address, held by one user or the yard), marked with bound true
	// beside the name. Who holds one is the store's to answer.
	Bound []string `json:"bound,omitempty"`
	// MintCommand is the command the declaration brings to mint with: a
	// program beside the declaration's, which the yard copies and hashes,
	// and its arguments. Used where the source's mint_command setting is
	// unset (the operator's wins); empty where the declaration brings none.
	MintCommand []string `json:"mint_command,omitempty"`
	// InPath says the service takes the credential in the request path,
	// so a call's path may name {credential}. It is said out loud because
	// a credential there is in the logs of every server on the way;
	// without it a path refuses one. A query refuses one either way.
	InPath bool `json:"in_path,omitempty"`
	// URL says the credential is the address every call goes to: a
	// webhook URL whose path is its secret (Zapier, Make, n8n). The file
	// then names no base and a call has no path of its own; no error,
	// event or command it is handed to repeats any of the address.
	URL bool `json:"url,omitempty"`
	// Login are the pack's own sign-in steps for this credential, nil
	// where the file names none: the shell lines that make the login, in
	// order, and what to paste where. A card shows them as the pack's
	// words and never runs them; the value itself still travels the
	// secret path, so they carry no secret and name no value.
	Login *CredentialLogin `json:"login,omitempty"`
}

// CredentialLogin is a credential's sign-in as its pack words it: the
// shell lines a person runs on their own machine to make the login, in
// order, and the one line that says what to paste into which card. The
// yard shows both and runs neither.
type CredentialLogin struct {
	Run   []string `json:"run"`
	Paste string   `json:"paste"`
}

// Minted reports whether the credential may be minted (nil-safe): the file names the variables a mint takes.
func (c *Credential) Minted() bool { return c != nil && len(c.Mint) > 0 }

// EnvOf is the name of the variable a call of kind is sent with.
func (c *Credential) EnvOf(kind string) string {
	if kind == Update && c.UpdateEnv != "" {
		return c.UpdateEnv
	}
	return c.Env
}

// Envs are the names of the variables the source's env file sets: the credential's, and the one of its updates where
// that is another.
func (c *Credential) Envs() []string {
	if c.UpdateEnv != "" {
		return []string{c.Env, c.UpdateEnv}
	}
	return []string{c.Env}
}

// Names says in words what an env file sets for the credential: the static
// names, or with a mint, the mint's names or the static ones, the mint first,
// since it is what a call uses when both are set.
func (c *Credential) Names() string {
	if !c.Minted() {
		return strings.Join(c.Envs(), " and ")
	}
	return strings.Join(c.Mint, " and ") + ", or " + strings.Join(c.Envs(), " and ")
}

// Variables are the names an env file may set for the credential, every one the declaration has: the mint's inputs
// first, then the static ones.
func (c *Credential) Variables() []string {
	if c == nil {
		return nil
	}
	return append(append([]string{}, c.Mint...), c.Envs()...)
}

// MintOrStatic are the names a user's own credential goes by unprompted, the
// mint's inputs where minted else the static ones: a call mints from the former
// and falls back to the latter, and an ask asks for these unless --env names others.
func (c *Credential) MintOrStatic() []string {
	if c == nil {
		return nil
	}
	if c.Minted() {
		return append([]string{}, c.Mint...)
	}
	return c.Envs()
}

// Concealed reports whether a secret ask for names is drawn hidden: false where every name is one the file marks as
// no secret, true for a mixed or secret one. Nil-safe, for a file that names no credential.
func (c *Credential) Concealed(names []string) bool {
	if c == nil || len(names) == 0 {
		return true
	}
	for _, n := range names {
		if !slices.Contains(c.NotSecret, n) {
			return true
		}
	}
	return false
}

// Call is one request the yard may make of the source, or one command it may
// run in place of a request.
type Call struct {
	Name    string  `json:"name"`
	Kind    string  `json:"kind"`
	About   string  `json:"about,omitempty"`
	Params  []Param `json:"params,omitempty"` // in file order
	Request Request `json:"request"`
	// Build is the command that prints the request's body, instead of
	// Request.Body. Its program is always a file beside the declaration's.
	Build []string `json:"build,omitempty"`
	// Run is the command that answers the call, for a service no request
	// reaches: the call has no Request and no Build then, and is a read.
	// Its program is always a file beside the declaration's, as Build's is.
	Run    []string `json:"run,omitempty"`
	Answer Answer   `json:"answer"`
}

// Param is one parameter of a call. All are required unless Optional or Default is set.
type Param struct {
	Name string `json:"name"`
	// Type is string, int, bool, enum, file or secret-file: a secret-file's
	// value names a file the yard reads the secret from at call time.
	Type    string `json:"type"`
	Pattern string `json:"pattern,omitempty"` // of a string: a regular expression it must match
	// Max bounds a value: of a string its length at most, in characters;
	// of a file or a secret-file its size at most, in bytes.
	Max      int      `json:"max,omitempty"`
	Values   []string `json:"values,omitempty"` // of an enum: what it may be
	Optional bool     `json:"optional,omitempty"`
	// Default is the typed JSON value sent when the caller leaves the
	// parameter out. Its raw form preserves false and zero in JSON.
	Default json.RawMessage `json:"default,omitempty"`
	// Log keeps the parameter's value in the record of each call (the
	// source_call event), which otherwise has its name alone: for what an
	// audit asks by (a customer's id), and never for free text.
	Log bool `json:"log,omitempty"`

	re *regexp.Regexp
}

// Request is what a call sends. A {param} in Path, Query or Body is
// replaced by the parameter's value, escaped by where it stands.
type Request struct {
	Method string       `json:"method"`
	Path   string       `json:"path"`
	Query  []QueryValue `json:"query,omitempty"` // in file order
	// Body is sent as JSON: maps, lists and scalars as the file has them.
	// A string that is exactly {param} becomes the parameter's typed value;
	// of a secret-file, the secret its file holds, which stands in no
	// parameter, card or record. A body that names a file parameter is
	// sent as a multipart upload instead: each file parameter one file
	// part, every other value a text field.
	Body any `json:"body,omitempty"`
}

// QueryValue is one key of a request's query.
type QueryValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Answer is what of the service's answer a call returns.
type Answer struct {
	// Select keeps top-level keys or dotted object paths as flat keys, of each element for a list (exact keys win); else the answer stays whole.
	Select []string `json:"select,omitempty"`
	// Errors keeps these dot paths of a refused answer's JSON object, rebuilt nested; without it a refusal keeps nothing of what was said.
	Errors []string `json:"errors,omitempty"`
	// File says the answer is a file's bytes, not JSON: a read request call only, downloaded beside Do under the caller's own bound, never Max.
	File bool `json:"file,omitempty"`
	// Max is the most a call returns, in bytes after Select: AnswerMax unless the file raises it, AnswerLimit at most.
	Max int `json:"max"`
}

// Address is where the source's calls go, in words: its base, or for a credential that is the address itself the
// variable it is in, since the address is the secret and the file holds none of it. Empty for a source that declares
// no call.
func (d *Declaration) Address() string {
	if d.Credential != nil && d.Credential.URL {
		return "the URL in " + d.Credential.Env
	}
	return d.Base
}

// Call returns the call of that name.
func (d *Declaration) Call(name string) (*Call, bool) {
	for i := range d.Calls {
		if d.Calls[i].Name == name {
			return &d.Calls[i], true
		}
	}
	return nil, false
}

func (c *Call) param(name string) (*Param, bool) {
	if c == nil {
		return nil, false
	}
	for i := range c.Params {
		if c.Params[i].Name == name {
			return &c.Params[i], true
		}
	}
	return nil, false
}

// usesParam reports whether the request sends name in its path, query or
// body. Declaring a parameter alone does not put its value in the request.
func (c *Call) usesParam(name string) bool {
	mark := "{" + name + "}"
	if strings.Contains(c.Request.Path, mark) {
		return true
	}
	for _, q := range c.Request.Query {
		if strings.Contains(q.Value, mark) {
			return true
		}
	}
	var inBody func(any) bool
	inBody = func(v any) bool {
		switch v := v.(type) {
		case string:
			return strings.Contains(v, mark)
		case map[string]any:
			for _, child := range v {
				if inBody(child) {
					return true
				}
			}
		case []any:
			for _, child := range v {
				if inBody(child) {
					return true
				}
			}
		}
		return false
	}
	return inBody(c.Request.Body)
}

// Line is the call in a line: "POST /messages.json".
func (r Request) Line() string { return r.Method + " " + r.Path }

// Line is the call as one line wherever it is shown: its request, or the
// command that answers it.
func (c Call) Line() string {
	if len(c.Run) > 0 {
		return "runs " + argvLine(c.Run)
	}
	return c.Request.Line()
}

// Error is one thing wrong with a declaration: the file, the line and the key as a dotted path from the file's root.
type Error struct {
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
	Key  string `json:"key,omitempty"`
	Msg  string `json:"error"`
}

func (e Error) Error() string {
	out := e.File
	if e.Line > 0 {
		out += ":" + strconv.Itoa(e.Line)
	}
	if e.Key != "" {
		out += ": " + e.Key
	}
	return out + ": " + e.Msg
}

// Errors is everything wrong with a declaration, in file order: it is read to its end so one run names every mistake.
type Errors []Error

func (es Errors) Error() string {
	lines := make([]string, len(es))
	for i, e := range es {
		lines[i] = e.Error()
	}
	return strings.Join(lines, "\n")
}

var (
	// validName is what a source and a call may be named: each becomes a key in the store and a word on a command line.
	validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	// paramName is also the name of a variable, for the credential's.
	paramName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// mark is a {name} in a path, a query or a body.
	mark       = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	headerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
	// hookKey is a key a hook's events may carry in their fields, as a subscription's --where names it.
	hookKey = regexp.MustCompile(`^[a-z0-9][a-z0-9_]*$`)
)

var (
	methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	types   = []string{"string", "int", "bool", "enum", "file", "secret-file"}
)

// booleans are the plain scalars YAML 1.1 reads as a boolean (see internal/pack, whose reader this one follows): a key
// or a word that is one would be a name to this reader and a boolean to the next.
var booleans = []string{"y", "yes", "n", "no", "on", "off", "true", "false"}

func booleanLike(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && (n.Tag == "!!bool" || (n.Style == 0 && slices.Contains(booleans, strings.ToLower(n.Value))))
}
