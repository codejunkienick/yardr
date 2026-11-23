// The source destination row of the run-ask list (runList): moving
// where a standing source sends its calls, by region base or by typed
// host, or back to the declaration's own base. It shares the words of
// source set with the limit row (runask_list.go), and parseRow tells
// the two apart by the setting: this row, the newer, is tried first
// and answers errNotRow for any other setting, so the limit row keeps
// the limits and keeps env_file and mint_command refused as the
// operator's alone (sourceSetAsk, runask_general.go's never-list).
// An empty value resets to the declaration's own base, as the typed
// command does. The row is registered here, beside its functions, and
// not in runList's literal: runask.go stands at its file-size ceiling,
// and runask_list.go at its own.
package store

import (
	"context"
	"fmt"
	"strings"

	"yardr.dev/yardr/internal/source"
)

// sourceDestUse is how a refusal names the source set a run ask takes
// for a destination.
const sourceDestUse = "yardr source set <source> base=<name>|host=<host>|site=<site>"

// sourceSetDescription is the help line of source set that the card of
// each of its forms keeps (internal/cli holds it to the command's
// Short).
const sourceSetDescription = "Change a source's credential, per-hour call limits, region base, host or site"

func init() {
	runList = append(runList, runCommand{
		words:       []string{"source", "set"},
		use:         sourceDestUse,
		description: sourceSetDescription,
		parse:       parseSourceDest,
		check:       checkSourceDest,
		card:        (*wtx).sourceDestCard,
		stale:       (*wtx).sourceDestStale,
		weigh:       sourceDestWeight,
		run:         runSourceDest,
	})
}

// parseSourceDest reads the words of source set <source>
// base=<name>|host=<host>|site=<site>: the source and the one destination
// setting, with an empty value kept empty so it resets to the
// declaration's own base. A command whose setting is no base, host or site
// is another row's, and answers errNotRow; one that mixes a
// destination with any other word is refused whole, since one
// command carries one setting, so a pair of destination settings (base
// with host, base with site, host with site) is refused here whole too.
func parseSourceDest(rest []string) ([]string, error) {
	if !hasDestSetting(rest) {
		return nil, errNotRow
	}
	args, _, err := runWords(sourceDestUse, "source set", "the operator's", rest, nil)
	if err != nil {
		return nil, err
	}
	if len(args) != 2 {
		return nil, fmt.Errorf("--run takes %s: one source and one setting, not %d words", sourceDestUse, len(args))
	}
	key, value, ok := strings.Cut(args[1], "=")
	if strings.Contains(args[0], "=") || !ok || key != SourceBase && key != SourceHost && key != SourceSite {
		return nil, fmt.Errorf("--run takes %s: the source first, then %s=<name>, %s=<host> or %s=<site>", sourceDestUse, SourceBase, SourceHost, SourceSite)
	}
	return []string{args[0], key + "=" + value}, nil
}

// hasDestSetting says whether any word of rest is a base=, host= or
// site= setting, in either position: parseSourceDest claims the
// command on it before it reads the words whole.
func hasDestSetting(rest []string) bool {
	for _, word := range rest {
		if key, _, ok := strings.Cut(word, "="); ok && (key == SourceBase || key == SourceHost || key == SourceSite) {
			return true
		}
	}
	return false
}

// sourceDestOf is the source, the setting and the value of a parsed
// source set <source> base=<name>|host=<host>; the value is empty
// where the command resets to the declaration's own base.
func sourceDestOf(argv []string) (name, key, value string) {
	key, value, _ = strings.Cut(argv[3], "=")
	return argv[2], key, value
}

// checkSourceDest is moving a source's destination as the asker: the
// check setSource refuses with, which a run ask may propose.
func checkSourceDest(ctx context.Context, w *wtx, argv []string, actor string) error {
	name, key, _ := sourceDestOf(argv)
	_, err := w.sourceSetCheck(ctx, name, key, actor)
	return err
}

// destAfter is the source src with the destination key=value applied
// as setSource leaves it: a base, a host and a site replace each other, an
// empty value restores the declaration's own base, and setting a base
// with no typed host or site standing re-pairs any standing credential with
// the destination host (paired is true when that re-pairing runs).
func destAfter(src *Source, d *source.Declaration, key, value string) (after Source, paired bool) {
	after = *src
	typed := src.Host != "" || src.Site != ""
	switch key {
	case SourceBase:
		after.Base, after.Host, after.Site = value, "", ""
	case SourceHost:
		after.Host, after.Base, after.Site = value, "", ""
	default:
		after.Site, after.Base, after.Host = value, "", ""
	}
	if key == SourceBase && !typed {
		after.pairCredential(d)
		paired = true
	}
	return after, paired
}

// standingCredential says whether src holds a credential a move
// must account for: a file stands, and the declaration names a
// credential that is not the address itself (CredentialHeld).
func standingCredential(src *Source, d *source.Declaration) bool {
	return src.EnvFile != "" && d.Credential != nil && !d.Credential.URL
}

// destCredentialSentence describes what the move does to a standing
// credential in the card's Does, matching setSource and
// CredentialHeld: re-paired at once when setting a base with no typed
// host or site standing moves the pairing, sent without being given again
// when the credential was already given for the destination host (and
// site, where one is set: the key names both, as scheme://host/segment)
// (such as resetting back off a typed host that was never keyed), and
// held back otherwise, naming the host and site it was actually given for
// (KeyedHost). Empty when no credential stands or the destination
// names no host.
func destCredentialSentence(src *Source, after *Source, d *source.Declaration, paired bool) string {
	if !standingCredential(src, d) {
		return ""
	}
	resolved := after.Resolved(d)
	if _, ok := baseHost(resolved.Base); !ok {
		return ""
	}
	afterHost := sourceHost(resolved)
	if after.CredentialHeld(d) != "" {
		why := "the yard has no record of the host it was given for"
		if src.KeyedHost != "" {
			why = fmt.Sprintf("it was given for %s", src.KeyedHost)
		}
		return fmt.Sprintf("its standing credential (in %s) is held back from %s until it is given again (yardr source key %s, or --secret %s on a card): %s, so no call sends it to the new address until then.", src.EnvFile, afterHost, src.Name, src.Name, why)
	}
	if paired && src.KeyedHost != afterHost {
		return fmt.Sprintf("its standing credential (in %s) is re-paired with %s at once, so it will be sent there without being given again.", src.EnvFile, afterHost)
	}
	return fmt.Sprintf("its standing credential (in %s) was given for %s, so calls will send it there without being given again.", src.EnvFile, afterHost)
}

// calledOrNone is where a source's calls go as its card names it:
// the resolved address, or that its declaration makes no call.
func calledOrNone(src *Source, d *source.Declaration) string {
	if addr := src.CalledAt(d); addr != "" {
		return addr
	}
	return "no address (its declaration makes no call)"
}

// sourceDestCard is the card of source set <source>
// base=<name>|host=<host>|site=<site>: where the source's calls go now and
// where they will go, each whole as CalledAt names it, and what the move
// does to a standing credential. The value is held to the
// declaration's own checks (CheckBase, CheckHost, CheckSite) here and again
// at approval, so a stored value the declaration no longer allows fails
// closed. Asking refuses a move that would change nothing. The
// command that takes it back restores the setting that stood, which
// a move across base, host and site must name across: setting the proposed
// key back would clear the others to the default instead. The
// approval is held to the source as it stands (Seen).
func (w *wtx) sourceDestCard(argv []string, asking bool) (AskRun, error) {
	name, key, value := sourceDestOf(argv)
	src, d, err := sourceIn(w.Tx, name)
	if err != nil {
		return AskRun{}, err
	}
	switch key {
	case SourceBase:
		err = d.CheckBase(value)
	case SourceHost:
		err = d.CheckHost(value)
	default:
		err = d.CheckSite(value)
	}
	if err != nil {
		return AskRun{}, err
	}
	after, paired := destAfter(src, d, key, value)
	if asking && after.Base == src.Base && after.Host == src.Host && after.Site == src.Site {
		return AskRun{}, fmt.Errorf("source %s: %s=%s is what it is already, so the command would change nothing", name, key, value)
	}
	fingerprint, err := w.sourceSeen(src)
	if err != nil {
		return AskRun{}, err
	}
	does := []string{fmt.Sprintf("the source %s: its calls go to %s now, and will go to %s.", name, calledOrNone(src, d), calledOrNone(&after, d))}
	if line := destCredentialSentence(src, &after, d, paired); line != "" {
		does = append(does, line)
	}
	// The setting that stood, empty where the source is at its
	// declaration's own base: naming it takes the move back, across
	// base, host and site included (site= where a site stands, as where()
	// names it first).
	backKey, backValue := key, ""
	switch {
	case src.Site != "":
		backKey, backValue = SourceSite, src.Site
	case src.Host != "":
		backKey, backValue = SourceHost, src.Host
	case src.Base != "":
		backKey, backValue = SourceBase, src.Base
	}
	return AskRun{
		Argv:   argv,
		Does:   does,
		Back:   runCommandLine([]string{"source", "set", name, backKey + "=" + backValue}),
		BackBy: "only the operator",
		Seen:   map[string]string{"source " + name: fingerprint},
	}, nil
}

// sourceDestStale says why the ask seq for source set <source>
// base=<name>|host=<host>|site=<site> is approved no more: the source is
// another under its name, or the card would read otherwise now.
func (w *wtx) sourceDestStale(seq uint64, was, now AskRun) error {
	name, _, _ := sourceDestOf(was.Argv)
	if err := w.staleSource(seq, name, was, now); err != nil {
		return err
	}
	return staleCard(seq, was, now)
}

// sourceDestWeight weighs a source destination card outside: the
// yard's calls then go to another address, and where a standing
// credential moves with a region base the particulars say it is
// re-paired with it.
func sourceDestWeight(w *wtx, run AskRun) ([]AskWeight, error) {
	name, key, value := sourceDestOf(run.Argv)
	src, d, err := sourceIn(w.Tx, name)
	if err != nil {
		return nil, err
	}
	after, paired := destAfter(src, d, key, value)
	particulars := fmt.Sprintf("the source %s calls will go to %s", name, calledOrNone(&after, d))
	if standingCredential(src, d) {
		resolved := after.Resolved(d)
		if _, ok := baseHost(resolved.Base); ok && after.CredentialHeld(d) == "" {
			afterHost := sourceHost(resolved)
			if paired && src.KeyedHost != afterHost {
				particulars += fmt.Sprintf(", and its standing credential is re-paired with %s", afterHost)
			} else {
				particulars += fmt.Sprintf(", and its standing credential (given for %s) will be sent there", afterHost)
			}
		}
	}
	return []AskWeight{weighs(WeightOutside, particulars)}, nil
}

// runSourceDest moves the source as actor through setSource, under
// the check the setting typed at a terminal has, and answers the
// line yardr source set prints for it.
func runSourceDest(ctx context.Context, w *wtx, argv []string, actor string, by *approvedOn) (string, error) {
	name, key, value := sourceDestOf(argv)
	src, err := w.setSource(ctx, name, key, value, 0, actor, by)
	if err != nil {
		return "", err
	}
	d, err := src.Declaration()
	if err != nil {
		return "", err
	}
	if addr := src.CalledAt(d); addr != "" {
		return "source " + src.Name + ": calls go to " + addr, nil
	}
	return "source " + src.Name + ": the file declares no call", nil
}
