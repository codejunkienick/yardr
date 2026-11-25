package store

import (
	"fmt"
	"slices"
	"strings"
)

// ForgetSource is how the yard makes a harness forget a session it started,
// as its file says (forget): once the session has ended and its tokens are
// counted, and AfterDays days have passed, the yard runs the kind's own
// program with Args, the session's id in the one word that is {session}. A
// harness keeps a session's record for as long as it likes, and some keep a
// lot (Codex keeps each thread's history in a database of its own, which
// removing the session's file leaves behind), so the way to forget one is the
// harness's, not a file's to unlink.
//
// The file names no program: what runs is Executable(kind), as Flags.Version
// and Flags.OneShot run, never a shell, so the key can start nothing but the
// harness. A kind that names none keeps what it keeps.
type ForgetSource struct {
	// AfterDays is how many whole days after the session ended it is
	// forgotten: after the count of its tokens is recorded (the count reads
	// the same file, and is done within minutes), and long enough that the
	// session has been looked at if anyone was going to.
	AfterDays int `yaml:"after_days" json:"after_days"`
	// Args are the arguments of the kind's program that forget one session,
	// with {session} in a word of its own where the id of the agent's session
	// goes. They must not wait for an answer: nothing is typed to the program.
	Args []string `yaml:"args" json:"args"`
}

// ArgsFor are Args for the session whose id the agent kept: the one word
// {session} is the id. A program is given a word that is no option and no
// path, since the caller refuses an id that would be either.
func (f ForgetSource) ArgsFor(id string) []string {
	out := slices.Clone(f.Args)
	for i, a := range out {
		if a == sessionID {
			out[i] = id
		}
	}
	return out
}

// check finds what a file's forget says that yardr cannot go by: a command it
// would run for no session, for the wrong one, or never. {session} stands as a
// whole word, once: in a longer word the id would be part of something else (a
// path, a setting), and any other braces would be a placeholder this does not
// fill.
func (f *ForgetSource) check() error {
	if f == nil {
		return nil
	}
	switch {
	case f.AfterDays < 1:
		return fmt.Errorf("forget.after_days: %d is not a number of days, 1 or more", f.AfterDays)
	case len(f.Args) == 0:
		return fmt.Errorf("forget.args: the arguments of the kind's own program that forget a session are needed")
	}
	whole := 0
	for _, a := range f.Args {
		switch {
		case a == sessionID:
			whole++
		case strings.ContainsAny(a, "{}"):
			return fmt.Errorf("forget.args: %q holds braces: %s is the only placeholder, and it is a word of its own", a, sessionID)
		}
	}
	if whole != 1 {
		return fmt.Errorf("forget.args: %d words are %s, want exactly one: it is where the session's id goes", whole, sessionID)
	}
	return nil
}
