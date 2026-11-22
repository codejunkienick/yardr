package store

import (
	"fmt"
	"slices"
	"strings"

	"yardr.dev/yardr/internal/source"
)

// A credential's sign-in steps (source.CredentialLogin) are the pack's
// words for making its login: a setup card shows them at its account
// step and a secret card for the same credential shows them above its
// field, so the person runs the pack's exact commands and pastes where
// the pack says. The yard shows them only and never runs them, and the
// value itself still travels the secret path as it always has.

// loginStepLines are login's steps as a card carries them: the run lines
// in order, marked as the pack's and as shown only, then the paste line
// marked as the pack's too so neither line can read as the yard's own
// sentence or start with the yard's weight mark. Each line is kept to
// one terminal-safe line, as the card keeps other source text
// (safeSourceCardLine).
func loginStepLines(run []string, paste string) []string {
	var lines []string
	if len(run) > 0 {
		lines = append(lines, safeSourceCardLine("the pack's sign-in, shown only and never run by the yard: run "+strings.Join(run, "; then ")))
	}
	if paste != "" {
		lines = append(lines, safeSourceCardLine("the pack's sign-in: "+paste))
	}
	return lines
}

// LoginLines are the secret ask's sign-in steps as its card carries
// them, nil where its credential declares none: then the card reads
// exactly as it did before a declaration could name them.
func (s AskSecret) LoginLines() []string { return loginStepLines(s.LoginRun, s.LoginPaste) }

// LoginLines are the shown secret's sign-in steps, as AskSecret's.
func (s SecretAsked) LoginLines() []string {
	return loginStepLines(s.LoginRun, s.LoginPaste)
}

// withLogin sets a secret ask's sign-in steps from the declaration of
// the source it asks for, where that source's credential declares
// them, and answers the ask: a credential without them leaves the ask
// exactly as it was.
func withLogin(sec *AskSecret, d *source.Declaration) *AskSecret {
	if d == nil || d.Credential == nil {
		return sec
	}
	return withLoginSteps(sec, d.Credential.Login)
}

// withLoginSteps sets a secret ask's sign-in steps from login, where
// one stands, and answers the ask.
func withLoginSteps(sec *AskSecret, login *source.CredentialLogin) *AskSecret {
	if login != nil {
		sec.LoginRun = append([]string{}, login.Run...)
		sec.LoginPaste = login.Paste
	}
	return sec
}

// packLogin is the sign-in the setup of a pack leads with: that of the
// first carried source, in the pack's order, whose credential declares
// one. A setup card carries one account choice, whose sources share
// the credential, so the first stands for the account; nil where none
// declares one.
func packLogin(decls map[string]*source.Declaration, carried []AskSource) *source.CredentialLogin {
	for i := range carried {
		if d := decls[carried[i].Name]; d != nil && d.Credential != nil && d.Credential.Login != nil {
			return d.Credential.Login
		}
	}
	return nil
}

// missingNeedLines are a setup card's lines for the programs the
// pack's declarations need that this machine lacks, each naming the
// command that installs them: the pack's need, in the yard's words,
// held to one terminal-safe line as the sign-in lines are. A program
// the machine has, and a pack that names none, give no line, so the
// card reads exactly as it did.
func missingNeedLines(decls map[string]*source.Declaration, carried []AskSource, pack string) []string {
	var lines []string
	seenProg := map[string]bool{}
	for i := range carried {
		d := decls[carried[i].Name]
		if d == nil {
			continue
		}
		for _, n := range d.MissingNeeds() {
			if seenProg[n.Program] {
				continue
			}
			seenProg[n.Program] = true
			lines = append(lines, safeSourceCardLine(fmt.Sprintf("the pack needs %s, which is not on this machine: yardr pack install %s installs it", n.Program, pack)))
		}
	}
	return lines
}

// setupDoesForStale is a setup card's Does lines for stale comparison:
// the build label stripped as pack cards strip it, and the advisory
// missing-need lines left out so installing a program the setup card
// itself named as missing does not invalidate the open setup card.
// Changes to the pack's needs declaration itself are still held by
// the pack's bytes fingerprint in Seen.
func setupDoesForStale(does []string, label, build string) []string {
	stripped := doesWithoutBuild(does, label, build)
	out := make([]string, 0, len(stripped))
	for _, line := range stripped {
		if strings.HasPrefix(line, "the pack needs ") && strings.HasSuffix(line, " installs it") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// setupAccountDoes is the setup card's account step: the account line,
// then the pack's sign-in steps where its credential declares them,
// then the programs the pack needs that this machine lacks. A
// credential without them gives the account line alone, so the card
// reads exactly as it did.
func setupAccountDoes(decls map[string]*source.Declaration, carried []AskSource, account []string, file string, given bool, missing []string, candidates []AskCandidate, pack string) []string {
	lines := []string{setupAccountLine(account, file, given, missing, candidates)}
	if login := packLogin(decls, carried); login != nil {
		lines = append(lines, loginStepLines(login.Run, login.Paste)...)
	}
	return append(lines, missingNeedLines(decls, carried, pack)...)
}

// googleLogin is the one sign-in the yard's Google sources declare for
// a Google paste, where exactly one distinct one stands: a Google ask
// names an account and no source, so its card can speak a pack's steps
// only where no two Google packs word them differently. Only sources
// whose credential mints GOOGLE_ACCOUNT count, so another pack's login
// steps never leak onto or hide a Google paste card. Where none or
// several stand, the ask keeps the words it has today.
func (w *wtx) googleLogin() *source.CredentialLogin {
	var found *source.CredentialLogin
	ambiguous := false
	same := func(a, b *source.CredentialLogin) bool {
		return a.Paste == b.Paste && slices.Equal(a.Run, b.Run)
	}
	_ = each(w.Tx, bSources, "", func(_ []byte, src *Source) error {
		if ambiguous {
			return nil
		}
		d, err := src.Declaration()
		if err != nil || d.Credential == nil || d.Credential.Login == nil ||
			!slices.Contains(d.Credential.Mint, "GOOGLE_ACCOUNT") {
			return nil
		}
		switch {
		case found == nil:
			found = d.Credential.Login
		case !same(found, d.Credential.Login):
			ambiguous = true
		}
		return nil
	})
	if ambiguous {
		return nil
	}
	return found
}
