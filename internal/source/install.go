package source

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"yardr.dev/yardr/internal/config"
)

// InstallTimeout bounds one run of a need's install: a package
// manager fetching one CLI on a slow mirror takes minutes, where a
// call's Timeout is seconds. A variable so a test need not wait it
// out; a longer install is the operator's by hand.
var InstallTimeout = 15 * time.Minute

// MaxInstallOutput bounds what an install may print on stdout: an
// install streams progress where a command's answer is one object,
// so the bound is a memory bound, not an answer's shape.
const MaxInstallOutput = 8 << 20

// NeedPath is the path a need's program is found at on this machine:
// the path its env variable names, where that is set and stands as a
// program to run, else the program on PATH. A leading ~/ in the env
// path is the user's home, as the mint that reads the same variable
// expands it.
func NeedPath(n Need) (string, bool) {
	if n.Env != "" {
		if p := strings.TrimSpace(os.Getenv(n.Env)); p != "" {
			if p == "~" {
				if home, err := os.UserHomeDir(); err == nil {
					p = home
				}
			} else if strings.HasPrefix(p, "~/") {
				if home, err := os.UserHomeDir(); err == nil {
					p = filepath.Join(home, p[2:])
				}
			}
			if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
				return p, true
			}
		}
	}
	p, err := exec.LookPath(n.Program)
	if err != nil {
		return "", false
	}
	return p, true
}

// NeedPresent reports whether a need's program is on this machine,
// as NeedPath finds it.
func NeedPresent(n Need) bool {
	_, ok := NeedPath(n)
	return ok
}

// MissingNeeds are a declaration's needs whose program is not on
// this machine, in file order.
func (d *Declaration) MissingNeeds() []Need {
	var out []Need
	for _, n := range d.Needs {
		if !NeedPresent(n) {
			out = append(out, n)
		}
	}
	return out
}

// WriteInstallProgram stands an install's bytes as a program to run:
// a relative install is a file beside its declaration, carried as
// text, so what runs is a copy of exactly those bytes, mode 0700 as
// every command copy stands. The caller removes the file.
func WriteInstallProgram(data []byte) (string, error) {
	f, err := os.CreateTemp("", "yardr-install-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Chmod(path, CopyMode); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// InstallArgv is a need's install as it is run for data, the bytes
// of its install file where its program is a relative one: the copy
// WriteInstallProgram stands, with the install's own arguments after
// it. An absolute program is run as it stands, and data is then
// unused. The caller removes a copy it stood.
func InstallArgv(n Need, data []byte) ([]string, func(), error) {
	none := func() {}
	if !strings.HasPrefix(n.Install[0], "/") {
		path, err := WriteInstallProgram(data)
		if err != nil {
			return nil, none, fmt.Errorf("install of %s: %w", n.Program, err)
		}
		argv := append([]string{path}, n.Install[1:]...)
		return argv, func() { _ = os.Remove(path) }, nil
	}
	return append([]string{}, n.Install...), none, nil
}

// InstallNeed runs a need's install, argv with its program already
// stood as InstallArgv stands it, and says whether the program is
// there after. The run is argv without a shell through
// config.Command, as every program the yard runs: stdin closed, in
// no pack's directory, under the declaration's command environment
// with no credential, for InstallTimeout at most. Success is an
// exit of 0 with the program present after, held again here, so an
// install that answers 0 and puts nothing fails the carry.
func InstallNeed(ctx context.Context, d *Declaration, n Need, argv []string) error {
	what := "install of " + n.Program + " for source " + d.Name + " (" + strings.Join(argv, " ") + ")"
	_, _, err := config.Command{What: what, Argv: argv, Env: d.CommandEnv(""), Timeout: InstallTimeout, Max: MaxInstallOutput}.OutputWithStderr(ctx)
	if err != nil {
		return err
	}
	if !NeedPresent(n) {
		return fmt.Errorf("%s: it answered 0 and %s is still not on this machine: it installed nothing the yard finds", what, n.Program)
	}
	return nil
}
