package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"io"
	"yardr.dev/yardr/internal/limits"
	"yardr.dev/yardr/internal/store"
)

func (a *app) flowCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "flow", Short: "Bead lifecycles: named flows of allowed stage transitions and terminal stages"}
	var file string
	set := &cobra.Command{
		Use:   "set <name> -f <file>",
		Short: "Set (replace) a named flow; depots and types point at it with 'yardr depot flow'",
		Example: `  yardr flow set code -f flows/code.flow
  yardr depot flow yard code                   # depot yard's beads follow it
  yardr depot flow webapp code                 # and webapp's, from the same flow

A flow file has one rule per line (# starts a comment):

  backlog -> new
  new -> review | backlog
  review -> approved | new
  approved -> merged | merge | new
  merge -> approved
  decide -> backlog
  merged terminal

Stages are ordered as the rules first name them; the first stage is where a
bead in a stage the flow does not know rejoins it. A JSON flow lists them
under "stages", or is refused when two source stages have no order.

Replacing a flow is refused, naming the beads, while an open bead that
follows it (through a depot's or type's pointer, or its own pin) sits in a
stage the new version lacks. Advance them to a stage it has, or pin them to
another flow (yardr bead update <id> --meta flow=<name>), then set it again.

The flow's stage files, markdown saying what each stage is for, are plain
files in the yard's home: <home>/flows/<name>/<stage>.md. Whoever works a
bead in the stage is shown it, in the brief and in yardr prime, in every depot
that follows the flow; yardr flow show <name> says which stages have one. (A
flow in a depot's repository has them beside it: ` + store.RepoFlowDir + `/<name>/<stage>.md.)`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if file == "" {
				return fmt.Errorf("-f is required (- reads stdin)")
			}
			var text string
			if file == "-" {
				b, err := io.ReadAll(c.InOrStdin())
				if err != nil {
					return err
				}
				text = strings.TrimRight(string(b), "\n")
			} else {
				raw, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				text = string(raw)
			}
			f, err := store.ParseFlow(text)
			if err != nil {
				return err
			}
			f.Name = args[0]
			st, err := a.store(c.Context())
			if err != nil {
				return err
			}
			saved, err := st.PutFlow(c.Context(), *f, a.who())
			if err != nil {
				return err
			}
			// Beside the result, so --json stays the flow.
			a.warnStagesWithoutAsk(c.ErrOrStderr(), c.Context(), st, saved)
			return a.out(c.OutOrStdout(), saved, func(w io.Writer) { fmt.Fprintf(w, "flow %s: %d stages\n", saved.Name, len(saved.Stages)) })
		},
	}
	set.Flags().StringVarP(&file, "file", "f", "", "flow file (- reads stdin)")

	show := &cobra.Command{
		Use:     "show <depot|flow>",
		Short:   "Show the flows governing a depot (each stage's group, members, open beads, stage file, next), or one named flow",
		Example: `  yardr flow show yardr`,
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			st, err := a.store(c.Context())
			if err != nil {
				return err
			}
			if _, err := st.GetDepot(c.Context(), args[0]); errors.Is(err, store.ErrNotFound) {
				return a.showNamedFlow(c.OutOrStdout(), c.ErrOrStderr(), c.Context(), st, args[0])
			}
			views, err := flowViews(c.Context(), st, args[0], a.home(), a.who())
			if err != nil {
				return err
			}
			if len(views) == 0 {
				return a.out(c.OutOrStdout(), views, func(w io.Writer) {
					fmt.Fprintf(w, "depot %s has no flow: beads may move to a stage with a matching route\n", args[0])
				})
			}
			return a.out(c.OutOrStdout(), views, func(w io.Writer) {
				for i, v := range views {
					if i > 0 {
						fmt.Fprintln(w)
					}
					writeFlowView(w, v)
				}
			})
		},
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List named flows in the store and flows in depots' repositories, with what each governs",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			st, err := a.store(c.Context())
			if err != nil {
				return err
			}
			rows, err := flowList(c.ErrOrStderr(), c.Context(), st)
			if err != nil {
				return err
			}
			return a.out(c.OutOrStdout(), rows, func(w io.Writer) {
				var out [][]string
				for _, r := range rows {
					out = append(out, []string{r.Name, r.Source, strconv.Itoa(r.Stages), or(strings.Join(r.Governs, ", "), "-")})
				}
				table(w, "NAME\tSOURCE\tSTAGES\tGOVERNS", out)
			})
		},
	}

	rm := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a named flow (refused while a depot, type or open bead uses it)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			st, err := a.store(c.Context())
			if err != nil {
				return err
			}
			return st.DeleteFlow(c.Context(), args[0], a.who())
		},
	}
	cmd.AddCommand(set, a.flowSetStageCmd(), a.flowCopyCmd(), show, list, rm)
	return cmd
}

// showNamedFlow prints a named flow from the store and the depots and types
// pointing at it, or the repositories' flows of that name when the store
// has none.
func (a *app) showNamedFlow(w, warn io.Writer, ctx context.Context, st store.API, name string) error {
	f, err := st.GetFlow(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return a.showRepoNamedFlow(w, warn, ctx, st, name)
	} else if err != nil {
		return err
	}
	ptrs, err := st.FlowPointers(ctx)
	if err != nil {
		return err
	}
	users := []string{}
	for _, p := range ptrs {
		if p.Flow == name {
			users = append(users, p.String())
		}
	}
	// The flow is in the store, so its stage files are in the yard's home.
	have, stray, err := store.HomeStageFiles(a.home(), f)
	if err != nil {
		return err
	}
	var broken []string
	files := map[string]*store.StageFile{}
	for _, s := range f.Stages {
		if have[s] == "" {
			continue
		}
		sf, err := store.HomeStageFile(a.home(), f.Name, s)
		if err != nil {
			return err
		}
		files[s] = sf
		if b := sf.BrokenAsk(f, s); b != "" {
			broken = append(broken, b)
		}
	}
	unasked, err := stagesWithoutAsk(ctx, st, f, files)
	if err != nil {
		return err
	}
	v := map[string]any{"flow": f, "pointed_at_by": users}
	if len(have) > 0 {
		v["stage_files"] = have
	}
	if len(stray) > 0 {
		v["stray_stage_files"] = stray
	}
	if len(broken) > 0 {
		v["broken_stage_files"] = broken
	}
	if len(unasked) > 0 {
		v["stages_without_ask"] = unasked
	}
	return a.out(w, v, func(w io.Writer) {
		var with []string
		for _, s := range f.Stages {
			if have[s] != "" {
				with = append(with, s)
			}
		}
		fmt.Fprintf(w, "flow %s: %d stages, pointed at by %s\n", f.Name, len(f.Stages), or(strings.Join(users, ", "), "nothing"))
		fmt.Fprintf(w, "stage files: %s (%s)\n\n%s", or(strings.Join(with, ", "), "none"), filepath.Join(store.HomeStageDir(a.home(), f.Name), "<stage>.md"), f)
		for _, s := range stray {
			fmt.Fprintf(w, "warning: stage file %s\n", s)
		}
		for _, s := range broken {
			fmt.Fprintf(w, "warning: stage file %s\n", s)
		}
		for _, s := range unasked {
			fmt.Fprintf(w, "warning: %s\n", noAskWarning(s))
		}
	})
}

// home is the yard's home, where a store flow's stage files live; "" when
// this run has none.
func (a *app) home() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Home
}

type flowRow struct {
	Name    string   `json:"name"`
	Source  string   `json:"source"` // store, or repo <depot>@<sha> <path>
	Stages  int      `json:"stages"`
	Error   string   `json:"error,omitempty"`
	Governs []string `json:"governs"` // depots and types it governs (pins aside)
}

// flowList is every named flow in the store, then every flow file in each
// depot's repository, with what each one governs now.
func flowList(warn io.Writer, ctx context.Context, st store.API) ([]flowRow, error) {
	flows, err := st.ListFlows(ctx)
	if err != nil {
		return nil, err
	}
	depots, err := st.Depots(ctx)
	if err != nil {
		return nil, err
	}
	governs := map[string][]string{} // store:<name> or repo:<depot>:<path>
	var rows []flowRow
	var repoRows []flowRow
	for _, r := range depots {
		rfs, err := st.DepotFlows(ctx, r.Name)
		if err != nil {
			fmt.Fprintf(warn, "warning: depot %s: %v\n", r.Name, err)
		}
		for _, rf := range rfs {
			k := "store:" + rf.Flow.Name
			if rf.Source.Path != "" {
				k = "repo:" + r.Name + ":" + rf.Source.Path
			}
			governs[k] = append(governs[k], store.FlowPointer{Depot: r.Name, Type: rf.Type}.String())
		}
		repo, err := st.RepoFlows(ctx, r.Name)
		if err != nil {
			fmt.Fprintf(warn, "warning: depot %s: %v\n", r.Name, err)
			continue
		}
		src := fmt.Sprintf("repo %s@%.7s", r.Name, repo.Commit)
		if repo.Dir != "" {
			src = "dir " + r.Name
		}
		for _, f := range repo.Flows {
			row := flowRow{Name: strings.TrimSuffix(strings.TrimPrefix(f.Path, store.RepoFlowDir+"/"), ".flow"),
				Source: src, Error: f.Error,
				Governs: governs["repo:"+r.Name+":"+f.Path]}
			if f.Flow != nil {
				row.Stages = len(f.Flow.Stages)
			}
			repoRows = append(repoRows, row)
		}
	}
	for _, f := range flows {
		rows = append(rows, flowRow{Name: f.Name, Source: "store", Stages: len(f.Stages), Governs: governs["store:"+f.Name]})
	}
	return append(rows, repoRows...), nil
}

type flowView struct {
	Flow     store.Flow       `json:"flow"`
	Type     string           `json:"type,omitempty"` // the bead type it governs; "" for the depot's own
	Source   store.FlowSource `json:"source"`
	Stages   []stageView      `json:"stages"`
	Warnings []string         `json:"warnings,omitempty"`
}

type stageView struct {
	Stage    string   `json:"stage"`
	Group    string   `json:"group,omitempty"`   // the route a bead of the flow's type in the depot would take
	Else     []string `json:"else,omitempty"`    // the route's further groups, in order: one is chosen at the claim, by the quota then
	Human    bool     `json:"human,omitempty"`   // Group is a manual group
	Members  []string `json:"members,omitempty"` // who may move a bead on from a human stage; none = anyone
	Yard     string   `json:"yard,omitempty"`    // the linked yard that works the stage: Group is a peer group
	Next     []string `json:"next"`
	Terminal bool     `json:"terminal,omitempty"`
	Open     int      `json:"open"` // open beads in the stage that this flow governs
	// StageFile is the stage's file, if it has one: in the depot's repository,
	// or in the yard's home for a store flow.
	StageFile string `json:"stage_file,omitempty"`
	// Siding is set when Group is a siding (store.SidingOf): what it folds by
	// and what waits in the stage now.
	Siding *sidingView `json:"siding,omitempty"`
}

// sidingView is a siding stage as flow show says it: the numbers its group
// folds by, where the carrier goes, and what waits in the stage now.
type sidingView struct {
	Fold      int    `json:"fold"`       // beads that fill it
	FoldAfter string `json:"fold_after"` // how long after the latest arrival it folds anyway, as a duration is written (ParseSpan)
	FoldMax   int    `json:"fold_max"`   // the most points one fold may carry
	// To is the stage the carrier goes to: the first the flow leads on to.
	// Absent when it leads nowhere, which Warning then says.
	To      string `json:"to,omitempty"`
	Waiting int    `json:"waiting"` // open beads in the stage that this flow governs
	Points  int    `json:"points"`  // what they weigh together
	Stuck   int    `json:"stuck"`   // those a fold would leave where they are (store.SidingBead)
	// FoldsAt is when the fold_after of the latest bead to arrive ends: the
	// time the siding folds at if the count is not reached before. Absent
	// with nothing waiting.
	FoldsAt *time.Time `json:"folds_at,omitempty"`
	// Warning says why nothing will fold here.
	Warning string `json:"warning,omitempty"`
}

// writeFlowView writes one view as flow show <depot> prints it: the flow
// with its source, each stage's group, members, open beads, stage file
// and next, the siding lines and the warnings. A repository flow shown
// by name is written the same way, under its depot's heading.
func writeFlowView(w io.Writer, v flowView) {
	fmt.Fprintf(w, "flow %s (%s)\n", v.Flow.Name, v.Source)
	rows := [][]string{}
	for _, s := range v.Stages {
		// Edges names the outcome that takes each move, if any.
		next := strings.Join(v.Flow.Edges(s.Stage), " | ")
		switch {
		case s.Terminal:
			next = "(terminal: closes the bead)"
		case len(s.Next) == 0:
			next = "(no transitions: any stage)"
		case v.Flow.QuestionBack(s.Stage):
			// A bead a question brought here may move back
			// to the stage that asked, past the listed moves.
			next += " (back)"
		}
		members := "-"
		if s.Human {
			members = or(strings.Join(s.Members, ","), "anyone")
		} else if s.Yard != "" {
			// Who works it is another yard, not anyone here.
			members = "yard " + s.Yard
		}
		file := "-"
		if s.StageFile != "" {
			file = "yes"
		}
		rows = append(rows, []string{s.Stage, or(strings.Join(append([]string{s.Group}, s.Else...), " else "), "-"), members, strconv.Itoa(s.Open), file, next})
	}
	table(w, "STAGE\tGROUP\tMEMBERS\tOPEN\tFILE\tNEXT", rows)
	for _, s := range v.Stages {
		if s.Siding != nil {
			for _, line := range sidingLines(s.Stage, s.Siding, time.Now()) {
				fmt.Fprintln(w, line)
			}
		}
	}
	for _, warning := range v.Warnings {
		fmt.Fprintf(w, "warning: %s\n", warning)
	}
}

// flowViews describes the flows governing depot's beads, the depot's own first.
// home is the yard's home, where the stage files of a store flow are. A stage
// file named after no stage is a warning: a store flow's in the home, and
// any on the depot's base (store.RepoFlows.StrayStageFiles).
func flowViews(ctx context.Context, st store.API, depot, home string, viewer ...string) ([]flowView, error) {
	if _, err := st.GetDepot(ctx, depot); err != nil {
		return nil, err
	}
	all, err := st.DepotFlows(ctx, depot)
	if err != nil {
		return nil, err
	}
	rt, err := readRouting(ctx, st)
	if err != nil {
		return nil, err
	}
	who := ""
	if len(viewer) > 0 {
		who = viewer[0]
	}
	beads, err := st.ListBeads(ctx, store.BeadFilter{Depot: depot, Viewer: who})
	if err != nil {
		return nil, err
	}
	typed := map[string]bool{}
	for _, rf := range all {
		typed[rf.Type] = rf.Type != ""
	}
	// The depot's base, for what its stage files say: a broken ## Ask
	// section warns under its stage, beside the stray files below.
	repo, err := st.RepoFlows(ctx, depot)
	if err != nil {
		return nil, err
	}
	rd := viewReads{rt: rt, beads: beads, typed: typed, repo: repo, home: home}
	views := []flowView{}
	for _, rf := range all {
		v, err := flowViewOf(ctx, st, depot, rf, rd)
		if err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	attachStrays(views, repo)
	// Beads in a stage their flow lacks, under the flow that lacks it; a
	// bead pinning a flow the depot does not follow is told under the depot's.
	stranded, err := st.Stranded(ctx, depot)
	if err != nil {
		return nil, err
	}
	for _, sb := range stranded {
		i := slices.IndexFunc(views, func(v flowView) bool { return v.Flow.Name == sb.Flow && v.Source == sb.Source })
		if i < 0 {
			// A bead may pin a flow that does not otherwise govern this depot.
			// Keep its warning visible even when there are no normal views.
			views = append(views, flowView{Flow: store.Flow{Name: sb.Flow}, Source: sb.Source})
			i = len(views) - 1
		}
		views[i].Warnings = append(views[i].Warnings, strandedWarning(sb))
	}
	return views, nil
}

// viewReads is what one depot's views are built from, read once: the
// routing, the depot's beads, which types have a flow of their own, the
// depot's repository flows and the yard's home.
type viewReads struct {
	rt    *routing
	beads []store.Bead
	typed map[string]bool
	repo  *store.RepoFlows
	home  string
}

// flowViewOf describes one of depot's flows: its stages with the group
// each is routed to, the open beads standing in them, and its warnings,
// as flow show prints them. rf is one of the depot's governing flows,
// or a repository flow shown by name that governs nothing yet.
func flowViewOf(ctx context.Context, st store.API, depot string, rf store.DepotFlow, rd viewReads) (flowView, error) {
	f := rf.Flow
	v := flowView{Flow: *f, Type: rf.Type, Source: rf.Source}
	var err error
	groups := rd.rt.groups
	files := rf.StageFiles
	if rf.Source.InStore() {
		var stray []string
		if files, stray, err = store.HomeStageFiles(rd.home, f); err != nil {
			return flowView{}, err
		}
		for _, s := range stray {
			v.Warnings = append(v.Warnings, "stage file "+s)
		}
	}
	for _, s := range f.Stages {
		sv := stageView{Stage: s, Next: []string{}, Terminal: f.IsTerminal(s), StageFile: files[s]}
		if next := f.Transitions[s]; len(next) > 0 {
			sv.Next = next
		}
		// A depot flow is shown for the default type.
		var siding *store.Siding
		if r := rd.rt.take(&depot, rf.Type, s); r != nil {
			sv.Group, sv.Else = r.Worker(), r.Further()
			if i := slices.IndexFunc(groups, func(g store.Group) bool { return g.Name == sv.Group }); i >= 0 && groups[i].Runner == "manual" {
				sv.Human, sv.Members = true, groups[i].Members
				if si, ok := store.SidingOf(groups[i]); ok {
					siding = &si
				}
			} else if i >= 0 {
				sv.Yard = groups[i].AtPeer()
			}
		} else if !sv.Terminal {
			v.Warnings = append(v.Warnings, fmt.Sprintf("stage %s has no route: beads there wait for a human", s))
		}
		var waiting []string
		for _, b := range rd.beads {
			// A bead that pins its flow follows that one instead.
			governed := store.PinnedFlow(&b) == "" && (b.Type == rf.Type || (rf.Type == "" && !rd.typed[b.Type]))
			if governed && b.Stage == s {
				sv.Open++
				waiting = append(waiting, b.ID)
			}
		}
		if siding != nil {
			if sv.Siding, err = newSidingView(ctx, st, *siding, sv, waiting); err != nil {
				return flowView{}, err
			}
		}
		// A broken ## Ask section in the stage's file warns beside
		// it: the repository's, or the yard's home for a store flow.
		var sf *store.StageFile
		if rf.Source.InStore() {
			if files[s] != "" {
				if sf, err = store.HomeStageFile(rd.home, f.Name, s); err != nil {
					return flowView{}, err
				}
			}
		} else {
			sf = rd.repo.StageFile(rf.Flow.Name, s)
		}
		if broken := sf.BrokenAsk(f, s); broken != "" {
			v.Warnings = append(v.Warnings, "stage file "+broken)
		}
		if waitsForPerson(f, sv, rd.rt.byName) && !sf.HasAsk() {
			v.Warnings = append(v.Warnings, noAskWarning(s))
		}
		v.Stages = append(v.Stages, sv)
	}
	return v, nil
}

// attachStrays warns of a stage file on the base that belongs to no
// stage, under the flow whose directory it is in, else under the depot's
// own: nobody is shown it.
func attachStrays(views []flowView, repo *store.RepoFlows) {
	for _, s := range repo.StrayStageFiles() {
		// The longest directory wins: type/train/x.md is type/train.flow's.
		at, most := 0, 0
		for i, v := range views {
			if dir := strings.TrimSuffix(v.Source.Path, ".flow") + "/"; !v.Source.InStore() && strings.HasPrefix(s, dir) && len(dir) > most {
				at, most = i, len(dir)
			}
		}
		if len(views) > 0 {
			views[at].Warnings = append(views[at].Warnings, "stage file "+s)
		}
	}
}

// strandedWarning is what flow show says of a bead in a stage its flow
// lacks, under the flow that lacks it.
func strandedWarning(sb store.StrandedBead) string {
	return fmt.Sprintf("bead %s: it is not routed and waits until someone advances it (yardr bead advance %s --to <stage>)", sb, sb.Bead)
}

// newSidingView describes the stage sv as a siding that folds by si, with the
// beads waiting in it. When each entered and whether a fold would take it is
// the store's to say (SidingBeads). The carrier goes to the first stage the
// flow leads on to; a stage no edge leaves has none, so nothing folds there
// (the flow's own way back to its first stage from such a stage is no place
// to fold to).
func newSidingView(ctx context.Context, st store.API, si store.Siding, sv stageView, waiting []string) (*sidingView, error) {
	v := &sidingView{Fold: si.Fold, FoldAfter: store.Span(si.After), FoldMax: si.Max, Waiting: len(waiting)}
	if len(sv.Next) > 0 {
		v.To = sv.Next[0]
	} else {
		v.Warning = fmt.Sprintf("%s has fold=%d but no next stage; nothing will fold here", sv.Stage, si.Fold)
	}
	if len(waiting) == 0 {
		return v, nil
	}
	beads, err := st.SidingBeads(ctx, waiting)
	if err != nil {
		return nil, err
	}
	var latest time.Time
	for _, b := range beads {
		v.Points += b.Points
		if b.Stuck != "" {
			v.Stuck++
		}
		if b.Entered.After(latest) {
			latest = b.Entered
		}
	}
	if !latest.IsZero() {
		at := latest.Add(si.After)
		v.FoldsAt = &at
	}
	return v, nil
}

// sidingLines are the lines flow show prints under the table for a siding
// stage: what it folds by and where the carrier goes, then what waits now and
// when it folds, indented to stand under the first. A stage with no next
// stage has the warning in place of the second. now is the day a time of
// today is written without its date.
func sidingLines(stage string, v *sidingView, now time.Time) []string {
	head := stage + ": "
	first := fmt.Sprintf("%sfolds at %s, or %s after the latest arrives; at most %s", head, plural(v.Fold, "bead"), v.FoldAfter, plural(v.FoldMax, "point"))
	if v.To != "" {
		first += "; the carrier goes to " + v.To
	}
	if v.Warning != "" {
		return []string{first, "warning: " + v.Warning}
	}
	second := fmt.Sprintf("now %d waiting, %s", v.Waiting, plural(v.Points, "point"))
	if v.Stuck > 0 {
		second += fmt.Sprintf(", %d stuck", v.Stuck)
	}
	second += fmt.Sprintf("; folds at %d", v.Fold)
	if v.FoldsAt != nil {
		second += " or at " + limits.Clock(*v.FoldsAt, now)
	}
	return []string{first, strings.Repeat(" ", len(head)) + second}
}
