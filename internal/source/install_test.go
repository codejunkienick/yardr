package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// installYard is a machine for install tests: a bin directory first
// on PATH, holding no fake-tool until an install puts one there; the
// system's directories stay after it for the shell's own tools.
func installYard(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	return bin
}

func writeProgram(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestNeedPresent(t *testing.T) {
	bin := installYard(t)
	tool := Need{Program: "fake-tool", Env: "FAKE_TOOL_BIN", Install: []string{"install-fake"}}
	if NeedPresent(tool) {
		t.Fatal("fake-tool is present on an empty PATH")
	}
	// The env path names another copy, checked first: executable there
	// is present with nothing on PATH.
	other := filepath.Join(t.TempDir(), "fake-tool")
	writeProgram(t, other, "#!/bin/sh\nexit 0\n")
	t.Setenv("FAKE_TOOL_BIN", other)
	if path, ok := NeedPath(tool); !ok || path != other {
		t.Fatalf("NeedPath = %q, %v; want the env path", path, ok)
	}
	// An env path that is no program falls back to PATH, still missing.
	writeProgram(t, other, "not a program\n")
	if err := os.Chmod(other, 0o600); err != nil {
		t.Fatal(err)
	}
	if NeedPresent(tool) {
		t.Fatal("a non-executable env path counts as present")
	}
	// On PATH it is present whichever way the env names it.
	t.Setenv("FAKE_TOOL_BIN", "")
	writeProgram(t, filepath.Join(bin, "fake-tool"), "#!/bin/sh\nexit 0\n")
	if !NeedPresent(tool) {
		t.Fatal("fake-tool on PATH is not present")
	}
}

func TestInstallNeed(t *testing.T) {
	bin := installYard(t)
	d := &Declaration{Name: "fake", Base: "https://fake.example.com", Needs: []Need{
		{Program: "fake-tool", Env: "FAKE_TOOL_BIN", Install: []string{"install-fake"}},
	}}
	if missing := d.MissingNeeds(); len(missing) != 1 {
		t.Fatalf("missing = %+v, want fake-tool", missing)
	}
	// The install stands the program in the first directory of PATH,
	// the bin this test put there.
	install := "#!/bin/sh\nset -eu\nd=${PATH%%:*}\nprintf '#!/bin/sh\\nexit 0\\n' > \"$d/fake-tool\"\nchmod +x \"$d/fake-tool\"\n"
	argv, cleanup, err := InstallArgv(d.Needs[0], []byte(install))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := InstallNeed(context.Background(), d, d.Needs[0], argv); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(bin, "fake-tool")); err != nil {
		t.Fatalf("the install put no fake-tool in %s: %v", bin, err)
	}
	if len(d.MissingNeeds()) != 0 {
		t.Fatalf("missing after install = %+v", d.MissingNeeds())
	}
}

func TestInstallNeedInstallsNothing(t *testing.T) {
	installYard(t)
	d := &Declaration{Name: "fake", Base: "https://fake.example.com", Needs: []Need{
		{Program: "fake-tool", Install: []string{"install-fake"}},
	}}
	argv, cleanup, err := InstallArgv(d.Needs[0], []byte("#!/bin/sh\nexit 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	err = InstallNeed(context.Background(), d, d.Needs[0], argv)
	if err == nil || !strings.Contains(err.Error(), "installed nothing") {
		t.Fatalf("InstallNeed = %v, want an installed-nothing refusal", err)
	}
}

func TestInstallNeedTimeout(t *testing.T) {
	installYard(t)
	old := InstallTimeout
	InstallTimeout = 50 * time.Millisecond
	defer func() { InstallTimeout = old }()
	d := &Declaration{Name: "fake", Base: "https://fake.example.com", Needs: []Need{
		{Program: "fake-tool", Install: []string{"install-fake"}},
	}}
	argv, cleanup, err := InstallArgv(d.Needs[0], []byte("#!/bin/sh\nexec sleep 30\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	start := time.Now()
	err = InstallNeed(context.Background(), d, d.Needs[0], argv)
	if err == nil || !strings.Contains(err.Error(), "no answer within") {
		t.Fatalf("InstallNeed = %v, want a timeout", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("the install ran %v against a 50ms timeout", took)
	}
}
