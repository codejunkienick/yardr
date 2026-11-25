package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/agents"
	"yardr.dev/yardr/internal/store"
)

// machineOf is doctor --json's machine items on a home with no yard, by
// program.
func machineOf(t *testing.T) map[string]need {
	t.Helper()
	rep, _ := doctorJSON(t, nil)
	out := map[string]need{}
	for _, n := range rep.Machine {
		out[n.Program] = n
	}
	return out
}

// herdr's fetch is not in a harness file, so nothing checks it when yardr
// starts: this does. It names a file for each system the onboarding installs
// on, and each is a different one.
func TestHerdrFetch(t *testing.T) {
	t.Parallel()
	if err := herdrFetch.Check("herdr"); err != nil {
		t.Fatal(err)
	}
	urls := map[string]bool{}
	for _, goos := range []string{"darwin", "linux"} {
		for _, arch := range []string{"arm64", "amd64"} {
			d := herdrFetch.For(goos, arch, "/home/x")
			if d == nil {
				t.Errorf("no herdr for %s/%s", goos, arch)
				continue
			}
			if urls[d.URL] {
				t.Errorf("%s/%s has a file that another system has: %s", goos, arch, d.URL)
			}
			urls[d.URL] = true
		}
	}
}

// On a Mac, doctor --json says for each missing program how a program that
// fetches gets it: herdr's file for this machine, with the path under this
// user's home and the command to paste beside it, and under the line that no
// harness is installed each harness that can be fetched. git has none: it is
// Apple's own dialog.
func TestDoctorFetchOnMac(t *testing.T) {
	fakeTools(t, nil)
	stub(t, &loginShell, func() string { return "" })
	stub(t, &goos, "darwin")
	stub(t, &goarch, "arm64")
	stub(t, &userHomeDir, func() (string, error) { return "/Users/pat", nil })
	m := machineOf(t)

	herdr := m["herdr"]
	if f := herdr.Fetch; herdr.Found || herdr.Get != "curl -fsSL https://herdr.dev/install.sh | sh (more: https://herdr.dev)" ||
		herdr.Command != "curl -fsSL https://herdr.dev/install.sh | sh" || f == nil ||
		f.Path != "/Users/pat/.local/bin/herdr" || f.Version != "0.9.4" || !strings.HasSuffix(f.URL, "/v0.9.4/herdr-macos-aarch64") ||
		f.SHA256 != herdrFetch.Targets["darwin/arm64"].SHA256 || f.Size == 0 || f.Member != "" || f.Run != nil ||
		f.Verified != (store.Verified{From: "https://herdr.dev/docs/install/", On: "2026-11-05"}) {
		t.Errorf("herdr: %+v, fetch %+v", herdr, f)
	}
	if git := m["git"]; git.Fetch != nil || git.Command != "" || !strings.Contains(git.Get, "xcode-select --install") {
		t.Errorf("git: %+v", git)
	}
	none := m["a harness"]
	if none.Fetch != nil || none.Get == "" || len(none.AnyOf) != 2 {
		t.Fatalf("a harness: %+v", none)
	}
	for i, kind := range []string{"claude", "codex"} {
		n := none.AnyOf[i]
		if f := n.Fetch; n.Harness != kind || n.Program != kind || n.Found || n.Get != agents.InstallHint(kind) || f == nil ||
			f.Path != "/Users/pat/.local/bin/"+kind || f.SHA256 != agents.Harnesses[i].Fetch.Targets["darwin/arm64"].SHA256 {
			t.Errorf("any_of %d: %+v, fetch %+v", i, n, f)
		}
	}
	if f := none.AnyOf[0].Fetch; strings.Join(f.Run, " ") != "install "+f.Version {
		t.Errorf("claude is run with %v", f.Run)
	}

	// The other kind of Mac gets its own file.
	stub(t, &goarch, "amd64")
	if f := machineOf(t)["herdr"].Fetch; f == nil || !strings.HasSuffix(f.URL, "/herdr-macos-x86_64") {
		t.Errorf("herdr on an Intel Mac: %+v", f)
	}

	// A claude on PATH that is another program is as missing as none, and a
	// program that fetches is told how to get the real one all the same;
	// the line that says no harness is installed still lists it among those
	// that can be fetched.
	fakeTools(t, map[string]string{"claude": toolAnswers})
	m = machineOf(t)
	if c := m["claude"]; c.Found || c.Path == "" || c.Version == nil || c.Version.State != versionNotHarness ||
		c.Get != agents.InstallHint("claude") || c.Fetch == nil || c.Fetch.Path != "/Users/pat/.local/bin/claude" {
		t.Errorf("a claude that is another program: %+v", c)
	}
	if none := m["a harness"]; len(none.AnyOf) != 2 || none.AnyOf[0].Harness != "claude" || none.AnyOf[0].Fetch == nil || none.AnyOf[0].Found {
		t.Errorf("a harness: %+v", none)
	}
}

// On Linux doctor --json says for a missing program how a program that fetches
// gets it, as on a Mac: where yardr pins a file for the system and the
// architecture, which today is herdr's alone, one static executable for each
// architecture. No harness pins a file for Linux, so a missing harness keeps
// its hint to install it by hand and the line that says no harness is
// installed lists none that can be fetched; git has no way anywhere. A machine
// that no file is pinned for gets none.
func TestDoctorFetchOnLinux(t *testing.T) {
	fakeTools(t, nil)
	stub(t, &loginShell, func() string { return "" })
	stub(t, &goos, "linux")
	stub(t, &userHomeDir, func() (string, error) { return "/home/pat", nil })
	for _, tc := range []struct{ arch, file string }{
		{"amd64", "herdr-linux-x86_64"},
		{"arm64", "herdr-linux-aarch64"},
	} {
		stub(t, &goarch, tc.arch)
		m := machineOf(t)

		herdr, pinned := m["herdr"], herdrFetch.Targets["linux/"+tc.arch]
		if f := herdr.Fetch; herdr.Found || herdr.Command != "curl -fsSL https://herdr.dev/install.sh | sh" || f == nil || !strings.HasSuffix(f.URL, "/v"+herdrFetch.Version+"/"+tc.file) ||
			f.SHA256 != pinned.SHA256 || f.Size != pinned.Size || f.Path != "/home/pat/.local/bin/herdr" || f.Version != herdrFetch.Version ||
			f.Member != "" || f.Run != nil || f.Verified != herdrFetch.Verified {
			t.Errorf("herdr on linux/%s: %+v, fetch %+v", tc.arch, herdr, f)
		}
		// The item as a program that reads the report has it: the fetch beside
		// the hint it was given for, and the command alone to copy.
		if b, err := json.Marshal(herdr); err != nil || !strings.Contains(string(b), `"command":"curl -fsSL https://herdr.dev/install.sh | sh"`) ||
			!strings.Contains(string(b), `"fetch":{`) || !strings.Contains(string(b), `"get":"curl -fsSL https://herdr.dev/install.sh | sh (more: https://herdr.dev)"`) {
			t.Errorf("herdr on linux/%s as json: %s, %v", tc.arch, b, err)
		}

		for program, n := range m {
			if program != "herdr" && (n.Fetch != nil || n.AnyOf != nil || n.Command != "" || n.Get == "") {
				t.Errorf("%s on linux/%s: get %q, command %q, fetch %+v, any_of %+v", program, tc.arch, n.Get, n.Command, n.Fetch, n.AnyOf)
			}
		}
	}

	// A harness is as missing as it can be here, a claude on PATH that is
	// another program, and has no fetch: its file pins no Linux box.
	fakeTools(t, map[string]string{"claude": toolAnswers})
	m := machineOf(t)
	if c := m["claude"]; c.Found || c.Version == nil || c.Version.State != versionNotHarness || c.Get != agents.InstallHint("claude") || c.Fetch != nil {
		t.Errorf("a claude that is another program: %+v", c)
	}
	if none := m["a harness"]; none.Found || none.Get == "" || none.Fetch != nil || none.AnyOf != nil {
		t.Errorf("a harness: %+v", none)
	}

	// Where no file is pinned for the architecture there is no fetch, on Linux
	// as on a Mac, and the hint is what is left.
	stub(t, &goarch, "riscv64")
	if herdr := machineOf(t)["herdr"]; herdr.Fetch != nil || herdr.Get == "" || herdr.Command != "curl -fsSL https://herdr.dev/install.sh | sh" {
		t.Errorf("herdr on linux/riscv64: %+v", herdr)
	}
}

// What is installed needs no fetch and no command to get it, on a Mac as
// anywhere.
func TestDoctorNoFetchOfWhatIsFound(t *testing.T) {
	fakeTools(t, map[string]string{"git": toolAnswers, "herdr": toolAnswers, "claude": claudeAnswers})
	stub(t, &herdrServer, func(context.Context, string) serverCheck { return serverCheck{State: herdrUp} })
	stub(t, &goos, "darwin")
	stub(t, &goarch, "arm64")
	for program, n := range machineOf(t) {
		if !n.Found || n.Fetch != nil || n.AnyOf != nil || n.Command != "" {
			t.Errorf("%s: %+v", program, n)
		}
	}
}
