package scripttest

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	code := m.Run()
	Remove()
	os.Exit(code)
}

// A stand-in is to its caller the script a file at its path would be: it
// has the arguments, the standard input, the $0 and the exit status of one,
// and a second Put at the path gives it a new body.
func TestStandInRunsAsAScriptAtItsPath(t *testing.T) {
	dir := t.TempDir()
	herdr := filepath.Join(dir, "herdr")
	Write(t, herdr, "#!/bin/sh\necho \"$0: $# args, second $2\" >\"$0.calls\"\ncat\necho gone >&2\nexit 3\n")
	run := func(path string, args ...string) (string, string, int) {
		t.Helper()
		cmd := exec.Command(path, args...)
		cmd.Stdin = strings.NewReader("from stdin\n")
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			t.Fatal(err)
		}
		return string(out), stderr.String(), cmd.ProcessState.ExitCode()
	}
	out, stderr, code := run(herdr, "one", "two words")
	if out != "from stdin\n" || stderr != "gone\n" || code != 3 {
		t.Fatalf("stdout %q, stderr %q, exit %d", out, stderr, code)
	}
	if calls, _ := os.ReadFile(herdr + ".calls"); string(calls) != herdr+": 2 args, second two words\n" {
		t.Fatalf("what the script saw: %q", calls)
	}
	// By its name on PATH, as a test's fake git is found.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, _, _ := run("herdr"); out != "from stdin\n" {
		t.Fatalf("found on PATH: %q", out)
	}
	Write(t, herdr, "echo again\n")
	if out, _, code := run(herdr); out != "again\n" || code != 0 {
		t.Fatalf("with a new body: %q, exit %d", out, code)
	}
	if to, err := os.Readlink(herdr); err != nil || filepath.Dir(to) == dir {
		t.Fatalf("the stand-in is no link to the launcher: %q %v", to, err)
	}
}

// A stand-in starts while the files beside its link are open for writing. An
// executable file of a test's own does not on Linux, for as long as any process
// holds it open for writing (text file busy), and a fork made beside a test
// that writes one holds its descriptor until the fork execs: a git written for
// a case then did not start, and the check that ran it answered false. The
// launcher is written once and run before any stand-in is, and a body is only
// read by the shell. Holding the body open here is that fork's hold, kept for
// the whole run instead of a moment. Where a system starts a file of its own
// that is open for writing too, the case tells nothing apart, and says so.
func TestStandInStartsWhileItsBodyIsOpenForWriting(t *testing.T) {
	dir := t.TempDir()
	hold := func(path string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
	}
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("#!/bin/sh\necho plain\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	hold(plain)
	refused := exec.Command(plain).Run() != nil
	git := filepath.Join(dir, "git")
	Write(t, git, "#!/bin/sh\necho stood-in\n")
	hold(git + ".sh")
	if out, err := exec.Command(git).Output(); err != nil || string(out) != "stood-in\n" {
		t.Errorf("a stand-in whose body is open for writing: %q, %v", out, err)
	}
	if !refused {
		t.Log("this system starts a file that is open for writing, so a stand-in is no different from one here")
	}
}

// The launcher is written with syscall.ForkLock held for reading, and only for
// the write: a fork begun beside it would carry its descriptor until the child
// execs, and the launcher's first run, which follows, would then find the file
// busy on Linux (yardr-wqbv). No fork has been seen to do it, so the case looks
// at the lock instead. The write is aimed at a FIFO, whose open waits for a
// reader, which holds a write under way for as long as the test likes where a
// regular file's would be over at once. A fork takes the lock for writing, so
// a lock that cannot be taken for writing is a write no fork can begin beside.
func TestLauncherIsWrittenUnderTheForkLock(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "sh")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("this system makes no FIFO: %v", err)
	}
	written := make(chan error, 1)
	go func() { written <- writeLauncher(fifo, "/bin/sh") }()
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(time.Millisecond) {
		if !syscall.ForkLock.TryLock() {
			break
		}
		syscall.ForkLock.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("a fork could begin while the launcher was being written: its child would carry the open file")
		}
	}
	// A reader lets the open go on, and the write ends.
	r, err := os.Open(fifo)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if want := "#!/bin/sh\n. \"$0.sh\"\n"; string(got) != want {
		t.Errorf("the launcher written is %q, want %q", got, want)
	}
	// The first run follows the write and starts a process, which takes the
	// lock for writing: it must have been let go.
	if !syscall.ForkLock.TryLock() {
		t.Fatal("the fork lock is held after the launcher was written: the first run could not start")
	}
	syscall.ForkLock.Unlock()
}

// Relink makes stand-ins of the executable scripts under a directory, each
// under its own shell, over a file that was there, and leaves alone what is
// no script of those shells and what cannot be run.
func TestRelink(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]struct {
		body string
		mode os.FileMode
	}{
		"plain":      {"#!/bin/sh\necho \"plain $1\"\n", 0o700},
		"sub/bashed": {"#!/bin/bash\na=(in bash)\necho \"${a[1]} $1\"\n", 0o700},
		"other":      {"#!/usr/bin/env sh\necho \"other $1\"\n", 0o700},
		"data":       {"#!/bin/sh\necho data\n", 0o600},
	}
	for name, f := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(f.body), f.mode); err != nil {
			t.Fatal(err)
		}
	}
	Relink(t, dir)
	for name, want := range map[string]string{"plain": "plain x\n", "sub/bashed": "bash x\n", "other": "other x\n"} {
		out, err := exec.Command(filepath.Join(dir, name), "x").CombinedOutput()
		if err != nil || string(out) != want {
			t.Errorf("%s: %q %v, want %q", name, out, err, want)
		}
	}
	for name, linked := range map[string]bool{"plain": true, "sub/bashed": true, "other": false, "data": false} {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil || (info.Mode()&os.ModeSymlink != 0) != linked {
			t.Errorf("%s: mode %v %v, want a link: %v", name, info.Mode(), err, linked)
		}
	}
	// A second time changes nothing: the links are no files of their own.
	Relink(t, dir)
	if out, err := exec.Command(filepath.Join(dir, "plain"), "y").CombinedOutput(); err != nil || string(out) != "plain y\n" {
		t.Errorf("after a second Relink: %q %v", out, err)
	}
}
