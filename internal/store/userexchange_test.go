package store_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
)

// exchangeThread writes jo's question to ada with ada's answer and jo's
// answer to that, and returns the three seqs oldest first.
func exchangeThread(t *testing.T, api store.API) (uint64, uint64, uint64) {
	t.Helper()
	ctx := context.Background()
	if err := api.Mail(ctx, "ada", "when is the freeze over?", "jo"); err != nil {
		t.Fatal(err)
	}
	first := replySeq(t, api)
	if _, err := api.ReplyMail(ctx, first, "Friday.", "ada"); err != nil {
		t.Fatal(err)
	}
	box, err := api.UserInbox(ctx, "jo", 0, "jo")
	if err != nil || len(box.Mail) != 1 {
		t.Fatalf("jo's inbox = %+v, %v", box, err)
	}
	second := box.Mail[0].Seq
	if _, err := api.ReplyMail(ctx, second, "land it, then.", "jo"); err != nil {
		t.Fatal(err)
	}
	box, err = api.UserInbox(ctx, "ada", 0, "ada")
	if err != nil || len(box.Mail) != 2 {
		t.Fatalf("ada's inbox = %+v, %v", box, err)
	}
	return first, second, box.Mail[1].Seq
}

// One letter with its exchange: ancestors, own sent replies and replies,
// oldest first, texts and answered-links pinned. A sent anchor reads, as a
// received one does, and an operator reads another user's.
func TestMailExchangeServesThread(t *testing.T) {
	t.Parallel()
	api, _ := replyYard(t)
	ctx := context.Background()
	first, second, third := exchangeThread(t, api)
	for _, tc := range []struct {
		name         string
		user         string
		anchor       uint64
		actor        string
		wantTexts    []string
		wantFrom     []string
		wantTo       []string
		wantAnswered []uint64
	}{
		{"oldest anchor", "ada", first, "ada",
			[]string{"when is the freeze over?", "Friday.", "land it, then."},
			[]string{"jo", "ada", "jo"}, []string{"ada", "jo", "ada"},
			[]uint64{0, first, second}},
		{"newest anchor", "jo", third, "jo",
			[]string{"when is the freeze over?", "Friday.", "land it, then."},
			[]string{"jo", "ada", "jo"}, []string{"ada", "jo", "ada"},
			[]uint64{0, first, second}},
		{"sent anchor", "ada", second, "ada",
			[]string{"when is the freeze over?", "Friday.", "land it, then."},
			[]string{"jo", "ada", "jo"}, []string{"ada", "jo", "ada"},
			[]uint64{0, first, second}},
		{"operator reads another's", "jo", second, "ada",
			[]string{"when is the freeze over?", "Friday.", "land it, then."},
			[]string{"jo", "ada", "jo"}, []string{"ada", "jo", "ada"},
			[]uint64{0, first, second}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ex, err := api.MailExchange(ctx, tc.user, tc.anchor, tc.actor)
			if err != nil {
				t.Fatal(err)
			}
			if ex.Truncated {
				t.Fatalf("the whole thread reads truncated: %+v", ex)
			}
			if len(ex.Letters) != 3 {
				t.Fatalf("the exchange = %+v, want the three letters", ex.Letters)
			}
			for i, l := range ex.Letters {
				if l.Text != tc.wantTexts[i] || l.From != tc.wantFrom[i] ||
					l.To != tc.wantTo[i] || l.Answered != tc.wantAnswered[i] {
					t.Fatalf("letter %d = %+v", i, l)
				}
				if i > 0 && ex.Letters[i-1].Seq >= l.Seq {
					t.Fatalf("the exchange is not oldest first: %+v", ex.Letters)
				}
			}
			if ex.Letters[0].Seq != first || ex.Letters[1].Seq != second || ex.Letters[2].Seq != third {
				t.Fatalf("the exchange seqs = %d, %d, %d, want %d, %d, %d",
					ex.Letters[0].Seq, ex.Letters[1].Seq, ex.Letters[2].Seq, first, second, third)
			}
		})
	}
}

// A number that is no mail of theirs is refused in ReplyMail's blind
// sentence: missing, what is not mail, another's, and no number at all
// read alike, and none of them is a not-found. A user the yard has not is
// no mail either, and reading another's exchange is refused as reading
// their inbox is.
func TestMailExchangeRefusesBlindAlike(t *testing.T) {
	t.Parallel()
	api, _ := replyYard(t)
	ctx := context.Background()
	if err := api.Mail(ctx, "ada", "when is the freeze over?", "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.AddUser(ctx, "sam", "ada"); err != nil {
		t.Fatal(err)
	}
	if err := api.Mail(ctx, "sam", "the shed is painted", "jo"); err != nil {
		t.Fatal(err)
	}
	box, err := api.UserInbox(ctx, "sam", 0, "sam")
	if err != nil || len(box.Mail) != 1 {
		t.Fatalf("sam's inbox = %+v, %v", box, err)
	}
	samSeq := box.Mail[0].Seq
	evs, err := api.Events(ctx, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	var built uint64
	for _, e := range evs {
		if e.Kind != "mail" {
			built = e.Seq
			break
		}
	}
	if built == 0 {
		t.Fatal("the log holds no event that is not mail")
	}
	for _, n := range []uint64{0, 999999, built, samSeq} {
		_, err := api.MailExchange(ctx, "ada", n, "ada")
		want := fmt.Sprintf("I can't find message %d among your mail; read_my_mail lists the numbers", n)
		if err == nil || err.Error() != want {
			t.Fatalf("MailExchange(%d) = %v, want %q", n, err, want)
		}
		if errors.Is(err, store.ErrNotFound) {
			t.Fatalf("MailExchange(%d) = %v, want no not-found", n, err)
		}
	}
	if _, err := api.MailExchange(ctx, "nobody", 1, "ada"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another yard's user = %v, want not-found", err)
	}
	if _, err := api.MailExchange(ctx, "ada", samSeq, "jo"); err == nil || strings.Contains(err.Error(), "I can't find") {
		t.Fatalf("jo reading ada's exchange = %v, want the inbox refusal", err)
	}
}

// A chain broken on a pruned letter ends truncated: the walk stops where
// the log no longer has the ancestor, and the pruned anchor itself is no
// mail of theirs.
func TestMailExchangeTruncatedOnPruned(t *testing.T) {
	t.Parallel()
	api, own := replyYard(t)
	ctx := context.Background()
	first, _, third := exchangeThread(t, api)
	// The prune stops at the first young event, so everything through the
	// ancestor goes old with it; the setup is state and stays.
	if err := own.Backdate(1, first, time.Now().Add(-100*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if res, err := api.Prune(ctx, keep90, "ada"); err != nil || res.Events != int(first) {
		t.Fatalf("prune = %+v, %v", res, err)
	}
	ex, err := api.MailExchange(ctx, "ada", third, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if !ex.Truncated || len(ex.Letters) != 2 {
		t.Fatalf("the pruned exchange = %+v, want two letters truncated", ex)
	}
	if ex.Letters[0].Answered != first || ex.Letters[1].Answered != ex.Letters[0].Seq {
		t.Fatalf("the pruned exchange links = %+v", ex.Letters)
	}
	if _, err := api.MailExchange(ctx, "ada", first, "ada"); err == nil ||
		err.Error() != fmt.Sprintf("I can't find message %d among your mail; read_my_mail lists the numbers", first) {
		t.Fatalf("the pruned anchor = %v, want the blind sentence", err)
	}
}

// Marking one letter read marks it alone: older unread stays unread, and
// the receipt names the letter, not a cursor through it. Marking again,
// and marking what is no received mail of theirs, is silent and writes
// nothing; an acknowledgement through drops the single marks it passed.
func TestAckUserMailMarksOneAlone(t *testing.T) {
	t.Parallel()
	api, _ := replyYard(t)
	ctx := context.Background()
	if err := api.Mail(ctx, "ada", "first note", "jo"); err != nil {
		t.Fatal(err)
	}
	if err := api.Mail(ctx, "ada", "second note", "jo"); err != nil {
		t.Fatal(err)
	}
	box, err := api.UserInbox(ctx, "ada", 0, "ada")
	if err != nil || len(box.Mail) != 2 {
		t.Fatalf("ada's inbox = %+v, %v", box, err)
	}
	first, second := box.Mail[0].Seq, box.Mail[1].Seq
	if err := api.AckUserMail(ctx, "ada", second, "ada"); err != nil {
		t.Fatal(err)
	}
	box, err = api.UserInbox(ctx, "ada", 0, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if box.Unread != 1 {
		t.Fatalf("unread after marking one = %d, want the older one", box.Unread)
	}
	read := map[uint64]bool{}
	for _, l := range box.Mail {
		read[l.Seq] = l.Read
	}
	if read[first] || !read[second] {
		t.Fatalf("read = %+v, want only %d", read, second)
	}
	u, err := api.GetUser(ctx, "ada")
	if err != nil || len(u.MailRead) != 1 || u.MailRead[0] != second {
		t.Fatalf("ada's single marks = %+v, %v", u, err)
	}
	reads, err := api.EventsOfKind(ctx, "mail_read", 10)
	if err != nil || len(reads) != 1 {
		t.Fatalf("mail_read events = %+v, %v", reads, err)
	}
	if seq, _ := reads[0].Data["seq"].(float64); uint64(seq) != second {
		t.Fatalf("the receipt names %+v, want seq %d", reads[0].Data, second)
	}
	if _, has := reads[0].Data["through"]; has {
		t.Fatalf("the receipt carries a cursor: %+v", reads[0].Data)
	}
	// Marking again, and marking what is no received mail of theirs, is
	// silent and writes nothing.
	for _, n := range []uint64{second, 999999} {
		if err := api.AckUserMail(ctx, "ada", n, "ada"); err != nil {
			t.Fatalf("re-marking %d = %v, want silence", n, err)
		}
	}
	if err := api.Mail(ctx, "jo", "sent, not received", "ada"); err != nil {
		t.Fatal(err)
	}
	sent, err := api.UserInbox(ctx, "jo", 0, "jo")
	if err != nil || len(sent.Mail) != 1 {
		t.Fatalf("jo's inbox = %+v, %v", sent, err)
	}
	if err := api.AckUserMail(ctx, "ada", sent.Mail[0].Seq, "ada"); err != nil {
		t.Fatal(err)
	}
	if reads, err := api.EventsOfKind(ctx, "mail_read", 10); err != nil || len(reads) != 1 {
		t.Fatalf("mail_read events after the silent marks = %+v, %v", reads, err)
	}
	if u, err := api.GetUser(ctx, "ada"); err != nil || len(u.MailRead) != 1 {
		t.Fatalf("ada's single marks after the silent marks = %+v, %v", u, err)
	}
	// An acknowledgement through drops the single marks it passed.
	if err := api.AckUserInbox(ctx, "ada", first, "ada"); err != nil {
		t.Fatal(err)
	}
	if u, err := api.GetUser(ctx, "ada"); err != nil || len(u.MailRead) != 1 {
		t.Fatalf("ada's single marks after an earlier through = %+v, %v", u, err)
	}
	if err := api.AckUserInbox(ctx, "ada", second, "ada"); err != nil {
		t.Fatal(err)
	}
	if u, err := api.GetUser(ctx, "ada"); err != nil || len(u.MailRead) != 0 {
		t.Fatalf("ada's single marks after a through past them = %+v, %v", u, err)
	}
}
