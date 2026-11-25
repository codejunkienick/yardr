package pack

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// Whose change a difference is, judged by digests alone: no record says
// nothing, the yard's flow being what an import wrote means the pack moved,
// and otherwise the yard changed it, and the pack moved too unless its version
// is one an import recorded. Depots share the store's flow, so the records
// are several, and any of them may be what the yard has. Only the DIVERGED
// line says what a take does with the yard's change: it drops it, but keeps a
// stage file's text in the stage history, and the other two lines say nothing
// of either.
func TestShapeOf(t *testing.T) {
	for _, tc := range []struct {
		name       string
		have, want string
		was        []string
		shape      keptShape
		stage      bool     // a stage file, not a flow
		says, not  []string // what its line says, and what it must not
	}{
		{"no record", "y", "p", nil, keptUnknown, false, []string{"no import recorded what it wrote"}, []string{"drops", "stage history"}},
		{"the yard has what an import wrote", "w", "p", []string{"w"}, notKept, false, nil, nil},
		{"the yard has what another depot's import wrote", "w2", "p", []string{"w1", "w2"}, notKept, false, nil, nil},
		{"the yard changed it and the pack did not move", "y", "w", []string{"w"}, keptEdited, false, []string{"the pack's has not changed since"}, []string{"drops", "stage history"}},
		{"the pack's version is another depot's record", "y", "w2", []string{"w1", "w2"}, keptEdited, false, []string{"the pack's has not changed since"}, []string{"drops", "stage history"}},
		{"both changed", "y", "p", []string{"w"}, keptDiverged, false, []string{"DIVERGED", "drops the yard's change"}, []string{"stage history"}},
		{"a stage file, no record", "y", "p", nil, keptUnknown, true, []string{"no import recorded what it wrote"}, []string{"drops", "stage history"}},
		{"a stage file the yard changed and the pack did not move", "y", "w", []string{"w"}, keptEdited, true, []string{"the pack's has not changed since"}, []string{"drops", "stage history"}},
		{"a stage file, both changed", "y", "p", []string{"w"}, keptDiverged, true, []string{"DIVERGED", "keeps the yard's change in the stage history"}, []string{"drops"}},
	} {
		got := shapeOf(tc.have, tc.want, tc.was)
		if got != tc.shape {
			t.Errorf("%s: shape %d, want %d", tc.name, got, tc.shape)
			continue
		}
		if tc.shape == notKept {
			continue
		}
		what, take := "the yard's flow", "flow/p.f"
		if tc.stage {
			what, take = "the yard's copy", "file/flows/f/new.md"
		}
		line := keptWords(got, what, take, tc.stage)
		if !strings.Contains(line, "--take "+take+" takes the pack's") || strings.Contains(line, "DIVERGED") != (tc.shape == keptDiverged) {
			t.Errorf("%s: line %q", tc.name, line)
		}
		for _, say := range tc.says {
			if !strings.Contains(line, say) {
				t.Errorf("%s: line %q lacks %q", tc.name, line, say)
			}
		}
		for _, no := range tc.not {
			if strings.Contains(line, no) {
				t.Errorf("%s: line %q says %q", tc.name, line, no)
			}
		}
	}
}

// A flow the store normalizes differently from how the pack built it is not
// the yard's edit: the digest the import records is that of the flow the store
// holds, so the pack's raw flow and the stored one are the same flow to a
// later plan, and a yard change to the stored one is still seen. The pack
// builds the form the store normalizes to (Flow.StoreFlow), so this holds the
// digest to it with a flow that is not.
func TestKeepFlowTakesTheNormalizedFlowForTheSame(t *testing.T) {
	st := storetest.New(t)
	raw := store.Flow{
		Name:   "p.f",
		Stages: []string{"new"}, // review and merged are reached by the store's walk
		Transitions: map[string][]string{
			"new":    {"review", "review"},
			"review": {"merged"},
		},
		Terminal: []string{"merged"},
	}
	stored, err := st.PutFlow(context.Background(), raw, "me")
	if err != nil || len(stored.Stages) != 3 {
		t.Fatalf("the store holds %+v, %v: it should have walked to the stages the flow reaches", stored, err)
	}
	po, ok := Ident(Object{Kind: "flow", Name: "p.f", Action: Create, Flow: &raw})
	if !ok || po.SHA256 == "" {
		t.Fatalf("Ident = %+v, %v", po, ok)
	}
	b := &planner{p: &Pack{Name: "p"}, all: []store.PackImport{{Pack: "p", Depot: "web", Objects: []store.PackObject{po}}}}
	if kept := b.keepFlow(*stored, raw); kept != nil {
		t.Fatalf("the flow the store holds reads as the yard's change: %q", kept)
	}
	edited := *stored
	edited.Transitions = map[string][]string{"new": {"review", "merged"}, "review": {"merged"}}
	if kept := b.keepFlow(edited, raw); len(kept) != 1 || !strings.Contains(kept[0], "the pack's has not changed since") {
		t.Fatalf("a flow with another edge reads as %q", kept)
	}
}

// Ident records a flow whatever the plan does with it, kept ones included,
// with the digest of the pack's version, and a flow the plan knows no version
// of (one a prune would remove) as the record it was.
func TestIdentRecordsTheFlowsDigest(t *testing.T) {
	f := store.Flow{Name: "p.f", Stages: []string{"new", "done"}, Transitions: map[string][]string{"new": {"done"}}, Terminal: []string{"done"}}
	want, err := f.Digest()
	if err != nil || want == "" {
		t.Fatalf("digest = %q, %v", want, err)
	}
	for _, action := range []string{Create, Change, Unchanged, Kept} {
		po, ok := Ident(Object{Kind: "flow", Name: "p.f", Action: action, Flow: &f})
		if !ok || !reflect.DeepEqual(po, store.PackObject{Kind: "flow", Name: "p.f", SHA256: want}) {
			t.Errorf("%s: Ident = %+v, %v", action, po, ok)
		}
	}
	owned := store.PackObject{Kind: "flow", Name: "p.f", SHA256: "was"}
	if po, ok := Ident(Object{Kind: "flow", Name: "p.f", Action: Dropped, Owned: &owned}); !ok || !reflect.DeepEqual(po, owned) {
		t.Errorf("dropped: Ident = %+v, %v", po, ok)
	}
}
