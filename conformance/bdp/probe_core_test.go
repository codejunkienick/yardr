// Tests for conformance/bdp/probe-core-test.sh, which drives probe-core.sh
// through the paths a healthy server never shows: a kit that fails, that
// crashes, that has nothing to read, a pnpm that refuses. It builds yardr and
// seeds and serves the 54-bead yard once for each of its eight scenarios, and
// needs bash, go, python3, curl, jq and node. CI's conformance job runs it
// beside probe-core.sh itself, which needs BDP's checkout and the network and
// stays CI's. The gate runs it for a change under conformance/, which .yardr/test
// marks with YARDR_CONFORMANCE. Without that run, a change to the seed or the
// verdict would pass the gate, land, and turn main's CI red. Every other run
// skips it, plain go test ./... among them, because CI's plain jobs have no
// node.
package bdp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tools are what the script's header says it needs, each with how to get it:
// Homebrew on macOS, apt on Debian and the tool's own install page, in the form
// of the gate's shellcheck line. Go's has no apt, because a distribution's
// package lags the version go.mod asks for. Node's says 18 or newer, the first
// release with the global fetch that the script's stand-in kit calls.
var tools = []struct{ name, install string }{
	{"bash", "brew install bash, sudo apt-get install -y bash, or see https://www.gnu.org/software/bash/"},
	{"go", "brew install go, or see https://go.dev/doc/install"},
	{"python3", "brew install python, sudo apt-get install -y python3, or see https://www.python.org/downloads/"},
	{"curl", "brew install curl, sudo apt-get install -y curl, or see https://curl.se/download.html"},
	{"jq", "brew install jq, sudo apt-get install -y jq, or see https://jqlang.org/download/"},
	{"node", "brew install node, sudo apt-get install -y nodejs (18 or newer), or see https://nodejs.org/en/download"},
}

// TestProbeCoreFailurePaths fails with the script's output when the script
// exits non-zero. Each scenario prints the status it got beside the one it
// wanted.
func TestProbeCoreFailurePaths(t *testing.T) {
	if os.Getenv("YARDR_CONFORMANCE") == "" {
		t.Skip("probe-core-test.sh runs from the gate, for a change under conformance/ (.yardr/test sets YARDR_CONFORMANCE=1), and from CI's conformance job")
	}
	// A machine without a tool fails and says how to get it, as the gate does
	// for shellcheck. A skip would let a change to the seed through to CI, and
	// stopping that is what this test is for.
	var missing []string
	for _, tool := range tools {
		if _, err := exec.LookPath(tool.name); err != nil {
			missing = append(missing, tool.name+" is not installed: "+tool.install)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("probe-core-test.sh needs tools this machine lacks, and the gate runs it for a change under conformance/:\n%s", strings.Join(missing, "\n"))
	}
	// Go's test cache keys on the files the test itself opens, and the script
	// reads this directory, not the test. Without this read, an edit to the
	// script would not invalidate a cached pass, and the next run would get the
	// result from before the edit.
	if _, err := os.ReadDir("."); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(root, "conformance/bdp/probe-core-test.sh"))
	cmd.Dir = root
	// The script takes the repository from ROOT, else from the directory it runs
	// in. Setting ROOT here keeps one from the caller's environment from
	// pointing it elsewhere.
	cmd.Env = append(os.Environ(), "ROOT="+root)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe-core-test.sh: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
