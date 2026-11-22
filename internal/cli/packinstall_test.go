package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// yardr pack install installs what a pack's declarations need that
// the machine lacks, from the pack's own install file, and changes
// nothing where the program already stands. The test sets the
// process PATH, so it stays sequential, and its installs are fixture
// programs only.
func TestPackInstall(t *testing.T) {
	a := packApp(t)
	requireNonInteractive(t)
	bin := t.TempDir()
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("FAKE_TOOL_BIN", "")
	dir := t.TempDir()
	files := map[string]string{
		"pack.yaml":    "name: needful\nkind: source\nsources:\n  needful:\n    declaration: needful.yaml\n",
		"needful.yaml": "name: needful\nbase: https://needful.example.com\ncredential: { env: NEED_TOKEN }\ncalls:\n  ping: { kind: read, request: { method: GET, path: /me } }\nneeds:\n  - { program: fake-tool, env: FAKE_TOOL_BIN, install: [install-fake] }\n",
		"install-fake": "#!/bin/sh\nset -eu\nd=${PATH%%:*}\nprintf '#!/bin/sh\\nexit 0\\n' > \"$d/fake-tool\"\nchmod +x \"$d/fake-tool\"\n",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := run(t, a.packCmd(), "install", dir)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if !strings.Contains(out, "installed fake-tool for pack needful") {
		t.Fatalf("the install said:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(bin, "fake-tool")); err != nil {
		t.Fatalf("fake-tool does not stand in %s after the install: %v", bin, err)
	}
	out, err = run(t, a.packCmd(), "install", dir)
	if err != nil {
		t.Fatalf("install again: %v\n%s", err, out)
	}
	if !strings.Contains(out, "every program it declares it needs is on this machine") {
		t.Fatalf("the second install said:\n%s", out)
	}
}
