package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// stageFlow is the store flow code: its stages, one of whose names holds a
// slash, have stage files of their own in the yard's home.
const stageFlow = "in -> work\nwork -> qa/smoke\nqa/smoke -> done\ndone terminal\n"

// stageYard is a yard with the store flow code, and the operator mckean.
func stageYard(t *testing.T) *askYard {
	t.Helper()
	y := listYard(t)
	f, err := ParseFlow(stageFlow)
	if err != nil {
		t.Fatal(err)
	}
	f.Name = "code"
	if _, err := y.PutFlow(y.ctx, *f, "mckean"); err != nil {
		t.Fatal(err)
	}
	return y
}

// stageAsk is the ask of flow set-stage with the words after the command's
// own, carrying text beside it.
func stageAsk(text string, words ...string) Ask {
	return Ask{Question: "Set the stage file?", To: "mckean", Context: []string{"the stage reads differently"},
		Run: &AskRun{Argv: append([]string{"flow", "set-stage"}, words...), Flow: text}}
}

// stagePath is where the stage file of stage in the flow code stands.
func (y *askYard) stagePath(stage string) string {
	return filepath.Join(y.home, "flows", "code", filepath.FromSlash(stage)+".md")
}

// putStage writes the stage file by hand, as a person in a terminal does,
// with the mode the file is given.
func (y *askYard) putStage(t *testing.T, stage, text string, mode os.FileMode) {
	t.Helper()
	p := y.stagePath(stage)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

// stageIs holds the stage file to the text and the mode it stands in, and a
// missing one to the text "".
func (y *askYard) stageIs(t *testing.T, stage, text string, mode os.FileMode) {
	t.Helper()
	got, err := os.ReadFile(y.stagePath(stage))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if string(got) != text {
		t.Errorf("the stage file of %s holds %q, want %q", stage, got, text)
	}
	if info, err := os.Stat(y.stagePath(stage)); err == nil && info.Mode().Perm() != mode {
		t.Errorf("the stage file of %s is %v, want %v", stage, info.Mode().Perm(), mode)
	}
}

// stageEvents are the flow_set events that name a stage, oldest first.
func (y *askYard) stageEvents(t *testing.T) []Event {
	t.Helper()
	all, err := y.EventsOfKind(y.ctx, "flow_set", 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []Event
	for _, e := range all {
		if _, ok := e.Data["stage"]; ok {
			out = append(out, e)
		}
	}
	return out
}

// A flow set-stage card adds a stage file or replaces the one that stands,
// shows what stands and what would, each whole, weighs code, and on approval
// writes the file: held to none or to the digest the ask names, at the mode
// the file stands in or rw-r--r--, a stage whose name holds a slash a
// directory deeper, and recorded as flow_set with the stage beside the flow.
func TestRunAskFlowSetStageAddsAndReplaces(t *testing.T) {
	t.Parallel()
	y := stageYard(t)
	file := y.stagePath("work")
	text := "Build it.\n\nTests pass.\n"

	// Added where none stands: held to none, whatever case it is typed in.
	seq := y.ask(t, stageAsk(text, "code", "work", "--digest", "NONE"), "yardmaster")
	r := y.asked(t, seq)
	if len(r.Ask.Weight) != 1 || r.Ask.Weight[0].Class != WeightCode || !StrongWeight(r.Ask.Weight) {
		t.Fatalf("weight = %+v, want one code line, which is strong", r.Ask.Weight)
	}
	if want := []string{"flow", "set-stage", "code", "work", "--digest", "none"}; !slices.Equal(r.Ask.Run.Argv, want) {
		t.Errorf("the command is stored as %q, want %q", r.Ask.Run.Argv, want)
	}
	strongHas(t, r.Ask.Run.Does,
		"adds the stage file of stage work of the flow code, where none stands: "+file,
		"On approval the file holds the text below, whole:", "Build it.", "", "Tests pass.")
	strongLacks(t, r.Ask.Run.Does, "The text that stands", "Warning")
	if want := "remove " + file + " in a terminal: no stage file stood for stage work of flow code"; r.Ask.Run.Back != want || r.Ask.Run.BackBy != "only the operator" {
		t.Errorf("taken back by %q (%s), want %q", r.Ask.Run.Back, r.Ask.Run.BackBy, want)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "weighty") {
		t.Fatalf("unweighed approval = %v, want a weighty refusal", err)
	}
	y.stageIs(t, "work", "", 0o644)
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "stage file code/work: " + file; done.Result != want {
		t.Errorf("approval printed %q, want %q", done.Result, want)
	}
	y.stageIs(t, "work", text, 0o644)
	evs := y.stageEvents(t)
	if len(evs) != 1 || evs[0].Actor != "mckean" || evs[0].Data["flow"] != "code" || evs[0].Data["stage"] != "work" {
		t.Fatalf("the approval left the events %+v, want one flow_set of code and work by mckean", evs)
	}

	// Replaced with no digest: the file that stands is shown whole, and
	// replaced at the mode it stands in.
	y.putStage(t, "work", text, 0o600)
	next := "Build it.\n\nTests pass, and the build is green.\n"
	seq = y.ask(t, stageAsk(next, "code", "work"), "yardmaster")
	r = y.asked(t, seq)
	strongHas(t, r.Ask.Run.Does,
		"replaces the stage file of stage work of the flow code whole: "+file,
		"The text that stands, whole:", "Build it.", "Tests pass.",
		"On approval the file holds the text below, whole:", "Build it.", "Tests pass, and the build is green.")
	if want := "yardr flow set-stage code work -f " + y.historyPath(text) + ", where the yard keeps the text that stands once this is approved"; r.Ask.Run.Back != want {
		t.Errorf("taken back by %q, want %q", r.Ask.Run.Back, want)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	y.stageIs(t, "work", next, 0o600)
	y.historyIs(t, text)

	// Replaced held to the digest of the file as it was read.
	last := "Build it, then say so.\n"
	seq = y.ask(t, stageAsk(last, "code", "work", "--digest="+strings.ToUpper(sha256Hex(next))), "yardmaster")
	if got := y.asked(t, seq).Ask.Run.Argv; got[len(got)-1] != sha256Hex(next) {
		t.Errorf("the digest is stored as %q, want it in lower case", got[len(got)-1])
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	y.stageIs(t, "work", last, 0o600)
	y.historyIs(t, next)

	// A file with nothing in it is none: added at the mode it stands in, and
	// it leaves nothing to keep.
	y.putStage(t, "work", " \n", 0o640)
	seq = y.ask(t, stageAsk(text, "code", "work", "--digest", "none"), "yardmaster")
	strongHas(t, y.asked(t, seq).Ask.Run.Does, "adds the stage file of stage work")
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	y.stageIs(t, "work", text, 0o640)

	// A stage whose name holds a slash has its file a directory deeper,
	// made as it is written.
	seq = y.ask(t, stageAsk("Smoke it.\n", "code", "qa/smoke", "--digest", "none"), "yardmaster")
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	y.stageIs(t, "qa/smoke", "Smoke it.\n", 0o644)
	if info, err := os.Stat(filepath.Dir(y.stagePath("qa/smoke"))); err != nil || !info.IsDir() {
		t.Errorf("the directory of qa/smoke: %v, %v", info, err)
	}
	// Replaced, it is kept in the one flat directory, not a directory deeper.
	seq = y.ask(t, stageAsk("Smoke it twice.\n", "code", "qa/smoke", "--digest", sha256Hex("Smoke it.\n")), "yardmaster")
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	y.stageIs(t, "qa/smoke", "Smoke it twice.\n", 0o644)
	y.historyIs(t, "Smoke it.\n")
	// What was replaced is kept, and nothing for a file added where none stood
	// or where one stood with nothing in it.
	y.historyHolds(t, text, next, "Smoke it.\n")
	// Each write says which text it wrote and which it replaced, by digest and
	// no more.
	if want := []string{
		"work " + sha8(text) + " <- -", "work " + sha8(next) + " <- " + sha8(text), "work " + sha8(last) + " <- " + sha8(next),
		"work " + sha8(text) + " <- -", "qa/smoke " + sha8("Smoke it.\n") + " <- -", "qa/smoke " + sha8("Smoke it twice.\n") + " <- " + sha8("Smoke it.\n"),
	}; !slices.Equal(y.stageWrites(t), want) {
		t.Errorf("the approvals recorded\n%q\nwant\n%q", y.stageWrites(t), want)
	}
	// What each approval read was the file itself: the yard's own temporary
	// files are not left beside it.
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(file), ".yardr-stage-*"))
	if len(left) > 0 {
		t.Errorf("the writes left %v", left)
	}
}

// A flow set-stage ask is refused at the ask, in the words of what is wrong:
// text a card cannot show whole or as written, a flow or stage that does not
// have a file to set, a file that stands where the ask says none does or
// that is not what the ask names, and a file the yard will not read or write
// through: a link, or what is no plain file. A refusal changes nothing.
func TestRunAskFlowSetStageRefusals(t *testing.T) {
	t.Parallel()
	text := "Build it.\n"
	elsewhere := func(t *testing.T) string { return t.TempDir() }
	for _, c := range []struct {
		name  string
		words []string
		text  string
		setup func(t *testing.T, y *askYard)
		want  string
	}{
		{name: "blank text", words: []string{"code", "work"}, text: " \n\t", want: "carries no text"},
		{name: "text no card shows whole", words: []string{"code", "work"}, text: strings.Repeat("x\n", MaxRunFlow/2+1),
			want: fmt.Sprintf("the text it carries is %d bytes: a card shows it whole and holds at most %d", MaxRunFlow+2, MaxRunFlow)},
		{name: "control character", words: []string{"code", "work"}, text: "Build \x1b[31mit.\n", want: "no text a card shows as written"},
		{name: "carriage return", words: []string{"code", "work"}, text: "Build it.\r\nTests pass.\r\n", want: "no text a card shows as written"},
		{name: "direction mark", words: []string{"code", "work"}, text: "Build it.\u202e\n", want: "no text a card shows as written"},
		{name: "file mark", words: []string{"code", "work"}, text: "Build it.\n" + NoteFilesMark + " a file\n", want: "no text a card shows as written"},
		{name: "weight mark", words: []string{"code", "work"}, text: "Build it.\n" + WeightMark + "code: nothing\n", want: "no text a card shows as written"},
		{name: "invalid UTF-8", words: []string{"code", "work"}, text: "Build \xff it.\n", want: "no text a card shows as written"},
		{name: "stage the flow lacks", words: []string{"code", "review"}, text: text, want: "flow code has no stage review; stages: in, work, qa/smoke, done"},
		{name: "flow of no kind", words: []string{"ghost", "work"}, text: text, want: "flow ghost: not found"},
		{name: "flow name with a character no command takes", words: []string{"gh$st", "work"}, text: text, want: "is none of their characters"},
		{name: "flow name no flow has", words: []string{"gh@st", "work"}, text: text, want: `flow name "gh@st"`},
		{name: "stage that leads out", words: []string{"code", "../work"}, text: text, want: `stage "../work" is no name of a stage file`},
		{name: "stage with an empty part", words: []string{"code", "qa//smoke"}, text: text, want: `stage "qa//smoke" is no name of a stage file`},
		{name: "stage typed as a flag", words: []string{"code", "--work"}, text: text, want: "--work"},
		{name: "text that stands", words: []string{"code", "work"}, text: text,
			setup: func(t *testing.T, y *askYard) { y.putStage(t, "work", text, 0o644) }, want: "holds this text already, so the command would change nothing"},
		{name: "none but one stands", words: []string{"code", "work", "--digest", "none"}, text: text,
			setup: func(t *testing.T, y *askYard) { y.putStage(t, "work", "Other.\n", 0o644) }, want: "though --digest none says none does: it appeared since it was read"},
		{name: "a digest where none stands", words: []string{"code", "work", "--digest", strings.Repeat("a", 64)}, text: text,
			want: "does not stand (or holds nothing), though --digest " + strings.Repeat("a", 64)},
		{name: "a digest that is not the file's", words: []string{"code", "work", "--digest", strings.Repeat("a", 64)}, text: text,
			setup: func(t *testing.T, y *askYard) { y.putStage(t, "work", "Other.\n", 0o644) }, want: "holds sha256 " + sha256Hex("Other.\n") + ", not the " + strings.Repeat("a", 64)},
		{name: "a digest of the wrong size", words: []string{"code", "work", "--digest", "abc"}, text: text, want: `digest "abc"`},
		{name: "the digest twice", words: []string{"code", "work", "--digest", "none", "--digest=none"}, text: text, want: "--digest once, not 2 times"},
		{name: "a flag it does not take", words: []string{"code", "work", "--force"}, text: text, want: "--force"},
		{name: "a word too many", words: []string{"code", "work", "and"}, text: text, want: "a flow and a stage, not 3 words"},
		{name: "a linked directory", words: []string{"code", "work"}, text: text, setup: func(t *testing.T, y *askYard) {
			if err := os.MkdirAll(filepath.Join(y.home, "flows"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere(t), filepath.Join(y.home, "flows", "code")); err != nil {
				t.Fatal(err)
			}
		}, want: "is a symbolic link"},
		{name: "a linked flows directory", words: []string{"code", "work"}, text: text, setup: func(t *testing.T, y *askYard) {
			if err := os.Symlink(elsewhere(t), filepath.Join(y.home, "flows")); err != nil {
				t.Fatal(err)
			}
		}, want: "is a symbolic link"},
		{name: "a linked file", words: []string{"code", "work"}, text: text, setup: func(t *testing.T, y *askYard) {
			other := filepath.Join(elsewhere(t), "other.md")
			if err := os.WriteFile(other, []byte("Not here.\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(y.stagePath("work")), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, y.stagePath("work")); err != nil {
				t.Fatal(err)
			}
		}, want: "is a symbolic link"},
		{name: "a directory where the file is", words: []string{"code", "work"}, text: text, setup: func(t *testing.T, y *askYard) {
			if err := os.MkdirAll(y.stagePath("work"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, want: "is not a regular file"},
		{name: "a file where the directory is", words: []string{"code", "work"}, text: text, setup: func(t *testing.T, y *askYard) {
			if err := os.MkdirAll(filepath.Join(y.home, "flows"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(y.home, "flows", "code"), []byte("a file\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, want: "is not a directory"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			y := stageYard(t)
			if c.setup != nil {
				c.setup(t, y)
			}
			if _, err := y.AddAsk(y.ctx, y.bead, stageAsk(c.text, c.words...), "yardmaster"); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("AddAsk = %v, want it refused with %q", err, c.want)
			}
		})
	}
}

// The card is held to the file it saw: one that appeared, changed, was
// removed or was emptied since the ask is stale, says which (naming the
// store's own write where there was one), and leaves the disk as it found
// it — even when someone else wrote identical text.
func TestRunAskFlowSetStageIsStaleWhereTheFileMoved(t *testing.T) {
	t.Parallel()
	y := stageYard(t)

	// Appeared since, with other text.
	seq := y.ask(t, stageAsk("Mine.\n", "code", "work", "--digest", "none"), "yardmaster")
	y.putStage(t, "work", "Theirs.\n", 0o644)
	y.refusedStale(t, seq, "was written when no stage file stood for stage work of the flow code: one appeared since. ")
	y.stageIs(t, "work", "Theirs.\n", 0o644)

	// Changed since, by the store's own write: that write is named.
	seq = y.ask(t, stageAsk("Mine.\n", "code", "work"), "yardmaster")
	if _, err := y.PutStageFile(y.ctx, "code", "work", "Theirs again.\n", "", "mckean", true); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, seq, "was written when the stage file of stage work of the flow code was another: it was changed since (flow_set, ")
	y.stageIs(t, "work", "Theirs again.\n", 0o644)

	// Emptied and removed since.
	seq = y.ask(t, stageAsk("Mine.\n", "code", "work", "--digest", sha256Hex("Theirs again.\n")), "yardmaster")
	y.putStage(t, "work", "\n", 0o644)
	y.refusedStale(t, seq, "had a stage file: it was removed or emptied since")
	if err := os.Remove(y.stagePath("work")); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, seq, "had a stage file: it was removed or emptied since")
	y.stageIs(t, "work", "", 0o644)

	// Even identical text written by someone else since the ask is stale:
	// the file appeared under --digest none, leaving the disk as it was.
	seq = y.ask(t, stageAsk("Mine.\n", "code", "work", "--digest", "none"), "yardmaster")
	y.putStage(t, "work", "Mine.\n", 0o640)
	y.refusedStale(t, seq, "was written when no stage file stood for stage work of the flow code: one appeared since")
	y.stageIs(t, "work", "Mine.\n", 0o640)

	// A flow that became a pack's since is another card, not the same
	// words with another file under them.
	seq = y.ask(t, stageAsk("Again.\n", "code", "work"), "yardmaster")
	if err := y.PutDepot(y.ctx, Depot{Name: "web"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.RecordPack(y.ctx, PackImport{Pack: "starter", Depot: "web", Objects: []PackObject{{Kind: "flow", Name: "code"}}}, true, "mckean"); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, seq, `the yard would say "The flow is the pack starter's`)
	y.stageIs(t, "work", "Mine.\n", 0o640)
}

// A flow of a depot's repository has its stage files there, so the card
// refuses it naming the path to change on the depot's branch, as the
// terminal command does; a type's flow always is one. A depot whose checkout
// does not read does not hide that, and a flow of the store is no reason to
// start git.
func TestFlowSetStageRefusesARepositoryFlow(t *testing.T) {
	t.Parallel()
	y := stageYard(t)
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	for name, text := range map[string]string{
		".yardr/flows/default.flow":       "new -> review\nreview -> done\ndone terminal\n",
		".yardr/flows/type/decision.flow": "ask -> answered\nanswered terminal\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-q", "-m", "flows")
	for _, d := range []Depot{{Name: "web", Path: repo, Base: "main"}, {Name: "broken", Path: t.TempDir(), Base: "main"}} {
		if err := y.PutDepot(y.ctx, d, "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := y.RepoFlows(y.ctx, "broken"); err == nil {
		t.Fatal("the test wants a depot whose checkout does not read")
	}
	for _, c := range []struct{ flow, stage, path string }{
		{"default", "new", ".yardr/flows/default/new.md"},
		{"type/decision", "ask", ".yardr/flows/type/decision/ask.md"},
		{"type/nothing", "ask", ".yardr/flows/type/nothing/ask.md"},
	} {
		want := "flow " + c.flow + " is a flow of a depot's repository, not of the store: its stage files are in that repository, so edit " + c.path + " on the depot's branch and commit it there by hand"
		if _, err := y.AddAsk(y.ctx, y.bead, stageAsk("Text.\n", c.flow, c.stage), "yardmaster"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("asking for the stage file of %s = %v, want %q", c.flow, err, want)
		}
		if _, err := y.PutStageFile(y.ctx, c.flow, c.stage, "Text.\n", "", "mckean", true); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("PutStageFile of %s = %v, want %q", c.flow, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(y.home, "flows", "default")); err == nil {
		t.Error("a refused write made the directory of the repository's flow")
	}
	if _, err := y.PutStageFile(y.ctx, "ghost", "new", "Text.\n", "", "mckean", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("PutStageFile of a flow no depot has = %v, want not found", err)
	}
	// A store flow is looked up in the store alone, and a repository holding
	// a flow of its name is not asked.
	reads := y.repoReads.Load()
	if _, err := y.AddAsk(y.ctx, y.bead, stageAsk("Text.\n", "code", "work"), "yardmaster"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.PutStageFile(y.ctx, "code", "work", "Text.\n", "", "mckean", true); err != nil {
		t.Fatal(err)
	}
	if got := y.repoReads.Load(); got != reads {
		t.Errorf("a flow of the store read %d repositories", got-reads)
	}
}

// A flow a pack manages gets its stage file carried like any other, and the
// card says whose it is, what pack apply then does with the yard's copy, and
// the door to the pack's, with the same words in its take-back. A card filed
// before the flow was a pack's is another card once it is (checked above).
func TestRunAskFlowSetStageSaysWhatPackApplyDoes(t *testing.T) {
	t.Parallel()
	y := stageYard(t)
	if err := y.PutDepot(y.ctx, Depot{Name: "web"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.RecordPack(y.ctx, PackImport{Pack: "starter", Depot: "web", Objects: []PackObject{{Kind: "flow", Name: "code"}}}, true, "mckean"); err != nil {
		t.Fatal(err)
	}
	y.putStage(t, "work", "Old.\n", 0o644)
	seq := y.ask(t, stageAsk("New.\n", "code", "work"), "yardmaster")
	r := y.asked(t, seq)
	if len(r.Ask.Run.Does) < 3 || !strings.HasPrefix(r.Ask.Run.Does[0], "replaces the stage file of stage work") {
		t.Fatalf("the card says %q", r.Ask.Run.Does)
	}
	if want := "The flow is the pack starter's, imported into depot web: this sets the yard's copy of the stage file, and changes nothing of the pack."; r.Ask.Run.Does[1] != want {
		t.Errorf("line 1 = %q, want %q", r.Ask.Run.Does[1], want)
	}
	if want := "yardr pack apply then keeps the yard's copy of it, whatever the pack moves, and sets the pack's again only where it is told to (--take file/<name>, the name yardr pack check starter --depot web gives the file)."; r.Ask.Run.Does[2] != want {
		t.Errorf("line 2 = %q, want %q", r.Ask.Run.Does[2], want)
	}
	for _, want := range []string{
		"yardr flow set-stage code work -f " + y.historyPath("Old.\n"),
		"; or, for the pack's own copy instead, yardr pack apply starter --depot web --take file/<name> in a terminal",
	} {
		if !strings.Contains(r.Ask.Run.Back, want) {
			t.Errorf("taken back by %q, want it to say %q", r.Ask.Run.Back, want)
		}
	}
	// A flow of no depot's import has none of those words.
	if _, err := y.PutFlow(y.ctx, Flow{Name: "plain", Stages: []string{"in", "done"}, Transitions: map[string][]string{"in": {"done"}}, Terminal: []string{"done"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	plain := y.ask(t, stageAsk("New.\n", "plain", "in"), "yardmaster")
	strongLacks(t, y.asked(t, plain).Ask.Run.Does, "The flow is the pack")
}

// A card whose text breaks the ## Ask section of its stage says so; one that
// has none, or a good one, says nothing. A standing file the card cannot
// show whole is said to be one, and the card still holds the approval to it.
func TestRunAskFlowSetStageWarnsOfABrokenAsk(t *testing.T) {
	t.Parallel()
	y := stageYard(t)
	broken := "Build it.\n\n## Ask\nkind: choice\nquestion: Accept it?\ncontext:\n- Why.\ndone: Go.\nbacklog: No.\n"
	seq := y.ask(t, stageAsk(broken, "code", "work"), "yardmaster")
	strongHas(t, y.asked(t, seq).Ask.Run.Does, "Warning: the ## Ask section of this text is broken (")
	good := "Build it.\n\n## Ask\nkind: input\nquestion: What was decided?\ncontext:\n- The bead waits.\nmove: none\n"
	seq = y.ask(t, stageAsk(good, "code", "work"), "yardmaster")
	strongLacks(t, y.asked(t, seq).Ask.Run.Does, "Warning")

	// A file the card cannot show whole: too long, or not text as written.
	for _, c := range []struct{ stands, why string }{
		{strings.Repeat("x\n", MaxRunFlow/2+1), fmt.Sprintf("it is %d bytes, and a card shows at most %d", MaxRunFlow+2, MaxRunFlow)},
		{"Old \x1b[31mtext\n", "it is not text a card shows as written"},
		{"Old\r\ntext\r\n", "it is not text a card shows as written"},
		{"Old \xff text\n", "it is not text a card shows as written"},
	} {
		y.putStage(t, "work", c.stands, 0o644)
		seq := y.ask(t, stageAsk("New.\n", "code", "work"), "yardmaster")
		r := y.asked(t, seq)
		strongHas(t, r.Ask.Run.Does, "replaces the stage file", "This card cannot show the standing stage file: "+c.why+".", "On approval the file holds the text below, whole:", "New.")
		strongLacks(t, r.Ask.Run.Does, "The text that stands, whole:")
		// Shown or not, the approval keeps the text that stands, so that is what
		// puts it back: by a save of that file, or, for bytes that are no text
		// a save takes, by a copy.
		want := "yardr flow set-stage code work -f " + y.historyPath(c.stands) + ", where the yard keeps the text that stands once this is approved"
		if !utf8.ValidString(c.stands) {
			want = "cp " + y.historyPath(c.stands) + " " + y.stagePath("work") + " in a terminal, where the yard keeps the text that stands once this is approved: it is not valid UTF-8"
		}
		if !strings.HasPrefix(r.Ask.Run.Back, want) {
			t.Errorf("taken back by %q, want it to begin %q", r.Ask.Run.Back, want)
		}
		// Held to the file that stands, shown or not.
		y.putStage(t, "work", c.stands+"more\n", 0o644)
		y.refusedStale(t, seq, "it was changed since")
	}
}

// PutStageFile is the terminal command's write: the operator's alone, held
// to the file it names, atomic, recorded as flow_set with the stage, and
// what HomeStageFile then reads.
func TestPutStageFile(t *testing.T) {
	t.Parallel()
	y := stageYard(t)
	text := "Build it.\n"
	for _, actor := range []string{"yardmaster", "ada"} {
		if _, err := y.PutStageFile(y.ctx, "code", "work", text, "", actor, true); err == nil || !strings.Contains(err.Error(), "only the operator (mckean) may change a flow's stages; this is "+actor) {
			t.Errorf("PutStageFile as %s = %v, want it the operator's alone", actor, err)
		}
	}
	y.stageIs(t, "work", "", 0o644)
	// A save is weighty code, as its card is: a client that does not say its
	// person saw that is refused first, whoever it is, with nothing read,
	// written or recorded (the events are counted below).
	for _, c := range []struct{ flow, actor string }{{"code", "mckean"}, {"code", "ada"}, {"no-such-flow", "mckean"}} {
		if _, err := y.PutStageFile(y.ctx, c.flow, "work", text, "", c.actor, false); err == nil ||
			!strings.Contains(err.Error(), `saving a stage file is weighty (code): a client sends "weighed": true once its person has passed its gate`) {
			t.Errorf("PutStageFile of %s as %s without weighed = %v, want it refused as weighty (code)", c.flow, c.actor, err)
		}
	}
	y.stageIs(t, "work", "", 0o644)

	sf, err := y.PutStageFile(y.ctx, "code", "work", text, "NONE", "mckean", true)
	if err != nil {
		t.Fatal(err)
	}
	if sf.Path != y.stagePath("work") || sf.Text != text {
		t.Errorf("PutStageFile answered %+v", sf)
	}
	if got, err := HomeStageFile(y.home, "code", "work"); err != nil || got == nil || got.Text != text || got.Path != sf.Path {
		t.Errorf("HomeStageFile reads %+v, %v", got, err)
	}
	// The digest held to is the file's, in any case; others are refused,
	// naming what stands.
	for _, c := range []struct{ expect, want string }{
		{"none", "stands (sha256 " + sha256Hex(text) + "), though --digest none says none does"},
		{strings.Repeat("0", 64), "holds sha256 " + sha256Hex(text) + ", not the " + strings.Repeat("0", 64)},
		{"zz", `digest "zz"`},
	} {
		if _, err := y.PutStageFile(y.ctx, "code", "work", "Other.\n", c.expect, "mckean", true); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("PutStageFile held to %q = %v, want %q", c.expect, err, c.want)
		}
	}
	y.stageIs(t, "work", text, 0o644)
	next := "Build it, then say so.\n"
	if _, err := y.PutStageFile(y.ctx, "code", "work", next, strings.ToUpper(sha256Hex(text)), "mckean", true); err != nil {
		t.Fatal(err)
	}
	y.stageIs(t, "work", next, 0o644)
	// What it is not given to write, or where, is refused before the disk.
	for _, c := range []struct{ flow, stage, text, want string }{
		{"code", "work", "", "a stage file with nothing in it"},
		{"code", "work", " \n", "a stage file with nothing in it"},
		{"code", "work", "Build \xff\n", "not valid UTF-8"},
		{"code", "../work", "x\n", "is no name of a stage file"},
		{"code", "work/../in", "x\n", "is no name of a stage file"},
		{"code", "review", "x\n", "flow code has no stage review"},
		{"a b", "work", "x\n", `flow name "a b"`},
	} {
		if _, err := y.PutStageFile(y.ctx, c.flow, c.stage, c.text, "", "mckean", true); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("PutStageFile(%q, %q, %q) = %v, want %q", c.flow, c.stage, c.text, err, c.want)
		}
	}
	y.stageIs(t, "work", next, 0o644)
	// A call a device made carries the device's mark (WithVia) on its event,
	// down the link from the edge as well, and one made at the yard's own
	// machine carries none.
	for _, device := range []string{"phone", "phone via edge"} {
		if _, err := y.PutStageFile(WithVia(y.ctx, "mckean", device), "code", "work", next+device+"\n", "", "mckean", true); err != nil {
			t.Fatal(err)
		}
	}
	evs := y.stageEvents(t)
	if len(evs) != 4 || evs[0].Data["flow"] != "code" || evs[1].Data["stage"] != "work" || evs[1].Actor != "mckean" {
		t.Fatalf("the writes left the events %+v, want four flow_set of code and work by mckean", evs)
	}
	for i, want := range []string{"", "", "phone", "phone via edge"} {
		if got := Via(evs[i]); got != want {
			t.Errorf("flow_set %d is marked via %q, want %q", i, got, want)
		}
	}
}

// A batch may run a write twice, and a file the first run wrote stays when its
// transaction is rolled back: the retry of that same write finds its own write
// on disk and commits cleanly (both for putStageFile and for approveAsk), while
// a separate call still enforces --digest even when the file already holds the
// same text.
func TestPutStageFileRunTwiceEndsTheSame(t *testing.T) {
	t.Parallel()
	y := stageYard(t)
	boom := errors.New("another write of the batch failed")
	runs := 0
	err := y.write(func(w *wtx) error {
		runs++
		if _, err := w.putStageFile(y.ctx, "code", "work", "Build it.\n", stageNone, "mckean"); err != nil {
			return err
		}
		if runs == 1 {
			return boom
		}
		return nil
	})
	if err != nil || runs != 2 {
		t.Fatalf("the write ran %d times and ended %v, want 2 runs and nil", runs, err)
	}
	y.stageIs(t, "work", "Build it.\n", 0o644)
	if evs := y.stageEvents(t); len(evs) != 1 {
		t.Errorf("the retried write left the events %+v, want one", evs)
	}
	// A separate call still holds --digest even when the text is identical.
	if _, err := y.PutStageFile(y.ctx, "code", "work", "Build it.\n", stageNone, "mckean", true); err == nil || !strings.Contains(err.Error(), "though --digest none says none does") {
		t.Errorf("separate PutStageFile held to none with identical text = %v, want refusal", err)
	}
	if _, err := y.PutStageFile(y.ctx, "code", "work", "Build it.\n", strings.Repeat("0", 64), "mckean", true); err == nil || !strings.Contains(err.Error(), "not the "+strings.Repeat("0", 64)) {
		t.Errorf("separate PutStageFile held to wrong digest with identical text = %v, want refusal", err)
	}
	// Unconditional or matching digest with identical text succeeds.
	if _, err := y.PutStageFile(y.ctx, "code", "work", "Build it.\n", sha256Hex("Build it.\n"), "mckean", true); err != nil {
		t.Errorf("PutStageFile held to matching digest with identical text: %v", err)
	}

	// A batch retry of an approval commits cleanly too: run 1 writes the
	// file and rolls back; run 2 sees its own write on disk and finishes.
	seq := y.ask(t, stageAsk("Smoke it.\n", "code", "qa/smoke", "--digest", "none"), "yardmaster")
	runs = 0
	var done *AskApproved
	err = y.write(func(w *wtx) error {
		runs++
		var err error
		done, err = w.approveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
		if err != nil {
			return err
		}
		if runs == 1 {
			return boom
		}
		return nil
	})
	if err != nil || runs != 2 {
		t.Fatalf("the approval ran %d times and ended %v, want 2 runs and nil", runs, err)
	}
	if want := "stage file code/qa/smoke: " + y.stagePath("qa/smoke"); done == nil || done.Result != want {
		t.Errorf("retried approval printed %+v, want %q", done, want)
	}
	y.stageIs(t, "qa/smoke", "Smoke it.\n", 0o644)

	// The same where a write replaces a file: the retry tells what its first
	// run replaced, in the event and in the answer, finds the copy that run
	// kept and makes no other, and a separate call is still held to what it
	// names even where the file holds the text already.
	once, twice := "Build it.\n", "Build it twice.\n"
	var got *LineStageFile
	runs = 0
	err = y.write(func(w *wtx) error {
		runs++
		var err error
		got, err = w.putStageFile(y.ctx, "code", "work", twice, sha256Hex(once), "mckean")
		if err != nil {
			return err
		}
		if runs == 1 {
			return boom
		}
		return nil
	})
	if err != nil || runs != 2 {
		t.Fatalf("the replacement ran %d times and ended %v, want 2 runs and nil", runs, err)
	}
	want := LineStageFile{Path: y.stagePath("work"), Text: twice, Digest: sha256Hex(twice), PriorDigest: sha256Hex(once), PriorText: once}
	if got == nil || *got != want {
		t.Errorf("the retried replacement answered %+v, want %+v", got, want)
	}
	y.stageIs(t, "work", twice, 0o644)
	y.historyHolds(t, once)
	for _, c := range []struct{ expect, want string }{
		{stageNone, "though --digest none says none does"},
		{sha256Hex(once), "not the " + sha256Hex(once)},
	} {
		if _, err := y.PutStageFile(y.ctx, "code", "work", twice, c.expect, "mckean", true); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("separate PutStageFile held to %q with identical text = %v, want refusal", c.expect, err)
		}
	}
	// Held to the file that stands, it succeeds and replaces nothing, so it
	// tells nothing was kept.
	if got, err := y.PutStageFile(y.ctx, "code", "work", twice, sha256Hex(twice), "mckean", true); err != nil || got.PriorDigest != "" || got.PriorText != "" {
		t.Errorf("separate PutStageFile held to the matching digest with identical text = %+v, %v, want no prior", got, err)
	}
	y.historyHolds(t, once)

	// A batch retry of the approval of a card that replaces: the same.
	seq = y.ask(t, stageAsk("Smoke it twice.\n", "code", "qa/smoke", "--digest", sha256Hex("Smoke it.\n")), "yardmaster")
	runs = 0
	done = nil
	err = y.write(func(w *wtx) error {
		runs++
		var err error
		done, err = w.approveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
		if err != nil {
			return err
		}
		if runs == 1 {
			return boom
		}
		return nil
	})
	if err != nil || runs != 2 {
		t.Fatalf("the replacing approval ran %d times and ended %v, want 2 runs and nil", runs, err)
	}
	if want := "stage file code/qa/smoke: " + y.stagePath("qa/smoke"); done == nil || done.Result != want {
		t.Errorf("retried approval printed %+v, want %q", done, want)
	}
	y.stageIs(t, "qa/smoke", "Smoke it twice.\n", 0o644)
	y.historyHolds(t, once, "Smoke it.\n")

	// Another writer's identical text still stales an approval, retry or not:
	// the file is not the one the card saw, and nothing is kept or written.
	thrice := "Build it thrice.\n"
	seq = y.ask(t, stageAsk(thrice, "code", "work"), "yardmaster")
	if _, err := y.PutStageFile(y.ctx, "code", "work", thrice, "", "mckean", true); err != nil {
		t.Fatal(err)
	}
	y.historyHolds(t, once, "Smoke it.\n", twice)
	y.refusedStale(t, seq, "was written when the stage file of stage work of the flow code was another: it was changed since")
	y.historyHolds(t, once, "Smoke it.\n", twice)

	if want := []string{
		"work " + sha8(once) + " <- -", "work " + sha8(once) + " <- -", "qa/smoke " + sha8("Smoke it.\n") + " <- -",
		"work " + sha8(twice) + " <- " + sha8(once), "work " + sha8(twice) + " <- -",
		"qa/smoke " + sha8("Smoke it twice.\n") + " <- " + sha8("Smoke it.\n"), "work " + sha8(thrice) + " <- " + sha8(twice),
	}; !slices.Equal(y.stageWrites(t), want) {
		t.Errorf("the writes recorded\n%q\nwant\n%q", y.stageWrites(t), want)
	}
}

// A stage file's write is a flow_set too, so footplate's Line read moves
// with it, but it moved no stage of the flow: a flow set card that is stale
// names the write of the flow, not a later write of a stage file.
func TestFlowSetStaleNamesTheFlowsWriteNotAStageFiles(t *testing.T) {
	t.Parallel()
	y := stageYard(t)
	seq := y.ask(t, flowSetAsk("code", "in -> work\nwork -> done\ndone terminal\n"), "yardmaster")
	f, err := ParseFlow("in -> done\ndone terminal\n")
	if err != nil {
		t.Fatal(err)
	}
	f.Name = "code"
	if _, err := y.PutFlow(y.ctx, *f, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.PutStageFile(y.ctx, "code", "in", "Wait here.\n", "", "mckean", true); err != nil {
		t.Fatal(err)
	}
	y.refusedStale(t, seq, "was written when the flow code was another: it was set since (flow_set, ")
	_, err = y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err == nil || !strings.Contains(err.Error(), "by ada)") || strings.Contains(err.Error(), "by mckean)") {
		t.Errorf("the refusal is %v, want it to name ada's write of the flow", err)
	}
}
