package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
)

// checkFlags are what pack check takes: where the pack goes and what
// answers its plan.
type checkFlags struct {
	packFlags
	answerFlags
}

// answered reports whether any flag that answers the plan was given.
func (f *answerFlags) answered() bool {
	return len(f.binds)+len(f.members)+len(f.opens)+len(f.calls) > 0
}

// checkAnswers are the answers check's flags give the plan, in read-only
// form: the flags answer, nothing is asked in a terminal, and nothing is
// written or granted. With no answering flag there are no answers at all,
// so the plan prints exactly as a check without them.
func (a *app) checkAnswers(ctx context.Context, st store.API, plan *pack.Plan, home string, f *answerFlags) (pack.Answers, *openAnswers, *callAnswers, error) {
	if !f.answered() {
		return nil, nil, nil, nil
	}
	preset, err := yardPreset(ctx, st)
	if err != nil {
		return nil, nil, nil, err
	}
	answers, err := packAnswers(plan, f.binds, f.members, home, true, preset)
	if err != nil {
		return nil, nil, nil, err
	}
	open, err := a.openThreads(ctx, st, plan, f.opens, false)
	if err != nil {
		return nil, nil, nil, err
	}
	calls, err := a.packCalls(ctx, st, plan, f.calls, false)
	if err != nil {
		return nil, nil, nil, err
	}
	return answers, open, calls, nil
}

func (a *app) packCheckCmd(naming string) *cobra.Command {
	var f checkFlags
	check := &cobra.Command{
		Use:   "check <pack> [--depot <depot>]",
		Short: "Read a pack and show what importing it into a depot would change",
		Long: `Reads the pack (pack.yaml, roles/, scripts/), checks it, and prints
what importing it into the depot would do to this yard, object by object:
create, change (field: old -> new), unchanged, not in the pack (the yard
has it, the pack does not, and an import leaves it alone), or no longer in
the pack (an earlier import brought it, the pack has dropped it since, and
pack apply --prune removes it). Then what an import would ask, which flow
would govern the depot, what each source a crew member subscribes to still
lacks, and which sources the pack asks that a crew member may open threads in
(open_threads): an import asks the operator, and only about a source the
yard has a hook and a credential for. For each crew member the pack asks to
have exposed to the yard's peers (expose_to_peers), the plan shows which
peers it is exposed to, which an import asks the operator about, and which
an earlier import was told no for. For each source the pack carries a
stage map for (sources.<source>: as, hears), the plan shows the map, the
yard's crew member it would be acted on as, whether the yard has a hook of
that source and whether that crew member may set a status there (set_state),
with the command for whatever is missing. For each source the pack declares
(sources.<name>: declaration), the plan shows where its calls go, the calls,
what a declaration the yard has under that name would change to, and what
the yard must still be told: the file its credential is read from, and for a
source that can tell a user, which user names it (an apply answers both with
--source-env and --notify); and, where the pack's file
replaces the yard's and adds or changes a read call, who gets that call
through a grant of read they hold. For each group and crew member the pack
asks calls of a declared source for (calls), the plan shows which it has,
which an import asks the operator about, which an earlier import was told
no for, and which lack something, with the command. Nothing is written.

The flags that answer what an import asks may be given here too, and the
plan is printed with their answers filled in: --bind, --members,
--open-threads and --calls, as pack apply takes them. An answered line
shows its value, and "an import would ask" lists only what is still open,
so what a plan still asks can be read before applying or asking. Nothing
is asked in a terminal, and nothing is written or granted: a value pack
apply would refuse is refused here the same way.

The pack's role is set on every agent group and crew member it names, also
where the depot has its own .yardr/roles/<name>.md: the plan names that file
as shadowed. --depot-roles keeps the depot's own role instead.

--group <pack's group>=<yard's group> has a group the yard has take a group
of the pack, instead of a <depot>-<group> of the depot's own: the plan says
"takes", the pack's routes lead to it, and nothing of it is changed. Where
its role or limit is not the pack's, the plan says so in a line.

A script group's env (groups.<group>.env) is the options of its script, set
before the script in the cmd of a group the import creates; the plan says
which ("cmd sets YARDR_LAND=squash before the script"). A group the yard has
keeps its own cmd, also when the pack's env has changed since the group was
made, and the plan says in a line what of the pack's env that cmd lacks.

A role or stage file is installed with {{operator}} filled with the yard's
operator and {{<crew member>}} with the yard's crew member that takes that
crew member of the pack; any other {{...}} in one makes the pack invalid.

A pack of kind source (kind: source) is imported into the yard itself, and
into no depot: check takes it without --depot, and refuses --depot for it.
It declares a source yardr has no Go for, with the commands for it; the
plan names each command, its file and the file's sha256. The yard runs
them all but the git credential helper, which a depot's git runs. What a
stage means in such a source is a pack for a depot's to say
(sources.<name>: hears): until the yard has the source, an import of that
pack skips the lines and says so, and the next apply sets them. A pack with
no flows, groups or crew (sources lines alone) is checked without --depot
too: the plan says what the lines would set on the yard's hooks, and each
line waits for the depot, whose flow it is held to once one is named.

It exits 1 on a pack that is not valid, naming every mistake by file, line
and key, and on a plan an import would refuse (open beads in a stage the
pack's flow lacks). A pack given as a directory is read and checked before
the yard is asked anything, so its mistakes are named on a home with no yard
too; a valid one stops there for the yard its plan is made against. After the plan
come the pack's setup notes (pack.yaml's setup), when it has any: its author's words
about what a setup takes, marked as the pack's, not checked by yardr. --json prints the plan as data. See docs/packs.md.
` + naming,
		Example: `  yardr pack check starter --depot yardr
  yardr pack check ~/packs/starter --depot yardr --crew yardmaster=boss
  yardr pack check contributors --depot signalbox --group builders=yardr-builders
  yardr pack check starter --depot yardr --bind builders=claude --members backlog=ada
  yardr pack check linear`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			// The first-run check, which pack check is let through
			// (noOnboarding), once a directory pack has been read.
			st, cfg, p, err := a.openPack(c.OutOrStdout(), c.ErrOrStderr(), c.Context(), args[0], &f.packFlags, func() error { return a.onboard(c) }, true)
			if err != nil {
				return err
			}
			// As an import binds them (planImport): a group that binds
			// as another and is bound here gets its own questions, as
			// any new agent group does.
			f.opts.Bound = boundGroups(f.binds)
			plan, err := pack.Build(c.Context(), st, p, f.opts)
			if err != nil {
				return err
			}
			answers, open, calls, err := a.checkAnswers(c.Context(), st, plan, cfg.Home, &f.answerFlags)
			if err != nil {
				return err
			}
			var openAnswers, callAnswers map[string]map[string]bool
			if open != nil {
				openAnswers = open.Answers
			}
			if calls != nil {
				callAnswers = calls.Answers
			}
			var data any = plan
			if f.answerFlags.answered() {
				data = map[string]any{"plan": plan, "answers": answers, "open_threads": open, "calls": calls}
			}
			if err := a.out(c.OutOrStdout(), data, func(w io.Writer) { printPlan(w, plan, answers, openAnswers, callAnswers, nil, true) }); err != nil {
				return err
			}
			if len(plan.Refused) > 0 {
				return errCheckFailed
			}
			return nil
		},
	}
	f.packFlags.register(check, "would be")
	f.answerFlags.register(check)
	return check
}
