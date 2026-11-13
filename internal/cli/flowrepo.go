package cli

// A named flow may live in depot repositories instead of the store, one
// file per depot, pointed at or not yet. flow show <name> falls back to
// them on a store miss: every depot carrying the file is shown, each
// under its own depot heading, in the view flow show <depot> prints for
// that flow, and --svg draws the one depot's.

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"yardr.dev/yardr/internal/flowdraw"
	"yardr.dev/yardr/internal/store"
)

// A repoFlowHit is a depot whose repository carries the named flow file,
// with the file and the repository's flows beside it.
type repoFlowHit struct {
	depot string
	file  store.RepoFlow
	repo  *store.RepoFlows
}

// repoFlowHits is every depot whose repository carries a flow file called
// name, depots sorted: the repositories' answer to a store miss. A name is
// the file's as flow list derives it, so a type's file answers to
// type/<type>. A depot whose repository cannot be read warns and is
// skipped, as in flow list.
func repoFlowHits(warn io.Writer, ctx context.Context, st store.API, name string) ([]repoFlowHit, error) {
	depots, err := st.Depots(ctx)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, d := range depots {
		names = append(names, d.Name)
	}
	slices.Sort(names)
	var hits []repoFlowHit
	for _, depot := range names {
		repo, err := st.RepoFlows(ctx, depot)
		if err != nil {
			fmt.Fprintf(warn, "warning: depot %s: %v\n", depot, err)
			continue
		}
		for _, rf := range repo.Flows {
			if strings.TrimSuffix(strings.TrimPrefix(rf.Path, store.RepoFlowDir+"/"), ".flow") == name {
				hits = append(hits, repoFlowHit{depot: depot, file: rf, repo: repo})
			}
		}
	}
	return hits, nil
}

// A namedFlowView is a repository flow shown by name: the depot whose
// repository carries the file, beside the view flow show <depot>
// prints for it, which the embedded flowView serves flat.
type namedFlowView struct {
	Depot string `json:"depot"`
	flowView
}

// showRepoNamedFlow prints the repository flow files called name: every
// depot whose repository carries one, each under its own depot heading,
// depots sorted, in the view flow show <depot> prints for that flow. A
// name no depot's repository carries keeps the store miss's error.
func (a *app) showRepoNamedFlow(w, warn io.Writer, ctx context.Context, st store.API, name string) error {
	hits, err := repoFlowHits(warn, ctx, st, name)
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		return fmt.Errorf("%s is neither a depot nor a named flow: %w", name, store.ErrNotFound)
	}
	views := make([]namedFlowView, 0, len(hits))
	for _, h := range hits {
		v, err := a.repoNamedView(warn, ctx, st, h, name)
		if err != nil {
			return err
		}
		views = append(views, namedFlowView{Depot: h.depot, flowView: v})
	}
	return a.out(w, views, func(w io.Writer) {
		for i, v := range views {
			if i > 0 {
				fmt.Fprintln(w)
			}
			fmt.Fprintf(w, "depot %s:\n", v.Depot)
			writeFlowView(w, v.flowView)
		}
	})
}

// repoNamedView is the view of the repository flow hit h: the governing
// flow resolving to the same file, as flow show <depot> shows it, or the
// file itself for the default type when nothing points at it yet. A file
// that is no flow shows with its error, as flow list shows it.
func (a *app) repoNamedView(warn io.Writer, ctx context.Context, st store.API, h repoFlowHit, name string) (flowView, error) {
	rf := h.file
	df := store.DepotFlow{
		Type:   rf.Type,
		Flow:   rf.Flow,
		Source: store.FlowSource{Kind: "repo", Type: rf.Type, Commit: h.repo.Commit, Path: rf.Path},
	}
	if rf.Error != "" {
		return flowView{
			Flow:     store.Flow{Name: name},
			Source:   df.Source,
			Warnings: []string{rf.Path + ": " + rf.Error},
		}, nil
	}
	all, err := st.DepotFlows(ctx, h.depot)
	if err != nil {
		// As flow list: a depot whose pointers do not resolve still
		// shows its files.
		fmt.Fprintf(warn, "warning: depot %s: %v\n", h.depot, err)
	}
	matched := false
	for _, g := range all {
		// DepotFlows orders the depot's own first, then by type: the
		// first resolving to the file is shown.
		if !g.Source.InStore() && g.Source.Path == rf.Path {
			df, matched = g, true
			break
		}
	}
	if !matched {
		// As the store resolves a repository flow's stage files
		// (stageFilesOf): each stage's file beside it.
		files := map[string]string{}
		for _, s := range rf.Flow.Stages {
			if sf := h.repo.StageFile(rf.Flow.Name, s); sf != nil {
				files[s] = sf.Path
			}
		}
		df.StageFiles = files
	}
	rt, err := readRouting(ctx, st)
	if err != nil {
		return flowView{}, err
	}
	beads, err := st.ListBeads(ctx, store.BeadFilter{Depot: h.depot, Viewer: a.who()})
	if err != nil {
		return flowView{}, err
	}
	typed := map[string]bool{}
	for _, g := range all {
		typed[g.Type] = g.Type != ""
	}
	v, err := flowViewOf(ctx, st, h.depot, df, viewReads{rt: rt, beads: beads, typed: typed, repo: h.repo, home: a.home()})
	if err != nil {
		return flowView{}, err
	}
	stranded, err := st.Stranded(ctx, h.depot)
	if err != nil {
		return flowView{}, err
	}
	for _, sb := range stranded {
		if sb.Flow == v.Flow.Name && sb.Source == v.Source {
			v.Warnings = append(v.Warnings, strandedWarning(sb))
		}
	}
	v.Warnings = append(v.Warnings, namedRepoFlowStrays(h.repo, h.file)...)
	return v, nil
}

// namedRepoFlowStrays are the stray stage files that belong to rf, using
// the repository's longest-flow-directory rule. A named view has only one
// flow, so attaching every unmatched file would blame it for other flows'
// files in the depot.
func namedRepoFlowStrays(repo *store.RepoFlows, rf store.RepoFlow) []string {
	want := strings.TrimSuffix(rf.Path, ".flow") + "/"
	var out []string
	for _, warning := range repo.StrayStageFiles() {
		longest := ""
		for _, candidate := range repo.Flows {
			dir := strings.TrimSuffix(candidate.Path, ".flow") + "/"
			if strings.HasPrefix(warning, dir) && len(dir) > len(longest) {
				longest = dir
			}
		}
		if longest == want {
			out = append(out, "stage file "+warning)
		}
	}
	return out
}

// repoDiagram is what flow show --svg draws of name when no depot or store
// flow is called that: the repository flow one depot carries, with no
// group at its stages, as for a named flow of the store. A name no
// repository carries keeps the store miss's error; one several carry
// names them, and the depot form draws one.
func repoDiagram(warn io.Writer, ctx context.Context, st store.API, name, which string) (*flowdraw.Diagram, error) {
	if which != "" && which != name {
		return nil, fmt.Errorf("%s is a named flow, and --flow picks among a depot's flows", name)
	}
	hits, err := repoFlowHits(warn, ctx, st, name)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("%s is neither a depot nor a named flow: %w", name, store.ErrNotFound)
	}
	if len(hits) > 1 {
		var depots []string
		for _, h := range hits {
			depots = append(depots, h.depot)
		}
		return nil, fmt.Errorf("%s is a repository flow in depots %s: draw one depot's with flow show <depot> --svg --flow %s", name, strings.Join(depots, ", "), name)
	}
	if hits[0].file.Error != "" {
		return nil, fmt.Errorf("%s: %s", hits[0].file.Path, hits[0].file.Error)
	}
	return &flowdraw.Diagram{Flow: *hits[0].file.Flow}, nil
}
