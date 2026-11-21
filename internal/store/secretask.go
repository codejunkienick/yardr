// A secret ask is an ask whose answer is no words: its fields say where the
// person's value goes (GiveSecret), never into a note. The ask says where
// its answer goes, by destination and name; the answer says which names
// were given. Neither holds a value, and nothing else an ask has can take
// one: a pick, words and a decline's words are refused on a secret ask
// (answerable).
//
// A secret ask may ask for a person's own address in a source that tells
// people instead (AskSecret.User): the variable the source's notify key
// names, kept in that user's env file. It is the user's to give, or the
// operator's for them, as the settings that say where a user is told are
// (mayTell). Or it may ask for a crew member's own credential in the
// source (AskSecret.Crew), kept in that crew member's env file: the
// operator's alone to give, taking effect when the crew member next
// starts. Or it may ask for a user's own credential in the source
// (AskSecret.As), kept in that user's env file: the user's to give, or
// the operator's for them, as their address is.
// A waiting login's input (AskSecret.Login) is handed to it and kept
// nowhere; a harness API key (AskSecret.Harness) is kept in the operator's
// key file for its kind and read by sessions on that login.
package store

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"yardr.dev/yardr/internal/source"
)

// AskSecret says where the answer of a secret ask goes. For a source ask,
// Env names variables of the source's credential as its declaration stood
// when the ask was written: the asker may narrow those names, but names no
// path and no variable the declaration does not have.
//
// With Login instead of a source it is a login-input ask: the value goes to
// the harness login that waits under that reference (loginwait.go), and Env
// is the one name it is asked for.
//
// With Harness instead of a source it is a harness-key ask: the value is
// the API key of that harness kind, and goes to the operator's key file
// of the kind (HarnessKeyFile, through giveHarnessKey), which every
// session of the kind on the operator's login reads; Env holds variables
// of the kind's login_env. Crew is the crew member whose session the
// yard's led login found at the login screen, and whose journal it is
// asked on. No source declares it.
type AskSecret struct {
	Source string   `json:"source"`
	Env    []string `json:"env"`
	// Hook is the hook whose signing secret is asked for, "" for a
	// credential: Source is then the hook's source, Env the one name
	// the value is given under (hookSecretEnv), and the value goes to
	// the hook's secret file (HookSecretFile), never to an env file.
	Hook string `json:"hook,omitempty"`
	// Login is the waiting login the value goes to, the reference its
	// leader printed in its input need. Set instead of a source; with As,
	// the login is that user's own.
	Login string `json:"login,omitempty"`
	// Harness is the kind whose API key is asked for, as its harness
	// file names it. Set instead of a source, with Crew: the crew member
	// whose login screen the key is asked for, not the only reader of it.
	Harness string `json:"harness,omitempty"`
	// Host is where the source's calls go, the scheme and host of its base:
	// what the card says beside the source's name. It is kept on the ask
	// because the views render an ask from its note alone; GiveSecret
	// refuses when the source's calls go elsewhere by then.
	Host string `json:"host,omitempty"`
	// User makes it an ask for that user's own address in the source, where
	// the source reaches a person: Env is then the one variable the source's
	// notify key names, and the answer goes to the user's env file, not the
	// source's. "" asks for the source's credential.
	User string `json:"user,omitempty"`
	// Crew makes it an ask for that crew member's own credential in the
	// source, which a reply answers with: Env is then variables of the
	// source's credential, as for the source's own, and the answer goes
	// to the crew member's env file. "" asks for the source's
	// credential. Never set with User.
	Crew string `json:"crew,omitempty"`
	// As makes it an ask for that user's own credential in the source,
	// which a call made as them reads: Env is then the names such a call
	// mints or uses, every name under mint where the credential is
	// minted and the static ones otherwise unless the asker narrowed to
	// others, and the answer goes to the user's env file. "" asks for
	// the source's credential. Never set with User or Crew. With a Login
	// it is the user whose own login that is, and they give its input.
	As string `json:"as,omitempty"`
	// Concealed says whether the value is drawn hidden: false where the
	// source marks every asked name as no secret, absent (concealed) on
	// any other secret. It is kept on the ask because the views render an
	// ask from its note alone, as Host is.
	Concealed *bool `json:"concealed,omitempty"`
}

// Names are the variables asked for, in words.
func (s AskSecret) Names() string { return strings.Join(s.Env, " and ") }

// Line is what a card says a secret ask asks for, in the yard's words and
// never the asker's: whatever the question and its context say, the value
// becomes these variables of this source, of this user's file, or of this
// crew member's. Giving a user's address does not say the user is told
// through the source, so the line names the command that does. A crew
// member's running session does not see the new value, so its line says
// the env_file line is the one written and when it takes effect. A user's
// own credential is read by a call made as them, so its line says as
// much. A login input goes to the login that waits, and names it, and
// whose it is where it is a user's own. A
// harness key goes to the operator's key file of the kind, and its line
// says the reach the person consents to: every session of the kind that
// burns the operator's login reads it.
func (s AskSecret) Line() string {
	if s.Hook != "" {
		return fmt.Sprintf("a secret: the signing secret for the hook %s (POST /hooks/%s), for the source %s (%s)", s.Hook, s.Hook, s.Source, s.Host)
	}
	if s.Login != "" {
		whose := "the"
		if s.As != "" {
			whose = s.As + "'s"
		}
		return fmt.Sprintf("a secret: %s, for %s waiting login %s", s.Names(), whose, s.Login)
	}
	if s.Harness != "" {
		label := s.Harness
		if l, ok := HarnessLabels[s.Harness]; ok && l != "" {
			label = l
		}
		return fmt.Sprintf("a secret: %s, the API key %s runs with, asked for crew member %s; it is read by every %s session that burns the operator's login (the crew, groups without login_for, the operator's conversations), from its next start",
			s.Names(), label, s.Crew, label)
	}
	if s.User != "" {
		return fmt.Sprintf("a secret: %s, %s's own address in the source %s (%s); %s is told through %s once it is given and yardr user set %s %s=%s is run",
			s.Names(), s.User, s.Source, s.Host, s.User, s.Source, s.User, UserNotify, s.Source)
	}
	if s.Crew != "" {
		return fmt.Sprintf("a secret: %s, for crew member %s in the source %s (%s); the env_file line is the one written, and it takes effect when %s next starts (rotation or yardr crew restart %s)",
			s.Names(), s.Crew, s.Source, s.Host, s.Crew, s.Crew)
	}
	if s.As != "" && notifierSource(s.Source) {
		return fmt.Sprintf("a secret: %s, %s's own address in the source %s (%s), read by the yard at each message it sends %s through %s",
			s.Names(), s.As, s.Source, s.Host, s.As, s.Source)
	}
	if s.As != "" {
		return fmt.Sprintf("a secret: %s, %s's own credential for the source %s (%s), read as %s when a call is made --as %s",
			s.Names(), s.As, s.Source, s.Host, s.As, s.As)
	}
	return fmt.Sprintf("a secret: %s, for the source %s (%s)", s.Names(), s.Source, s.Host)
}

// SecretAsked is the secret an ask asks for, as a client shows it: the
// source, the names to ask the person for, the user whose own address it
// is (absent for a source's credential), the crew member whose own
// credential or harness key it is (absent for any other secret), the user
// whose own credential it is (absent for any other secret), the waiting
// login it goes to (absent for any other secret), the harness kind whose
// API key it is (absent for any other secret), the card's line whole, and
// what the value reaches (absent for a harness key and a waiting login).
type SecretAsked struct {
	Source string   `json:"source"`
	Env    []string `json:"env"`
	User   string   `json:"user,omitempty"`
	Crew   string   `json:"crew,omitempty"`
	As     string   `json:"as,omitempty"`
	Login  string   `json:"login,omitempty"`
	// Hook is the hook whose signing secret is asked for; absent on any other secret.
	Hook string `json:"hook,omitempty"`
	// Harness is the kind whose API key is asked for; absent on any other secret.
	Harness string   `json:"harness,omitempty"`
	Reach   []string `json:"reach,omitempty"`
	Line    string   `json:"line"`
}

// asked is the secret of the ask a as a client shows it, nil for an ask
// that asks for none.
func (a Ask) asked() *SecretAsked {
	if a.Secret == nil {
		return nil
	}
	return &SecretAsked{Source: a.Secret.Source, Env: a.Secret.Env, User: a.Secret.User, Crew: a.Secret.Crew, As: a.Secret.As, Login: a.Secret.Login, Hook: a.Secret.Hook, Harness: a.Secret.Harness, Reach: a.Secret.Reach(), Line: a.Secret.Line()}
}

// concealed is the ask's concealment as a client shows it: false where the
// source marks every asked name as no secret, nil (concealed) for a
// concealed secret and for an ask that asks for none.
func (a Ask) concealed() *bool {
	if a.Secret == nil {
		return nil
	}
	return a.Secret.Concealed
}

// sourceHost is where the calls of the source d declares go: the scheme and
// host of its base, or the variable whose value is the address, which is
// what the ask gives.
func sourceHost(d *source.Declaration) string {
	if d.Credential != nil && d.Credential.URL {
		return d.Address()
	}
	if host, ok := baseHost(d.Base); ok {
		return host
	}
	return d.Base
}

// concealedAsk is the ask's concealment for names of d: false where the
// source marks every asked name as no secret, nil (concealed) otherwise. Nil
// is what an ask from before the field stores, so an old and a concealed ask
// read the same.
func concealedAsk(d *source.Declaration, names []string) *bool {
	if d.Credential.Concealed(names) {
		return nil
	}
	f := false
	return &f
}

// askedSecret checks the secret an ask for the user to asks for, and
// answers it as the ask stores it: the source by its own name, the names in
// the declaration's order (the mint's inputs where the credential is
// minted, else every one of it, when the asker narrowed to none), and the
// host. It refuses a source the yard does not have, one whose declaration
// names no credential, a name the declaration does not have, and anyone but
// the operator as the one asked: giving is setting a source's credential,
// which is the operator's alone (maySource).
//
// With a User it is that user's own address in the source that is asked
// for (askedAddress), with a Crew that crew member's own credential in
// it (askedCrewSecret), and with an As that user's own credential in it
// (askedUserSecret). With a Login it is the waiting login's input
// (askedLogin), and with a Harness that crew member's harness API key
// (askedHarnessKey).
func (w *wtx) askedSecret(sec AskSecret, to string) (*AskSecret, error) {
	if strings.TrimSpace(sec.Login) != "" {
		return w.askedLogin(sec, to)
	}
	if strings.TrimSpace(sec.Harness) != "" {
		return w.askedHarnessKey(sec, to)
	}
	name := strings.TrimSpace(sec.Source)
	if name == "" {
		return nil, fmt.Errorf("a secret ask names the source the secret is for: --secret <source>")
	}
	var src Source
	if ok, err := get(w.Tx, bSources, name, &src); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("a secret is asked for a source the yard has (yardr source list), which the operator adds: source %s: %w", name, ErrNotFound)
	}
	d, err := src.Declaration()
	if err != nil {
		return nil, err
	}
	d = src.Resolved(d)
	if sec.Hook != "" {
		hook := strings.TrimSpace(sec.Hook)
		if hook == "" {
			return nil, fmt.Errorf("hook name %q: use lowercase letters, digits, - and _", sec.Hook)
		}
		return w.askedHookSecret(d, hook, sec, to)
	}
	if user := strings.TrimSpace(sec.User); user != "" {
		if crew := strings.TrimSpace(sec.Crew); crew != "" {
			return nil, fmt.Errorf("a secret ask names a user or a crew member, not both: --for-user %s or --for-crew %s", user, crew)
		}
		if as := strings.TrimSpace(sec.As); as != "" {
			return nil, fmt.Errorf("a secret ask names a user's address or a user's credential, not both: --for-user %s or --as %s", user, as)
		}
		return w.askedAddress(d, user, sec.Env, to)
	}
	if crew := strings.TrimSpace(sec.Crew); crew != "" {
		if as := strings.TrimSpace(sec.As); as != "" {
			return nil, fmt.Errorf("a secret ask names a crew member's credential or a user's, not both: --for-crew %s or --as %s", crew, as)
		}
		return w.askedCrewSecret(d, crew, sec.Env, to)
	}
	if as := strings.TrimSpace(sec.As); as != "" {
		return w.askedUserSecret(d, as, sec.Env, to)
	}
	if d.Credential == nil {
		return nil, fmt.Errorf("source %s: its declaration names no credential, so there is no secret to ask for", name)
	}
	has := d.Credential.Variables()
	for _, n := range sec.Env {
		if !slices.Contains(has, n) {
			return nil, fmt.Errorf("source %s: %s is no variable of its credential (its declaration names %s)", name, n, d.Credential.Names())
		}
	}
	// Without --env the ask is for what the source needs given: the
	// mint's inputs where the credential is minted, since the token is
	// minted per call and never given, else every variable of it. The
	// mint's names and the static ones are alternatives, not all
	// required, and --env names any of them instead.
	env := d.Credential.MintOrStatic()
	if len(sec.Env) > 0 {
		env = slices.DeleteFunc(slices.Clone(has), func(n string) bool { return !slices.Contains(sec.Env, n) })
	}
	ops, err := operatorsIn(w.Tx)
	if err != nil {
		return nil, err
	}
	switch {
	case ops.none():
		return nil, fmt.Errorf("a secret is the operator's to give, and this yard has none: yardr settings set operator <name>")
	case !ops.is(to):
		return nil, fmt.Errorf("a secret is %s to give (%s): setting a source's credential is theirs alone, so %s cannot be asked for one", ops.article(), ops, to)
	}
	return &AskSecret{Source: d.Name, Env: env, Host: sourceHost(d), Concealed: concealedAsk(d, env)}, nil
}

// hookSecretEnv is the one name a hook's signing secret is given under:
// the hook's name as a variable, so a giving typed as NAME=value lines
// names it and a card's "gave <name>" says which hook it was for.
func hookSecretEnv(hook string) string {
	name := strings.ToUpper(strings.NewReplacer("-", "_").Replace(hook))
	if name[0] >= '0' && name[0] <= '9' {
		name = "_" + name
	}
	return name + "_SIGNING_SECRET"
}

// askedHookSecret checks an ask for the signing secret of the hook hook
// of the source d declares, asked of to, and answers it as the ask
// stores it: the hook, the source by its own name, the one name the
// value is given under and the host. It refuses a hook name no hook
// may take, a source whose declaration has no hook key, a user beside
// the hook, and anyone but the operator as the one asked: giving is
// the operator's alone, as adding the hook is. A hook that stands is
// asked for only where it serves this source from the file the yard
// owns for it (HookSecretFile), which rotates its secret; a hook that
// serves another source, naming it, or names a custom file keeps the
// terminal refusal.
func (w *wtx) askedHookSecret(d *source.Declaration, hook string, sec AskSecret, to string) (*AskSecret, error) {
	switch {
	case strings.TrimSpace(sec.User) != "":
		return nil, fmt.Errorf("a hook's signing secret is the operator's to give, not %s's address: it takes no --for-user", strings.TrimSpace(sec.User))
	case strings.TrimSpace(sec.Crew) != "":
		return nil, fmt.Errorf("a hook's signing secret is the operator's to give, not crew member %s's credential: it takes no --for-crew", strings.TrimSpace(sec.Crew))
	case strings.TrimSpace(sec.As) != "":
		return nil, fmt.Errorf("a hook's signing secret is the operator's to give, not %s's credential: it takes no --as", strings.TrimSpace(sec.As))
	case !hookName.MatchString(hook):
		return nil, fmt.Errorf("hook name %q: use lowercase letters, digits, - and _", hook)
	case d.Hook == nil:
		return nil, fmt.Errorf("source %s: its declaration has no hook key, so the yard hears nothing from it and there is no signing secret to ask for", d.Name)
	case d.Hook.RegistersSecret():
		return nil, fmt.Errorf("source %s registers its webhook itself (call %s): the yard makes the signing secret for hook %s, and no secret is typed: ask the hook alone (the run ask hook add %s --source %s)", d.Name, d.Hook.Register.Call, hook, hook, d.Name)
	}
	var standing Hook
	had, err := get(w.Tx, bHooks, hook, &standing)
	if err != nil {
		return nil, err
	}
	switch {
	case had && standing.Source != d.Name:
		return nil, fmt.Errorf("hook %s is in the yard already: it serves the source %s, not %s", hook, standing.Source, d.Name)
	case had && standing.SecretFile != HookSecretFile(w.home, hook):
		return nil, fmt.Errorf("hook %s is in the yard already: replacing it, secret included, is the operator's to type in a terminal (yardr hook add %s --source %s --secret-file <file>)", hook, hook, d.Name)
	}
	ops, err := operatorsIn(w.Tx)
	if err != nil {
		return nil, err
	}
	switch {
	case ops.none():
		return nil, fmt.Errorf("a secret is the operator's to give, and this yard has none: yardr settings set operator <name>")
	case !ops.is(to):
		return nil, fmt.Errorf("a secret is %s to give (%s): giving a hook's signing secret is theirs alone, so %s cannot be asked for one", ops.article(), ops, to)
	}
	return &AskSecret{Source: d.Name, Env: []string{hookSecretEnv(hook)}, Host: sourceHost(d), Hook: hook}, nil
}

// askedLogin checks an ask for the input of the waiting login sec names,
// asked of to, and answers it as the ask stores it: the login's reference
// and the one name the input is asked for. It refuses a source or a user's
// address beside the login, a name beside the one, and anyone but the
// operator as the one asked: giving is handing the operator's login its
// answer, which is the operator's alone (maySource).
//
// With an As the login is that user's own, led for the group that carries it
// (yardr user add --login): the code the sign-in page shows is the person's
// to read, so the ask is theirs, or the operator's for them, as their own
// credential in a source is (askedUserSecret), and they give it (mayTell).
// Whether the login waits is GiveSecret's to say when the value comes, not
// the ask's: the wait may end between the two.
func (w *wtx) askedLogin(sec AskSecret, to string) (*AskSecret, error) {
	login := strings.TrimSpace(sec.Login)
	as := strings.TrimSpace(sec.As)
	switch {
	case strings.TrimSpace(sec.Source) != "":
		return nil, fmt.Errorf("a secret ask names a waiting login or a source, not both: --secret-login %s or --secret <source>", login)
	case strings.TrimSpace(sec.Harness) != "":
		return nil, fmt.Errorf("a secret ask names a waiting login or a harness, not both: --secret-login %s or an API key", login)
	case strings.TrimSpace(sec.User) != "":
		return nil, fmt.Errorf("a login input is the login's to take, not %s's address: it takes no --for-user", strings.TrimSpace(sec.User))
	case strings.TrimSpace(sec.Crew) != "":
		return nil, fmt.Errorf("a login input is the login's to take, not crew member %s's credential: it takes no --for-crew", strings.TrimSpace(sec.Crew))
	}
	for _, n := range sec.Env {
		if n != loginInputName {
			return nil, fmt.Errorf("a login input is asked for as %s, and %s is no name of it", loginInputName, n)
		}
	}
	ops, err := operatorsIn(w.Tx)
	if err != nil {
		return nil, err
	}
	switch {
	case ops.none():
		return nil, fmt.Errorf("a secret is the operator's to give, and this yard has none: yardr settings set operator <name>")
	case as == "":
		if !ops.is(to) {
			return nil, fmt.Errorf("a secret is %s to give (%s): answering the login is theirs alone, so %s cannot be asked for one", ops.article(), ops, to)
		}
		return &AskSecret{Env: []string{loginInputName}, Login: login}, nil
	}
	if _, err := getUser(w.Tx, as); err != nil {
		return nil, fmt.Errorf("a login input is asked for the login of a user of the yard (yardr user list): %w", err)
	}
	if to != as && !ops.is(to) {
		whose := fmt.Sprintf("the code that signs %s in is theirs to give", as)
		if !ops.is(as) {
			whose += ", or " + ops.whose() + " for them"
		}
		return nil, fmt.Errorf("%s, so %s cannot be asked for it", whose, to)
	}
	return &AskSecret{Env: []string{loginInputName}, Login: login, As: as}, nil
}

// OperatorLoginInput reports whether the ask is for the input of the
// operator's own waiting login and nothing else: the form askedLogin stores
// for a Login with no As, and the one the daemon asks for the code a
// harness's sign-in page shows (askInput in dispatch). The whole of the
// struct is compared with that form, so a field AskSecret gains later makes
// an ask not this one until this says it is. It is what the listener takes
// from the operator's device down the link on any yard, and on one that is not
// hosted the only secret it takes from it (rpc.UserServer.loginCode).
func (s AskSecret) OperatorLoginInput() bool {
	return s.Login != "" && reflect.DeepEqual(s, AskSecret{Env: []string{loginInputName}, Login: s.Login})
}

// askedHarnessKey checks an ask for the API key of the harness kind sec
// names, for the crew member whose session sits at its login screen, asked
// of to, and answers it as the ask stores it: the kind by its own name, the
// member, and the names in the file's order (every one of login_env when
// the asker narrowed to none). It refuses a kind the yard has no harness file
// for, one whose file names no login_env, a name the file does not name,
// no crew member at all, one the yard does not have, and anyone but the
// operator as the one asked: giving is setting what a session of the yard
// runs with, which is the operator's alone, as a crew member's credential
// in a source is.
func (w *wtx) askedHarnessKey(sec AskSecret, to string) (*AskSecret, error) {
	kind := strings.TrimSpace(sec.Harness)
	switch {
	case strings.TrimSpace(sec.Source) != "":
		return nil, fmt.Errorf("a secret ask names a harness or a source, not both: an API key or --secret <source>")
	case strings.TrimSpace(sec.User) != "":
		return nil, fmt.Errorf("a harness API key is a crew member's to run with, not %s's address: it takes no --for-user", strings.TrimSpace(sec.User))
	case strings.TrimSpace(sec.As) != "":
		return nil, fmt.Errorf("a harness API key is a crew member's to run with, not %s's credential: it takes no --as", strings.TrimSpace(sec.As))
	case strings.TrimSpace(sec.Hook) != "":
		return nil, fmt.Errorf("a harness API key is no hook's signing secret: it takes no hook")
	}
	if _, ok := HarnessLabels[kind]; !ok {
		return nil, fmt.Errorf("harness %s: the yard has no harness file for it (yardr harness list)", kind)
	}
	loginEnv := HarnessLoginEnv[kind]
	if len(loginEnv) == 0 {
		return nil, fmt.Errorf("harness %s: its file names no login_env, so there is no API key to ask for", kind)
	}
	crew := strings.TrimSpace(sec.Crew)
	if crew == "" {
		return nil, fmt.Errorf("a harness API key is asked for the crew member whose harness runs with it")
	}
	if w.Bucket(bCrew).Get([]byte(crew)) == nil {
		return nil, fmt.Errorf("a harness API key is asked for a crew member of the yard (yardr crew list): crew member %s: %w", crew, ErrNotFound)
	}
	for _, n := range sec.Env {
		if !slices.Contains(loginEnv, n) {
			return nil, fmt.Errorf("harness %s: %s is no variable of its API key (its file names %s)", kind, n, strings.Join(loginEnv, ", "))
		}
	}
	names := slices.Clone(loginEnv)
	if len(sec.Env) > 0 {
		names = slices.DeleteFunc(slices.Clone(loginEnv), func(n string) bool { return !slices.Contains(sec.Env, n) })
	}
	ops, err := operatorsIn(w.Tx)
	if err != nil {
		return nil, err
	}
	switch {
	case ops.none():
		return nil, fmt.Errorf("a secret is the operator's to give, and this yard has none: yardr settings set operator <name>")
	case !ops.is(to):
		return nil, fmt.Errorf("a secret is %s to give (%s): giving a harness API key is theirs alone, so %s cannot be asked for one", ops.article(), ops, to)
	}
	return &AskSecret{Harness: kind, Crew: crew, Env: names}, nil
}

// askedAddress checks an ask for the user's own address in the source d
// declares, asked of to, and answers it as the ask stores it: the one
// variable the source's notify key names. It refuses a source that reaches
// no person, a user the yard does not have, a name the notify key does not
// name, and anyone but that user and the operator as the one asked: where a
// user is told is theirs to say, or the operator's for them (mayTell).
func (w *wtx) askedAddress(d *source.Declaration, user string, env []string, to string) (*AskSecret, error) {
	if d.Notify == nil {
		return nil, fmt.Errorf("source %s: its declaration has no notify key, so it reaches no person and there is no address of %s's to ask for", d.Name, user)
	}
	if _, err := getUser(w.Tx, user); err != nil {
		return nil, fmt.Errorf("an address is asked for a user of the yard (yardr user list): %w", err)
	}
	for _, n := range env {
		if n != d.Notify.AddressEnv {
			return nil, fmt.Errorf("source %s: %s is not what it reads of a user's file (its notify key names %s)", d.Name, n, d.Notify.AddressEnv)
		}
	}
	ops, err := operatorsIn(w.Tx)
	if err != nil {
		return nil, err
	}
	if to != user && !ops.is(to) {
		whose := fmt.Sprintf("%s's own address in the source %s is theirs to give", user, d.Name)
		if !ops.none() && !ops.is(user) {
			whose += ", or " + ops.whose() + " for them"
		}
		return nil, fmt.Errorf("%s: what lands in their inbox goes there, so %s cannot be asked for it", whose, to)
	}
	return &AskSecret{Source: d.Name, Env: []string{d.Notify.AddressEnv}, Host: sourceHost(d), User: user}, nil
}

// askedCrewSecret checks an ask for the crew member's own credential in
// the source d declares, asked of to, and answers it as the ask stores
// it: the source by its own name, the names in the declaration's order
// (the mint's inputs where the credential is minted, else every one of
// it, when the asker narrowed to none), and the host. It refuses a source
// whose declaration names no credential, a crew member the yard does not
// have, a name the declaration does not have, and anyone but the operator
// as the one asked: giving is setting what a session of the yard runs
// with, which is the operator's alone.
func (w *wtx) askedCrewSecret(d *source.Declaration, crew string, env []string, to string) (*AskSecret, error) {
	if d.Credential == nil {
		return nil, fmt.Errorf("source %s: its declaration names no credential, so there is no secret to ask for", d.Name)
	}
	if w.Bucket(bCrew).Get([]byte(crew)) == nil {
		return nil, fmt.Errorf("a crew member's credential is asked for a crew member of the yard (yardr crew list): crew member %s: %w", crew, ErrNotFound)
	}
	has := d.Credential.Variables()
	for _, n := range env {
		if !slices.Contains(has, n) {
			return nil, fmt.Errorf("source %s: %s is no variable of its credential (its declaration names %s)", d.Name, n, d.Credential.Names())
		}
	}
	// Without --env the ask is for what the source needs given, as a
	// source's own credential is: the mint's inputs where it is minted,
	// else every variable of it.
	names := d.Credential.MintOrStatic()
	if len(env) > 0 {
		names = slices.DeleteFunc(slices.Clone(has), func(n string) bool { return !slices.Contains(env, n) })
	}
	ops, err := operatorsIn(w.Tx)
	if err != nil {
		return nil, err
	}
	switch {
	case ops.none():
		return nil, fmt.Errorf("a secret is the operator's to give, and this yard has none: yardr settings set operator <name>")
	case !ops.is(to):
		return nil, fmt.Errorf("a secret is %s to give (%s): giving a crew member's credential is theirs alone, so %s cannot be asked for one", ops.article(), ops, to)
	}
	return &AskSecret{Source: d.Name, Env: names, Host: sourceHost(d), Crew: crew, Concealed: concealedAsk(d, names)}, nil
}

// askedUserSecret checks an ask for the user's own credential in the
// source d declares, asked of to, and answers it as the ask stores it:
// the source by its own name, the names in the declaration's order (every
// name under mint where the credential is minted, else the static ones,
// when the asker narrowed to none), and the host. It refuses a source
// whose declaration names no credential, a user the yard does not have,
// a name the declaration does not have, and anyone but that user and the
// operator as the one asked: a user's own credential is theirs to give,
// or the operator's for them, as their address is (mayTell).
func (w *wtx) askedUserSecret(d *source.Declaration, user string, env []string, to string) (*AskSecret, error) {
	if d.Credential == nil {
		return nil, fmt.Errorf("source %s: its declaration names no credential, so there is no secret to ask for", d.Name)
	}
	if _, err := getUser(w.Tx, user); err != nil {
		return nil, fmt.Errorf("a user's credential is asked for a user of the yard (yardr user list): %w", err)
	}
	has := d.Credential.Variables()
	for _, n := range env {
		if !slices.Contains(has, n) {
			return nil, fmt.Errorf("source %s: %s is no variable of its credential (its declaration names %s)", d.Name, n, d.Credential.Names())
		}
	}
	names := d.Credential.MintOrStatic()
	if len(env) > 0 {
		names = slices.DeleteFunc(slices.Clone(has), func(n string) bool { return !slices.Contains(env, n) })
	}
	ops, err := operatorsIn(w.Tx)
	if err != nil {
		return nil, err
	}
	if to != user && !ops.is(to) {
		whose := fmt.Sprintf("%s's own credential in the source %s is theirs to give", user, d.Name)
		if !ops.none() && !ops.is(user) {
			whose += ", or " + ops.whose() + " for them"
		}
		return nil, fmt.Errorf("%s: a call made as them reads it, so %s cannot be asked for it", whose, to)
	}
	return &AskSecret{Source: d.Name, Env: names, Host: sourceHost(d), As: user, Concealed: concealedAsk(d, names)}, nil
}
