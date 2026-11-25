package store

import (
	"slices"
	"strings"
	"testing"
)

// A file's forget runs the kind's own program on one session: it must name
// the days, name arguments, and put the session's id in exactly one word of
// its own, so that no other word or placeholder is left for the yard to fill
// or guess at.
func TestForgetCheck(t *testing.T) {
	t.Parallel()
	args := func(a ...string) []string { return a }
	for _, c := range []struct {
		name   string
		forget *ForgetSource
		want   string // part of the refusal; "" where the file is fine
	}{
		{"no forget", nil, ""},
		{"codex's own", &ForgetSource{AfterDays: 7, Args: args("delete", "--force", "{session}")}, ""},
		{"the id alone", &ForgetSource{AfterDays: 1, Args: args("{session}")}, ""},
		{"no days", &ForgetSource{Args: args("delete", "{session}")}, "forget.after_days: 0 is not"},
		{"days below one", &ForgetSource{AfterDays: -3, Args: args("delete", "{session}")}, "forget.after_days: -3 is not"},
		{"no arguments", &ForgetSource{AfterDays: 7}, "forget.args: the arguments"},
		{"empty arguments", &ForgetSource{AfterDays: 7, Args: []string{}}, "forget.args: the arguments"},
		{"no id", &ForgetSource{AfterDays: 7, Args: args("delete", "--force")}, "0 words are {session}"},
		{"two ids", &ForgetSource{AfterDays: 7, Args: args("delete", "{session}", "{session}")}, "2 words are {session}"},
		{"the id inside a longer word", &ForgetSource{AfterDays: 7, Args: args("delete", "--id={session}")}, `"--id={session}" holds braces`},
		{"the id inside a longer word beside a whole one", &ForgetSource{AfterDays: 7, Args: args("{session}", "x-{session}")}, `"x-{session}" holds braces`},
		{"another placeholder", &ForgetSource{AfterDays: 7, Args: args("delete", "{level}", "{session}")}, `"{level}" holds braces`},
		{"an unclosed brace", &ForgetSource{AfterDays: 7, Args: args("delete", "{session", "{session}")}, `"{session" holds braces`},
	} {
		err := c.forget.check()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: check = %v, want none", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: check = %v, want one saying %q", c.name, err, c.want)
		}
	}
}

// Harness.Check holds a file to it, so a file that names a forget the yard
// cannot go by is refused when the files are loaded, not on the day it runs.
func TestHarnessCheckHoldsForget(t *testing.T) {
	t.Parallel()
	h := Harness{Kind: "codex", Version: FileVersion, Label: "Codex", Forget: &ForgetSource{AfterDays: 7, Args: []string{"delete", "{session}"}}}
	if err := h.Check(); err != nil {
		t.Fatalf("Check of a good forget = %v", err)
	}
	h.Forget.AfterDays = 0
	if err := h.Check(); err == nil || !strings.Contains(err.Error(), "forget.after_days") {
		t.Errorf("Check of a forget with no days = %v, want its refusal", err)
	}
}

// The id goes in the word that is {session} and nowhere else, and the file's
// own words are not changed by it.
func TestForgetArgsFor(t *testing.T) {
	t.Parallel()
	f := ForgetSource{AfterDays: 7, Args: []string{"delete", "--force", "{session}"}}
	if got, want := f.ArgsFor("01a2-b3"), []string{"delete", "--force", "01a2-b3"}; !slices.Equal(got, want) {
		t.Errorf("ArgsFor = %q, want %q", got, want)
	}
	if !slices.Equal(f.Args, []string{"delete", "--force", "{session}"}) {
		t.Errorf("ArgsFor changed the file's own words: %q", f.Args)
	}
}
