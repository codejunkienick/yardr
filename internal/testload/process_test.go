package testload

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// needPS skips a test that asks ps for what it checks Running against.
func needPS(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("no ps in PATH")
	}
}

// statOf is the STAT word ps gives pid, empty where it lists none.
func statOf(pid int) string {
	out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	return strings.TrimSpace(string(out))
}

// until waits for cond, for a fuse sized to the machine like the suites'
// others.
func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(Scale(5 * time.Second)); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen", what)
		}
	}
}

// A killed process nobody has waited for is a zombie, and Running counts it as
// dead whatever reaps on the machine the suite runs on. A STAT word with a Z in
// it names one, and so does none listed at all; and a real zombie is a child of
// the test that is killed and not waited for until the test ends, which kill -0
// still answers for.
func TestRunningCountsAZombieAsDead(t *testing.T) {
	for _, c := range []struct {
		stat string
		want bool
	}{
		{"Z", true},
		{"Z+", true},
		{"Ss", false},
		{"R", false},
		{"", true}, // ps lists nothing: dead between the checks
	} {
		if got := Zombie(c.stat); got != c.want {
			t.Errorf("Zombie(%q) = %v, want %v", c.stat, got, c.want)
		}
	}
	needPS(t)
	if Running(0) || Running(-1) {
		t.Error("a pid that names no process, or a group, is running")
	}
	if !Running(os.Getpid()) {
		t.Fatal("the test's own process is not running")
	}
	sleeper := exec.Command("sleep", "300")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	pid := sleeper.Process.Pid
	t.Cleanup(func() {
		_ = sleeper.Process.Kill()
		_ = sleeper.Wait()
	})
	if !Running(pid) {
		t.Fatalf("a sleeping child (pid %d) is not running", pid)
	}
	if err := sleeper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	until(t, "a killed child that nobody waited for to count as dead", func() bool { return !Running(pid) })
	if syscall.Kill(pid, 0) != nil {
		t.Errorf("kill -0 no longer answers for the child (pid %d): it was reaped, so the case proves nothing", pid)
	}
}

// GroupRunning is Running's question for a group: it is running while any of
// its members is, whichever is its leader, and a group of zombies is not. The
// leader is a child of the test that is killed and not waited for, so that it is
// a zombie on any machine, and the sleep it started, orphaned by its death, is
// the member that runs.
func TestGroupRunningCountsAGroupOfZombiesAsGone(t *testing.T) {
	needPS(t)
	for _, pgid := range []int{-5, 0, 1} {
		if GroupRunning(pgid) {
			t.Errorf("GroupRunning(%d): a group id that is no group's is running", pgid)
		}
	}
	leader := exec.Command("sh", "-c", "sleep 300 & echo $!; wait")
	leader.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := leader.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := leader.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := leader.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		_ = leader.Wait()
	})
	if _, err := bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatalf("the leader did not start its sleep: %v", err)
	}
	if !GroupRunning(pgid) {
		t.Fatalf("a group of a leader and its sleep (group %d) is not running", pgid)
	}
	if err := leader.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	until(t, "the leader to be a zombie", func() bool { return Zombie(statOf(pgid)) })
	if !GroupRunning(pgid) {
		t.Errorf("a group whose leader is a zombie, with its sleep still running (group %d), is not running", pgid)
	}
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	until(t, "a group of zombies nobody waited for to count as gone", func() bool { return !GroupRunning(pgid) })
	if stat := statOf(pgid); !strings.Contains(stat, "Z") {
		t.Errorf("the leader (pid %d) is %q, not a zombie: it was reaped, so the case proves nothing", pgid, stat)
	}
}
