package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/source"
)

// needfulDecl declares fake-tool under needs, installed by the file
// install-fake beside the declaration.
const needfulDecl = `name: needful
base: https://needful.example.com
credential: { env: NEED_TOKEN }
calls:
  ping: { kind: read, request: { method: GET, path: /me } }
needs:
  - { program: fake-tool, env: FAKE_TOOL_BIN, install: [install-fake] }
`

// installFake stands fake-tool in the first directory of PATH.
const installFake = "#!/bin/sh\nset -eu\nd=${PATH%%:*}\nprintf '#!/bin/sh\\nexit 0\\n' > \"$d/fake-tool\"\nchmod +x \"$d/fake-tool\"\n"

// needfulYard is a run yard whose server reads the pack needful from
// installText, which a test moves between asking and approving. Its
// machine has a bin first on PATH with no fake-tool in it. The test
// that uses it stays sequential: it sets the process PATH.
func needfulYard(t *testing.T, installText *string) (*askYard, string) {
	t.Helper()
	y, _ := runYard(t)
	bin := t.TempDir()
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("FAKE_TOOL_BIN", "")
	y.Store.SetPackReader(func(ctx context.Context, name string, depot string, mappings Mappings, yard API) (*PackReading, error) {
		sum := sha256.Sum256([]byte(*installText))
		return &PackReading{Name: name, Kind: "source", Label: "embedded in yardr test", Build: "test",
			Sources: []AskSource{{Name: "needful", Text: needfulDecl,
				Files: []AskFile{{Program: "install-fake", Text: *installText, SHA256: hex.EncodeToString(sum[:])}}}}}, nil
	})
	return y, bin
}

func needfulAsk(t *testing.T, y *askYard) uint64 {
	t.Helper()
	return y.ask(t, listAsk("mckean", "pack install needful"), "yardmaster")
}

// The card names the missing program, its pack, and its install file
// whole with its sha256, and weighs code (strong).
func TestPackInstallCard(t *testing.T) {
	text := installFake
	y, _ := needfulYard(t, &text)
	seq := needfulAsk(t, y)
	r := y.asked(t, seq)
	if r.Ask.Run == nil {
		t.Fatalf("the ask holds no run: %+v", r.Ask)
	}
	joined := strings.Join(r.Ask.Run.Does, "\n")
	sum := sha256.Sum256([]byte(installFake))
	for _, want := range []string{"pack needful", "needs fake-tool, which is not on this machine", "install-fake", hex.EncodeToString(sum[:]), "its text whole:", "#!/bin/sh"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the card says nothing of %q:\n%s", want, joined)
		}
	}
	classes := weightClasses(r.Ask.Weight)
	if len(classes) != 1 || classes[0] != WeightCode {
		t.Fatalf("the card weighs %v, want code alone", classes)
	}
	if !StrongWeight(r.Ask.Weight) {
		t.Fatalf("the card is not strong: %v", r.Ask.Weight)
	}
}

// Approving installs the missing program from the card's bytes and
// records what it installed; the program stands after.
func TestPackInstallApprove(t *testing.T) {
	text := installFake
	y, bin := needfulYard(t, &text)
	seq := needfulAsk(t, y)
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Result != "pack needful: installed fake-tool" {
		t.Fatalf("the approval recorded %q", done.Result)
	}
	if _, err := os.Stat(filepath.Join(bin, "fake-tool")); err != nil {
		t.Fatalf("fake-tool does not stand in %s after approval: %v", bin, err)
	}
	if r := y.asked(t, seq); r.State != AskAnswered {
		t.Fatalf("the ask is %s after approval", r.State)
	}
}

// Approval refuses where the install's bytes are not the card's, and
// where the program was installed since the card was written; both
// leave the ask open.
func TestPackInstallStale(t *testing.T) {
	t.Run("bytes changed", func(t *testing.T) {
		text := installFake
		y, _ := needfulYard(t, &text)
		seq := needfulAsk(t, y)
		text = "#!/bin/sh\nexit 0\n"
		_, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
		if err == nil || !strings.Contains(err.Error(), "its bytes changed since") {
			t.Fatalf("ApproveAsk = %v, want a bytes-changed refusal", err)
		}
		if r := y.asked(t, seq); r.State != AskOpen {
			t.Fatalf("the ask is %s after a refused approval", r.State)
		}
	})
	t.Run("installed since", func(t *testing.T) {
		text := installFake
		y, bin := needfulYard(t, &text)
		seq := needfulAsk(t, y)
		if err := os.WriteFile(filepath.Join(bin, "fake-tool"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
		if err == nil || !strings.Contains(err.Error(), "it is installed since") {
			t.Fatalf("ApproveAsk = %v, want an installed-since refusal", err)
		}
		if r := y.asked(t, seq); r.State != AskOpen {
			t.Fatalf("the ask is %s after a refused approval", r.State)
		}
	})
}

// A failed install leaves the ask open, and approving again retries
// it: the fixture install fails once and stands the program the
// second time.
func TestPackInstallRetry(t *testing.T) {
	text := "#!/bin/sh\nset -eu\nd=${PATH%%:*}\nif [ ! -e \"$d/tried\" ]; then : > \"$d/tried\"; exit 1; fi\nprintf '#!/bin/sh\\nexit 0\\n' > \"$d/fake-tool\"\nchmod +x \"$d/fake-tool\"\n"
	y, bin := needfulYard(t, &text)
	seq := needfulAsk(t, y)
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "Approve again to retry") {
		t.Fatalf("the first approval = %v, want a failure that leaves the ask open", err)
	}
	if r := y.asked(t, seq); r.State != AskOpen {
		t.Fatalf("the ask is %s after a failed install", r.State)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Result != "pack needful: installed fake-tool" {
		t.Fatalf("the retry recorded %q", done.Result)
	}
	if _, err := os.Stat(filepath.Join(bin, "fake-tool")); err != nil {
		t.Fatalf("fake-tool does not stand after the retry: %v", err)
	}
}

// Asking refuses where nothing is missing, and where the install
// names an absolute program, which stays in a terminal.
func TestPackInstallRefusals(t *testing.T) {
	t.Run("nothing missing", func(t *testing.T) {
		text := installFake
		y, bin := needfulYard(t, &text)
		if err := os.WriteFile(filepath.Join(bin, "fake-tool"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", "pack install needful"), "yardmaster")
		if err == nil || !strings.Contains(err.Error(), "needs nothing this machine lacks") {
			t.Fatalf("AddAsk = %v, want a nothing-missing refusal", err)
		}
	})
	t.Run("absolute install", func(t *testing.T) {
		y, _ := runYard(t)
		t.Setenv("PATH", t.TempDir()+":/usr/bin:/bin")
		decl := strings.Replace(needfulDecl, "install: [install-fake]", "install: [/opt/needful/install]", 1)
		y.Store.SetPackReader(func(ctx context.Context, name string, depot string, mappings Mappings, yard API) (*PackReading, error) {
			return &PackReading{Name: name, Kind: "source", Label: "embedded in yardr test", Build: "test",
				Sources: []AskSource{{Name: "needful", Text: decl}}}, nil
		})
		_, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", "pack install needful"), "yardmaster")
		if err == nil || !strings.Contains(err.Error(), "in a terminal") {
			t.Fatalf("AddAsk = %v, want a terminal-only refusal", err)
		}
	})
	t.Run("hidden control characters", func(t *testing.T) {
		text := "#!/bin/sh\necho hi\r# hidden\n"
		y, _ := needfulYard(t, &text)
		_, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", "pack install needful"), "yardmaster")
		if err == nil || !strings.Contains(err.Error(), "holds control characters") {
			t.Fatalf("AddAsk = %v, want a control-character refusal", err)
		}
	})
	t.Run("install file past bound", func(t *testing.T) {
		text := "#!/bin/sh\n#" + strings.Repeat("a", MaxRunSourceCommandFile) + "\n"
		y, _ := needfulYard(t, &text)
		_, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", "pack install needful"), "yardmaster")
		if err == nil || !strings.Contains(err.Error(), "a card holds at most") {
			t.Fatalf("AddAsk = %v, want a file-bound refusal", err)
		}
	})
	t.Run("install files past total bound", func(t *testing.T) {
		y, _ := runYard(t)
		t.Setenv("PATH", t.TempDir()+":/usr/bin:/bin")
		chunk := "#!/bin/sh\n#" + strings.Repeat("a", MaxRunSourceCommandFile-20) + "\n"
		sum := sha256.Sum256([]byte(chunk))
		hexSum := hex.EncodeToString(sum[:])
		var decl strings.Builder
		decl.WriteString("name: needful\nbase: https://needful.example.com\ncredential: { env: NEED_TOKEN }\ncalls:\n  ping: { kind: read, request: { method: GET, path: /me } }\nneeds:\n")
		var files []AskFile
		for _, n := range []string{"a", "b", "c", "d", "e"} {
			decl.WriteString("  - { program: fake-" + n + ", install: [install-" + n + "] }\n")
			files = append(files, AskFile{Program: "install-" + n, Text: chunk, SHA256: hexSum})
		}
		y.Store.SetPackReader(func(ctx context.Context, name string, depot string, mappings Mappings, yard API) (*PackReading, error) {
			return &PackReading{Name: name, Kind: "source", Label: "embedded in yardr test", Build: "test",
				Sources: []AskSource{{Name: "needful", Text: decl.String(), Files: files}}}, nil
		})
		_, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", "pack install needful"), "yardmaster")
		if err == nil || !strings.Contains(err.Error(), "run to") || !strings.Contains(err.Error(), "together") {
			t.Fatalf("AddAsk = %v, want a total-text-bound refusal", err)
		}
	})
}

// A setup card's missing-program lines name the install command for
// a program the machine lacks and stay silent for one it has.
func TestMissingNeedLines(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("FAKE_TOOL_BIN", "")
	d, err := source.Parse("needful.yaml", []byte(needfulDecl))
	if err != nil {
		t.Fatal(err)
	}
	decls := map[string]*source.Declaration{"needful": d}
	carried := []AskSource{{Name: "needful", Text: needfulDecl}}
	lines := missingNeedLines(decls, carried, "needful")
	if len(lines) != 1 || !strings.Contains(lines[0], "the pack needs fake-tool") || !strings.Contains(lines[0], "yardr pack install needful") {
		t.Fatalf("lines = %v, want one naming the install command", lines)
	}
	if err := os.WriteFile(filepath.Join(bin, "fake-tool"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if lines := missingNeedLines(decls, carried, "needful"); len(lines) != 0 {
		t.Fatalf("lines with fake-tool present = %v, want none", lines)
	}
}
