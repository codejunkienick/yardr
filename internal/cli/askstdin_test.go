package cli

import (
	"reflect"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
)

// An ask on stdin is one of two forms by its first line: labeled when that
// line starts with a label, else a question with its context lines after it.
// The two are never mixed, a field is one line, and a refusal says where.
func TestParseAskStdin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, in string
		want     askStdin
	}{
		{"a question and its context", "Move them now?\n64 links still point at the old source.\nThe move was tested on a copy.\n",
			askStdin{question: "Move them now?", context: []string{"64 links still point at the old source.", "The move was tested on a copy."}}},
		{"blank lines and indentation are skipped", "\n  Move them now?\n\n    C:\\yard and http://old.example are context, since only a letter, a colon and a space is a label.\n",
			askStdin{question: "Move them now?", context: []string{"C:\\yard and http://old.example are context, since only a letter, a colon and a space is a label."}}},
		{"a question alone", "Move them now?", askStdin{question: "Move them now?"}},
		{"every label", "Q: Move them now?\nC: 64 links still point at the old source.\nC: The move was tested on a copy.\nO: Move them now: all 64 links move tonight\nO: After Friday: keep the old source\nW: the test passed\nR: depot test web\nT: go test ./...\n",
			askStdin{question: "Move them now?", context: []string{"64 links still point at the old source.", "The move was tested on a copy."},
				options: []string{"Move them now: all 64 links move tonight", "After Friday: keep the old source"},
				why:     "the test passed", run: "depot test web", runTest: "go test ./..."}},
		{"labels in any order", "C: why.\nW: because\nQ: Go?\n", askStdin{question: "Go?", context: []string{"why."}, why: "because"}},
		{"a label's text keeps its colons", "Q: Rename 'depot'? Yes: no\nO: Keep: it\n", askStdin{question: "Rename 'depot'? Yes: no", options: []string{"Keep: it"}}},
		{"says and makes lines in order", "Q: Replace the hook?\nC: why.\nR: hook add linear --source linear\nS: action=opened event=issues -> done\nS: action=closed event=issues -> changes\nM: action=opened event=issues -> task:new\n",
			askStdin{question: "Replace the hook?", context: []string{"why."}, run: "hook add linear --source linear",
				says:  []string{"action=opened event=issues -> done", "action=closed event=issues -> changes"},
				makes: []string{"action=opened event=issues -> task:new"}}},
	} {
		got, err := parseAskStdin(tc.in)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: parseAskStdin(%q) = %+v, %v, want %+v", tc.name, tc.in, got, err, tc.want)
		}
	}

	// A refusal names what is wrong and where; the ones about the form also
	// name the labels, so that a person who read no help is told what to
	// write.
	for _, tc := range []struct {
		name, in, want string
		labels         bool
	}{
		{"nothing", "", "stdin gave no question, so nothing was asked", false},
		{"only white space", " \n\t\n", "stdin gave no question", false},
		{"a line with no label after a labeled one", "Q: Go?\nC: why.\nand some more of it", "stdin line 3 has no label", true},
		{"a wrapped field", "Q: Go?\n  and on\nC: why.", "stdin line 2 has no label", true},
		{"a labeled line after an unlabeled one", "Go?\nwhy.\nC: and why", "stdin line 3 starts with the label C:", true},
		{"an unknown label", "Q: Go?\nX: why.", "stdin line 2 has the label X:, which is none of them", true},
		{"a lower case label", "q: Go?", "stdin line 1 has the label q:", true},
		{"a label with no words", "Q: Go?\nC:", "stdin line 2 is C: with no words after it", true},
		{"a question with no words", "Q:\nC: why.", "stdin line 1 is Q: with no words after it", true},
		{"two questions", "Q: Go?\nC: why.\nQ: Or not?", "the label Q: on lines 1 and 3", false},
		{"two whys", "Q: Go?\nW: a\nW: b", "the label W: on lines 2 and 3", false},
		{"two commands", "Q: Go?\nR: a\nR: b", "the label R: on lines 2 and 3", false},
		{"two tests", "Q: Go?\nR: depot test web\nT: a\nT: b", "the label T: on lines 3 and 4", false},
		{"no question", "C: why.\nO: Go: it goes", "no Q: line", true},
		{"a test with no command", "Q: Go?\nC: why.\nT: go test ./...", "T: with no R: after it", false},
		{"a says line with no command", "Q: Go?\nC: why.\nS: action=opened event=issues -> done\n", "S: with no R: after it", false},
		{"a makes line with no command", "Q: Go?\nC: why.\nM: action=opened event=issues -> task:new\n", "M: with no R: after it", false},
	} {
		_, err := parseAskStdin(tc.in)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: parseAskStdin(%q) = %v, want %q", tc.name, tc.in, err, tc.want)
			continue
		}
		if tc.labels && !strings.Contains(err.Error(), "Q: the question, C: a line of context") {
			t.Errorf("%s: %v names no labels", tc.name, err)
		}
	}
}

// testBeadAskStdin is a step of TestBeadAsk: an ask on stdin stores what the
// flags would have, refuses text flags beside -, and leaves secret values
// out of stdin.
func testBeadAskStdin(t *testing.T) {
	t.Helper()
	st, ctx, id := askYard(t)
	a := &app{st: st, actor: "yardmaster"}
	ask := func(stdin string, args ...string) (string, error) {
		t.Helper()
		c := a.beadCmd()
		c.SetIn(strings.NewReader(stdin))
		return run(t, c, append([]string{"ask"}, args...)...)
	}
	notes := func() []store.Note {
		t.Helper()
		n, err := st.Notes(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	// The flags' ask, then the same ask on stdin in both forms: the notes
	// hold one text.
	if _, err := run(t, a.beadCmd(), askArgs(id)...); err != nil {
		t.Fatal(err)
	}
	if _, err := ask("Move the links now?\n64 links still point at the old source.\nThe move was tested on a copy and passed.\n", id, "-"); err != nil {
		t.Fatalf("an unlabeled ask: %v", err)
	}
	out, err := ask("Q: Move the links now?\nC: 64 links still point at the old source.\nC: The move was tested on a copy and passed.\n"+
		"O: Move them now: all 64 links move tonight\nO: After Friday: keep the old source\nW: the test passed\n", id, "-", "--recommend", "1")
	if err != nil {
		t.Fatalf("a labeled ask: %v", err)
	}
	for _, want := range []string{
		"ask 3 · open", "to mckean", "asked by yardmaster", "Move the links now?",
		"1. Move them now - all 64 links move tonight (recommended: the test passed)",
		"3. You decide - leave it to yardmaster",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the labeled ask printed no %q:\n%s", want, out)
		}
	}
	got := notes()
	if len(got) != 3 || got[2].Text != got[0].Text {
		t.Fatalf("the ask on stdin is not the ask of the flags:\n%+v", got)
	}
	if got[1].Text == got[0].Text || !strings.Contains(got[1].Text, `"question":"Move the links now?"`) || strings.Contains(got[1].Text, `"options"`) {
		t.Fatalf("the unlabeled ask = %s", got[1].Text)
	}

	// Flags that name no prose go beside it, as ever.
	if out, err := ask("Go?\nwhy.\n", id, "-", "--to", "ada"); err != nil || !strings.Contains(out, "to ada") {
		t.Fatalf("an ask to ada on stdin: %v\n%s", err, out)
	}

	// With -, no text is a flag's: each is refused, in words, and nothing is
	// stored.
	before := len(notes())
	for _, flag := range [][]string{{"--context", "why."}, {"--option", "Go: it goes"}, {"--why", "because"}, {"--run", "settings set max_sessions 20"}, {"--run-test", "go test ./..."}, {"--run-says", "action=opened event=issues -> done"}, {"--run-makes", "action=opened event=issues -> task:new"}} {
		if _, err := ask("Go?\nwhy.\n", append([]string{id, "-"}, flag...)...); err == nil ||
			!strings.Contains(err.Error(), "with -, put every text on stdin: "+flag[0]+" is given beside it") {
			t.Errorf("- with %s = %v", flag[0], err)
		}
	}
	for _, tc := range []struct{ name, in, want string }{
		{"nothing on stdin", "", "stdin gave no question"},
		{"a mix of forms", "Go?\nC: why.\n", "stdin line 2 starts with the label C:"},
		{"an unknown label", "Q: Go?\nC: why.\nZ: more.\n", "stdin line 3 has the label Z:"},
		{"a label with no words", "Q: Go?\nC: why.\nW:\n", "stdin line 3 is W: with no words after it"},
		{"a test with no command", "Q: Go?\nC: why.\nT: go test ./...\n", "T: with no R:"},
		{"no context", "Go?\n", "an ask needs context"},
	} {
		if _, err := ask(tc.in, id, "-"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	if len(notes()) != before {
		t.Fatalf("a refused ask left notes: %+v", notes()[before:])
	}

	// A secret flag names a source, a user, a crew member or a login, and
	// never reads stdin: its - is the name, so that nothing of a secret is in
	// the shell's history, and the answer to a secret ask is typed at a
	// terminal, never an argument. The question is typed here, so that any
	// stdin read would be a flag's.
	const unread = "a question\nand a line of context\n"
	for _, flag := range [][]string{{"--secret", "-"}, {"--secret", "-", "--for-user", "-"}, {"--secret", "-", "--for-crew", "-"}, {"--secret", "-", "--as", "-"}, {"--secret-login", "-"}, {"--secret-hook", "-", "--source", "-"}} {
		in := strings.NewReader(unread)
		c := a.beadCmd()
		c.SetIn(in)
		if _, err := run(t, c, append([]string{"ask", id, "The key?", "--context", "where to get it."}, flag...)...); err != nil && strings.Contains(err.Error(), "stdin") {
			t.Errorf("ask %v = %v, want no word of stdin", flag, err)
		}
		if in.Len() != len(unread) {
			t.Errorf("ask %v read stdin", flag)
		}
	}

}
