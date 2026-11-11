package cli

// A delegated pack apply (design yardr-m90n 3.2 and 3.9): the yardmaster
// applies a pack yardr embeds, or a pack of the packs repository pinned by
// its digest, for an operator on their cited mail, past the card bounds of
// internal/store/runask_pack.go, which bind cards and not this leg. The
// command takes the depot and its mappings alone (and, for a pack of the
// repository, the pin); a plan that still asks after them refuses whole,
// and no answer path is built for it here. pack.Apply keeps the run's
// delegated record around the import (internal/pack/apply_delegated.go).

import (
	"fmt"
	"io"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/version"
	"yardr.dev/yardr/packs"
)

// packApplyRun is pack apply's run: a delegated run (--for) takes the
// delegated road, and every other run the planned import's.
func (a *app) packApplyRun(c *cobra.Command, name string, f *importFlags) error {
	if delegatedRun(c) {
		return a.delegatedPackApply(c, name, f)
	}
	imp, err := a.planImport(c.OutOrStdout(), c.ErrOrStderr(), c.Context(), "pack apply", name, f, "", nil)
	if imp == nil || err != nil {
		return err
	}
	return imp.apply(c.OutOrStdout(), c.Context(), a, nil, nil)
}

// delegatedPackFlags are the flags a delegated pack apply refuses, each
// shaping or answering the import past its mappings: the run takes
// --depot and the mappings (--flow, --crew, --group, --bind, --members,
// --open-threads, --calls) alone, and for a pack of the packs repository
// the pin (--sha256) beside them.
var delegatedPackFlags = []string{"auth", "depot-roles", "pack-roles", "source-env", "source-base", "notify", "yes", "sha256", "prune"}

// refuseFlags refuses a delegated pack apply that carries a flag of
// delegatedPackFlags other than allow, naming the command the run takes.
func refuseFlags(c *cobra.Command, name, use, allow string) error {
	for _, flag := range delegatedPackFlags {
		if flag != allow && c.Flags().Changed(flag) {
			return fmt.Errorf("pack %s: a delegated run takes yardr pack apply %s --depot <depot> with its mappings (--flow, --crew, --group, --bind, --members, --open-threads, --calls) and no other flag, not --%s", name, use, flag)
		}
	}
	return nil
}

// delegatedPack is the pack a delegated apply takes: one yardr embeds,
// by name alone, or one of the packs repository, by its URL and its pin.
// Anything else — a directory, another URL, an imported pack — is the
// operator's to type in a terminal, and is refused before a file of it is
// read.
func (a *app) delegatedPack(c *cobra.Command, name string, f *importFlags) (*pack.Pack, error) {
	if pack.IsURL(name) {
		return a.delegatedRepositoryPack(c, name, f)
	}
	if store.IsPackDir(name) {
		return nil, fmt.Errorf("pack %s: a delegated run applies a pack yardr embeds, by name, or one of the packs repository, by its URL and --sha256: a directory is the operator's to type in a terminal", name)
	}
	if _, err := fs.Stat(packs.FS, name+"/pack.yaml"); err != nil {
		return nil, fmt.Errorf("pack %s: yardr embeds no pack of that name (it embeds %s): a pack of the packs repository is named by its URL (%s<name>/) and pinned with --sha256, and a pack read from anywhere else is the operator's to type in a terminal", name, strings.Join(embeddedPacks(), ", "), pack.Repository)
	}
	if err := refuseFlags(c, name, "<name>", ""); err != nil {
		return nil, err
	}
	sub, err := fs.Sub(packs.FS, name)
	if err != nil {
		return nil, err
	}
	return pack.Load(sub, packs.Embedded+" in yardr "+version.Describe())
}

// delegatedRepositoryPack reads the pack of the packs repository the URL
// from names, held to the digest --sha256 pins before anything of it is
// planned, asked or written. A URL that is not the repository's, and an
// apply with no pin, refuse before a request goes out. The store fetches the
// pack again itself and holds the run to what it read (store.PackFetcher):
// this is the command's own copy, which it installs from.
func (a *app) delegatedRepositoryPack(c *cobra.Command, from string, f *importFlags) (*pack.Pack, error) {
	origin := or(a.packsOrigin, pack.Repository)
	name, url, err := pack.RepositoryPack(origin, from)
	if err != nil {
		return nil, err
	}
	if !c.Flags().Changed("sha256") {
		return nil, fmt.Errorf("pack %s: a pack of the packs repository is applied pinned: add --sha256 <digest>, as yardr pack check %s prints it", name, url)
	}
	if err := refuseFlags(c, name, "<url> --sha256 <digest>", "sha256"); err != nil {
		return nil, err
	}
	return pack.LoadRepository(origin, url, string(f.sha256))
}

// delegatedPackApply applies the pack name, an embedded one or one of the
// packs repository, for the operator the run's delegation names, with no
// terminal to ask in: what the mappings do not answer, the plan still
// asks, and that refuses the run whole before anything writes.
func (a *app) delegatedPackApply(c *cobra.Command, name string, f *importFlags) error {
	ctx := c.Context()
	p, err := a.delegatedPack(c, name, f)
	if err != nil {
		return err
	}
	st, cfg, err := a.packOptions(c.ErrOrStderr(), ctx, &f.packFlags)
	if err != nil {
		return err
	}
	if err := checkDepot(p, f.opts.Depot); err != nil {
		return err
	}
	if f.opts.Depot == "" && p.Kind != pack.KindSource && !p.FrameOnly() {
		return fmt.Errorf(`required flag(s) "depot" not set`)
	}
	f.opts.Bound = boundGroups(f.binds)
	plan, err := pack.Build(ctx, st, p, f.opts)
	if err != nil {
		return err
	}
	preset, err := yardPreset(ctx, st)
	if err != nil {
		return err
	}
	answers, err := packAnswers(plan, f.binds, f.members, cfg.Home, true, preset)
	if err != nil {
		return err
	}
	if f.opts.Flow != "" {
		answers[pack.FlowQuestion] = plan.Flow
	}
	open, calls, err := delegatedGrantAnswers(plan, f)
	if err != nil {
		return err
	}
	if err := delegatedAsks(plan, answers, open, calls); err != nil {
		return err
	}
	if !a.json {
		printPlan(c.OutOrStdout(), plan, answers, open, calls, nil, false)
	}
	release, err := config.AcquireApplyLock(plan.Home)
	if err != nil {
		return err
	}
	defer release()
	res, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{Answers: answers, OpenThreads: open, Calls: calls, Expose: map[string]map[string]bool{}, Actor: a.who(),
		DelegatedMappings: store.Mappings{Flow: f.opts.Flow, Crew: f.opts.Crew, Groups: f.opts.Groups, Binds: f.binds, Members: f.members, OpenThreads: f.opens, Calls: f.calls}})
	if res != nil {
		if oerr := a.out(c.OutOrStdout(), map[string]any{"plan": plan, "result": res}, func(w io.Writer) {
			printResult(w, plan, res, false, answers, &openAnswers{Answers: open}, &callAnswers{Answers: calls}, &exposeAnswers{Answers: map[string]map[string]bool{}}, nil, nil)
		}); oerr != nil {
			return oerr
		}
	}
	return err
}

// delegatedAsks refuses a plan that still asks after its mappings: its
// unanswered questions, and the thread, call and peer grants nobody
// answered. No delegated run starts for such a plan, and no answer path
// is built for it: the operator's way is the pack card where it applies,
// and otherwise a terminal.
func delegatedAsks(plan *pack.Plan, answers pack.Answers, open, calls map[string]map[string]bool) error {
	var asks []string
	for _, q := range plan.Missing(answers) {
		asks = append(asks, q.ID+": "+q.Ask)
	}
	for _, ot := range plan.OpenThreads {
		for _, src := range ot.Asks {
			if _, done := open[ot.CrewMember][src]; !done {
				asks = append(asks, fmt.Sprintf("let %s open threads in %s", ot.CrewMember, src))
			}
		}
	}
	for _, cl := range plan.Calls {
		for _, w := range cl.Asks {
			if _, done := calls[cl.Grantee().String()][pack.CallKey(cl.Source, w)]; !done {
				asks = append(asks, fmt.Sprintf("let %s make the call %s of the source %s", cl.Grantee(), w, cl.Source))
			}
		}
	}
	for _, e := range plan.Expose {
		for _, peer := range e.Asks {
			asks = append(asks, fmt.Sprintf("expose %s to the peer %s", e.CrewMember, peer))
		}
	}
	asks = append(asks, plan.Refused...)
	if len(asks) == 0 {
		return nil
	}
	return fmt.Errorf("pack %s: its plan asks (%s), and a delegated run answers none of a plan's questions: it refuses whole, and nothing was written", plan.Pack, strings.Join(asks, "; "))
}

// delegatedGrantAnswers are the thread and call grants the mappings
// answer, as the server's reading answers them for a card
// (mappingAnswers): a flagged crew member or grantee is answered, yes
// for the sources and calls it names and no for the rest it was asked.
func delegatedGrantAnswers(plan *pack.Plan, f *importFlags) (open, calls map[string]map[string]bool, err error) {
	if open, err = delegatedOpenAnswers(plan, f.opens); err != nil {
		return nil, nil, err
	}
	if calls, err = delegatedCallAnswers(plan, f.calls); err != nil {
		return nil, nil, err
	}
	return open, calls, nil
}

// delegatedOpenAnswers is delegatedGrantAnswers for open threads.
func delegatedOpenAnswers(plan *pack.Plan, flags []string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	named := map[string][]string{}
	for _, kv := range flags {
		name, v, ok := strings.Cut(kv, "=")
		if !ok || name == "" || v == "" {
			return nil, fmt.Errorf("--open-threads %q: want <pack's crew member>=<source>[,<source>], or =none", kv)
		}
		if _, ok := plan.Crew[name]; !ok {
			return nil, fmt.Errorf("--open-threads %q: the pack has no crew member %s (it has: %s)", kv, name, strings.Join(slices.Sorted(maps.Keys(plan.Crew)), ", "))
		}
		i := slices.IndexFunc(plan.OpenThreads, func(ot pack.OpenThreads) bool { return ot.Pack == name })
		if i < 0 {
			return nil, fmt.Errorf("--open-threads %q: the pack asks for no source its crew member %s may open threads in (open_threads)", kv, name)
		}
		if _, twice := named[name]; twice {
			return nil, fmt.Errorf("--open-threads %q: %s is answered twice", kv, name)
		}
		named[name] = []string{}
		if v == "none" {
			continue
		}
		for _, src := range strings.Split(v, ",") {
			if src = strings.TrimSpace(src); !slices.Contains(plan.OpenThreads[i].Wants, src) {
				return nil, fmt.Errorf("--open-threads %q: the pack does not ask that %s may open threads in %q (it asks for: %s)", kv, name, src, strings.Join(plan.OpenThreads[i].Wants, ", "))
			}
			named[name] = append(named[name], src)
		}
	}
	for _, ot := range plan.OpenThreads {
		yes, flagged := named[ot.Pack]
		if !flagged {
			continue
		}
		answers := map[string]bool{}
		for _, src := range ot.Wants {
			switch {
			case slices.Contains(yes, src) && ot.Grantable(src):
				answers[src] = true
			case slices.Contains(ot.Asks, src):
				answers[src] = false
			}
		}
		if len(answers) > 0 {
			out[ot.CrewMember] = answers
		}
	}
	return out, nil
}

// delegatedCallAnswers is delegatedGrantAnswers for declared-source calls.
func delegatedCallAnswers(plan *pack.Plan, flags []string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	named := map[int][]string{}
	for _, kv := range flags {
		name, v, ok := strings.Cut(kv, "=")
		src, words, ok2 := strings.Cut(v, ":")
		if !ok || !ok2 || name == "" || src == "" || words == "" {
			return nil, fmt.Errorf("--calls %q: want <group>=<source>:<call>[,<call>] with read for every read call, crew:<crew member>=… for a crew member, or <source>:none", kv)
		}
		member, isCrew := strings.CutPrefix(name, "crew:")
		names, what := plan.Groups, "group"
		if isCrew {
			names, what = plan.Crew, "crew member"
		} else {
			member = name
		}
		if _, ok := names[member]; !ok {
			return nil, fmt.Errorf("--calls %q: the pack has no %s %s (it has: %s)", kv, what, member, strings.Join(slices.Sorted(maps.Keys(names)), ", "))
		}
		i := slices.IndexFunc(plan.Calls, func(cl pack.Calls) bool { return delegatedCallsFlag(cl) == name && cl.Source == src })
		if i < 0 {
			return nil, fmt.Errorf("--calls %q: the pack asks for no call of the source %s for its %s %s (calls)", kv, src, what, member)
		}
		if _, twice := named[i]; twice {
			return nil, fmt.Errorf("--calls %q: %s is answered twice for the source %s", kv, name, src)
		}
		named[i] = []string{}
		if words == "none" {
			continue
		}
		for _, w := range strings.Split(words, ",") {
			if w = strings.TrimSpace(w); !slices.Contains(plan.Calls[i].Wants, w) {
				return nil, fmt.Errorf("--calls %q: the pack does not ask that %s may call %q of %s (it asks for: %s)", kv, name, w, src, strings.Join(plan.Calls[i].Wants, ", "))
			}
			named[i] = append(named[i], w)
		}
	}
	for i, cl := range plan.Calls {
		yes, flagged := named[i]
		if !flagged {
			continue
		}
		who := cl.Grantee().String()
		for _, w := range cl.Wants {
			switch {
			case slices.Contains(yes, w) && cl.Grantable(w):
				if out[who] == nil {
					out[who] = map[string]bool{}
				}
				out[who][pack.CallKey(cl.Source, w)] = true
			case slices.Contains(cl.Asks, w):
				if out[who] == nil {
					out[who] = map[string]bool{}
				}
				out[who][pack.CallKey(cl.Source, w)] = false
			}
		}
	}
	return out, nil
}

// delegatedCallsFlag is the name a calls request has in --calls: the
// pack's name for the group, or crew:<its name for the crew member>.
func delegatedCallsFlag(cl pack.Calls) string {
	if cl.CrewMember != "" {
		return "crew:" + cl.Pack
	}
	return cl.Pack
}
