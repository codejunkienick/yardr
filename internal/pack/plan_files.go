package pack

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"yardr.dev/yardr/internal/store"
)

// files are the pack's roles and scripts, copied under Install, and the
// stage files of the flows the import uses, copied to where the yard reads
// a store flow's: <home>/flows/<pack>.<flow>/<stage>.md.
func (b *planner) files(flows []Flow) error {
	want := map[string]bool{}
	for _, rel := range b.p.Files() {
		op := &FileOp{To: b.installed(rel), Fill: b.p.Filled(rel)}
		want[op.To] = true
		if err := b.file(rel, op); err != nil {
			return err
		}
	}
	// What an earlier import left there and this pack no longer names.
	b.otherFiles(b.plan.Install, "", want, "")
	for _, f := range flows {
		name := FlowName(b.p.Name, f.Name)
		dir := store.HomeStageDir(b.o.Home, name)
		for _, s := range f.Stages {
			if s.File == "" {
				continue
			}
			// The reader refuses a name that leads out of the flow's
			// directory; nothing is written by a pack that got past it.
			rel := filepath.FromSlash(s.Name) + ".md"
			if !filepath.IsLocal(rel) || filepath.Clean(rel) != rel {
				return fmt.Errorf("stage %s of flow %s: its stage file would be written outside %s", s.Name, f.Name, dir)
			}
			op := &FileOp{To: filepath.Join(dir, rel), Flow: name, Stage: s.Name, Fill: b.p.Filled(s.File)}
			want[op.To] = true
			if err := b.file(s.File, op); err != nil {
				return err
			}
		}
		// The flow's other stage files: written by hand for a stage the pack
		// has no file for, or left by an earlier import.
		b.otherFiles(dir, stageFileName(name, ""), want, name)
	}
	return nil
}

// stageFileName is what a plan calls a stage file of store flow that the
// pack does not carry: its path under the yard's home, since no path in the
// pack names it.
func stageFileName(flow, rel string) string {
	return "flows/" + flow + "/" + filepath.ToSlash(rel)
}

// file plans the copy of the pack's file rel to op.To. What is compared
// with the installed copy, and hashed for the record, is the text as it
// would be installed: filled, for a role or stage file. So an import just
// made shows no change, and a copy counts as edited by hand only when it
// differs from what an import wrote.
func (b *planner) file(rel string, op *FileOp) error {
	op.From = joinName(b.p.Dir, rel)
	text, err := b.p.read(rel)
	if err != nil {
		return err
	}
	if op.Fill {
		text = Fill(text, b.plan.Fill)
	}
	op.Mode, op.SHA256 = "0644", sum(text)
	// A script, by the pack's saying so and not by its mode in the source
	// (Pack.script): the frame is not one, though nothing is filled in it.
	if b.p.script(rel) {
		op.Mode = "0755"
	}
	var changes []Diff
	have, err := os.ReadFile(op.To)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	edited := false
	if exists {
		if !bytes.Equal(have, text) {
			changes = append(changes, Diff{"content", "sha256 " + sum(have)[:12], "sha256 " + op.SHA256[:12]})
			// Not what the last import installed either: someone edited
			// the copy, and the import would undo that.
			was := b.installedSums(op.To)
			edited = len(was) > 0 && !slices.Contains(was, sum(have))
		}
		if hi, err := os.Stat(op.To); err == nil && (hi.Mode().Perm()&0o111 != 0) != (op.Mode == "0755") {
			changes = append(changes, Diff{"mode", fmt.Sprintf("%04o", hi.Mode().Perm()), op.Mode})
		}
	}
	b.add(Object{Kind: "file", Name: rel, Action: action(exists, changes), Changes: changes, File: op, Edited: edited})
	return nil
}

// otherFiles adds the files under dir that the import does not write, as not
// in the pack, named prefix and their path under dir; flow is the store flow
// whose stage files dir holds, "" for the pack's own directory.
func (b *planner) otherFiles(dir, prefix string, want map[string]bool, flow string) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || want[path] {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		b.add(Object{Kind: "file", Name: prefix + filepath.ToSlash(rel), Action: NotInPack, File: &FileOp{To: path, Flow: flow}})
		return nil
	})
}

// stageFlow names the store flow whose stage file path is, when path is
// where an import of this pack puts one: <StageFiles>/<pack>.<flow>/<stage>.md.
func (p *Plan) stageFlow(path string) (flow string, ok bool) {
	if p.StageFiles == "" {
		return "", false
	}
	rel, err := filepath.Rel(p.StageFiles, path)
	if err != nil || !filepath.IsLocal(rel) || filepath.Ext(rel) != ".md" {
		return "", false
	}
	flow = filepath.Dir(rel)
	if flow != filepath.Base(flow) || !strings.HasPrefix(flow, p.Pack+".") {
		return "", false
	}
	return flow, true
}

// Removable reports whether path is one a prune may remove: a file under
// the pack's own directory in the home, or a stage file of one of its
// flows. The record says which files an import brought; this says where an
// import could have put one, so a record naming a file elsewhere removes
// nothing.
func (p *Plan) Removable(path string) bool {
	if rel, err := filepath.Rel(p.Install, path); err == nil && filepath.IsLocal(rel) {
		return true
	}
	_, ok := p.stageFlow(path)
	return ok
}

// installedSums are the contents the imports of this pack recorded for the
// installed file to, one per depot it was imported into: the depots share the
// copy, so the last import into any of them may have written it. Empty when
// no record says, as those older than the field do not.
func (b *planner) installedSums(to string) []string {
	var out []string
	for _, r := range b.all {
		if r.Pack != b.p.Name {
			continue
		}
		for _, po := range r.Objects {
			if po.Kind == "file" && po.Name == to && po.SHA256 != "" {
				out = append(out, po.SHA256)
			}
		}
	}
	return out
}

// EditedFiles are the installed files the plan would overwrite though they
// were edited by hand since the last import, each with why that is refused.
func (p *Plan) EditedFiles() []string {
	var out []string
	for _, o := range p.Objects {
		if o.Kind == "file" && o.Edited && o.File != nil {
			verb := "overwrite"
			if o.Action == Dropped {
				verb = "remove"
			}
			out = append(out, fmt.Sprintf("%s was edited by hand since the last import, and the import would %s it: the edit belongs in the pack's source (%s)", o.File.To, verb, o.File.From))
		}
	}
	return out
}
