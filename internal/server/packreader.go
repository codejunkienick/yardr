package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/version"
	"yardr.dev/yardr/packs"
)

// packReaderFor is the reading of the packs yardr embeds that the
// server registers on its store (store.SetPackReader): a pack apply run
// ask is carded from the binary this server runs, never from a
// directory, a URL or an earlier import's record. home is the yard's,
// where a depot plan reads the installed files it would change.
func packReaderFor(home string) store.PackReader {
	return func(ctx context.Context, name string, depot string, mappings store.Mappings, yard store.API) (*store.PackReading, error) {
		return readEmbeddedPack(ctx, name, depot, mappings, yard, home)
	}
}

// presetReaderFor is the reading of one preset of one source that the
// server registers on its store (store.SetPresetReader): a preset grant
// run ask is carded from the binary this server runs, never from a
// directory or an earlier import's record. The preset is resolved by
// source: the one embedded pack of kind source declaring it.
func presetReaderFor() store.PresetReader {
	return func(source, preset string) (*store.PresetReading, error) {
		name, calls, err := pack.PresetOfSource(packs.FS, source, preset)
		if err != nil {
			return nil, err
		}
		return &store.PresetReading{Pack: name, Preset: preset, Label: packs.Embedded + " in yardr " + version.Describe(), Build: version.Describe(), Calls: calls}, nil
	}
}

// readEmbeddedPack reads the embedded pack of a name: its kind, each
// declared source's whole text with each command file's bytes, and for
// a depot kind the plan built against the named depot with the named
// mappings, or, naming neither, against the depot that already holds
// the pack, as a re-apply always has.
func readEmbeddedPack(ctx context.Context, name string, depot string, mappings store.Mappings, yard store.API, home string) (*store.PackReading, error) {
	if _, err := fs.Stat(packs.FS, name+"/pack.yaml"); err != nil {
		// The sentinel is the word a setup card goes on from, to the
		// directory the operator applied the pack from (appliedReaderFor).
		return nil, fmt.Errorf("pack %s: %w: a pack that is a directory, a URL or an earlier import's record is the operator's to apply in a terminal (yardr pack check %s shows its plan there)", name, store.ErrNotEmbedded, name)
	}
	sub, err := fs.Sub(packs.FS, name)
	if err != nil {
		return nil, err
	}
	label := packs.Embedded + " in yardr " + version.Describe()
	p, err := pack.Load(sub, label)
	if err != nil {
		return nil, err
	}
	return readPack(ctx, p, func(rel string) ([]byte, error) { return fs.ReadFile(sub, rel) }, label, name, depot, mappings, yard, home)
}

// readPack is the reading of a loaded pack: the embedded pack of a name
// (readEmbeddedPack), or a pack of the packs repository the server fetched
// and held to its digest (packFetcherFor). read is how a file of the pack is
// read, so that a pack served over HTTP is read from the bytes its digest
// was checked on and never from a second answer of the server.
func readPack(ctx context.Context, p *pack.Pack, read func(rel string) ([]byte, error), label, name, depot string, mappings store.Mappings, yard store.API, home string) (*store.PackReading, error) {
	reading := &store.PackReading{Name: p.Name, Kind: p.Kind, Mappings: mappings, Label: label, Build: version.Describe()}
	for _, d := range p.Declared {
		src := store.AskSource{Name: d.Name, Text: d.Text}
		seenProg := map[string]bool{}
		for _, c := range d.Declaration.Commands() {
			if !c.Relative() || seenProg[c.Argv[0]] {
				continue
			}
			seenProg[c.Argv[0]] = true
			data, err := read(path.Join(declDirOf(d.File), c.Argv[0]))
			if err != nil {
				return nil, fmt.Errorf("declaration %s names the command file %s: %v", d.File, c.Argv[0], err)
			}
			sum := sha256.Sum256(data)
			src.Files = append(src.Files, store.AskFile{Program: c.Argv[0], Text: string(data), SHA256: hex.EncodeToString(sum[:])})
		}
		reading.Sources = append(reading.Sources, src)
	}
	for _, pr := range p.Presets {
		var grants []store.AskPresetGrant
		for _, g := range pr.Grants {
			grants = append(grants, store.AskPresetGrant{Source: g.Source, Calls: append([]string{}, g.Calls...)})
		}
		reading.Presets = append(reading.Presets, store.AskPreset{Name: pr.Name, Grants: grants})
	}
	if p.Kind == pack.KindSource {
		if !mappings.IsZero() {
			return nil, fmt.Errorf("pack %s is of kind source and takes no depot mappings", name)
		}
		plan, err := pack.Build(ctx, yard, p, pack.Options{Home: home})
		if err != nil {
			return nil, err
		}
		reading.Plan = askPlanFrom(read, plan, pack.Answers{}, nil, nil)
		reading.Digest, reading.Programs = plan.Digest, plan.Programs
		imp := plan.ImportRecord(pack.ApplyOptions{})
		reading.Import = &imp
		return reading, nil
	}
	target := depot
	if target == "" {
		recs, err := yard.Packs(ctx)
		if err != nil {
			return nil, err
		}
		var depots []string
		for _, r := range recs {
			if r.Pack == p.Name && !contains(depots, r.Depot) {
				depots = append(depots, r.Depot)
			}
		}
		switch {
		case len(depots) == 0:
			return nil, fmt.Errorf("pack %s: this yard has no standing import of it, so there is nothing to re-apply: a first install is asked with its depot named and the mappings that answer its plan (the run ask pack apply %s --depot <depot>); a plan that still asks is the operator's to type in a terminal (yardr pack check %s --depot <depot> shows its plan there)", name, name, name)
		case len(depots) > 1:
			return nil, fmt.Errorf("pack %s: this yard holds it in depots %s, so with no depot named there is no single depot to re-apply to: the ask names which (the run ask pack apply %s --depot <depot>; yardr pack check %s --depot <depot> shows its plan)", name, strings.Join(depots, ", "), name, name)
		}
		target = depots[0]
	}
	plan, err := pack.Build(ctx, yard, p, packOptions(target, home, mappings))
	if err != nil {
		return nil, err
	}
	// No mappings read exactly as a re-apply always has, byte for byte;
	// mappings answer what they name, and leave the rest still asking.
	answers, open, calls := pack.Answers{}, map[string]map[string]bool(nil), map[string]map[string]bool(nil)
	if !mappings.IsZero() {
		if answers, open, calls, err = mappingAnswers(ctx, yard, plan, mappings, home); err != nil {
			return nil, err
		}
	}
	reading.Depot = target
	reading.Plan = askPlanFrom(read, plan, answers, open, calls)
	reading.Digest, reading.Programs = plan.Digest, plan.Programs
	reading.Grants = packGrantsOf(open, calls)
	// The record the apply of this plan, answered by these mappings, leaves
	// of its import: a delegated run holds its own record to it.
	imp := plan.ImportRecord(pack.ApplyOptions{OpenThreads: open, Calls: calls})
	reading.Import = &imp
	targets, err := packTargetsOf(ctx, yard, plan, answers)
	if err != nil {
		return nil, err
	}
	reading.Targets = targets
	return reading, nil
}

// packGrantsOf are the grants a reading's mapping answers said yes
// to, in a delegated record's words (store's Delegation.Grants).
func packGrantsOf(open, calls map[string]map[string]bool) []string {
	out := []string{}
	for crew, sources := range open {
		for src, yes := range sources {
			if yes {
				out = append(out, "threads "+crew+" "+src)
			}
		}
	}
	for who, granted := range calls {
		for key, yes := range granted {
			if yes {
				out = append(out, "call "+who+" "+key)
			}
		}
	}
	slices.Sort(out)
	return out
}

// packTargetsOf are the objects a plan would write, by kind and name,
// each as JSON of the object as the plan and its answers make it: a
// delegated write under the plan's record is held to them in the
// store. A file is installed by its bytes, and a declared source by
// its text, so neither stands here.
func packTargetsOf(ctx context.Context, yard store.API, plan *pack.Plan, answers pack.Answers) (map[string]string, error) {
	out := map[string]string{}
	put := func(kind, name string, v any) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		out[kind+"\x00"+name] = string(raw)
		return nil
	}
	for i := range plan.Objects {
		o := &plan.Objects[i]
		if o.Action != pack.Create && o.Action != pack.Change {
			continue
		}
		switch o.Kind {
		case "group":
			if err := put(o.Kind, o.Name, targetGroup(plan, o, answers)); err != nil {
				return nil, err
			}
		case "flow":
			if err := put(o.Kind, o.Name, o.Flow); err != nil {
				return nil, err
			}
		case "pointer":
			if err := put(o.Kind, o.Name, o.Pointer); err != nil {
				return nil, err
			}
		case "route":
			if err := put(o.Kind, o.Name, o.Route); err != nil {
				return nil, err
			}
		case "crew":
			def, err := targetCrew(ctx, yard, o, answers)
			if err != nil {
				return nil, err
			}
			if err := put(o.Kind, o.Name, def); err != nil {
				return nil, err
			}
		case "map":
			if err := put(o.Kind, o.Name, o.Map); err != nil {
				return nil, err
			}
		case "subscription":
			if err := put(o.Kind, o.Name, o.Subscription); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// targetGroup is a group object as an import writes it: its plan's
// group, with the answers for the group it binds as, and its members.
func targetGroup(plan *pack.Plan, o *pack.Object, answers pack.Answers) store.Group {
	g := *o.Group
	g.Config = map[string]any{}
	for k, v := range o.Group.Config {
		g.Config[k] = v
	}
	answerOf := func(of *pack.Object, field string) (string, bool) {
		id := of.Kind + "." + of.Name + "." + field
		v, ok := answers[id]
		return strings.TrimSpace(v), ok && slices.Contains(of.Asks, id)
	}
	agent := o
	for agent.Binds != "" {
		i := slices.IndexFunc(plan.Objects, func(x pack.Object) bool { return x.Kind == "group" && x.Name == agent.Binds })
		if i < 0 {
			break
		}
		agent = &plan.Objects[i]
	}
	if v, ok := answerOf(agent, "kind"); ok {
		g.Config["kind"] = v
	}
	if v, ok := answerOf(agent, "model"); ok && v != "" {
		g.Config["model"] = v
	}
	if v, ok := answerOf(agent, "args"); ok && v != "" {
		var args []any
		for _, s := range strings.Fields(v) {
			args = append(args, s)
		}
		g.Config["args"] = args
	}
	if v, ok := answerOf(o, "members"); ok {
		g.Members = strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' })
	}
	return g
}

// crewTargetJSON is a crew member object as its target stands on a
// delegated record: the definition an import writes, and nothing of
// its runtime.
type crewTargetJSON struct {
	Config         map[string]any `json:"config"`
	Dir            string         `json:"dir"`
	On             []string       `json:"on"`
	Runner         string         `json:"runner"`
	MaxAge         string         `json:"max_age"`
	HandoffGrace   string         `json:"handoff_grace"`
	ContextHandoff string         `json:"context_handoff,omitempty"`
}

// targetCrew is a crew object as an import writes it: the member as
// it stands with the pack's role and what it hears, or a new one from
// its answers.
func targetCrew(ctx context.Context, yard store.API, o *pack.Object, answers pack.Answers) (crewTargetJSON, error) {
	op := o.CrewMember
	def := store.CrewMember{Name: op.CrewMember, Runner: "herdr", Config: map[string]any{}, HandoffGrace: "15m"}
	answer := func(field string) (string, bool) {
		id := o.Kind + "." + o.Name + "." + field
		v, ok := answers[id]
		return strings.TrimSpace(v), ok && slices.Contains(o.Asks, id)
	}
	have, err := yard.GetCrew(ctx, op.CrewMember)
	switch {
	case err == nil:
		def = *have
		if def.Config == nil {
			def.Config = map[string]any{}
		}
	case errors.Is(err, store.ErrNotFound):
		if v, ok := answer("kind"); ok {
			def.Config["kind"] = v
		}
		if v, ok := answer("model"); ok && v != "" {
			def.Config["model"] = v
		}
		if v, ok := answer("args"); ok && v != "" {
			var args []any
			for _, s := range strings.Fields(v) {
				args = append(args, s)
			}
			def.Config["args"] = args
		}
		def.Dir, _ = answer("dir")
	default:
		return crewTargetJSON{}, err
	}
	if op.Prompt != "" {
		def.Config["prompt"] = op.Prompt
	}
	def.On = op.On
	return crewTargetJSON{Config: def.Config, Dir: def.Dir, On: def.On, Runner: def.Runner,
		MaxAge: def.MaxAge, HandoffGrace: def.HandoffGrace, ContextHandoff: def.ContextHandoff}, nil
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// declDirOf is a declaration file's directory in its pack, "" at its root.
func declDirOf(file string) string {
	if dir := path.Dir(file); dir != "." {
		return dir
	}
	return ""
}

// askPlanOf is askPlanFrom for the files of an embedded pack, sub.
func askPlanOf(sub fs.FS, plan *pack.Plan, answers pack.Answers, open, calls map[string]map[string]bool) *store.AskPlan {
	return askPlanFrom(func(rel string) ([]byte, error) { return fs.ReadFile(sub, rel) }, plan, answers, open, calls)
}

// askPlanFrom is a pack plan as a card or delegated record holds it, with each created or
// changed file's text as the import would install it (filled) and its
// unified diff against the installed text, computed here from the two
// texts the file's hashes already name. What the mappings already
// answer, in answers, open and calls, is no part of what it still asks.
// read reads a file of the pack by its path in it.
func askPlanFrom(read func(rel string) ([]byte, error), plan *pack.Plan, answers pack.Answers, open, calls map[string]map[string]bool) *store.AskPlan {
	out := &store.AskPlan{}
	for _, q := range plan.Missing(answers) {
		out.Missing = append(out.Missing, q.ID+": "+q.Ask)
	}
	for _, ot := range plan.OpenThreads {
		for _, a := range ot.Asks {
			if _, done := open[ot.CrewMember][a]; !done {
				out.Asks = append(out.Asks, fmt.Sprintf("let %s open threads in %s", ot.CrewMember, a))
			}
		}
	}
	for _, c := range plan.Calls {
		for _, a := range c.Asks {
			if _, done := calls[c.Grantee().String()][pack.CallKey(c.Source, a)]; !done {
				out.Asks = append(out.Asks, fmt.Sprintf("let %s make the call %s of the source %s", c.Grantee(), a, c.Source))
			}
		}
	}
	for _, e := range plan.Expose {
		for _, a := range e.Asks {
			out.Asks = append(out.Asks, fmt.Sprintf("expose %s to the peer %s", e.CrewMember, a))
		}
	}
	out.Refused = plan.Refused
	out.Edited = plan.EditedFiles()
	for _, o := range plan.Objects {
		ao := store.AskPlanObject{Kind: o.Kind, Name: o.Name, Action: o.Action, Keeps: o.Keeps, Shadows: o.Shadows, Env: o.Env}
		if o.Kind == "file" && o.File != nil {
			ao.Edited = o.Edited
			// A program, a role or a script the yard runs: not a stage
			// file, and not the page frame, which only a reader's
			// browser runs.
			ao.Runs = o.File.Flow == "" && (plan.Frame == nil || o.Name != plan.Frame.File)
		}
		for _, ch := range o.Changes {
			ao.Changes = append(ao.Changes, store.AskChange{Field: ch.Field, Old: planValue(ch.Old), New: planValue(ch.New)})
		}
		if o.Kind == "file" && o.File != nil && (o.Action == pack.Create || o.Action == pack.Change) {
			ao.FileNewSHA = o.File.SHA256
			oldText, hasOld := "", false
			if data, err := os.ReadFile(o.File.To); err == nil {
				hasOld, oldText = true, string(data)
				sum := sha256.Sum256(data)
				ao.FileOldSHA = hex.EncodeToString(sum[:])
			}
			if raw, err := read(o.Name); err == nil {
				if o.File.Fill {
					raw = pack.Fill(raw, plan.Fill)
				}
				ao.Text = string(raw)
			}
			if !utf8.ValidString(ao.Text) || hasOld && !utf8.ValidString(oldText) {
				out.NonText = append(out.NonText, o.Name)
			}
			ao.Diff = store.Unified(oldText, ao.Text)
		}
		out.Objects = append(out.Objects, ao)
	}
	for _, g := range plan.Governs {
		out.Governs = append(out.Governs, store.AskGovern{Type: g.Type, Flow: g.Flow, Now: g.Now})
	}
	return out
}

// planValue is a plan change's value as a card prints it.
func planValue(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
