package agents

import (
	"slices"
	"testing"

	"yardr.dev/yardr/internal/store"
)

// The store validates kinds, presets and harness keys against static tables
// instead of this catalogue, so that it imports no internal package. Those
// tables are copies of what the harness files say; this test pins them to
// the catalogue, so a file that renames a label, adds login_env or offers a
// new preset fails until the tables follow.
func TestStoreTablesMatchCatalogue(t *testing.T) {
	t.Parallel()
	if len(store.HarnessLabels) != len(Harnesses) {
		t.Fatalf("store.HarnessLabels has %d kinds, the catalogue %d", len(store.HarnessLabels), len(Harnesses))
	}
	var presets []string
	for _, h := range Harnesses {
		if store.HarnessLabels[h.Kind] != h.Label {
			t.Errorf("store.HarnessLabels[%q] = %q, the file says %q", h.Kind, store.HarnessLabels[h.Kind], h.Label)
		}
		if !slices.Equal(store.HarnessLoginEnv[h.Kind], h.LoginEnv) {
			t.Errorf("store.HarnessLoginEnv[%q] = %v, the file says %v", h.Kind, store.HarnessLoginEnv[h.Kind], h.LoginEnv)
		}
		for _, p := range h.Flags.Permissions {
			if k := store.PresetLabel(p.Label); !slices.Contains(presets, k) {
				presets = append(presets, k)
			}
		}
		if got, want := store.HarnessPresets[h.Kind], h.Flags.Permissions; len(got) != len(want) {
			t.Errorf("store.HarnessPresets[%q] has %d presets, the file %d", h.Kind, len(got), len(want))
		} else {
			for i := range want {
				if got[i].Label != want[i].Label || !slices.Equal(got[i].Args, want[i].Args) || got[i].Note != want[i].Note {
					t.Errorf("store.HarnessPresets[%q][%d] = %+v, the file says %+v", h.Kind, i, got[i], want[i])
				}
			}
		}
	}
	for _, h := range Harnesses {
		var offered []string
		for _, o := range h.Offers {
			offered = append(offered, o.Model)
		}
		if !slices.Equal(store.HarnessOffers[h.Kind], offered) {
			t.Errorf("store.HarnessOffers[%q] = %v, the file offers %v", h.Kind, store.HarnessOffers[h.Kind], offered)
		}
	}
	for kind := range store.HarnessOffers {
		if _, ok := store.HarnessLabels[kind]; !ok {
			t.Errorf("store.HarnessOffers names %q, which has no harness file", kind)
		}
	}
	for _, h := range Harnesses {
		got, takes := store.HarnessEfforts[h.Kind]
		switch e := h.Flags.Effort; {
		case e == nil && takes:
			t.Errorf("store.HarnessEfforts names %q, whose file says no effort", h.Kind)
		case e != nil && (!takes || !slices.Equal(got.Args, e.Args) || !slices.Equal(got.Levels, e.Levels) || !slices.Equal(got.Aliases, e.Aliases)):
			t.Errorf("store.HarnessEfforts[%q] = %+v, the file says %+v", h.Kind, got, *e)
		}
	}
	for kind := range store.HarnessEfforts {
		if _, ok := store.HarnessLabels[kind]; !ok {
			t.Errorf("store.HarnessEfforts names %q, which has no harness file", kind)
		}
	}
	for _, h := range Harnesses {
		if (h.Flags.Model == "") != store.HarnessNoModelFlag[h.Kind] {
			t.Errorf("store.HarnessNoModelFlag names %q: %v, the file names a model flag: %v", h.Kind, store.HarnessNoModelFlag[h.Kind], h.Flags.Model != "")
		}
	}
	for kind := range store.HarnessNoModelFlag {
		if _, ok := store.HarnessLabels[kind]; !ok {
			t.Errorf("store.HarnessNoModelFlag names %q, which has no harness file", kind)
		}
	}
	for _, h := range Harnesses {
		got, takes := store.HarnessModelEnvs[h.Kind]
		switch e := h.Flags.ModelEnv; {
		case e == nil && takes:
			t.Errorf("store.HarnessModelEnvs names %q, whose file says no model environment", h.Kind)
		case e != nil && (!takes || got != *e):
			t.Errorf("store.HarnessModelEnvs[%q] = %+v, the file says %+v", h.Kind, got, *e)
		}
	}
	for kind := range store.HarnessModelEnvs {
		if _, ok := store.HarnessLabels[kind]; !ok {
			t.Errorf("store.HarnessModelEnvs names %q, which has no harness file", kind)
		}
	}
	// A config that names no kind is the one the runner starts, which holds
	// its effort to that kind's levels.
	if store.EffortKind(nil) != DefaultKind {
		t.Errorf("store.EffortKind(nil) = %q, the runner starts %q", store.EffortKind(nil), DefaultKind)
	}
	if store.ModelKind(nil) != DefaultKind {
		t.Errorf("store.ModelKind(nil) = %q, the runner starts %q", store.ModelKind(nil), DefaultKind)
	}
	for kind := range store.HarnessLoginEnv {
		if _, ok := store.HarnessLabels[kind]; !ok {
			t.Errorf("store.HarnessLoginEnv names %q, which has no harness file", kind)
		}
	}
	slices.Sort(presets)
	if !slices.Equal(store.PresetNames, presets) {
		t.Errorf("store.PresetNames = %v, the files say %v", store.PresetNames, presets)
	}
}
