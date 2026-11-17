package pack

import (
	"fmt"
	"slices"
	"strings"

	"yardr.dev/yardr/internal/store"
)

// A pack is a starting point: what the yard changes of its flow, a stage
// file or a role stays changed. A plan compares what the yard has with the
// pack's version and with what the imports of the pack recorded of it, and
// leaves the yard's version where the yard is the one that changed it
// (Kept), saying which and why. The pack's version is set only where an
// import is told to take it by name (ApplyOptions.Take).

// keptShape is how an object that differs from the pack's stands against
// what the imports of the pack recorded of it.
type keptShape int

const (
	// notKept: the yard has what an import wrote, so the pack is what moved
	// and the import brings its change, as it always did.
	notKept keptShape = iota
	// keptEdited: the yard changed it since the last import, and the pack
	// has not changed since.
	keptEdited
	// keptDiverged: the yard changed it and so did the pack. Kept, the yard's
	// version would drop the pack's change unseen; the line says so.
	keptDiverged
	// keptUnknown: no import recorded what it wrote (a record older than the
	// digest, or none), so whose change the difference is cannot be told. It
	// is kept all the same, since the yard's version is the one that can be
	// lost.
	keptUnknown
)

// shapeOf judges an installed object by digests: have is the yard's, want
// the pack's, and was what the imports of the pack recorded of it.
func shapeOf(have, want string, was []string) keptShape {
	switch {
	case len(was) == 0:
		return keptUnknown
	case slices.Contains(was, have):
		return notKept
	case slices.Contains(was, want):
		return keptEdited
	}
	return keptDiverged
}

// keptWords is the Keeps line of an object the import leaves as the yard has
// it: what stays (what is the yard's version of it), why, and the flag that
// takes the pack's version instead.
func keptWords(s keptShape, what, take string) string {
	switch s {
	case keptDiverged:
		return fmt.Sprintf("%s, DIVERGED: changed since the last import, and the pack's has changed too; --take %s takes the pack's and drops the yard's change", what, take)
	case keptUnknown:
		return fmt.Sprintf("%s: no import recorded what it wrote, so it cannot be told whose change it is; --take %s takes the pack's", what, take)
	}
	return fmt.Sprintf("%s, changed since the last import (the pack's has not changed since); --take %s takes the pack's", what, take)
}

// keepFlow is the Keeps line of the flow the import would leave as the yard
// has it, have, though the pack's, want, differs: nil where the yard has what
// an import last wrote and the pack's change is to be brought. Both are
// digested as the store keeps a flow (store.Flow.Digest); a flow that cannot
// be is the store's to refuse when it is written, as it was.
func (b *planner) keepFlow(have, want store.Flow) []string {
	hd, _ := have.Digest()
	wd, _ := want.Digest()
	shape := shapeOf(hd, wd, b.recordedSums("flow", want.Name))
	if shape == notKept {
		return nil
	}
	return []string{keptWords(shape, "the yard's flow", "flow/"+want.Name)}
}

// flowRecord is the record of a flow the plan carries: its name and the
// digest of the pack's version, of the flow the import wrote or would have
// written. It is the baseline the next plan holds the yard's flow against, so
// a flow left as the yard has it records the pack's version too: the next
// plan then tells a pack that moved since from one that did not.
func flowRecord(o Object) store.PackObject {
	po := store.PackObject{Kind: o.Kind, Name: o.Name}
	if o.Flow != nil {
		po.SHA256, _ = o.Flow.Digest()
	}
	return po
}

// TakeName is what --take calls the object: its kind and its name as the
// plan prints them, such as flow/starter.default or file/roles/builder.md.
func (o Object) TakeName() string { return o.Kind + "/" + o.Name }

// keptWords is what Result.Kept says of an object left as the yard has it.
func (o Object) keptWords() string {
	return o.Kind + " " + o.Name + ": " + strings.Join(o.Keeps, "; ")
}

// takes reports whether the import is told to take the pack's version of obj,
// a kept object, which it then writes as a change.
func (o ApplyOptions) takes(obj Object) bool {
	return obj.Action == Kept && slices.Contains(o.Take, obj.TakeName())
}

// takeProblems are the refusals of the names an import is told to take
// (ApplyOptions.Take), each a sentence, before anything is written: a name
// that is no <kind>/<name>, a declared source, which stays as the pack
// declares it, an object the plan does not have, and one it has but does not
// keep. A name that is refused leaves the whole import unwritten, so a take of
// an object another import took since fails loudly and never silently.
func (p *Plan) takeProblems(take []string) []string {
	var kept []string
	for _, o := range p.Objects {
		if o.Action == Kept {
			kept = append(kept, o.TakeName())
		}
	}
	slices.Sort(kept)
	keeps := "it keeps nothing"
	if len(kept) > 0 {
		keeps = "it keeps " + strings.Join(kept, ", ")
	}
	var out []string
	for _, name := range take {
		kind, rest, ok := strings.Cut(name, "/")
		if !ok || kind == "" || rest == "" {
			out = append(out, fmt.Sprintf("--take %s: name an object as the plan prints it, <kind>/<name>: flow/%s.default, file/roles/builder.md", name, p.Pack))
			continue
		}
		if kind == "source" {
			out = append(out, fmt.Sprintf("--take %s: a declared source stays as the pack declares it, and nothing of it is kept", name))
			continue
		}
		i := slices.IndexFunc(p.Objects, func(o Object) bool { return o.TakeName() == name })
		switch {
		case i < 0:
			out = append(out, fmt.Sprintf("--take %s: the plan has no such object (%s)", name, keeps))
		case !slices.ContainsFunc(p.Objects, func(o Object) bool { return o.TakeName() == name && o.Action == Kept }):
			out = append(out, fmt.Sprintf("--take %s: the plan does not keep it, so there is nothing to take (it is %s in the plan; %s)", name, p.Objects[i].Action, keeps))
		}
	}
	return out
}

// CheckTake refuses names to take that the plan cannot carry out, in the
// words Apply gives them, so that a command can refuse before it shows or asks
// anything.
func (p *Plan) CheckTake(take []string) error {
	if problems := p.takeProblems(take); len(problems) > 0 {
		return fmt.Errorf("nothing was changed: %s", strings.Join(problems, "; "))
	}
	return nil
}

// Taking is the plan as an import told to take these names would carry it
// out, for a command to show: each kept object it takes is a change, with
// nothing of it kept. The plan itself is not changed, since Apply takes the
// names from its options.
func (p *Plan) Taking(take []string) *Plan {
	if len(take) == 0 {
		return p
	}
	c := *p
	c.Objects = slices.Clone(p.Objects)
	for i, o := range c.Objects {
		if (ApplyOptions{Take: take}).takes(o) {
			c.Objects[i].Action, c.Objects[i].Keeps = Change, nil
		}
	}
	return &c
}
