package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/config"
)

// An up-to-date import of an embedded pack makes doctor print nothing
// about it; a copied role as an earlier yardr left it, which the running
// binary's differs from, makes it name the import as behind, as a notice and
// not a fault. A role the yard changed itself is kept by an import, so it is
// not behind: pack apply would leave it as it is.
func TestDoctorNamesStaleEmbeddedPackCopy(t *testing.T) {
	t.Parallel()
	a := packApp(t)
	requireNonInteractive(t)
	args := append([]string{"apply", "starter", "--depot", "web", "--yes"}, starterFlags...)
	if out, err := run(t, a.packCmd(), args...); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	// Up to date: nothing about packs behind.
	out, _ := run(t, a.doctorCmd())
	if strings.Contains(out, "behind yardr") {
		t.Fatalf("an up-to-date import is named as behind:\n%s", out)
	}
	// A copied role the yard changed: the import keeps it, and nothing is behind.
	role := filepath.Join(config.At(a.cfg.Home).Pack("starter"), "roles", "builder.md")
	text, err := os.ReadFile(role)
	if err != nil {
		t.Fatal(err)
	}
	edited := append(text, "\nStale test edit.\n"...)
	if err := os.WriteFile(role, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _ = run(t, a.doctorCmd()); strings.Contains(out, "behind yardr") {
		t.Fatalf("a role the yard changed is named as behind:\n%s", out)
	}
	// The same copy as an earlier yardr's import left it: the record holds
	// what is on disk, and the running binary's role is what moved.
	ctx := context.Background()
	recs, err := a.st.Packs(ctx)
	if err != nil || len(recs) != 1 {
		t.Fatalf("records = %+v, %v", recs, err)
	}
	for i, po := range recs[0].Objects {
		if po.Kind == "file" && po.Name == role {
			recs[0].Objects[i].SHA256 = sum256(edited)
		}
	}
	if _, err := a.st.RecordPack(ctx, recs[0], true, "me"); err != nil {
		t.Fatal(err)
	}
	out, _ = run(t, a.doctorCmd())
	for _, want := range []string{"note", "packs", "pack starter in depot web", "behind yardr", "roles/builder.md",
		"pack check starter --depot web", "pack apply starter --depot web"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor lacks %q:\n%s", want, out)
		}
	}
	// A notice, not a fault.
	rep := doctorPackJSON(t, a)
	if len(rep.State.StalePacks) != 1 || !strings.Contains(rep.State.StalePacks[0], "pack starter in depot web") {
		t.Errorf("stale_packs = %v", rep.State.StalePacks)
	}
	for _, f := range rep.Faults {
		if strings.Contains(f, "pack starter") {
			t.Errorf("the stale pack is a fault: %q", f)
		}
	}
}

// Only embedded sources are compared: a pack imported from a directory
// whose copy changed is not named.
func TestDoctorIgnoresStaleDirectoryPackCopy(t *testing.T) {
	t.Parallel()
	a := packApp(t)
	requireNonInteractive(t)
	args := append([]string{"apply", "../../packs/starter", "--depot", "web", "--yes"}, starterFlags...)
	if out, err := run(t, a.packCmd(), args...); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	role := filepath.Join(config.At(a.cfg.Home).Pack("starter"), "roles", "builder.md")
	text, err := os.ReadFile(role)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(role, append(text, "\nStale test edit.\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := doctorPackJSON(t, a)
	if len(rep.State.StalePacks) != 0 {
		t.Errorf("a directory import is named as behind: %v", rep.State.StalePacks)
	}
	if out, _ := run(t, a.doctorCmd()); strings.Contains(out, "behind yardr") {
		t.Errorf("a directory import is named as behind:\n%s", out)
	}
}

func doctorPackJSON(t *testing.T, a *app) doctorReport {
	t.Helper()
	out, _ := run(t, a.rootCmd(), "doctor", "--json")
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("doctor --json: %v\n%s", err, out)
	}
	if rep.State == nil {
		t.Fatalf("doctor --json has no state:\n%s", out)
	}
	return rep
}
