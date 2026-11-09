package diskkeep

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/scripttest"
)

// The table as macOS prints it (the full path of the executable, which can
// hold spaces) and as procps does (the bare name), a go process among the
// others. They are one rule: the command is go by its basename.
const (
	macTable = `    1 Thu Nov  5 05:57:29 2026     /sbin/launchd
  325 Thu Nov  5 05:58:00 2026     /usr/libexec/logd
  329 Thu Nov  5 05:58:00 2026     /System/Library/CoreServices/Software Update.app/Contents/Resources/suhelperd
 4711 Mon Nov  9 06:10:03 2026     /opt/homebrew/bin/go
 4800 Mon Nov  9 06:22:41 2026     /opt/homebrew/bin/gofmt
`
	linuxTable = `      1 Thu Nov  5 09:05:24 2026 systemd
    133 Thu Nov  5 09:05:24 2026 systemd-journal
 857012 Mon Nov  9 06:10:03 2026 go
 857020 Mon Nov  9 06:22:41 2026 gofmt
 857021 Mon Nov  9 06:22:41 2026 Web Content
`
)

// Which processes the probe counts as go, and when the oldest of them started.
func TestParseGoProcs(t *testing.T) {
	at := func(s string) time.Time {
		t.Helper()
		got, err := time.Parse("2006-01-02 15:04:05", s)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	for _, c := range []struct {
		name    string
		table   string
		running bool
		oldest  string // when the oldest go process started, UTC
		wantErr string // a word of the error
	}{
		{name: "macOS", table: macTable, running: true, oldest: "2026-11-09 06:10:03"},
		{name: "Linux", table: linuxTable, running: true, oldest: "2026-11-09 06:10:03"},
		{name: "the oldest of several, in any order", running: true, oldest: "2026-11-09 05:59:59", table: `
 9 Mon Nov  9 06:10:03 2026 go
 8 Mon Nov  9 05:59:59 2026 /usr/local/go/bin/go
 7 Mon Nov  9 06:30:00 2026 /Users/ada/sdk/go1.27.3/bin/go
`},
		{name: "a path with spaces is go by its basename", running: true, oldest: "2026-11-09 06:10:03",
			table: " 9 Mon Nov  9 06:10:03 2026 /Applications/Go Tools.app/Contents/MacOS/go\n"},
		{name: "a two-digit day older than a one-digit one", running: true, oldest: "2026-10-28 23:59:59",
			table: "1 Thu Nov  5 00:00:00 2026 go\n2 Wed Oct 28 23:59:59 2026 go\n"},
		{name: "what is only like go", table: `
 1 Mon Nov  9 06:10:03 2026 /usr/local/go/bin/gofmt
 2 Mon Nov  9 06:10:03 2026 go1.22.1
 3 Mon Nov  9 06:10:03 2026 /usr/bin/django-admin
 4 Mon Nov  9 06:10:03 2026 /Users/ada/go/bin/golangci-lint
 5 Mon Nov  9 06:10:03 2026 /tmp/go-build123/b001/diskkeep.test
 6 Mon Nov  9 06:10:03 2026 my go
 7 Mon Nov  9 06:10:03 2026 (go)
 8 Mon Nov  9 06:10:03 2026
`},
		{name: "the start of a process that is not go is not read", table: " 1 Mon Xxx  9 06:10:03 2026 /sbin/launchd\n"},
		{name: "no ps", table: "", wantErr: "no process"},
		{name: "blank lines only", table: "\n   \n", wantErr: "no process"},
		{name: "no process id", table: "  x Mon Nov  9 06:10:03 2026 go\n", wantErr: "no process"},
		{name: "fewer fields than a process line", table: " 1 Mon Nov  9\n", wantErr: "no process"},
		{name: "a broken line among good ones", table: macTable + "kernel_task\n", wantErr: "no process"},
		{name: "an unknown month", table: " 1 Mon Xxx  9 06:10:03 2026 go\n", wantErr: "cannot be read"},
		{name: "an hour that is no hour", table: " 1 Mon Nov  9 25:10:03 2026 go\n", wantErr: "cannot be read"},
		{name: "a bad go among good ones", table: macTable + " 1 Mon Nov 99 06:10:03 2026 go\n", wantErr: "cannot be read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseGoProcs(c.table)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("parseGoProcs = %+v, %v; want an error with %q", got, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.running != c.running || (c.running && !got.oldest.Equal(at(c.oldest))) {
				t.Errorf("running = %v, oldest = %v; want %v, %s", got.running, got.oldest, c.running, c.oldest)
			}
		})
	}
}

// A cache is held by a go process that started before the record of its
// rotation, at that second included: ps cuts a start off at the second, so a
// process that started in the second of the record may have started before it.
// One that started after, and a table with no go process, hold nothing.
func TestGoProcsHold(t *testing.T) {
	rotated := time.Date(2026, 11, 9, 6, 21, 15, 400_000_000, time.UTC)
	for _, c := range []struct {
		name  string
		table string
		held  bool
	}{
		{"a go process started before it, macOS", " 1 Mon Nov  9 06:10:03 2026 /opt/homebrew/bin/go\n", true},
		{"a go process started before it, Linux", " 1 Mon Nov  9 06:10:03 2026 go\n", true},
		{"in the second of the record", " 1 Mon Nov  9 06:21:15 2026 go\n", true},
		{"a second after the record's", " 1 Mon Nov  9 06:21:16 2026 go\n", false},
		{"after it, macOS", " 1 Mon Nov  9 06:30:00 2026 /opt/homebrew/bin/go\n", false},
		{"after it, Linux", " 1 Mon Nov  9 06:30:00 2026 go\n", false},
		{"one before it is enough", " 1 Mon Nov  9 06:30:00 2026 go\n 2 Mon Nov  9 06:00:00 2026 go\n", true},
		{"no go process", " 1 Mon Nov  9 06:10:03 2026 /sbin/launchd\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			procs, err := parseGoProcs(c.table)
			if err != nil {
				t.Fatal(err)
			}
			if got := procs.holds(rotated); got != c.held {
				t.Errorf("holds = %v, want %v", got, c.held)
			}
		})
	}
}

// psTable runs ps as it must be run to be read: the columns, and the locale and
// zone the parser assumes whatever the caller's are. The stand-in prints what it
// was given and a table, so the whole path from the exec to the answer is
// walked; a ps that hangs ends at the deadline, and a failing one is an error.
func TestPsTableRunsPsAsAsked(t *testing.T) {
	dir := t.TempDir()
	seen := filepath.Join(dir, "seen")
	scripttest.Write(t, filepath.Join(dir, "ps"), `printf '%s\n' "$*" "$LC_ALL" "$TZ" >"`+seen+`"
cat <<'EOF'
`+macTable+`EOF
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("TZ", "Asia/Tokyo")

	procs, err := readGoProcs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 9, 6, 10, 3, 0, time.UTC); !procs.running || !procs.oldest.Equal(want) {
		t.Errorf("procs = %+v, want the go process of %v", procs, want)
	}
	got, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	if want := "-axo pid=,lstart=,comm=\nC\nUTC\n"; string(got) != want {
		t.Errorf("ps was run with %q (arguments, LC_ALL, TZ), want %q", got, want)
	}

	// A ps that fails, and one that prints nothing, hold every cache.
	scripttest.Write(t, filepath.Join(dir, "ps"), "echo 'ps: bad -o argument' >&2\nexit 1\n")
	if _, err := readGoProcs(context.Background()); err == nil || !strings.Contains(err.Error(), "ps") {
		t.Errorf("a failing ps: err = %v", err)
	}
	scripttest.Write(t, filepath.Join(dir, "ps"), "exit 0\n")
	if _, err := readGoProcs(context.Background()); err == nil {
		t.Errorf("a ps that printed nothing was taken for a table with no go process")
	}

	// One that does not answer is stopped at the deadline, not waited for.
	scripttest.Write(t, filepath.Join(dir, "ps"), "exec sleep 30\n")
	save := psWait
	psWait = 50 * time.Millisecond
	t.Cleanup(func() { psWait = save })
	start := time.Now()
	_, err = readGoProcs(context.Background())
	if err == nil || time.Since(start) > 10*time.Second {
		t.Errorf("a ps that hangs: err = %v after %v, want an error at the deadline", err, time.Since(start))
	}
}

// The machine's own ps is read by the parser: this process is in the table, its
// start is in UTC and recent (a ps that ignored TZ, in a zone that is not UTC,
// is off by hours), and its command is this test binary by its basename. So a
// format the stand-ins above got wrong would fail here, on whichever OS runs
// the tests.
func TestPsTableOfThisMachine(t *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("no ps on PATH")
	}
	table, err := psTable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseGoProcs(string(table)); err != nil {
		t.Fatalf("the machine's process table does not parse: %v", err)
	}
	self := strconv.Itoa(os.Getpid())
	for _, line := range strings.Split(string(table), "\n") {
		f := strings.Fields(line)
		if len(f) < 7 || f[0] != self {
			continue
		}
		started, err := time.Parse(startLayout, strings.Join(f[1:6], " "))
		if err != nil {
			t.Fatal(err)
		}
		if ago := time.Since(started); ago < -5*time.Second || ago > time.Hour {
			t.Errorf("this process started %v ago by ps's account: the table is not in UTC", ago)
		}
		// Linux cuts a command at 15 bytes.
		want := filepath.Base(os.Args[0])
		if got := filepath.Base(strings.Join(f[6:], " ")); !strings.HasPrefix(want, got) {
			t.Errorf("this process's command is %q by its basename, want %q", got, want)
		}
		return
	}
	t.Fatalf("this process (%s) is not in the table:\n%s", self, table)
}
