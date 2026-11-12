package store

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// generalYard is a list yard with general cards on, turned on by the
// operator in a terminal as the setting requires.
func generalYard(t *testing.T) *askYard {
	t.Helper()
	y := listYard(t)
	if err := y.SetSetting(y.ctx, GeneralCardsSetting, "true", "mckean"); err != nil {
		t.Fatal(err)
	}
	return y
}

func generalAsk(words ...string) Ask {
	return Ask{Question: "May I?", To: "mckean", Context: []string{"it is needed for the work"},
		Run: &AskRun{General: words}}
}

func generalRefused(t *testing.T, y *askYard, ask Ask, actor, want string) {
	t.Helper()
	if _, err := y.AddAsk(y.ctx, y.bead, ask, actor); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("AddAsk(%v) as %s = %v, want a refusal holding %q", ask.Run, actor, err, want)
	}
}

// A general card is refused at filing for what the list carries, for
// the verbatim never-list, for the alone words and for its own gates,
// each in words that say why.
func TestGeneralAskRefusals(t *testing.T) {
	t.Parallel()
	y := generalYard(t)
	for _, tc := range []struct {
		words []string
		want  string
	}{
		{[]string{"peer", "allow", "airy", "--mail-limit", "300"}, "the specific card is the only door"},
		{[]string{"group", "add", "g3", "--runner", "manual"}, "the specific card is the only door"},
		{[]string{"bead", "answer", "yardr-x", "1"}, "never carried"},
		{[]string{"bead", "answer"}, "never carried"},
		{[]string{"bead", "ask", "--take-back", "yardr-x", "1", "why"}, "never carried"},
		{[]string{"upgrade"}, "never carries"},
		{[]string{"upgrade", "--yes"}, "never carries"},
		{[]string{"--json", "upgrade"}, "no flag before it"},
		{[]string{"--json", "user", "device", "add", "ada", "phone"}, "no flag before it"},
		{[]string{"source", "add", "/tmp/x.yaml"}, "operator's alone"},
		{[]string{"source", "key", "crm"}, "operator's alone"},
		{[]string{"user", "device", "add", "ada", "phone"}, "operator's alone"},
		{[]string{"bead", "create", "--actor", "mckean", "Title"}, "--actor or --home"},
		{[]string{"bead", "create", "Title", "--home=/tmp/y"}, "--actor or --home"},
		{[]string{"peer", "allow", "airy", "--mail-limit", "--actor"}, "--actor or --home"},
		{[]string{"mail", "ada", "-"}, "carries no standard input"},
		{[]string{"bead", "create", "bad\x00word"}, "control character"},
		{[]string{"watch"}, "never carries"},
		{[]string{"watch", "yardr-x"}, "never carries"},
		{[]string{"settings", "set", "operator", "mckean"}, "operator's alone"},
		{[]string{"settings", "set", "operator"}, "operator's alone"},
		{[]string{"settings", "set", "operator="}, "operator's alone"},
		{[]string{"settings", "set", "operator", "Some", "Name"}, "operator's alone"},
		{[]string{"settings", "set", "operator", "what?"}, "operator's alone"},
		{[]string{"settings", "set", "yardmaster_delegation", "true"}, "operator's alone"},
		{[]string{"settings", "set", "general_cards", "true"}, "operator's alone"},
		{[]string{"settings", "set", "no_such_key", "x"}, "no setting"},
		{[]string{"settings", "set"}, "names no setting"},
		{[]string{"source", "set", "crm", "env_file=/tmp/x"}, "never names env_file"},
		{[]string{"source", "set", "crm", "env_file="}, "never names env_file"},
		{[]string{"source", "set", "crm", "env_file"}, "never carries"},
		{[]string{"source", "set", "crm", "mint_command=/bin/mint"}, "never carries"},
		{[]string{"source", "set", "crm", "mint_command=/bin/mint?x"}, "never carries"},
		{[]string{"source", "set", "crm", "calls_per_hour"}, "names no setting"},
		{[]string{"source", "set", "crm"}, "names no setting"},
		{[]string{"user", "key"}, "never carries"},
		{[]string{"user", "key", "ada", "crm"}, "never carries"},
		{[]string{"down"}, "never carries"},
		{[]string{"down", "--yes"}, "never carries"},
		{[]string{"uninstall", "--yes"}, "never carries"},
		{[]string{"daemon", "status"}, "never carries"},
		{[]string{"server"}, "never carries"},
		{[]string{"reply", "1", "hi"}, "never carries"},
		{[]string{"react", "1"}, "never carries"},
		{[]string{"inbox"}, "never carries"},
		{[]string{"mail", "ada", "--with", "3", "hi"}, "never carries"},
		{[]string{"mail", "ada", "--file=/tmp/x", "hi"}, "never carries"},
		{[]string{"call", "crm", "find", "--as", "ada"}, "never carries"},
		{[]string{"call", "crm", "find", "--as=ada"}, "never carries"},
		{[]string{"crew", "add", "jo", "--set", "peers=airy"}, "operator's alone"},
		{[]string{"crew", "add", "jo", "--set", "open_threads=crm"}, "operator's alone"},
		{[]string{"group", "set", "builders", "env_file=/tmp/x"}, "never names env_file"},
		{[]string{"group", "add", "gx", "--set", "env_file=/tmp/x"}, "never names env_file"},
		{[]string{"bead", "create", "env_file=/tmp/x"}, "never names env_file"},
		{[]string{"group", "set", "builders", "log=relative.log"}, "is relative"},
		{[]string{"group", "set", "builders", "prompt=relative.md"}, "is relative"},
	} {
		generalRefused(t, y, generalAsk(tc.words...), "yardmaster", tc.want)
	}
	generalRefused(t, y, Ask{Question: "May I?", To: "mckean", Context: []string{"c"},
		Run: &AskRun{Argv: []string{"bead create x"}, General: []string{"bead", "create", "x"}}},
		"yardmaster", "verbatim words alone")
	generalRefused(t, y, Ask{Question: "May I?", To: "mckean", Context: []string{"c"},
		Run: &AskRun{General: []string{}}}, "yardmaster", "names no command")
	generalRefused(t, y, generalAsk("yardr"), "yardmaster", "names no command")
	generalRefused(t, y, generalAsk("bead", "create", "x"), "ada", "yardmaster's")
	generalRefused(t, y, Ask{Question: "May I?", To: "ada", Context: []string{"c"},
		Run: &AskRun{General: []string{"bead", "create", "x"}}}, "yardmaster", "to approve")
}

// What no row carries, in a form its row refuses, files as a general
// card with the words whole: an Askable key the specific card cannot
// hold, free group and crew keys, and text the character walk refuses.
func TestGeneralAskFiles(t *testing.T) {
	t.Parallel()
	y := generalYard(t)
	for _, tc := range []struct {
		name  string
		words []string
	}{
		{"an Askable key with a spaced value", []string{"settings", "set", "hook_public_url", "https://yard.example/my", "hooks"}},
		{"an Askable key with a ? value", []string{"settings", "set", "hook_public_url", "https://yard.example/hooks?x=1"}},
		{"an Askable key set empty in one word", []string{"settings", "set", "hook_public_url="}},
		{"a bead title with a ?", []string{"bead", "create", "Fix", "the", "?", "bug"}},
		{"a group add key the row refuses", []string{"group", "add", "g2", "--set", "model=opus"}},
		{"an inexact alone form", []string{"user", "device", "add", "ada"}},
		{"group args", []string{"group", "set", "builders", "args=--careful"}},
		{"group unset of its command", []string{"group", "set", "builders", "--unset", "cmd"}},
		{"group runner exec", []string{"group", "set", "builders", "runner=exec"}},
		{"crew env_command", []string{"crew", "add", "jo2", "--set", "env_command=make env"}},
		{"a source base set empty", []string{"source", "set", "crm", "base="}},
		{"an absolute file word", []string{"group", "set", "builders", "log=/tmp/x.log"}},
		{"literal mail text", []string{"mail", "ada", "hello there"}},
	} {
		seq := y.ask(t, generalAsk(tc.words...), "yardmaster")
		run := y.asked(t, seq).Ask.Run
		if run == nil || !slices.Equal(run.General, tc.words) || !slices.Equal(run.Argv, tc.words) {
			t.Errorf("%s: the ask stores argv %v general %v, want the words %v whole as both", tc.name, run.Argv, run.General, tc.words)
		}
		if run.Description != generalDescription || run.Back != "" || run.BackBy != "" {
			t.Errorf("%s: the card reads description %q back %q backBy %q", tc.name, run.Description, run.Back, run.BackBy)
		}
	}
	// A setter typed with its space rides as one word, whole.
	seq := y.ask(t, generalAsk("group", "set", "builders", "cmd =x"), "yardmaster")
	if run := y.asked(t, seq).Ask.Run; len(run.General) != 4 || run.General[3] != "cmd =x" {
		t.Errorf("cmd =x stores %v, want the four words with the last whole", run.General)
	}
	// A leading yardr is no part of the stored command.
	seq = y.ask(t, generalAsk("yardr", "bead", "create", "Hello"), "yardmaster")
	if run := y.asked(t, seq).Ask.Run; !slices.Equal(run.General, []string{"bead", "create", "Hello"}) {
		t.Errorf("a leading yardr stores %v", run.General)
	}
}

// The card shows the words numbered with their lengths, through
// Letters, cut past 128 with the cut marked, file words marked
// unread, and says it is unchecked with no way back, good until when,
// and that what runs is told to the asker.
func TestGeneralCardWords(t *testing.T) {
	t.Parallel()
	y := generalYard(t)
	long := strings.Repeat("a", 200)
	seq := y.ask(t, generalAsk("bead", "create", long, "log=/tmp/x.log", "bad\u202eword", NoteFilesMark+" files"), "yardmaster")
	run := y.asked(t, seq).Ask.Run
	does := strings.Join(run.Does, "\n")
	for _, want := range []string{
		"The yard has not checked this command",
		"No way back is shown",
		"It is good until ",
		"What the command prints is told to the asker.",
		"1: (4) bead",
		"2: (6) create",
		"(not read by the yard)",
	} {
		if !strings.Contains(does, want) {
			t.Errorf("the card does not say %q:\n%s", want, does)
		}
	}
	if !strings.Contains(does, "[+72 cut]") || !strings.Contains(does, "(200)") {
		t.Errorf("the long word is not cut with its length marked:\n%s", does)
	}
	if strings.ContainsRune(does, '\u202e') || strings.Contains(does, NoteFilesMark) {
		t.Errorf("the card keeps a direction or files mark Letters strips:\n%q", does)
	}
	expiry, err := time.Parse(time.RFC3339, run.Seen["general expires"])
	if err != nil || len(run.Seen) != 1 {
		t.Fatalf("Seen = %v, %v, want the deadline alone", run.Seen, err)
	}
	if until := time.Until(expiry); until < 59*time.Minute || until > 61*time.Minute {
		t.Errorf("the card is good for %v, want an hour", until)
	}
	if got := y.asked(t, seq).Ask.Weight; len(got) != 1 || got[0].Class != WeightCode || !StrongWeight(got) {
		t.Errorf("the card weighs %v, want code and strong", got)
	}
}

// Approving a general card records it to be carried out, once: a
// second approval is refused, and a stored form the yard did not
// write runs nothing.
func TestGeneralApprovalCarriesOnce(t *testing.T) {
	t.Parallel()
	y := generalYard(t)
	words := []string{"bead", "create", "Held", "for", "review"}
	seq := y.ask(t, generalAsk(words...), "yardmaster")
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil)
	if err != nil || done.Result != CarryPending || !slices.Equal(done.Argv, words) {
		t.Fatalf("ApproveAsk = %+v, %v, want CarryPending with the words", done, err)
	}
	rec, err := y.CarryOf(y.ctx, y.bead, seq)
	if err != nil || rec == nil || !rec.General || rec.State != CarryApproved || !slices.Equal(rec.Argv, words) || rec.Actor != "mckean" {
		t.Errorf("the carrying record = %+v, %v, want a general approved one", rec, err)
	}
	if !needsCarry(*y.asked(t, seq).Ask.Run) {
		t.Error("needsCarry holds no general ask")
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "approved already") {
		t.Errorf("a second approval = %v, want approved already", err)
	}
	// The stored form is held whole: another Argv under the same
	// General runs nothing.
	seq2 := y.ask(t, generalAsk("bead", "create", "Other"), "yardmaster")
	if err := y.write(func(w *wtx) error {
		row, err := w.loadBead(y.bead)
		if err != nil {
			return err
		}
		notes, err := beadNotes(w.Tx, row.UUID)
		if err != nil {
			return err
		}
		for _, n := range notes {
			if n.Seq != seq2 || n.Kind != NoteAsk {
				continue
			}
			ask, err := ParseAsk(n)
			if err != nil {
				return err
			}
			ask.Run.Argv = []string{"bead", "create", "Tampered"}
			raw, err := json.Marshal(ask)
			if err != nil {
				return err
			}
			n.Text = string(raw)
			return put(w.Tx, bNotes, join(row.UUID, seqKey(n.Seq)), n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq2, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "did not write") {
		t.Errorf("a tampered approval = %v, want the stored-form refusal", err)
	}
}

// Before its deadline a general approval is held to the fresh card;
// past it nothing runs and the ask stays open.
func TestGeneralExpiry(t *testing.T) {
	y := generalYard(t)
	pinClock(t, &y.now)
	seq := y.ask(t, generalAsk("bead", "create", "Soon"), "yardmaster")
	y.now = y.now.Add(30 * time.Minute)
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatalf("before the deadline: %v", err)
	}
	seq2 := y.ask(t, generalAsk("bead", "create", "Late"), "yardmaster")
	y.now = y.now.Add(2 * time.Hour)
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq2, "mckean", true, nil); err == nil ||
		!strings.Contains(err.Error(), "has expired") || !strings.Contains(err.Error(), nothingRan) {
		t.Errorf("past the deadline = %v, want the expiry with nothing ran", err)
	}
	if got := y.asked(t, seq2).State; got == AskAnswered {
		t.Errorf("an expired approval answered the ask: %v", got)
	}
}

// A general card's words and what it ran are held from other devices,
// and nothing of it crosses a link: no copy is queued, and a bead
// sent to a peer carries none of its ask or answer notes.
func TestGeneralHeldAndNoLink(t *testing.T) {
	t.Parallel()
	y := generalYard(t)
	seq := y.ask(t, generalAsk("bead", "create", "Secret", "plans"), "yardmaster")
	run := y.asked(t, seq).Ask.Run
	if !RunHeld(*run) || RunHeld(AskRun{Argv: []string{"peer", "allow", "airy", "--mail-limit", "300"}}) {
		t.Error("RunHeld does not hold exactly the general words")
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	// The approval's event carries the held mark, and a device that is
	// no operator's is told no sentence for it.
	events, err := y.Events(y.ctx, y.bead, 100)
	if err != nil {
		t.Fatal(err)
	}
	marked := false
	for _, e := range events {
		if held, _ := e.Data["answer_held"].(bool); held {
			marked = true
			if told, wasHeld := e.WithoutHeld(); !wasHeld {
				t.Error("the marked event reads as told whole")
			} else if _, ok := told.Data["answer"]; ok {
				t.Error("the held sentence is told to a device that is no operator's")
			}
		}
	}
	if !marked {
		t.Error("no approval event carries the held mark")
	}
	// No copy of the ask waits for a peer, where a plain ask for the
	// operator is copied by reach: a run ask leaves by no link, and a
	// bead sent to a peer carries plain notes alone (peersend.go).
	if err := y.PeerHeard(y.ctx, "airy", y.asKey(t), &PeerSays{Understands: []string{PeerMail, PeerAsk},
		Roster: []PeerRosterEntry{{Name: "mckean", Kind: RosterUser, Mail: true}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AllowPeer(y.ctx, "airy", PeerAllow{Send: []string{PeerAsk}, Receive: []string{PeerAsk},
		Trust: map[string]string{"mckean": "mckean"}, Reach: map[string]string{"mckean": "mckean"}}, "mckean"); err != nil {
		t.Fatal(err)
	}
	copies := func(seq uint64) int {
		t.Helper()
		var out []askCopy
		if err := y.write(func(w *wtx) error {
			row, err := w.loadBead(y.bead)
			if err != nil {
				return err
			}
			_, err = get(w.Tx, bAskCopies, join(row.UUID, seqKey(seq)), &out)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return len(out)
	}
	if plain := y.ask(t, Ask{Question: "Which?", To: "mckean", Context: []string{"c"}}, "yardmaster"); copies(plain) != 1 {
		t.Fatalf("the test wants a link that copies a plain ask: %d copies", copies(plain))
	}
	if copies(seq) != 0 {
		t.Errorf("the general ask was copied to %d names, want none", copies(seq))
	}
	incoming := Ask{Question: "May I?", To: "mckean@airy", Context: []string{"c"}, Run: run}
	if validAsk("mckean", incoming) {
		t.Error("a general ask reads as one a link carries in")
	}
	incoming.Run = nil
	if !validAsk("mckean", incoming) {
		t.Error("the test wants an ask taken but for its general command")
	}
}

// A general record an earlier boot claimed is finished as failed at
// the next start and never carried again; one only approved still
// carries.
func TestGeneralResumeFailsClaimedOnce(t *testing.T) {
	t.Parallel()
	y := generalYard(t)
	seq := y.ask(t, generalAsk("bead", "create", "Once"), "yardmaster")
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	claimed, err := y.ClaimCarry(y.ctx, "boot-old", time.Now())
	if err != nil || claimed == nil || claimed.Ask != seq {
		t.Fatalf("ClaimCarry = %+v, %v", claimed, err)
	}
	if _, err := y.ResumeCarry(y.ctx, "boot-new"); err != nil {
		t.Fatal(err)
	}
	rec, err := y.CarryOf(y.ctx, y.bead, seq)
	if err != nil || rec == nil || rec.State != CarryFailed ||
		rec.Result != "the yard stopped while this ran; whether it finished is not known. Ask again if it is still wanted." {
		t.Errorf("the resumed record = %+v, %v, want failed with the unknown words", rec, err)
	}
	if got := y.asked(t, seq); got.Current == nil || got.Current.Answer.Ran == nil || !got.Current.Answer.Ran.Failed {
		t.Errorf("the resumed answer = %+v, want failed", got.Current)
	}
	if next, err := y.ClaimCarry(y.ctx, "boot-new", time.Now()); err != nil || next != nil {
		t.Errorf("ClaimCarry after the resume = %+v, %v, want nothing: it never runs twice", next, err)
	}
	seq2 := y.ask(t, generalAsk("bead", "create", "Twice"), "yardmaster")
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq2, "mckean", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ResumeCarry(y.ctx, "boot-new"); err != nil {
		t.Fatal(err)
	}
	next, err := y.ClaimCarry(y.ctx, "boot-new", time.Now())
	if err != nil || next == nil || next.Ask != seq2 || !next.General {
		t.Errorf("an approved general record after a resume = %+v, %v, want it carried", next, err)
	}
}

// The stored words with General stripped are refused by a build
// without the row, through its own unknown and alone words: mixed
// versions fail closed.
func TestGeneralStrippedRefused(t *testing.T) {
	t.Parallel()
	y := generalYard(t)
	seq := y.ask(t, generalAsk("group", "set", "builders", "args=--careful"), "yardmaster")
	stored := y.asked(t, seq).Ask.Run.Argv
	if _, err := y.AddAsk(y.ctx, y.bead, listAsk("mckean", stored...), "yardmaster"); err == nil ||
		!strings.Contains(err.Error(), "operator's alone") {
		t.Errorf("the stripped words as --run = %v, want the alone refusal", err)
	}
}

// With the setting off, or filed by anyone but the yardmaster, a
// general card is refused before its words are read.
func TestGeneralAskGates(t *testing.T) {
	t.Parallel()
	off := listYard(t)
	generalRefused(t, off, generalAsk("bead", "create", "x"), "yardmaster", "are off")
	y := generalYard(t)
	if _, err := y.PutCrew(y.ctx, CrewMember{Name: "helper", Runner: "herdr", Dir: "/yard"}, "mckean"); err != nil {
		t.Fatal(err)
	}
	generalRefused(t, y, generalAsk("bead", "create", "x"), "helper", "yardmaster's")
}
