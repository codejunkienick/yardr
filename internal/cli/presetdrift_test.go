package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/agents"
	"yardr.dev/yardr/internal/store"
)

// codexReadOnly is the Codex preset the cases below fall behind: its last
// setting is what the file gave it after groups had been set from it, and
// behind is the list those groups kept.
func codexReadOnly(t *testing.T) (preset store.Permission, behind []string, last string) {
	t.Helper()
	preset, ok := agents.Preset("codex", "read only")
	if n := len(preset.Args); !ok || n < 4 || preset.Args[n-2] != "-c" {
		t.Fatalf("the codex read only preset is %+v, %v: these cases drop its last -c setting", preset, ok)
	}
	n := len(preset.Args)
	return preset, preset.Args[:n-2], preset.Args[n-1]
}

func anyList(words []string) []any {
	out := make([]any, len(words))
	for i, w := range words {
		out[i] = w
	}
	return out
}

// A group set from a preset keeps the arguments it was given when the preset
// gains one, so doctor names a group whose arguments hold a setting only the
// preset assigns and lack an argument it has now (yardr-mbdr). A group
// current with its preset, or holding nothing of one, earns no note.
func TestPresetDriftNotes(t *testing.T) {
	t.Parallel()
	ro, behind, last := codexReadOnly(t)
	bypass, _ := agents.Preset("codex", "bypass")
	herdr := func(cfg map[string]any) store.Group {
		return store.Group{Name: "reviewers", Runner: "herdr", Config: cfg}
	}
	codex := func(args any) store.Group { return herdr(map[string]any{"kind": "codex", "args": args}) }
	note := func(lacks, replaces string) []string {
		return []string{fmt.Sprintf("group reviewers starts codex agents with arguments behind the read only preset: they lack %s, which the preset has. Set the preset's arguments%s: yardr group set reviewers preset=read-only", lacks, replaces)}
	}
	own := `permissions.yardr_read_only.filesystem={"/old"="write"}`
	pair := "-c " + shellQuote(last)
	for _, c := range []struct {
		name  string
		group store.Group
		def   string
		want  []string
	}{
		{"the last setting missing", codex(anyList(behind)), "", note(pair, "")},
		{"args as a string", codex(strings.Join(behind, " ")), "", note(pair, "")},
		{"an argument of its own, which the preset's replace", codex(anyList(append(slices.Clone(behind), "--verbose"))), "",
			note(pair, " (which replace its own, dropping --verbose)")},
		{"a -c missing though its setting is held", codex(anyList(slices.Delete(slices.Clone(ro.Args), len(ro.Args)-2, len(ro.Args)-1))), "", note("-c", "")},
		{"a setting that changed", codex(anyList(append(slices.Clone(ro.Args[:len(ro.Args)-1]), own))), "",
			note(shellQuote(last), " (which replace its own, dropping "+shellQuote(own)+")")},
		{"the kind from default_kind", herdr(map[string]any{"args": anyList(behind)}), "codex", note(pair, "")},
		{"no kind and no default_kind is claude, whose presets these are not", herdr(map[string]any{"args": anyList(behind)}), "", nil},

		{"current", codex(anyList(ro.Args)), "", nil},
		{"current, with an argument of its own", codex(anyList(append(slices.Clone(ro.Args), "--verbose"))), "", nil},
		{"another preset's arguments", codex(anyList(bypass.Args)), "", nil},
		{"an operator's own -c setting", codex([]any{"-c", "model_reasoning_effort=high"}), "", nil},
		{"a bare flag and word of the preset", codex([]any{"--enable", "network_proxy"}), "", nil},
		{"no args", herdr(map[string]any{"kind": "codex"}), "", nil},
		{"not a herdr group", store.Group{Name: "reviewers", Runner: "exec", Config: map[string]any{"kind": "codex", "args": anyList(behind)}}, "", nil},
		{"a kind with no presets", herdr(map[string]any{"kind": "pi", "args": anyList(behind)}), "", nil},
		{"a kind yardr has no file for", herdr(map[string]any{"kind": "nonesuch", "args": anyList(behind)}), "", nil},
	} {
		if got := presetDriftNotes(c.group, c.def, "yardr"); !slices.Equal(got, c.want) {
			t.Errorf("%s: notes = %q, want %q", c.name, got, c.want)
		}
	}
	// Whichever single argument of the preset is missing, the group is named
	// and the argument with it: a flag the preset repeats is counted.
	for i, w := range ro.Args {
		group := codex(anyList(slices.Delete(slices.Clone(ro.Args), i, i+1)))
		if got, want := presetDriftNotes(group, "", "yardr"), note(shellQuote(w), ""); !slices.Equal(got, want) {
			t.Errorf("without argument %d of the preset, %q: notes = %q, want %q", i, w, got, want)
		}
	}
	// No preset of any kind is behind itself: whatever a harness file offers,
	// a group set to it exactly is current.
	for _, kind := range agents.Kinds() {
		h, _ := agents.Lookup(kind)
		for _, p := range h.Flags.Permissions {
			if got := presetDriftNotes(herdr(map[string]any{"kind": kind, "args": anyList(p.Args)}), "", "yardr"); len(got) > 0 {
				t.Errorf("a group set to the %s %s preset is noted: %q", kind, p.Label, got)
			}
		}
	}
}

// A marker is a setting that one preset of a kind assigns: a bare flag or
// word says nothing of which preset a group was set from, and neither does a
// setting two presets assign.
func TestPresetMarker(t *testing.T) {
	t.Parallel()
	all := []store.Permission{
		{Label: "one", Args: []string{"-c", "a=1", "-c", "shared=2", "--flag", "word"}},
		{Label: "two", Args: []string{"-c", "b=3", "-c", "shared=2"}},
	}
	for _, c := range []struct {
		preset int
		arg    string
		want   bool
	}{
		{0, "a=1", true},
		{1, "b=3", true},
		{0, "shared=2", false},
		{1, "shared=2", false},
		{0, "-c", false},
		{0, "--flag", false},
		{0, "word", false},
		{0, "b=3", false},
	} {
		if got := presetMarker(all, all[c.preset], c.arg); got != c.want {
			t.Errorf("presetMarker(%s, %q) = %v, want %v", all[c.preset].Label, c.arg, got, c.want)
		}
	}
}

// Doctor names a group behind its preset among its config notes, with what
// it lacks, and the command it names sets the preset's arguments and clears
// the note: the group is made as the operator makes one from a preset, then
// left behind it as a harness file's change leaves a group (yardr-mbdr).
func TestDoctorNotesAGroupBehindItsPreset(t *testing.T) {
	t.Parallel()
	st := userYard(t)
	y := yardrAt(yardEnv(t, st)["YARDR_HOME"])
	notes := func() []string {
		t.Helper()
		rep, _ := doctorJSON(t, st)
		return rep.State.Config
	}
	_, behind, last := codexReadOnly(t)

	mustAs(t, st, "jo", "group", "add", "reviewers", "--runner", "herdr", "--set", "kind=codex", "--set", "preset=read-only")
	if got := notes(); len(got) != 0 {
		t.Fatalf("a group set to its preset has config notes: %q", got)
	}
	old, err := json.Marshal(behind)
	if err != nil {
		t.Fatal(err)
	}
	mustAs(t, st, "jo", "group", "set", "reviewers", "args="+string(old))
	want := fmt.Sprintf("group reviewers starts codex agents with arguments behind the read only preset: they lack -c %s, which the preset has. Set the preset's arguments: %s group set reviewers preset=read-only", shellQuote(last), y)
	got := notes()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("config notes of a group behind its preset:\n%q\nwant:\n%q", got, []string{want})
	}

	// The command the note names, as the note spells it.
	cmd := strings.Fields(strings.TrimPrefix(got[0][strings.LastIndex(got[0], ": ")+2:], y+" "))
	mustAs(t, st, "jo", cmd...)
	if got := notes(); len(got) != 0 {
		t.Errorf("the command the note names left config notes: %q", got)
	}
}
