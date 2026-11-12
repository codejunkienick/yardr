// Where a setup's calls go, as the setup card says it and its approval
// carries it: the region base the asker chose (--source-base), a bounded pick
// from what the declarations name, and the host the operator types for a
// server of their own, free text that decides where the credential goes and so
// is the operator's alone to say, on the card or in a terminal, never an
// asker's (design yardr-7d4e, B2).
package store

import (
	"errors"
	"fmt"
	"strings"

	"yardr.dev/yardr/internal/source"
)

// checkSetupBase holds the setup's region base to the pack's carried
// sources: "" is the declarations' own, and anything else is declared by
// every carried source that declares bases. A pack none of whose sources
// declares bases takes none.
func checkSetupBase(pack string, carried []AskSource, decls map[string]*source.Declaration, base string) error {
	if base == "" {
		return nil
	}
	any := false
	for _, c := range carried {
		d := decls[c.Name]
		if d == nil || len(d.Bases) == 0 {
			continue
		}
		any = true
		if err := d.CheckBase(base); err != nil {
			return err
		}
	}
	if !any {
		return fmt.Errorf("--source-base %s: pack %s declares no bases: none of its sources names region bases", base, pack)
	}
	return nil
}

// setupHostTemplate is the address a host the operator types fills, for a
// client to show where the host goes: the base_template of the first carried
// source that declares one, "" where none does and the card asks no host.
func setupHostTemplate(carried []AskSource, decls map[string]*source.Declaration) string {
	for _, c := range carried {
		if d := decls[c.Name]; d != nil && d.BaseTemplate != "" {
			return d.BaseTemplate
		}
	}
	return ""
}

// setupSourceAt is where a carried source's calls will go after the base the
// asker chose is set, or under the source's settings where that base does not
// replace them. A selected region base clears a standing host, as SetSource
// does; without one, a source already in the yard keeps its host or base.
// This is what both the preset's consequences and the host sentence say, so a
// setup card does not promise grants to a declaration's default while calls
// will go somewhere else.
func (w *wtx) setupSourceAt(name string, d *source.Declaration, base string) (*source.Declaration, *Source, error) {
	src, _, err := sourceIn(w.Tx, name)
	if errors.Is(err, ErrNotFound) {
		return d.WithBase(base), nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if base != "" {
		if _, ok := d.BaseURL(base); ok {
			chosen := *src
			chosen.Base, chosen.Host = base, ""
			src = &chosen
		}
	}
	return src.Resolved(d), src, nil
}

// setupHostDoes are the card's sentences about the host, above the account's:
// for each carried source that declares a template, where its calls go now and
// how to send them to a server of the operator's own. Where the source stands
// in the yard it is the address under the settings it keeps, a host set before
// included, and where an account stands too the sentence says what a new host
// does to it: that key was given for the host in use, so the host held it back
// until it is typed again, as setting a host in a terminal does. None where no
// carried source declares a template (setupHostTemplate).
func (w *wtx) setupHostDoes(carried []AskSource, decls map[string]*source.Declaration, base string, stands bool) ([]string, error) {
	var out []string
	for _, c := range carried {
		d := decls[c.Name]
		if d == nil || d.BaseTemplate == "" {
			continue
		}
		resolved, src, err := w.setupSourceAt(c.Name, d, base)
		if err != nil {
			return nil, err
		}
		addr := resolved.Address()
		if src != nil {
			addr = src.CalledAt(d)
		}
		line := fmt.Sprintf("the host of source %s: its calls go to %s. To send them to a server of your own instead, give its host with --host <host> (a name or an address with an optional port, yours to type): they then go to %s, and the account below is given for that host.",
			c.Name, addr, strings.Replace(d.BaseTemplate, source.HostMark, "<host>", 1))
		if stands {
			line += " The account that stands was given for the host in use, so a new host holds it back until you type it again."
		}
		out = append(out, line)
	}
	return out, nil
}

// checkSetupHost holds the host an approval carries to the card it answers:
// none leaves each source's address as it stands, and a host is taken where
// the card asked one (HostTemplate), each carried source that declares a
// template then holding the address it makes to what a base is, in the checks'
// words.
func checkSetupHost(seq uint64, was AskSetup, host string) error {
	if host == "" {
		return nil
	}
	if was.HostTemplate == "" {
		return fmt.Errorf("ask %d takes no host: a host is asked where a source's declaration names a base_template, and none of this pack's does", seq)
	}
	for _, c := range was.Carried {
		d, err := source.Parse("pack source "+c.Name, []byte(c.Text))
		if err != nil {
			return fmt.Errorf("ask %d: %s", seq, OneLine(err.Error()))
		}
		if d.BaseTemplate == "" {
			continue
		}
		if err := d.CheckHost(host); err != nil {
			return fmt.Errorf("ask %d: %s", seq, OneLine(err.Error()))
		}
	}
	return nil
}
