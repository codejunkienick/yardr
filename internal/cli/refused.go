package cli

import (
	"context"
	"fmt"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/source"
	"yardr.dev/yardr/internal/store"
)

// The words a person reads when a service refuses the credential a command
// sent (401), read by whoever ran the command and nowhere else: they name the
// source, what was sent, the file it was read from and the one line that sets
// it right, where the failure alone is a command's exit status and curl's
// line. Only a 401 is told; every other failure keeps the words it has.

// threadRefused is err, the failure of a thread command that crew ran in the
// source name with the credential cred, with the words of a 401 before it
// when it says the service answered one: where cred came from is the crew
// member's own env_file or env_command, read when its session started, so a
// value given since reaches it at its next start. err itself, unchanged,
// where it is no 401 or where the words cannot be made.
func threadRefused(ctx context.Context, st store.API, name, crew, cred string, err error) error {
	if err == nil || !source.Refused401(err.Error()) {
		return err
	}
	d, _, derr := declaredSource(ctx, st, name)
	if derr != nil || d == nil || d.Credential == nil {
		return err
	}
	inputs, _ := source.UnmintRef(cred, d.Credential.Mint)
	minted := inputs != nil
	from := "as set in the environment of the process that ran the command"
	if r, cerr := st.GetCrew(ctx, crew); cerr == nil {
		file, _ := r.Config["env_file"].(string)
		command := len(config.StringList(r.Config["env_command"])) > 0
		switch {
		case file != "" && command:
			from = fmt.Sprintf("as crew member %s's env_file %s and env_command gave it when its session started", crew, file)
		case file != "":
			from = fmt.Sprintf("as read from crew member %s's env_file %s when its session started", crew, file)
		case command:
			from = fmt.Sprintf("as crew member %s's env_command printed it when its session started", crew)
		default:
			from += fmt.Sprintf(" (crew member %s has no env_file or env_command)", crew)
		}
	}
	it := "it"
	if minted {
		it = "its inputs"
	}
	fix := fmt.Sprintf("give %s again (yardr bead ask <bead> - --secret %s --for-crew %s), which a session already running sees when it next starts (yardr crew restart %s), or check the application that issued it",
		it, name, crew, crew)
	return fmt.Errorf("%s: %w", d.RefusedWords(source.Refusal{Minted: minted, From: from, Fix: fix}), err)
}

// callRefused is the words of a 401 for the declared call of the source name
// made as the user as, "" for the source's own credential: the file the
// credential was read from, as the call read it, and, where the service's
// answer was the yard's own (answered), how the request carried it; a command
// that called the service builds its own header. "" where they cannot be
// made, which leaves the failure as it was.
func callRefused(ctx context.Context, st store.API, name, call, as string, answered bool) string {
	d, src, err := declaredSource(ctx, st, name)
	if err != nil || d == nil || d.Credential == nil {
		return ""
	}
	c, ok := d.Call(call)
	if !ok {
		return ""
	}
	file, whose := src.EnvFile, "the source's "
	again := fmt.Sprintf("(yardr source key %s, or yardr bead ask <bead> - --secret %s asks the operator for it)", name, name)
	if as != "" {
		u, err := st.GetUser(ctx, as)
		if err != nil {
			return ""
		}
		file, whose = u.EnvFile, as+"'s "
		again = fmt.Sprintf("(yardr bead ask <bead> - --secret %s --as %s asks %s for it)", name, as, as)
	}
	sel, err := d.SelectCredential(file, c.Kind, store.ReadEnvFileEntries)
	if err != nil || sel == nil {
		return ""
	}
	it := "it"
	if sel.Inputs != nil {
		it = "its inputs"
	}
	return d.RefusedWords(source.Refusal{Kind: c.Kind, Header: answered, Minted: sel.Inputs != nil, From: "as set in " + whose + "env_file " + sel.File,
		Fix: fmt.Sprintf("give %s again %s, or check the application that issued it", it, again)})
}
