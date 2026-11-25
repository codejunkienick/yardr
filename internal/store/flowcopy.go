// Copying a depot's repository flows into the yard store. A flow in a depot's
// repository is read from the tip of its base branch, so what it says changes
// on a branch and lands, and yardr flow set and yardr flow set-stage change
// the store's flows only. An operator who wants to change a stage's context in
// the yard has first to have the flow there: this is the copy, made when the
// operator says so and never as a side effect of anything else.
//
// The copy is one write. Every flow that governs the depot from its repository
// (the file DepotFlows lists with a path: default.flow, each type's file, and
// a named flow a pointer leads to) becomes a store flow, with its stage files
// under <home>/flows/<flow>/, and the depot, and each of those types, point at
// the copies together. Together, because a pointer in front of the repository
// is read before any file of it (resolveFlow): a depot pointed at its copy of
// default.flow alone would stop reading the type files it has not copied.
package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// CopiedFlow is one repository flow that CopyRepoFlows copied into the store.
type CopiedFlow struct {
	Type       string `json:"type,omitempty"`     // "" for the depot's own flow, else the bead type it governs
	From       string `json:"from"`               // its file in the repository, e.g. .yardr/flows/default.flow
	Flow       string `json:"flow"`               // the store flow it was copied to, e.g. yardr.default
	StageFiles int    `json:"stage_files"`        // the non-empty stage files copied under <home>/flows/<flow>/
	Replaced   bool   `json:"replaced,omitempty"` // an unpointed earlier copy under that name was replaced
	Back       string `json:"back"`               // the command that points this pointer back at the repository
}

// CopiedFlows is what CopyRepoFlows did to one depot.
type CopiedFlows struct {
	Depot  string       `json:"depot"`
	Source string       `json:"source"` // repo@<short commit>, or repo (directory)
	Flows  []CopiedFlow `json:"flows"`
	// Kept names the flows that governed the depot from the store already,
	// and were left as they are: default, or type/<type>.
	Kept []string `json:"kept,omitempty"`
}

// CopyRepoFlows copies every flow of depot's repository that governs its
// beads into the store, with its stage files, and points the depot and each
// of those types at the copies, all in one write, so that no bead is ever
// governed by a half of the copy. It is the operator's (ActChangeFlow), as a
// flow's stages are; a crew member or a user proposes it as the card of the
// same name. A flow of the depot that is in the store already is left as it
// is, and a depot with none to copy is refused, naming what it has.
//
// A copy goes under <depot>.default or <depot>.<type>. A store flow of that
// name that no depot, type or open bead uses, and that no pack manages, is the
// leftover of an earlier copy that was switched back (yardr depot source
// <depot> flow depot clears the pointer and leaves the flow), and is replaced,
// its stage files with it; one in use, or a pack's, refuses the whole copy,
// naming who uses it. From a session's or a crew member's pane the write is
// recorded as that session's actor or that crew member, whatever actor says
// (paneAuthor).
//
// A copy is weighty code, as the card of the same name is: a call that does not
// say its person saw that (weighed) is refused first, before the repository is
// read or anything is written. A paired device of the operator makes the call,
// and what it writes is marked with the device (writeVia), as an approval's is.
func (s *Store) CopyRepoFlows(ctx context.Context, depot, actor string, weighed bool) (*CopiedFlows, error) {
	if err := weighedWriteRefusal("copying a depot's repository flows", weighed); err != nil {
		return nil, err
	}
	// Read before the transaction: git must not run inside one.
	repo, err := s.RepoFlows(ctx, depot)
	if err != nil {
		return nil, err
	}
	var out *CopiedFlows
	err = s.writeVia(ctx, func(w *wtx) error {
		resolved, err := paneAuthor(w.Tx, callerOf(ctx), actor)
		if err != nil {
			return err
		}
		out, err = w.copyRepoFlows(ctx, repo, resolved)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// copyFile is one stage file of a flow being copied: the repository's text,
// and what stands at its place in the home now.
type copyFile struct {
	stage, text string
	st          *stageStand
}

// copyItem is one repository flow a copy writes into the store.
type copyItem struct {
	df DepotFlow
	// pointer is the flow the depot's pointer named, where one led to this
	// flow in the repository, and "" for a file no pointer was in front of.
	pointer string
	target  string // the store flow the copy is written as
	// replaced says that a store flow, or a directory of stage files, of the
	// target's name stands unused: the copy writes over it.
	replaced bool
	files    []copyFile
	// stale are the stage files of a replaced copy that the new copy has no
	// file for, as paths on disk: they are removed, so that none of the old
	// copy is read as the new one's.
	stale []string
	back  string // the command that points the pointer back at the repository
}

// copyPlan is what a copy would do, read from the yard as it stands: the
// card shows it and the write makes it, so the one is what the other did.
type copyPlan struct {
	repo  *RepoFlows
	items []copyItem
	// kept are the places (default, type/<type>) that govern from the store
	// already, and keptWords the same with the flow, for a refusal and a card.
	kept, keptWords []string
}

// copyPlace names where a flow of typ governs in a depot: default, or
// type/<type>, the place a store pointer or a repository file has.
func copyPlace(typ string) string {
	if typ == "" {
		return "default"
	}
	return "type/" + typ
}

// source is where the plan's flows were read, as a flow's source says it:
// repo@<short commit>, or repo (directory) for a depot with no base.
func (p *copyPlan) source() string {
	return FlowSource{Kind: "repo", Commit: p.repo.Commit}.String()
}

// governingFlows lists the flows that govern depot's beads, pins aside, as
// DepotFlows does: the depot's own first, then one per type that has a
// pointer or a file, by type, and not a type's file that a pointer of the
// depot stands in front of. It is the same walk inside the write, since
// flows.go is full: a copy is planned from the pointers the write holds.
func (w *wtx) governingFlows(repo *RepoFlows) ([]DepotFlow, error) {
	depot := repo.Depot
	var types []string
	if err := each(w.Tx, bFlowPtrs, join(depot, ""), func(_ []byte, p *FlowPointer) error {
		if p.Depot == depot && p.Type != "" {
			types = append(types, p.Type)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for _, rf := range repo.Flows {
		if rf.Type != "" {
			types = append(types, rf.Type)
		}
	}
	slices.Sort(types)
	var out []DepotFlow
	for _, t := range append([]string{""}, slices.Compact(types)...) {
		// A governing file that does not parse fails here, naming it, before
		// anything is written.
		f, src, err := resolveFlow(w.Tx, &depot, t, "", repo)
		if err != nil {
			return nil, err
		}
		if f != nil && src.Type == t {
			out = append(out, DepotFlow{Type: t, Flow: f, Source: *src})
		}
	}
	return out, nil
}

// planCopy reads what a copy of repo's flows would do, and refuses what it
// may not: a depot that is gone, a governing file that does not parse, a
// target in use or a pack's, a link where the copy would write, and a depot
// with no flow of its repository to copy. It writes nothing.
func (w *wtx) planCopy(repo *RepoFlows) (*copyPlan, error) {
	depot := repo.Depot
	switch {
	case w.home == "":
		return nil, errNoStageHome
	case w.Bucket(bDepots).Get([]byte(depot)) == nil:
		return nil, fmt.Errorf("depot %s: %w", depot, ErrNotFound)
	}
	governing, err := w.governingFlows(repo)
	if err != nil {
		return nil, err
	}
	marked, retried := w.copyMarked(depot)
	plan := &copyPlan{repo: repo}
	for _, df := range governing {
		if df.Source.InStore() {
			plan.kept = append(plan.kept, copyPlace(df.Type))
			plan.keptWords = append(plan.keptWords, fmt.Sprintf("%s (flow %s)", copyPlace(df.Type), df.Flow.Name))
			continue
		}
		it, err := w.planCopyItem(repo, df, marked, retried)
		if err != nil {
			return nil, err
		}
		if i := slices.IndexFunc(plan.items, func(o copyItem) bool { return o.target == it.target }); i >= 0 {
			return nil, fmt.Errorf("the flows of %s and %s of depot %s would both be copied to the store flow %s: one name cannot hold two flows",
				copyPlace(plan.items[i].df.Type), copyPlace(df.Type), depot, it.target)
		}
		plan.items = append(plan.items, it)
	}
	switch {
	case len(plan.items) > 0:
		if err := w.copyRepoStrays(plan); err != nil {
			return nil, err
		}
		return plan, nil
	case len(plan.keptWords) > 0:
		return nil, fmt.Errorf("every flow that governs depot %s is in the store already (%s), so there is no repository flow to copy", depot, strings.Join(plan.keptWords, ", "))
	}
	return nil, fmt.Errorf("depot %s has no flow in its repository (%s %s) that governs its beads, so there is nothing to copy", depot, RepoFlowDir, repo.where())
}

// planCopyItem plans the copy of the repository flow df. Its target is
// <depot>.default or <depot>.<type>, and with ".local" after it where a flow
// of the repository has that name: a store flow is looked up before the
// repository's (resolveFlow), so a copy under the repository flow's own name
// would stand in front of that flow wherever a pointer or a pin names it, and
// the command that points back at it would lead to the copy. marked says,
// for a retried write, what its first attempt replaced (copyMarked).
func (w *wtx) planCopyItem(repo *RepoFlows, df DepotFlow, marked map[string]bool, retried bool) (copyItem, error) {
	depot := repo.Depot
	it := copyItem{df: df, target: depot + ".default"}
	if df.Type != "" {
		it.target = depot + "." + df.Type
	}
	if repo.find(it.target, "") != nil {
		it.target += ".local"
	}
	switch {
	case !validFlowName.MatchString(it.target):
		return it, fmt.Errorf("the flow of %s of depot %s would be copied to %q, which is no name of a flow: use letters, digits, '.', '_' or '-'", copyPlace(df.Type), depot, it.target)
	case repo.find(it.target, "") != nil:
		return it, fmt.Errorf("the flow of %s of depot %s would be copied to %s, which the repository has a flow of too: a copy of that name would stand in front of it", copyPlace(df.Type), depot, it.target)
	}
	flag := ""
	if df.Type != "" {
		flag = " --type " + df.Type
	}
	it.back = "yardr depot source " + depot + " flow" + flag + " depot"
	if df.Source.Kind == "store" {
		// A pointer led to a flow of the repository: what points back is the
		// pointer as it was, not the clearing of it, which would leave the
		// type to the repository's type file or to none.
		var p FlowPointer
		if _, err := get(w.Tx, bFlowPtrs, join(depot, df.Type), &p); err != nil {
			return it, err
		}
		it.pointer = p.Flow
		it.back = "yardr depot flow " + depot + flag + " " + p.Flow
	}
	keep := map[string]bool{}
	for _, stage := range df.Flow.Stages {
		sf := repo.StageFile(df.Flow.Name, stage)
		if sf == nil {
			continue
		}
		// Read now, so that a link or a file in the way refuses the copy
		// before it has written anything.
		st, err := standStage(w.home, it.target, stage)
		if err != nil {
			return it, err
		}
		it.files = append(it.files, copyFile{stage: stage, text: sf.Text, st: st})
		keep[stage] = true
	}
	dir, stands, err := w.homeFlowDir(it.target)
	if err != nil {
		return it, err
	}
	exists := w.Bucket(bFlows).Get([]byte(it.target)) != nil || stands
	if err := w.copyTargetFree(it.target, exists); err != nil {
		return it, err
	}
	if !exists {
		return it, nil
	}
	it.replaced = true
	if retried {
		// What the first attempt wrote stands on disk, and is no earlier copy.
		it.replaced = marked[it.target]
	}
	if stands {
		if it.stale, err = strayHomeStageFiles(dir, "", keep); err != nil {
			return it, fmt.Errorf("flow %s: %w", it.target, err)
		}
	}
	return it, nil
}

// copyTargetFree holds a copy target to being unused: a flow it would write
// over is followed by no pointer or open bead's pin and managed by no pack,
// and a name with no flow yet is followed by no broken pin that the copy
// would make resolve.
func (w *wtx) copyTargetFree(target string, exists bool) error {
	users, err := flowUsers(w.Tx, target)
	if err != nil {
		return err
	}
	if len(users) > 0 {
		if exists {
			return fmt.Errorf("flow %s stands in the store and is in use by %s: a copy would write over a flow they follow, so nothing is copied; point them at another flow first", target, strings.Join(users, ", "))
		}
		return fmt.Errorf("flow name %s is in use by %s: a copy would make the name resolve for them, so nothing is copied; point them at another flow first", target, strings.Join(users, ", "))
	}
	pack, depot, err := w.flowPackManaged(target)
	if err != nil {
		return err
	}
	if pack != "" {
		into := "the yard"
		if depot != "" {
			into = "depot " + depot
		}
		if !exists {
			return fmt.Errorf("flow name %s is managed by pack %s, imported into %s: a copy would take a name the pack manages, so nothing is copied", target, pack, into)
		}
		return fmt.Errorf("flow %s stands in the store and is the pack %s's, imported into %s: a copy would write over a flow the pack manages, so nothing is copied", target, pack, into)
	}
	return nil
}

// copyRepoStrays checks the flows a copy would leave in place before either
// a terminal write or an ask card is made. Copying preserves each governing
// repository flow's stages, so the current resolution has the same stages as
// the pointers after the copy; a pre-existing bead stranded by repository
// drift therefore refuses the proposal instead of making a card approval
// fail later.
func (w *wtx) copyRepoStrays(plan *copyPlan) error {
	for _, it := range plan.items {
		strays, err := strayBeads(w.Tx, plan.repo.Depot, it.df.Type, false, plan.repo, nil)
		if err != nil {
			return err
		}
		if len(strays) > 0 {
			p := FlowPointer{Depot: plan.repo.Depot, Type: it.df.Type, Flow: it.target}
			return fmt.Errorf("%s: %s; advance them to a stage the flow has, or pin them to the flow they follow now (yardr bead update <id> --meta flow=<name>), then copy again",
				p.change(), strings.Join(strays, ", "))
		}
	}
	return nil
}

// homeFlowDir is where the stage files of the store flow named flow stand in
// the home as the disk has the home, and whether that directory stands. The
// walk to it follows no symbolic link, and one in the way is refused.
func (w *wtx) homeFlowDir(flow string) (dir string, stands bool, err error) {
	_, real, err := homeDirsAt(w.home, "flow "+flow)
	if err != nil {
		return "", false, err
	}
	dir = HomeStageDir(real, flow)
	if stands, err = stageDirs(FlowsDir(real), dir, false); err != nil {
		return "", false, fmt.Errorf("flow %s: %w", flow, err)
	}
	return dir, stands, nil
}

// strayHomeStageFiles lists, as paths on disk, the markdown files under dir
// (rel is dir's place in the flow's directory, as a stage name has it) that
// are the stage file of no stage of keep: what is left of an earlier copy once
// the new one is written. A link met on the way is refused, not followed and
// not removed: the yard removes a stage file only where a plain file stands.
func strayHomeStageFiles(dir, rel string, keep map[string]bool) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		p, name := filepath.Join(dir, e.Name()), path.Join(rel, e.Name())
		switch {
		case e.Type()&fs.ModeSymlink != 0:
			return nil, fmt.Errorf("%s is a symbolic link: the yard removes a stage file only where a plain file stands", p)
		case e.IsDir():
			sub, err := strayHomeStageFiles(p, name, keep)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
		case e.Type().IsRegular() && filepath.Ext(e.Name()) == ".md" && !keep[strings.TrimSuffix(name, ".md")]:
			out = append(out, p)
		}
	}
	return out, nil
}

// marker is what a copy leaves in w.stageWrote, the note that outlives a
// transaction attempt of Store.write (a stage file's write keeps its own
// there): bbolt.Batch may run the write again after rolling its transaction
// back, and the files the first attempt wrote stay on disk. The retry would
// take a directory of its own making for an earlier copy and answer
// Replaced for a copy that replaced nothing, and a card that said "new"
// would read as another card at its retry. The marker names the targets the
// first attempt replaced, so the retry answers as the first did.
func (p *copyPlan) marker() string {
	var replaced []string
	for _, it := range p.items {
		if it.replaced {
			replaced = append(replaced, it.target)
		}
	}
	return "copy-repo " + p.repo.Depot + ":" + strings.Join(replaced, ",")
}

// copyMarked reads the marker of a first attempt of this copy: the targets it
// replaced, and whether there was a first attempt at all.
func (w *wtx) copyMarked(depot string) (replaced map[string]bool, retried bool) {
	if w.stageWrote == nil {
		return nil, false
	}
	rest, ok := strings.CutPrefix(w.stageWrote.key, "copy-repo "+depot+":")
	if !ok {
		return nil, false
	}
	replaced = map[string]bool{}
	for _, target := range strings.Split(rest, ",") {
		if target != "" {
			replaced[target] = true
		}
	}
	return replaced, true
}

// copyRepoFlows is CopyRepoFlows inside the write, as actor, whom the
// caller's pane has been resolved to, and what the approval of a flow
// copy-repo card runs: the plan held, the operator's gate asked of every flow
// it writes, then the flows and every pointer written before a bead is
// looked at, since the first pointer alone would hand a type's beads to the
// depot's flow (strayBeads skips a type only once its own pointer stands),
// then the stage files, which only a write that has passed all of that
// reaches. A retry finds the first attempt's files identical and leaves them.
func (w *wtx) copyRepoFlows(ctx context.Context, repo *RepoFlows, actor string) (*CopiedFlows, error) {
	plan, err := w.planCopy(repo)
	if err != nil {
		return nil, err
	}
	for _, it := range plan.items {
		if err := w.may(ctx, actor, ActChangeFlow, Object{Kind: KindFlow, Name: it.target}); err != nil {
			return nil, err
		}
	}
	depot := repo.Depot
	for _, it := range plan.items {
		f := *it.df.Flow
		f.Name = it.target
		if err := w.packPutFlow(ctx, f, actor); err != nil {
			return nil, err
		}
		if err := w.packDepotPointer(ctx, depot, it.df.Type, it.target, actor); err != nil {
			return nil, err
		}
		if err := put(w.Tx, bFlowPtrs, join(depot, it.df.Type), FlowPointer{Depot: depot, Type: it.df.Type, Flow: it.target}); err != nil {
			return nil, err
		}
	}
	// With every pointer in place, each bead resolves to the copy that
	// governs it now; an error here rolls the whole copy back.
	for _, it := range plan.items {
		p := FlowPointer{Depot: depot, Type: it.df.Type, Flow: it.target}
		strays, err := strayBeads(w.Tx, depot, it.df.Type, false, repo, nil)
		if err != nil {
			return nil, err
		}
		if len(strays) > 0 {
			return nil, fmt.Errorf("%s: %s; advance them to a stage the flow has, or pin them to the flow they follow now (yardr bead update <id> --meta flow=<name>), then copy again",
				p.change(), strings.Join(strays, ", "))
		}
	}
	out := &CopiedFlows{Depot: depot, Source: plan.source(), Kept: plan.kept}
	for _, it := range plan.items {
		if err := w.event("", "flow_set", actor, map[string]any{"flow": it.target}); err != nil {
			return nil, err
		}
		if err := w.event("", "flow_pointer_set", actor, map[string]any{"depot": depot, "type": it.df.Type, "flow": it.target}); err != nil {
			return nil, err
		}
		out.Flows = append(out.Flows, CopiedFlow{Type: it.df.Type, From: it.df.Source.Path, Flow: it.target,
			StageFiles: len(it.files), Replaced: it.replaced, Back: it.back})
	}
	if w.stageWrote != nil {
		w.stageWrote.key = plan.marker()
	}
	for _, it := range plan.items {
		if err := it.writeFiles(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// writeFiles puts the copy's stage files on disk, each whole or not at all
// (stageStand.write), at rw-r--r-- whatever a file of an earlier copy was kept
// at, and leaves a file that holds the text already as it is. The files of an
// earlier copy that the new one has none for are removed after, so that a
// reader never meets a gap where both have a file.
func (it copyItem) writeFiles() error {
	for _, f := range it.files {
		if f.st.digest == stageDigest([]byte(f.text)) {
			continue
		}
		f.st.mode = 0o644
		if err := f.st.write(f.text); err != nil {
			return fmt.Errorf("stage file of %s in flow %s: %w", f.stage, it.target, err)
		}
	}
	for _, p := range it.stale {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("flow %s: removing the stage file %s of the copy it replaces: %w", it.target, p, err)
		}
	}
	return nil
}
