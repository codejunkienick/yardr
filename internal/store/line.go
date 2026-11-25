// The Line read: one depot's line as footplate's Line window draws it
// (design yardr-y1cn 3.1): its governing flows with their stages, who
// serves each stage, the stage files and the prompts, read whole. It is
// built from DepotFlows, the depot's effective routes and Groups, with
// Packs joined for a store flow's provenance, and it writes nothing. A
// prompt and a cmd are the operator's to read, so the call stands in
// rpc.OperatorCalls beside SessionScreen, under its transport rule; no
// env file path stands anywhere in the answer.
package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Line is one depot's line: its flows, and below them its crew members'
// prompts, which no stage carries.
type Line struct {
	Depot string     `json:"depot"`
	Kind  string     `json:"kind"` // DepotGit or DepotDir
	Flows []LineFlow `json:"flows"`
	Crew  []LineCrew `json:"crew"`
}

// LineFlow is one flow governing the depot: the depot's own (Type "") or
// a bead type's.
type LineFlow struct {
	Name string `json:"name"`
	// Type is the bead type the flow governs, "" for the depot's own.
	Type string `json:"type"`
	// Source is where the flow comes from: "repo@<commit>" for a git
	// depot's repository flow, "repo (directory)" for a dir depot's
	// directory flow, "pack <name>" for a pack-managed store flow, and
	// "store" for any other store flow (FlowSource.String's words, with
	// the pack told apart by the Packs join).
	Source string `json:"source"`
	// Directory is the directory a dir depot's directory flow was read
	// from, named beside its source; absent on any other flow.
	Directory string      `json:"directory,omitempty"`
	Stages    []LineStage `json:"stages"`
	// Warnings are the flow's stranded beads, as flow show prints them.
	Warnings []string `json:"warnings,omitempty"`
}

// LineStage is one stage of a flow and who serves it.
type LineStage struct {
	Stage string `json:"stage"`
	// Next are the stages a bead in Stage may move to, in the flow's
	// order; empty where the flow allows none (a terminal stage, or one
	// the flow does not know, whose beads rejoin at the first stage:
	// Flow.Next says which, and Terminal and the flow's order say it).
	Next     []string  `json:"next"`
	Outcomes []Outcome `json:"outcomes,omitempty"`
	Terminal bool      `json:"terminal"`
	// Route is the stage's representative route, whole; absent where no
	// route serves the stage, which Warnings then says.
	Route *LineRoute `json:"route,omitempty"`
	// OtherRoutes are every other effective route serving the stage.
	OtherRoutes []LineOtherRoute `json:"other_routes"`
	// Open is the open beads in the stage that this flow governs, counted
	// once here and split by no rule.
	Open int `json:"open"`
	// Warnings say why beads in the stage wait: no route serves it.
	Warnings  []string       `json:"warnings,omitempty"`
	StageFile *LineStageFile `json:"stage_file,omitempty"`
}

// LineStageFile is a stage's file where it has one: its path as flow show
// names it, its text whole, and what a save of it goes by. It is also what
// PutStageFile answers with, so a save and a read give footplate and the
// terminal one shape.
type LineStageFile struct {
	Path string `json:"path"`
	Text string `json:"text"`
	// Digest is the SHA-256 of Text in lower case hex, as sha256sum prints it:
	// what a save's expectDigest (yardr flow set-stage --digest) holds it to.
	Digest string `json:"digest,omitempty"`
	// PriorDigest and PriorText are the text the latest save of a store flow's
	// stage file replaced, which the yard keeps (StageHistoryDir) so that the
	// save can be undone by one more; absent where that save created the file,
	// the kept text is gone, is not what its name says or is longer than a
	// card carries (MaxRunFlow), and on a flow of a depot's repository.
	PriorDigest string `json:"prior_digest,omitempty"`
	PriorText   string `json:"prior_text,omitempty"`
	// Warning says where the text breaks its ## Ask section, as BrokenAsk does:
	// "<path>: <why>", absent where it does not. A save is never refused for
	// it, since the operator may be part way through an edit.
	Warning string `json:"warning,omitempty"`
}

// lineStageFile is a stage file as the Line carries it, with the digest it is
// saved by and the warning of a ## Ask section it breaks under f, the flow it
// belongs to.
func lineStageFile(sf *StageFile, f *Flow, stage string) *LineStageFile {
	return &LineStageFile{Path: sf.Path, Text: sf.Text, Digest: stageDigest([]byte(sf.Text)), Warning: sf.BrokenAsk(f, stage)}
}

// LineRoute is a stage's representative route, whole: the route a probe
// bead of the depot, of the flow's type at the default priority with no
// labels reading as the operator's, would take (MatchRoute's probe, as
// the board and flow show pick it).
type LineRoute struct {
	ID      string      `json:"id"`
	Scope   string      `json:"scope"` // ScopeOwn or ScopeYardWide
	Filters LineFilters `json:"filters"`
	// Chain is the route's groups in order, each with its quota condition.
	Chain []LineGroup `json:"chain"`
	// Limit is the first group's seat limit, 0 pausing the group; absent
	// where the first group is not in the yard's groups.
	Limit *int `json:"limit,omitempty"`
}

// LineOtherRoute is one further effective route serving a stage, behind
// the representative: its id, scope, filters and first group, so a stage
// whose filtered beads take another rule shows that rule.
type LineOtherRoute struct {
	ID      string      `json:"id"`
	Scope   string      `json:"scope"`
	Filters LineFilters `json:"filters"`
	Group   string      `json:"group"` // the chain's first group
}

// The scopes a Line route names, in the design's words.
const (
	ScopeOwn      = "this depot's own"
	ScopeYardWide = "yard-wide"
)

// LineFilters are the filters a route sets, each named and each shown as
// unset where it is unset: an empty Type takes any type, empty Labels no
// label, an empty Priority any priority and an empty Originator the
// operator's beads alone (Route's match, in the words route show uses).
type LineFilters struct {
	Type       string   `json:"type"`
	Labels     []string `json:"labels"`
	Priority   string   `json:"priority"` // "P0-P1", "P0", "" for any (BeadPriorities)
	Originator string   `json:"originator"`
}

// LineGroup is one group of a representative route's chain, with what a
// session of it runs: its runner, and for a herdr group its kind and
// model, for an exec group its cmd whole, for a manual group its members,
// for a peer group its yard. No env file path stands here.
type LineGroup struct {
	Name string `json:"name"`
	// When is the group's quota condition in the chain, "" always holds.
	When   string `json:"when,omitempty"`
	Runner string `json:"runner"`
	Kind   string `json:"kind,omitempty"`  // herdr: the kind its sessions start as (Group.Kind)
	Model  string `json:"model,omitempty"` // herdr
	Cmd    string `json:"cmd,omitempty"`   // exec, whole
	// Members are a manual group's members; none means anyone.
	Members []string `json:"members,omitempty"`
	Yard    string   `json:"yard,omitempty"` // peer: the linked yard (AtPeer)
	// Prompt is the group's prompt where its config names one and the
	// group starts agents; absent otherwise.
	Prompt *LinePrompt `json:"prompt,omitempty"`
}

// LineCrew is one crew member and its prompt, which no stage carries.
type LineCrew struct {
	Name   string      `json:"name"`
	Prompt *LinePrompt `json:"prompt,omitempty"`
}

// LinePrompt is one prompt as the Line read answers it: the path the
// config names, where the lookup found the text, the text whole, its
// digest where the file is one the yard wrote, and whether a card can
// carry it, with why not where it cannot (promptCarriage's verdict, the
// card's own rules). One that cannot be read carries Tried and Error
// instead of Text and fails nothing else of the answer.
type LinePrompt struct {
	Path string `json:"path"` // as the config names it
	// Source is where the lookup found the text: "depot", "home",
	// "pack <name>" for a pack's installed copy, or "file" for a file as
	// it stands (PromptSource's kinds, with the pack told apart).
	Source string `json:"source,omitempty"`
	// Found is where the text was found: relative to the depot's root
	// for a file of the depot, else absolute. Absent where none was.
	Found string `json:"found,omitempty"`
	// Commit is the base commit a depot's file was read at.
	Commit string `json:"commit,omitempty"`
	Text   string `json:"text"`
	// Digest is the digest a yard-written prompt file is named by, in
	// hex; absent where the file is none the yard wrote so.
	Digest string `json:"digest,omitempty"`
	// Carriable says a card can carry the prompt whole; Why says why not
	// where it cannot, absent where it can.
	Carriable bool     `json:"carriable"`
	Why       string   `json:"why,omitempty"`
	Tried     []string `json:"tried,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// scopeOf is a route's scope in the Line's words: its own depot's where
// it names depot, yard-wide where it names none.
func lineScope(r Route, depot string) string {
	if r.Depot != nil && *r.Depot == depot {
		return ScopeOwn
	}
	return ScopeYardWide
}

// filtersOf is a route's filters as LineFilters names them.
func lineFilters(r Route) LineFilters {
	f := LineFilters{Labels: r.Labels(), Priority: r.BeadPriorities()}
	if f.Labels == nil {
		f.Labels = []string{}
	}
	if r.Type != nil {
		f.Type = *r.Type
	}
	if r.Originator != nil {
		f.Originator = *r.Originator
	}
	return f
}

// flowPackName is the pack imported into depot that brought the store
// flow name, "" where none did: flowPack's join (depot sources), in the
// store, which cannot import the cli. Only imports into this depot
// count, as there.
func flowPackName(packs []PackImport, depot, name string) string {
	for _, p := range packs {
		if p.Depot != depot {
			continue
		}
		if slices.ContainsFunc(p.Objects, func(o PackObject) bool { return o.Kind == "flow" && o.Name == name }) {
			return p.Pack
		}
	}
	return ""
}

// Line answers depot's line (see the type). It is the operator's call
// (rpc.OperatorCalls): on the socket the operator's alone, and on the
// listener a device of an operator's, under SessionScreen's transport
// rule. A depot the yard has not is not found.
func (s *Store) Line(ctx context.Context, depot string) (*Line, error) {
	d, err := s.GetDepot(ctx, depot)
	if err != nil {
		return nil, err
	}
	flows, err := s.DepotFlows(ctx, depot)
	if err != nil {
		return nil, err
	}
	repo, err := s.RepoFlows(ctx, depot)
	if err != nil {
		return nil, err
	}
	routes, err := s.Routes(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := s.Groups(ctx)
	if err != nil {
		return nil, err
	}
	byGroup := make(map[string]Group, len(groups))
	for _, g := range groups {
		byGroup[g.Name] = g
	}
	crew, err := s.Crew(ctx)
	if err != nil {
		return nil, err
	}
	packs, err := s.Packs(ctx)
	if err != nil {
		return nil, err
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	operator := settings[OperatorSetting]
	defKind := settings[DefaultKindSetting]
	beads, err := s.ListBeads(ctx, BeadFilter{Depot: depot})
	if err != nil {
		return nil, err
	}
	stranded, err := s.Stranded(ctx, depot)
	if err != nil {
		return nil, err
	}
	typed := map[string]bool{}
	for _, rf := range flows {
		typed[rf.Type] = rf.Type != ""
	}
	effective := EffectiveRoutes(routes, &depot, operator, byGroup)
	out := &Line{Depot: depot, Kind: d.Kind, Flows: []LineFlow{}, Crew: []LineCrew{}}
	for _, rf := range flows {
		lf := LineFlow{Name: rf.Flow.Name, Type: rf.Type, Stages: []LineStage{}}
		switch {
		case !rf.Source.InStore():
			if rf.Source.Commit != "" {
				lf.Source = "repo@" + shortSHA(rf.Source.Commit)
			} else {
				lf.Source = "repo (directory)"
				lf.Directory = repo.Dir
			}
		case flowPackName(packs, depot, rf.Flow.Name) != "":
			lf.Source = "pack " + flowPackName(packs, depot, rf.Flow.Name)
		default:
			lf.Source = "store"
		}
		for _, sb := range stranded {
			if sb.Flow == rf.Flow.Name && sb.Source == rf.Source {
				lf.Warnings = append(lf.Warnings, fmt.Sprintf("bead %s: it is not routed and waits until someone advances it (yardr bead advance %s --to <stage>)", sb.Bead, sb.Bead))
			}
		}
		for _, stage := range rf.Flow.Stages {
			ls, err := s.lineStage(ctx, d, rf, stage, repo, routes, effective, byGroup, operator, defKind, beads, typed)
			if err != nil {
				return nil, err
			}
			lf.Stages = append(lf.Stages, ls)
		}
		out.Flows = append(out.Flows, lf)
	}
	for _, m := range crew {
		lc := LineCrew{Name: m.Name}
		if p, _ := m.Config["prompt"].(string); p != "" {
			lc.Prompt = s.crewPrompt(ctx, m, p)
		}
		out.Crew = append(out.Crew, lc)
	}
	return out, nil
}

// lineStage is one stage's LineStage: its file, its representative and
// other routes, and its open count under flow rf.
func (s *Store) lineStage(ctx context.Context, d *Depot, rf DepotFlow, stage string, repo *RepoFlows, routes, effective []Route, byGroup map[string]Group, operator, defKind string, beads []Bead, typed map[string]bool) (LineStage, error) {
	f := rf.Flow
	ls := LineStage{Stage: stage, Next: []string{}, OtherRoutes: []LineOtherRoute{}, Terminal: f.IsTerminal(stage)}
	if next := f.Transitions[stage]; len(next) > 0 {
		ls.Next = slices.Clone(next)
	}
	ls.Outcomes = f.OutcomesFrom(stage)
	// Stage file as a brief reads it: the repository's for a repository
	// flow, the home's for a store flow.
	if rf.Source.InStore() {
		sf, err := HomeStageFile(s.home, f.Name, stage)
		if err != nil {
			return LineStage{}, err
		}
		if sf != nil {
			ls.StageFile = lineStageFile(sf, f, stage)
			ls.StageFile.PriorDigest, ls.StageFile.PriorText = s.stagePrior(f.Name, stage, ls.StageFile.Digest)
		}
	} else if sf := repo.StageFile(f.Name, stage); sf != nil {
		ls.StageFile = lineStageFile(sf, f, stage)
	}
	probe := Bead{Stage: stage, Type: or(rf.Type, "task"), Depot: &d.Name, Priority: DefaultPriority}
	rep := MatchRoute(routes, probe, operator, byGroup)
	if rep != nil {
		// MatchRoute copies: find the effective route it picked by id, so
		// the chain answered is the depot's resolved one.
		picked := rep
		lr := LineRoute{ID: picked.ID, Scope: lineScope(*picked, d.Name), Filters: lineFilters(*picked), Chain: []LineGroup{}}
		for _, rg := range picked.Groups() {
			lg := LineGroup{Name: rg.Name, When: rg.When}
			if g, ok := byGroup[rg.Name]; ok {
				lg.Runner = g.Runner
				switch g.Runner {
				case "herdr":
					lg.Kind = g.Kind(defKind)
					lg.Model, _ = g.Config["model"].(string)
				case "exec":
					lg.Cmd, _ = g.Config["cmd"].(string)
				case "manual":
					lg.Members = slices.Clone(g.Members)
				}
				if g.Runner == PeerRunner {
					lg.Yard = g.AtPeer()
				}
				if g.Runner == "herdr" {
					if p, _ := g.Config["prompt"].(string); p != "" {
						lg.Prompt = s.groupPrompt(ctx, d, p)
					}
				}
			}
			lr.Chain = append(lr.Chain, lg)
		}
		if first, ok := byGroup[picked.Worker()]; ok {
			limit := first.Limit
			lr.Limit = &limit
		}
		ls.Route = &lr
	} else if !ls.Terminal {
		ls.Warnings = append(ls.Warnings, fmt.Sprintf("stage %s has no route: beads there wait for a human", stage))
	}
	for _, r := range effective {
		if r.Stage != stage || (rep != nil && r.ID == rep.ID) {
			continue
		}
		ls.OtherRoutes = append(ls.OtherRoutes, LineOtherRoute{ID: r.ID, Scope: lineScope(r, d.Name), Filters: lineFilters(r), Group: r.Worker()})
	}
	for i := range beads {
		b := &beads[i]
		governed := PinnedFlow(b) == "" && (b.Type == rf.Type || (rf.Type == "" && !typed[b.Type]))
		if governed && b.Stage == stage {
			ls.Open++
		}
	}
	return ls, nil
}

// groupPrompt is the prompt of a herdr group working a bead of depot d:
// the lookup a brief makes (LookupPrompt), with the card's verdict on the
// path the config names.
func (s *Store) groupPrompt(ctx context.Context, d *Depot, p string) *LinePrompt {
	text, src, tried, err := LookupPrompt(ctx, p, d, s.home)
	lp := &LinePrompt{Path: p, Tried: tried}
	_, why, _ := promptCarriage(s.home, p)
	lp.Carriable, lp.Why = why == "", why
	if err != nil {
		// Tried names the places looked where the prompt was not found
		// there; a read that itself failed says so in Error instead.
		lp.Error = err.Error()
		return lp
	}
	lp.Source, lp.Found, lp.Commit = linePromptSource(src, s.home), src.Path, src.Commit
	lp.Text = string(text)
	lp.Digest = yardPromptDigest(s.home, src.Path)
	return lp
}

// crewPrompt is a crew member's prompt: read as its prime reads it, a
// relative path under the member's directory, with the card's verdict on
// the path the config names.
func (s *Store) crewPrompt(ctx context.Context, m CrewMember, p string) *LinePrompt {
	path := p
	if !filepath.IsAbs(path) && m.Dir != "" {
		path = filepath.Join(m.Dir, path)
	}
	lp := &LinePrompt{Path: p}
	_, why, _ := promptCarriage(s.home, p)
	lp.Carriable, lp.Why = why == "", why
	text, err := os.ReadFile(path)
	if err != nil {
		lp.Tried = []string{path}
		lp.Error = err.Error()
		return lp
	}
	lp.Found = path
	lp.Source = linePromptSource(PromptSource{Kind: PromptFile, Path: path}, s.home)
	lp.Text = string(text)
	lp.Digest = yardPromptDigest(s.home, path)
	return lp
}

// linePromptSource is a prompt's source in the Line's words: the lookup's
// kind, with a pack's installed copy under the home told apart by the
// pack it stands under.
func linePromptSource(src PromptSource, home string) string {
	if src.Kind == PromptDepot {
		return PromptDepot
	}
	for _, p := range []string{src.Path} {
		if home == "" || p == "" {
			continue
		}
		if rel, err := filepath.Rel(filepath.Join(home, "packs"), p); err == nil && filepath.IsLocal(rel) {
			if pack, _, ok := strings.Cut(filepath.ToSlash(rel), "/"); ok && pack != "" && pack != ".." {
				return "pack " + pack
			}
		}
	}
	if src.Kind == PromptHome {
		return PromptHome
	}
	return PromptFile
}
