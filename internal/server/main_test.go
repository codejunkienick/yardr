package server

import (
	"os"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/scripttest"
	"yardr.dev/yardr/internal/testload"
)

func TestMain(m *testing.M) {
	// The package's tests and the code under them make hundreds of git calls
	// (the setup cards of an applied directory among them), and on macOS each
	// is two execs, the system's shim and the git it runs, so the real git goes
	// first on PATH (testload.PreferRealGit). What the pane or hook that runs
	// the tests sets for git goes first: GIT_EXEC_PATH can make the shim report
	// a directory unrelated to the git it runs.
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "GIT_") {
			os.Unsetenv(name)
		}
	}
	testload.PreferRealGit()
	code := m.Run()
	scripttest.Remove()
	os.Exit(code)
}
