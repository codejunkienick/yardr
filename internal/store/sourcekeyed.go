package store

import (
	"fmt"
	"strings"
)

// SourceKeyed is what KeySource, KeyUser, KeyUserCredential or GiveSecret
// did: the names it wrote, the file they are in, and what the yard can say
// of those names without showing a value. A source credential has the state
// yardr source show reports; a hook signing secret says it is set; an
// address, credential for a user or crew, or harness key says which names
// are set. A giving to a waiting login holds the login's reference instead
// of a file.
type SourceKeyed struct {
	Source *Source  `json:"source"`
	Given  []string `json:"given"`
	File   string   `json:"file"`
	// Hook is the hook whose signing secret was given; set instead of a
	// credential's state, with File the hook's secret file.
	Hook string `json:"hook,omitempty"`
	// Google is the Google account whose sign-in was given, with File
	// its credentials file and As the user whose own account it is
	// ("" for the yard's own); set instead of a credential's state.
	Google string `json:"google,omitempty"`
	// Login is the waiting login the value was handed to; set instead of a
	// file, and "" for a source's credential or a user's address.
	Login string `json:"login,omitempty"`
	// User is whose own address in the source was given, and whose file
	// File is; with Harness, whose login's key file File is. It is "" for
	// a source's credential and for a user's own credential (As). Next is
	// then the command that comes after it, in
	// a line: the one that tells the user through the source, or once
	// that is said the one that tries it.
	User string `json:"user,omitempty"`
	// Crew is whose own credential in a source was given, and
	// whose file File is; "" for a source's credential, a harness API key,
	// and a user's address. Next is then when the value takes effect, in a
	// line: the running session does not see it, so it is used when the next
	// one starts.
	Crew string `json:"crew,omitempty"`
	// Harness is the kind whose API key was given, and User then whose
	// login's key file of the kind File is (HarnessKeyFile). Next is when
	// it takes effect, in a line: as each session on that login next
	// starts.
	Harness string `json:"harness,omitempty"`
	// As is whose own credential in the source was given, and whose file
	// File is; "" for a source's credential and for a user's address.
	// Next is then the command that tries it, in a line: the source's
	// check, made as that user.
	As   string `json:"as,omitempty"`
	Next string `json:"next,omitempty"`
	// State is the yard's description of what the giving set: CredentialState
	// for a source credential, a setting sentence for another file-backed
	// secret. Missing is what a source still wants when the names given are
	// not all it needs, in the words of its error.
	State   string `json:"credential,omitempty"`
	Missing string `json:"missing,omitempty"`
}

// keyed is what a giving that was committed did, with the credential's
// state read as a call would read it, after the write: the words never
// hold a value.
func (k *sourceKey) keyed(home string) *SourceKeyed {
	out := &SourceKeyed{Source: k.src, Given: k.names, File: k.file, Hook: k.hook}
	if k.google != "" {
		out.Google, out.As = k.google, k.googleUser
		out.State = fmt.Sprintf("the sign-in for the Google account %s is set in %s", k.google, k.file)
		out.Next = "google-mint reads it on the next Google call"
		if k.googleUser != "" {
			out.Next = fmt.Sprintf("google-mint reads it on the next Google call made --as %s", k.googleUser)
		}
		return out
	}
	if k.hook != "" {
		out.State = fmt.Sprintf("the signing secret for the hook %s is set in %s", k.hook, k.file)
		return out
	}
	if u := k.user; u != nil {
		if k.as {
			out.As = u.Name
			out.State = fmt.Sprintf("%s is set in %s", strings.Join(k.names, " and "), k.file)
			out.Next = fmt.Sprintf("yardr source check %s --as %s", k.src.Name, u.Name)
			if notifierSource(k.src.Name) {
				// The value is the address the yard tells the user at,
				// not a credential a check made as them reads.
				out.Next = fmt.Sprintf("yardr user set %s %s=%s tells %s beside their phone through it", u.Name, UserWebhook, k.src.Name, u.Name)
				if u.Webhook == k.src.Name {
					out.Next = fmt.Sprintf("the yard sends %s's messages through %s from the file at each send", u.Name, k.src.Name)
				}
			}
			return out
		}
		out.User = u.Name
		out.State = fmt.Sprintf("%s is set in %s", strings.Join(k.names, " and "), k.file)
		out.Next = fmt.Sprintf("yardr source check %s --user %s sends one test message", k.src.Name, u.Name)
		if u.Notify != k.src.Name {
			out.Next = fmt.Sprintf("yardr user set %s %s=%s tells %s through it", u.Name, UserNotify, k.src.Name, u.Name)
		}
		return out
	}
	if h := k.harness; h != nil {
		out.Harness, out.User = h.Kind, k.login
		label := h.Kind
		if h.Label != "" {
			label = h.Label
		}
		out.State = fmt.Sprintf("%s is set in %s", strings.Join(k.names, " and "), k.file)
		out.Next = fmt.Sprintf("every %s session that burns %s's login reads it from its next start", label, k.login)
		return out
	}
	if c := k.crew; c != nil {
		out.Crew = c.Name
		out.State = fmt.Sprintf("%s is set in %s", strings.Join(k.names, " and "), k.file)
		out.Next = fmt.Sprintf("it takes effect when %s next starts (rotation or yardr crew restart %s)", c.Name, c.Name)
		return out
	}
	if d, err := k.src.Declaration(); err == nil {
		out.State, err = d.CredentialState(k.src.EnvFile, d.MintArgv(home, k.src.MintCommand), ReadEnvFileEntries)
		if err != nil {
			out.Missing = err.Error()
		}
	}
	return out
}

// sourceKey is one giving of a source's credential, as keySource left it,
// of a user's own address in the source, as keyUser did (user is then
// set), of a user's own credential in it, as keyUser did for one (user
// and as are then set), of a crew member's own credential in a source
// (crew is then set), or of a harness API key (harness is then set). It
// outlives the transaction: a batch may run any of them twice, and renamed
// stays true once any run renamed the file.
type sourceKey struct {
	src  *Source
	user *User
	// as is set for a user's own credential in the source rather than
	// their address: keyed then says whose credential and the command
	// that tries it.
	as   bool
	crew *CrewMember
	// harness is the kind whose API key was given, and login the user
	// whose key file of the kind File is; nil for any other giving.
	harness *Harness
	login   string
	// hook is the hook whose signing secret was given, "" for a
	// credential: File is then the hook's secret file.
	hook string
	// google is the Google account whose sign-in was given, and
	// googleUser the user whose own account it is ("" for the yard's
	// own): File is then the account's credentials file.
	google     string
	googleUser string
	names      []string
	file       string
	renamed    bool
}
