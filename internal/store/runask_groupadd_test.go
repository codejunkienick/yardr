package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// groupAddPromptText is a prompt of builder size: over fifty lines, which a
// card must show whole.
func groupAddPromptText() string {
	var b strings.Builder
	b.WriteString("You are a builder.\n\n")
	for i := 0; i < 48; i++ {
		b.WriteString("Build the bead in its worktree and note what you changed.\n")
	}
	return b.String()
}

// groupAddAsk is a group add ask carrying role as the text of its prompt
// file, "" for a group that names none.
func groupAddAsk(role string, argv ...string) Ask {
	return Ask{Question: "Add the group?", To: "mckean", Context: []string{"the yard needs a group for it"},
		Run: &AskRun{Argv: argv, Role: role}}
}

// groupAddFile writes a prompt under the yard's home and answers the path
// as given and as the group will save it, with its symlinks resolved.
func groupAddFile(t *testing.T, y *askYard, name, text string) (given, saved string) {
	t.Helper()
	given = filepath.Join(y.Store.home, "roles", name+".md")
	if err := os.MkdirAll(filepath.Dir(given), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(given, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	saved, err := filepath.EvalSymlinks(given)
	if err != nil {
		t.Fatal(err)
	}
	return given, saved
}

// yardPromptFile is the file the approval of a prompt writes the text of
// name's prompt to: the yard's roles directory, by the name and the SHA-256
// of the text. It is built here from the home as its symlinks resolve it, so
// a test does not take the path from the code it holds.
func yardPromptFile(t *testing.T, y *askYard, name, text string) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(y.Store.home)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(text))
	return filepath.Join(home, "roles", name+"."+hex.EncodeToString(sum[:])+".md")
}

// yardPromptHolds holds a file the approval wrote to what it promises: a
// regular file, read-only, with exactly the text.
func yardPromptHolds(t *testing.T, path, text string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Errorf("the yard's prompt file: %v", err)
		return
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o400 {
		t.Errorf("%s is %v, want a regular file of mode 0400", path, info.Mode())
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != text {
		t.Errorf("%s holds %q, %v; want %q", path, got, err, text)
	}
}

// A group add ask from a crew pane shows a weighty code card with the
// command whole, and approving it creates the group exactly as the
// terminal's group add would.
func TestRunAskGroupAddExecApproves(t *testing.T) {
	t.Parallel()
	y, _ := runYard(t)
	dir := filepath.Join(y.Store.home, "post")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The flags in the order a terminal takes them and the yard stores them.
	seq := y.ask(t, groupAddAsk("", "group", "add", "--set", "dir="+dir, "yardr-post", "--set=cmd=/srv/bin/post-runner", "--limit=2", "--runner=exec"), "yardmaster")
	r := y.asked(t, seq)
	if r.Ask.Run == nil {
		t.Fatal("the ask holds no run")
	}
	want := []string{"group", "add", "yardr-post", "--runner", "exec", "--limit", "2", "--set", "cmd=/srv/bin/post-runner", "--set", "dir=" + dir}
	if !reflect.DeepEqual(r.Ask.Run.Argv, want) {
		t.Errorf("stored command = %q, want %q", r.Ask.Run.Argv, want)
	}
	if len(r.Ask.Weight) != 1 || r.Ask.Weight[0].Class != WeightCode {
		t.Fatalf("weight = %+v, want one code line", r.Ask.Weight)
	}
	joined := strings.Join(r.Ask.Run.Does, "\n")
	for _, want := range []string{"adds the group yardr-post on the exec runner, with limit 2.", `runs sh -c with "/srv/bin/post-runner", as its program stands when the session starts.`, "The command runs in " + dir, "It sets no env_file"} {
		if !strings.Contains(joined, want) {
			t.Errorf("card does not say %q:\n%s", want, joined)
		}
	}
	// Its words are a command and paths of the yard's machine: a device that
	// is no operator's is told none of them.
	if !RunArgvHeld(r.Ask.Run.Argv) {
		t.Error("RunArgvHeld does not hold the words of a group add")
	}
	if r.Ask.Run.Description != "Add a new group without replacing one" {
		t.Errorf("description = %q, want the add-only card description", r.Ask.Run.Description)
	}
	if r.Ask.Run.Back != "yardr group rm yardr-post" || r.Ask.Run.BackBy != "the operator or the yardmaster" {
		t.Errorf("taken back by %q: %q", r.Ask.Run.BackBy, r.Ask.Run.Back)
	}
	// A weighty card approved without its person seeing the weight runs
	// nothing.
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "weighty") {
		t.Fatalf("unweighed approval = %v, want a weighty refusal", err)
	}
	if _, err := y.GetGroup(y.ctx, "yardr-post"); err == nil {
		t.Fatal("a refused approval made the group")
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Result != "group yardr-post (exec · limit 2)" {
		t.Errorf("result = %q", done.Result)
	}
	g, err := y.GetGroup(y.ctx, "yardr-post")
	if err != nil {
		t.Fatal(err)
	}
	if g.Runner != "exec" || g.Limit != 2 || !reflect.DeepEqual(g.Config, map[string]any{"cmd": "/srv/bin/post-runner", "dir": dir}) {
		t.Errorf("group = %+v", g)
	}
	// The group is as the terminal's group add saves it.
	typed := Group{Name: "typed", Runner: "exec", Limit: 2, Config: map[string]any{"cmd": "/srv/bin/post-runner", "dir": dir}}
	if err := y.PutGroup(y.ctx, typed, "mckean"); err != nil {
		t.Fatal(err)
	}
	if t2, err := y.GetGroup(y.ctx, "typed"); err != nil || t2.Runner != g.Runner || t2.Limit != g.Limit || !reflect.DeepEqual(t2.Config, g.Config) {
		t.Errorf("typed = %+v, %v; approved = %+v", t2, err, g)
	}
}

// A herdr group's card shows the prompt text the ask carries whole, its
// kind (the yard's default where it names none) and the arguments it starts
// the agent with, and approving it writes that text to a file of the yard's
// own and saves its path. The file the ask names is held to the text until
// then: a file that holds other text than the ask carried by then is
// refused, and the ask goes through once it holds the text again. After the
// approval it is no more than where the text was read from: a write to it
// reaches no session.
func TestRunAskGroupAddHerdrHoldsThePrompt(t *testing.T) {
	t.Parallel()
	y, _ := runYard(t)
	if err := y.SetSetting(y.ctx, DefaultKindSetting, "codex", "mckean"); err != nil {
		t.Fatal(err)
	}
	text := groupAddPromptText()
	// The asker's file stands outside roles/, which this yard does not have
	// yet (its roles came from a pack): the approval makes the directory.
	given := filepath.Join(y.Store.home, "notes", "builder.md")
	if err := os.MkdirAll(filepath.Dir(given), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(given, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	yardFile := yardPromptFile(t, y, "post-builders", text)
	seq := y.ask(t, groupAddAsk(text, "group", "add", "post-builders", "--runner", "herdr", "--set", "args=--dangerously-skip-permissions", "--set", "prompt="+given), "yardmaster")
	r := y.asked(t, seq)
	joined := strings.Join(r.Ask.Run.Does, "\n")
	for _, want := range []string{"adds the group post-builders on the herdr runner, with limit 1, the runner's default.",
		"It starts codex (Codex) agents, the yard's default_kind.", `It starts them with "--dangerously-skip-permissions".`,
		"It skips permissions: it runs without asking for permission.", "On approval the yard writes the text below to the file " + yardFile + ", read-only.", strings.TrimSuffix(text, "\n")} {
		if !strings.Contains(joined, want) {
			t.Errorf("card does not say %q:\n%s", want, joined)
		}
	}
	// Automatic review inside a sandbox is no skipping of permissions (design
	// yardr-m90n 3.5), so the flags check the card keeps for free args leaves
	// --approve-for-me out, as crew add's reading of the Strong mark does.
	review := y.ask(t, groupAddAsk("", "group", "add", "review-builders", "--runner", "herdr", "--set", "args=--approve-for-me"), "yardmaster")
	if reviewed := strings.Join(y.asked(t, review).Ask.Run.Does, "\n"); !strings.Contains(reviewed, `It starts them with "--approve-for-me".`) || strings.Contains(reviewed, "It skips permissions") {
		t.Errorf("a card for --approve-for-me:\n%s\nwant its flags and no skipped permissions", reviewed)
	}
	if got := r.Ask.Run.Seen["prompt "+yardFile]; len(got) != 64 {
		t.Errorf("card's prompt fingerprint = %q, want the SHA-256 of the text", got)
	}
	if r.Ask.Run.Role != text {
		t.Errorf("card's prompt text = %q, want the text the ask carried", r.Ask.Run.Role)
	}
	// A byte more or less is another prompt, though no line of the card
	// need change with it. The approval says so, and shows nothing of the
	// file.
	for _, changed := range []string{strings.TrimSuffix(text, "\n"), text + "\n", text[:len(text)-2] + "!\n"} {
		if err := os.WriteFile(given, []byte(changed), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "holds other text than the ask carries") || !strings.Contains(err.Error(), "Nothing ran") {
			t.Errorf("approval over a prompt ending %q = %v, want it refused", changed[len(changed)-3:], err)
		}
		if _, err := y.GetGroup(y.ctx, "post-builders"); err == nil {
			t.Fatal("a refused approval made the group")
		}
	}
	if err := os.WriteFile(given, []byte("You are another builder.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "holds other text than the ask carries") {
		t.Errorf("approval over another prompt = %v, want it refused", err)
	}
	// Nothing of the yard's is written until the approval.
	if _, err := os.Lstat(filepath.Dir(yardFile)); !os.IsNotExist(err) {
		t.Fatalf("the yard's roles directory after refused approvals: %v", err)
	}
	if err := os.WriteFile(given, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	g, err := y.GetGroup(y.ctx, "post-builders")
	if err != nil {
		t.Fatal(err)
	}
	if g.Runner != "herdr" || g.Limit != 1 || !reflect.DeepEqual(g.Config, map[string]any{"kind": "codex", "args": "--dangerously-skip-permissions", "prompt": yardFile}) {
		t.Errorf("group = %+v", g)
	}
	yardPromptHolds(t, yardFile, text)
	if info, err := os.Stat(filepath.Dir(yardFile)); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the roles directory the approval made = %v, %v, want mode 0700", info, err)
	}
	// The file the ask named is where the text was read from: a write to it
	// after the approval changes no session, which read the yard's own.
	if err := os.WriteFile(given, []byte("You are another builder.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if g, err := y.GetGroup(y.ctx, "post-builders"); err != nil || g.Config["prompt"] != yardFile {
		t.Errorf("group after a write to the named file = %+v, %v", g, err)
	}
	yardPromptHolds(t, yardFile, text)
	// A prompt that went away since is refused in the proposal's words, and
	// nothing is made.
	_, other := groupAddFile(t, y, "other", text)
	seq2 := y.ask(t, groupAddAsk(text, "group", "add", "other-builders", "--runner", "herdr", "--set", "kind=claude", "--set", "prompt="+other), "yardmaster")
	if err := os.Remove(other); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq2, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "prompt file") || !strings.Contains(err.Error(), "Nothing ran") {
		t.Errorf("approval over a prompt that is gone = %v, want it refused", err)
	}
	if _, err := y.GetGroup(y.ctx, "other-builders"); err == nil {
		t.Error("a refused approval made the group")
	}
}

// A name taken since the ask refuses the approval: this adds and never
// replaces, whoever took the name.
func TestRunAskGroupAddStaleOnATakenName(t *testing.T) {
	t.Parallel()
	y, _ := runYard(t)
	seq := y.ask(t, groupAddAsk("", "group", "add", "post", "--runner", "exec", "--set", "cmd=/srv/bin/post"), "yardmaster")
	if err := y.PutGroup(y.ctx, Group{Name: "post", Runner: "manual", Limit: 3}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil || !strings.Contains(err.Error(), "never replaces") || !strings.Contains(err.Error(), "Nothing ran") {
		t.Errorf("approval over a name taken since = %v, want it refused", err)
	}
	if g, err := y.GetGroup(y.ctx, "post"); err != nil || g.Runner != "manual" || g.Limit != 3 {
		t.Errorf("the group that took the name = %+v, %v, want it as it was", g, err)
	}
	// Declining adds nothing either.
	seq2 := y.ask(t, groupAddAsk("", "group", "add", "later", "--runner", "exec", "--set", "cmd=/srv/bin/later"), "yardmaster")
	y.answer(t, Answer{Ask: seq2, Decline: true, Words: "not now"}, "mckean")
	if _, err := y.GetGroup(y.ctx, "later"); err == nil {
		t.Fatal("a declined group add made the group")
	}
}

// Each setting this row leaves out is refused, in words that say where it
// goes instead. Without its parse each ask below would be stored.
func TestRunAskGroupAddRefusesLeftOutKeys(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		argv []string
		want string
	}{
		{"env_file", []string{"group", "add", "post", "--runner", "exec", "--set", "cmd=/srv/bin/post", "--set", "env_file=/srv/post.env"}, "yardr group set <name> env_file=<file>"},
		{"env_command", []string{"group", "add", "post", "--runner", "herdr", "--set", "env_command=printenv"}, "env is the operator's alone"},
		{"log", []string{"group", "add", "post", "--runner", "exec", "--set", "cmd=/srv/bin/post", "--set", "log=/srv/post.log"}, "yardr group set <name> log=<file>"},
		{"peer runner", []string{"group", "add", "post", "--runner", "peer", "--set", "peer=airy"}, "a peer group hands its beads to another yard"},
		{"peer key", []string{"group", "add", "post", "--runner", "herdr", "--set", "peer=airy"}, "a peer group's"},
		{"remote key", []string{"group", "add", "post", "--runner", "herdr", "--set", "remote=origin"}, "a peer group's"},
		{"tries key", []string{"group", "add", "post", "--runner", "herdr", "--set", "tries=3"}, "a peer group's"},
		{"another setting", []string{"group", "add", "post", "--runner", "herdr", "--set", "max_age=2h"}, "yardr group set <name> max_age=<value>"},
		{"model", []string{"group", "add", "post", "--runner", "herdr", "--set", "model=opus"}, "no setting of this card"},
		{"members", []string{"group", "add", "post", "--runner", "manual", "--members", "ada"}, "run it directly"},
		{"another flag", []string{"group", "add", "post", "--runner", "exec", "--actor", "mckean"}, "the operator's to type in a terminal"},
		{"a setting beside the name", []string{"group", "add", "post", "cmd=/srv/bin/post", "--runner", "exec"}, "is no group name"},
		{"two names", []string{"group", "add", "post", "other", "--runner", "exec"}, "one group, not post and other"},
		{"no name", []string{"group", "add", "--runner", "exec"}, "names no group"},
		{"no runner", []string{"group", "add", "post"}, "names no runner"},
		{"unknown runner", []string{"group", "add", "post", "--runner", "docker"}, "none of the runners a card adds"},
		{"runner twice", []string{"group", "add", "post", "--runner", "exec", "--runner", "herdr"}, "--runner once"},
		{"set twice", []string{"group", "add", "post", "--runner", "exec", "--set", "cmd=a", "--set", "cmd=b"}, "cmd is set twice"},
		{"limit zero", []string{"group", "add", "post", "--runner", "exec", "--limit", "0"}, "no whole number of 1 or more"},
		{"limit negative", []string{"group", "add", "post", "--runner", "exec", "--limit", "-1"}, "no whole number of 1 or more"},
		{"limit not a number", []string{"group", "add", "post", "--runner", "exec", "--limit", "many"}, "no whole number of 1 or more"},
		{"limit twice", []string{"group", "add", "post", "--runner", "exec", "--limit", "2", "--limit", "3"}, "--limit once"},
		{"flag without a value", []string{"group", "add", "post", "--runner"}, "--runner needs its value"},
		{"empty value", []string{"group", "add", "post", "--runner", "exec", "--set", "cmd="}, "cmd needs its value"},
		{"value that reads as JSON", []string{"group", "add", "post", "--runner", "exec", "--set", "cmd=true"}, "reads as JSON"},
		{"setting without a value", []string{"group", "add", "post", "--runner", "exec", "--set", "cmd"}, "want key=value"},
		// The run-ask line splits at spaces and quotes nothing.
		{"command of several words", []string{"group", "add post --runner exec --set cmd=make merge-bead"}, "one group, not post and merge-bead"},
		{"quoted command", []string{`group add post --runner exec --set cmd="make"`}, `'"' is none of their characters`},
		{"args as a list", []string{"group add post --runner herdr --set args=[--yolo,--auto]"}, "',' is none of their characters"},
	} {
		t.Run(c.name, func(t *testing.T) {
			y, _ := runYard(t)
			if _, err := y.AddAsk(y.ctx, y.bead, groupAddAsk("", c.argv...), "yardmaster"); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("AddAsk = %v, want a refusal saying %q", err, c.want)
			}
			if notes, err := y.Notes(y.ctx, y.bead); err != nil || len(notes) != 0 {
				t.Fatalf("the refused ask left %d notes, %v", len(notes), err)
			}
		})
	}
}

// A name in use, a user's or a crew member's name, a kind the yard does not
// know, a config the runner does not read, a directory outside the known
// places and a prompt the card cannot show or the file does not hold are
// each refused.
func TestRunAskGroupAddRefusals(t *testing.T) {
	t.Parallel()
	y, _ := runYard(t)
	home := y.Store.home
	sibling, err := os.MkdirTemp(filepath.Dir(home), "beside-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sibling) })
	outside := filepath.Join(sibling, "outside.md")
	if err := os.WriteFile(outside, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "roles"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "roles", "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	dirLink := filepath.Join(home, "etc-link")
	if err := os.Symlink("/etc", dirLink); err != nil {
		t.Fatal(err)
	}
	// A file under the home that holds "x": the text the asks below carry
	// where the file is not what they are refused for.
	any, _ := groupAddFile(t, y, "any", "x\n")
	if err := y.PutGroup(y.ctx, Group{Name: "post", Runner: "manual"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	const x = "x\n"
	herdr := []string{"group", "add", "post2", "--runner", "herdr", "--set", "kind=claude"}
	exec := []string{"group", "add", "post2", "--runner", "exec", "--set", "cmd=/srv/bin/post"}
	with := func(argv []string, more ...string) []string { return append(append([]string{}, argv...), more...) }
	// What stands at the yard's names: a prompt file the yard wrote, which no
	// ask names, and files put at the name a prompt would be written to,
	// which no approval uses.
	written := yardPromptFile(t, y, "any", x)
	if err := os.WriteFile(written, []byte(x), 0o400); err != nil {
		t.Fatal(err)
	}
	writtenLink := filepath.Join(home, "roles", "to-written.md")
	if err := os.Symlink(written, writtenLink); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yardPromptFile(t, y, "post2", x), []byte("other text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(any, yardPromptFile(t, y, "post5", x)); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		role string
		argv []string
		want string
	}{
		{"name in use", "", []string{"group", "add", "post", "--runner", "exec", "--set", "cmd=/srv/bin/post"}, "never replaces"},
		{"a user's name", "", []string{"group", "add", "ada", "--runner", "exec", "--set", "cmd=/srv/bin/post"}, "that is a user's name"},
		{"a crew member's name", "", []string{"group", "add", "yardmaster", "--runner", "exec", "--set", "cmd=/srv/bin/post"}, "that is a crew member's name"},
		{"the route keyword wait", "", []string{"group", "add", "wait", "--runner", "exec", "--set", "cmd=/srv/bin/post"}, "so no group takes the name"},
		{"a name with a dot path part", "", []string{"group", "add", "a/../b", "--runner", "exec", "--set", "cmd=/srv/bin/post"}, "dot path part"},
		{"a name of another shape", "", []string{"group", "add", "-x", "--runner", "exec"}, "the operator's to type in a terminal"},
		{"exec with no command", "", []string{"group", "add", "post2", "--runner", "exec"}, "exec runner needs config.cmd"},
		{"a key the runner does not read", "", with(exec, "--set", "kind=claude"), "nothing reads it"},
		{"a command on a herdr group", "", with(herdr, "--set", "cmd=/srv/bin/post"), "nothing reads it"},
		{"a prompt on a manual group", x, []string{"group", "add", "post2", "--runner", "manual", "--set", "prompt=" + any}, "nothing reads it"},
		{"unknown kind", "", []string{"group", "add", "post2", "--runner", "herdr", "--set", "kind=nosuch"}, "no harness kind the yard knows"},
		{"dir outside", "", with(exec, "--set", "dir=/etc"), "known places"},
		{"dir that is no directory", "", with(exec, "--set", "dir="+any), "is no directory"},
		{"relative dir", "", with(exec, "--set", "dir=work"), "no absolute directory"},
		{"dir under the home's parent", "", with(exec, "--set", "dir="+sibling), "known places"},
		{"dir through a symlink", "", with(exec, "--set", "dir="+dirLink), "outside the yard's known places"},
		{"worktrees outside", "", with(herdr, "--set", "worktrees=/etc"), "is outside the yard's known places"},
		{"prompt with no text carried", "", with(herdr, "--set", "prompt="+any), "carries no prompt text"},
		{"prompt text of blanks", " \n", with(herdr, "--set", "prompt="+any), "carries no prompt text"},
		{"prompt text with no prompt named", x, herdr, "carries prompt text but names no prompt"},
		{"relative prompt", x, with(herdr, "--set", "prompt=roles/any.md"), "no absolute path"},
		{"prompt that is gone", x, with(herdr, "--set", "prompt="+filepath.Join(home, "roles", "gone.md")), "prompt file"},
		{"prompt outside the home", x, with(herdr, "--set", "prompt="+outside), "outside the yard's home"},
		// Said before the disk is asked, so a path that stands and one that
		// does not read alike.
		{"prompt that is not there outside the home", x, with(herdr, "--set", "prompt=/no/such/dir/prompt.md"), "outside the yard's home"},
		{"prompt through a symlink out of the home", x, with(herdr, "--set", "prompt="+link), "outside the yard's home"},
		{"prompt that is a directory", x, with(herdr, "--set", "prompt="+filepath.Join(home, "roles")), "not a regular file"},
		{"prompt file that holds other text", "y\n", with(herdr, "--set", "prompt="+any), "holds other text than the ask carries"},
		{"prompt file that holds more text", "x", with(herdr, "--set", "prompt="+any), "holds other text than the ask carries"},
		{"prompt that is a file the yard wrote", x, with(herdr, "--set", "prompt="+written), "is one the yard wrote when it approved a prompt"},
		{"prompt through a symlink to a file the yard wrote", x, with(herdr, "--set", "prompt="+writtenLink), "is one the yard wrote when it approved a prompt"},
		{"a file at the yard's name that holds other text", x, with(herdr, "--set", "prompt="+any), "holds other text than its name says"},
		// A symlink there would make the file it points to the live prompt,
		// though it holds the very text.
		{"a symlink at the yard's name", x, []string{"group", "add", "post5", "--runner", "herdr", "--set", "kind=claude", "--set", "prompt=" + any}, "is not one it can use"},
		{"prompt text that is no text", string([]byte{0xff, 0xfe}), with(herdr, "--set", "prompt="+any), "not valid UTF-8"},
		{"escape in prompt", "shown\x1b[8mhidden\n", with(herdr, "--set", "prompt="+any), "control characters"},
		{"weight mark in prompt", "WEIGHTY · routine\n", with(herdr, "--set", "prompt="+any), "a mark the yard draws"},
		{"files mark in prompt", NoteFilesMark + " dropped on a card\n", with(herdr, "--set", "prompt="+any), "a mark the yard draws"},
		{"prompt past the bound", strings.Repeat("x", MaxRunCrewRole+1), with(herdr, "--set", "prompt="+any), "a card holds and shows whole"},
		// What the asker may run themselves needs no card.
		{"a group of people", "", []string{"group", "add", "people", "--runner", "manual", "--limit", "3"}, "needs no approval: run it yourself"},
	} {
		if _, err := y.AddAsk(y.ctx, y.bead, groupAddAsk(c.role, c.argv...), "yardmaster"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: AddAsk = %v, want a refusal saying %q", c.name, err, c.want)
		}
	}
	// A prompt file's content never reaches the refusal: a file under the
	// home the asker cannot read is not read back to them.
	secret := filepath.Join(home, "roles", "secret.env")
	if err := os.WriteFile(secret, []byte("TOKEN=hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"x\n", "TOKEN=hunter", "TOKEN=hunter2\nmore\n"} {
		_, err := y.AddAsk(y.ctx, y.bead, groupAddAsk(role, with(herdr, "--set", "prompt="+secret)...), "yardmaster")
		if err == nil || !strings.Contains(err.Error(), "holds other text than the ask carries") || strings.Contains(err.Error(), "hunter2") {
			t.Errorf("AddAsk of a file that holds other text = %v, want it refused without the file's text", err)
		}
	}
	// A herdr group that names no kind where the yard names none defines
	// nothing, so it needs no card either.
	if err := y.SetSetting(y.ctx, DefaultKindSetting, "", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddAsk(y.ctx, y.bead, groupAddAsk("", "group", "add", "plain", "--runner", "herdr"), "yardmaster"); err == nil || !strings.Contains(err.Error(), "needs no approval: run it yourself") {
		t.Errorf("AddAsk of a herdr group that defines nothing = %v, want it to need no approval", err)
	}
	// A directory under the home, a depot and the home's parent are known.
	if err := os.MkdirAll(filepath.Join(home, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(home, "work"), filepath.Dir(home)} {
		if _, err := y.AddAsk(y.ctx, y.bead, groupAddAsk("", "group", "add", "post3", "--runner", "exec", "--set", "cmd=/srv/bin/post", "--set", "dir="+dir), "yardmaster"); err != nil {
			t.Errorf("a group in %s was refused: %v", dir, err)
		}
	}
	// A prompt of the size a real builder's role is fits.
	text := groupAddPromptText()
	given, _ := groupAddFile(t, y, "builder", text)
	if _, err := y.AddAsk(y.ctx, y.bead, groupAddAsk(text, "group", "add", "post4", "--runner", "herdr", "--set", "kind=claude", "--set", "prompt="+given), "yardmaster"); err != nil {
		t.Errorf("a builder-sized prompt was refused: %v", err)
	}
}

// A digest-named prompt file longer than the bound is still said to be
// rewritten, but the hash covers only the bounded read: the card's "now"
// read runs in the store's write, and a same-user rewrite past the bound
// may be of any size.
func TestHomeFileDigestHashesOnlyTheBoundedRead(t *testing.T) {
	y := newAskYard(t)
	path := yardPromptFile(t, y, "porter", "You are the porter.\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	rewrite := strings.Repeat("x", MaxRunCrewRole+100)
	if err := os.WriteFile(path, []byte(rewrite), 0o600); err != nil {
		t.Fatal(err)
	}
	var data, sum []byte
	if err := y.Store.write(func(w *wtx) error {
		var err error
		_, data, sum, err = w.homeFile("crew member porter", path, MaxRunCrewRole, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(data) != MaxRunCrewRole+1 {
		t.Fatalf("homeFile read %d bytes, want the bound and one more (%d)", len(data), MaxRunCrewRole+1)
	}
	want := sha256.Sum256([]byte(rewrite[:MaxRunCrewRole+1]))
	if !bytes.Equal(sum, want[:]) {
		t.Errorf("homeFile hashed past the bounded read")
	}
	if !promptDigestRewritten(path, sum) {
		t.Errorf("an oversized digest-named file was not said to be rewritten")
	}
}
