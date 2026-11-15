package source

import (
	"fmt"
	"regexp"
	"strings"
)

// curl401 is curl's line for a 401, ending where the number does.
var curl401 = regexp.MustCompile(`returned error: 401\b`)

// Refused401 reports whether text, the words of a command that failed (they
// end with its stderr), say that the service the command called answered 401.
// curl, which the shipped commands call their services with, says so under
// --fail and --fail-with-body: "The requested URL returned error: 401". Only
// 401 is told: a 403, a 429 and every other failure keep the words they have.
func Refused401(text string) bool {
	return curl401.MatchString(text)
}

// Refusal is what the words of a 401 say beside the declaration's own keys.
type Refusal struct {
	// Kind is read or update for a declared call, whose kind says which
	// variable of the credential it was sent with; "" for a thread command,
	// which sends the one.
	Kind string
	// Header says to name the header the declaration sends the credential in:
	// for a request the yard made itself. A command that calls the service
	// builds its own header, so none is named for it.
	Header bool
	// Minted says that a token minted from the mint's inputs was sent, not a
	// static key.
	Minted bool
	// From says where the credential was read from, after "as": whose file
	// and which, or the environment a process was started with.
	From string
	// Fix is the one line that sets it right, after "to fix:".
	Fix string
}

// RefusedWords are the words of a service's 401, to put before the failure's
// own: which source refused, what was sent (the variable, or the inputs a
// token was minted from), where it was read from as the caller says, how a
// call carried it, and the one line that sets it right. They hold no value.
// "" for a source that names no credential.
func (d *Declaration) RefusedWords(r Refusal) string {
	if d.Credential == nil {
		return ""
	}
	sent := d.Credential.EnvOf(r.Kind)
	if r.Minted {
		sent = "the token minted from " + strings.Join(d.Credential.Mint, " and ")
	}
	words := fmt.Sprintf("%s refused the credential (401): %s, %s", d.Name, sent, r.From)
	if r.Header && d.Credential.Header != "" {
		words += ", sent as " + strings.ReplaceAll(d.Credential.Header, "{credential}", "<credential>")
	}
	return words + "; to fix: " + r.Fix
}
