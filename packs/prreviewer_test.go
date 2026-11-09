package packs_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// indentedBlocks are the code blocks of a role as its Markdown gives them:
// each run of lines indented by four spaces or more, dedented by the indent
// of the run's first line. A blank line ends a run.
func indentedBlocks(body string) []string {
	var blocks, cur []string
	indent := 0
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n")+"\n")
			cur = nil
		}
	}
	for _, line := range strings.Split(body, "\n") {
		n := len(line) - len(strings.TrimLeft(line, " "))
		if n < 4 || strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if cur == nil {
			indent = n
		}
		cur = append(cur, line[min(n, indent):])
	}
	flush()
	return blocks
}

// A group bound before the read-only preset gained its private temporary
// directory has a sandbox in which its reviewer's shell can make no file. zsh
// makes one for every here-document, at TMPPREFIX and not at TMPDIR, so the
// role's first form for a note, a quoted heredoc, is refused there. The same
// refusal inside the $( ) of the review's update does not stop it: the update
// runs with an empty review (yd-yjpa: a review went unstored for this
// reason). The role gives a second form that makes no file. This test runs
// the role's own blocks, as they stand, in a zsh whose TMPPREFIX cannot be
// written. Its heredocs are refused, and its pipe and its single-quoted
// argument hand yardr the note and the review as written, backticks and all.
//
// yardr and git are functions of the shell the command runs in. That is the
// cheapest stand-in that sees the command's own arguments and stdin.
func TestPRReviewerStoresWithoutATemporaryFile(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed, and its here-document is what the role's second form stands in for")
	}
	var blocks []string
	for _, b := range indentedBlocks(packFiles(t)["contributors/roles/pr-reviewer.md"]) {
		blocks = append(blocks, strings.ReplaceAll(b, "<id>", "yd-test"))
	}
	pick := func(markers ...string) string {
		t.Helper()
		var found []string
		for _, b := range blocks {
			if !slices.ContainsFunc(markers, func(m string) bool { return !strings.Contains(b, m) }) {
				found = append(found, b)
			}
		}
		if len(found) != 1 {
			t.Fatalf("the role has %d code blocks with %q, want 1", len(found), markers)
		}
		return found[0]
	}
	noteHeredoc := pick("yardr bead note yd-test - <<'NOTE'")
	reviewHeredoc := pick("--meta review=\"$(cat <<'EOF'")
	notePipe := pick("printf '%s\\n'", "yardr bead note yd-test -")
	reviewQuoted := pick("--meta review='")

	const sha = "0123456789abcdef0123456789abcdef01234567"
	const prelude = `yardr() { printf '%s\0' "$@" >"$REC.args"; cat >"$REC.stdin"; }
git() { [ "$*" = "rev-parse HEAD" ] && echo ` + sha + `; }
`
	dir := t.TempDir()
	gone := filepath.Join(dir, "gone") // never made
	runs := 0
	// run gives the block to zsh and says what yardr was run with: its
	// arguments and its stdin, or ran false for a block that never got as far.
	run := func(block string) (args []string, stdin, out string, ran bool, err error) {
		runs++
		rec := filepath.Join(dir, "rec"+string(rune('0'+runs)))
		cmd := exec.Command(zsh, "-c", prelude+block)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "TMPDIR=" + gone, "TMPPREFIX=" + filepath.Join(gone, "zsh"), "REC=" + rec}
		o, err := cmd.CombinedOutput()
		raw, readErr := os.ReadFile(rec + ".args")
		if readErr != nil {
			return nil, "", string(o), false, err
		}
		in, _ := os.ReadFile(rec + ".stdin")
		return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00"), string(in), string(o), true, err
	}

	// The premise: the heredocs are refused in this shell, and the refusal
	// of the review's lets its update run with an empty review. Where this
	// zsh makes the file somewhere else, there is nothing for the second
	// form to stand in for.
	if _, _, out, ran, err := run(noteHeredoc); err == nil || ran || !strings.Contains(out, "here document") {
		t.Skipf("this zsh does not refuse the role's heredoc for want of a temporary file (ran %v, err %v): %s", ran, err, out)
	}
	args, _, out, ran, err := run(reviewHeredoc)
	if err != nil || !ran || len(args) != 7 || args[6] != "review=" {
		t.Errorf("a refused here-document in the review's $( ) leaves the update to run with an empty review, as the role says: ran %v, args %q, err %v: %s", ran, args, err, out)
	}

	args, stdin, out, ran, err := run(notePipe)
	if err != nil || !ran {
		t.Fatalf("the pipe form of the note did not run: err %v: %s", err, out)
	}
	if !slices.Equal(args, []string{"bead", "note", "yd-test", "-"}) {
		t.Errorf("the pipe form of the note ran yardr with %q", args)
	}
	// The backtick span arrived as text, not as a command, and the empty
	// argument made the blank line.
	if !strings.Contains(stdin, "`code`") || !strings.Contains(stdin, "\n\n") || !strings.HasSuffix(stdin, "\n") {
		t.Errorf("the pipe form of the note gave yardr %q, want its paragraphs with a blank line between and `code` as it was written", stdin)
	}

	args, _, out, ran, err = run(reviewQuoted)
	if err != nil || !ran {
		t.Fatalf("the single-quoted form of the review did not run: err %v: %s", err, out)
	}
	if len(args) != 7 || !slices.Equal(args[:5], []string{"bead", "update", "yd-test", "--meta", "reviewed_head=" + sha}) || args[5] != "--meta" || !strings.HasPrefix(args[6], "review=") {
		t.Fatalf("the single-quoted form of the review ran yardr with %q", args)
	}
	var review struct {
		Outcome  string `json:"outcome"`
		Body     string `json:"body"`
		Comments []any  `json:"comments"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(args[6], "review=")), &review); err != nil {
		t.Fatalf("the review the single-quoted form gives yardr is not JSON: %v: %s", err, args[6])
	}
	if review.Outcome != "done" || review.Body == "" || review.Comments == nil || len(review.Comments) != 0 {
		t.Errorf("the review the single-quoted form gives yardr is %+v, want outcome done, a body and no comments", review)
	}
}
