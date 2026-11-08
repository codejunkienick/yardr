package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
	"yardr.dev/yardr/internal/yardkey"
)

// replyYard is a yard with users ada (operator) and jo, for answering mail.
func replyYard(t *testing.T) (store.API, *store.Store) {
	t.Helper()
	api, own := storetest.NewOwned(t)
	ctx := context.Background()
	if _, err := api.AddUser(ctx, "ada", "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.AddUser(ctx, "jo", "ada"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetSetting(ctx, store.OperatorSetting, "ada", "ada"); err != nil {
		t.Fatal(err)
	}
	return api, own
}

// replySeq is the seq of ada's one mail.
func replySeq(t *testing.T, api store.API) uint64 {
	t.Helper()
	box, err := api.UserInbox(context.Background(), "ada", 0, "ada")
	if err != nil || len(box.Mail) != 1 {
		t.Fatalf("ada's inbox = %+v, %v", box, err)
	}
	return box.Mail[0].Seq
}

// linkNorth links the peer north, receiving mail and exposing ada: the name
// peer mail to ada is taken under. It answers the key the intake is signed
// with.
func linkNorth(t *testing.T, api store.API) string {
	t.Helper()
	ctx := context.Background()
	k, _, err := yardkey.Ensure(filepath.Join(t.TempDir(), "north.key"))
	if err != nil {
		t.Fatal(err)
	}
	key := store.Public(k)
	if _, err := api.AddPeer(ctx, store.Peer{Name: "north", Key: key, URL: "http://north.test"}, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.AllowPeer(ctx, "north", store.PeerAllow{Receive: []string{"mail"}, Expose: []string{"ada"}}, "ada"); err != nil {
		t.Fatal(err)
	}
	return key
}

// takePeerMail takes one mail from the peer, as the receiver takes it.
func takePeerMail(t *testing.T, own *store.Store, key, id, to, from, text string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"to": to, "from": from, "text": text})
	got, err := own.PeerReceive(context.Background(), "north", key, []store.PeerMessage{{ID: id, Kind: store.PeerMail, Body: body}})
	if err != nil || got[0].Refused != "" {
		t.Fatalf("PeerReceive = %+v, %v", got, err)
	}
}

// replySessionPane is the pid of the pane the session-mail test makes up:
// no process has it, so none of the test's own calls is taken for the
// session's.
const replySessionPane = 1 << 30

// sessionMailToUser is mail to to from the agent working yd-1, written from
// its pane, so the mail names its bead.
func sessionMailToUser(t *testing.T, api store.API, own *store.Store, to, text string) {
	t.Helper()
	ctx := context.Background()
	if err := api.PutGroup(ctx, store.Group{Name: "g", Runner: "fake"}, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := api.CreateBead(ctx, store.NewBead{ID: "yd-1", Title: "the shed", Stage: "new"}, "ada"); err != nil {
		t.Fatal(err)
	}
	sess, err := api.Claim(ctx, "yd-1", "g", "new", "yard")
	if err != nil {
		t.Fatal(err)
	}
	if err := api.MarkRunning(ctx, sess.ID, map[string]any{"agent": "yd-1-new", "shell_pid": replySessionPane}); err != nil {
		t.Fatal(err)
	}
	if err := own.Mail(store.WithCaller(ctx, replySessionPane), to, text, "yd-1-new"); err != nil {
		t.Fatal(err)
	}
}

// A reply answers mail to its sender and carries the answered seq: the
// sender's inbox and the conversation read it back as the letter's link,
// and plain mail carries no answered key.
func TestMailReplyAnswersThread(t *testing.T) {
	t.Parallel()
	api, _ := replyYard(t)
	ctx := context.Background()
	if err := api.Mail(ctx, "ada", "when is the freeze over?", "jo"); err != nil {
		t.Fatal(err)
	}
	seq := replySeq(t, api)
	if err := api.MailReply(ctx, "jo", "Friday.", "ada", seq); err != nil {
		t.Fatal(err)
	}
	jo, err := api.UserInbox(ctx, "jo", 0, "jo")
	if err != nil || len(jo.Mail) != 1 || jo.Mail[0].From != "ada" ||
		jo.Mail[0].Text != "Friday." || jo.Mail[0].Answered != seq {
		t.Fatalf("jo's inbox = %+v, %v", jo, err)
	}
	letters, _, err := api.Conversation(ctx, "ada", "jo", 0, 0, 0)
	if err != nil || len(letters) != 2 || letters[0].Answered != 0 || letters[1].Answered != seq {
		t.Fatalf("the conversation = %+v, %v", letters, err)
	}
	plain, _ := json.Marshal(letters[0])
	if strings.Contains(string(plain), "answered") {
		t.Errorf("plain mail carries an answered key: %s", plain)
	}
	reply, _ := json.Marshal(letters[1])
	if !strings.Contains(string(reply), `"answered":`) {
		t.Errorf("the reply carries no answered key: %s", reply)
	}
}

// What is no answer is refused, and nothing is sent: no mail of that
// number, what is not mail, mail to somebody else, a recipient that is
// not the sender, and a text no mail holds.
func TestMailReplyRefusals(t *testing.T) {
	t.Parallel()
	api, _ := replyYard(t)
	ctx := context.Background()
	if err := api.Mail(ctx, "ada", "when is the freeze over?", "jo"); err != nil {
		t.Fatal(err)
	}
	seq := replySeq(t, api)
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
	for _, tc := range []struct {
		name            string
		to, text, actor string
		answered        uint64
		want            string
		wantNotFound    bool
	}{
		{"no mail of that number", "jo", "Friday.", "ada", 999999, "message 999999", true},
		{"what is not mail", "jo", "Friday.", "ada", built, "not mail", false},
		{"mail to somebody else", "ada", "mine now", "jo", seq, "not mail to jo", false},
		{"another recipient", "nobody", "Friday.", "ada", seq, "is from jo, not nobody", false},
		{"an empty text", "jo", "  ", "ada", seq, "mail must be 1..", false},
		{"a text no mail holds", "jo", strings.Repeat("x", store.MaxMailBytes+1), "ada", seq, "mail must be 1..", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := api.MailReply(ctx, tc.to, tc.text, tc.actor, tc.answered)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("MailReply = %v, want %q", err, tc.want)
			}
			if tc.wantNotFound && !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("MailReply = %v, want not found", err)
			}
		})
	}
	mails, err := api.EventsOfKind(ctx, "mail", 10)
	if err != nil || len(mails) != 1 {
		t.Fatalf("mail events after the refusals = %+v, %v", mails, err)
	}
}

// Mail from another yard is not answered here: the refusal names the
// terminal and the commands that answer there, and nothing is sent.
func TestMailReplyRefusesPeerMail(t *testing.T) {
	t.Parallel()
	api, own := replyYard(t)
	ctx := context.Background()
	key := linkNorth(t, api)
	takePeerMail(t, own, key, "m1", "ada", "clerk", "is the ledger closed?")
	seq := replySeq(t, api)
	err := api.MailReply(ctx, "clerk@north", "Friday.", "ada", seq)
	for _, want := range []string{"another yard", "clerk@north", "terminal", "yardr mail clerk@north", "yardr reply"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("MailReply = %v, want %q", err, want)
		}
	}
	mails, merr := api.EventsOfKind(ctx, "mail", 10)
	if merr != nil || len(mails) != 1 {
		t.Fatalf("mail events after the refusal = %+v, %v", mails, merr)
	}
}

// Mail from a bead's session is not answered by mail: its answer is a
// note, and the refusal names the ways, with no yard noun in them.
func TestMailReplyRefusesSessionMail(t *testing.T) {
	t.Parallel()
	api, own := replyYard(t)
	ctx := context.Background()
	sessionMailToUser(t, api, own, "ada", "may I narrow it to the shed?")
	seq := replySeq(t, api)
	err := api.MailReply(ctx, "yd-1-new", "Yes: only the shed.", "ada", seq)
	for _, want := range []string{"a note on that work", "footplate", "yardmaster"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("MailReply = %v, want %q", err, want)
		}
	}
	if err != nil && strings.Contains(err.Error(), "bead") {
		t.Fatalf("the refusal uses the yard word %q:\n%s", "bead", err)
	}
	mails, merr := api.EventsOfKind(ctx, "mail", 10)
	if merr != nil || len(mails) != 1 {
		t.Fatalf("mail events after the refusal = %+v, %v", mails, merr)
	}
}

// Mail from a yard no longer linked is refused as the command refuses
// it: no address would reach its sender.
func TestMailReplyRefusesUnlinkedMail(t *testing.T) {
	t.Parallel()
	api, own := replyYard(t)
	ctx := context.Background()
	key := linkNorth(t, api)
	takePeerMail(t, own, key, "m1", "ada", "clerk", "is the ledger closed?")
	if err := api.DeletePeer(ctx, "north", "ada"); err != nil {
		t.Fatal(err)
	}
	seq := replySeq(t, api)
	err := api.MailReply(ctx, "clerk@north", "Friday.", "ada", seq)
	for _, want := range []string{"clerk", "no longer linked", "was north", "no address would reach its sender"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("MailReply = %v, want %q", err, want)
		}
	}
}

// A long reply is numbered mails, as a long text is, with the answered
// seq on the first part alone; what is too long, or stopped midway, is
// told as MailText tells it.
func TestMailReplyTextFirstCarriesRef(t *testing.T) {
	t.Parallel()
	api, _ := replyYard(t)
	ctx := context.Background()
	if err := api.Mail(ctx, "ada", "when is the freeze over?", "jo"); err != nil {
		t.Fatal(err)
	}
	seq := replySeq(t, api)
	text := strings.Repeat("x", 10<<10)
	if err := store.MailReplyText(api, ctx, "jo", text, "ada", seq); err != nil {
		t.Fatal(err)
	}
	jo, err := api.UserInbox(ctx, "jo", 0, "jo")
	if err != nil || len(jo.Mail) != 3 {
		t.Fatalf("jo's inbox = %+v, %v", jo, err)
	}
	bodies := ""
	for i, m := range jo.Mail {
		head, body, _ := strings.Cut(m.Text, "\n")
		want := uint64(0)
		if i == 0 {
			want = seq
		}
		if head != fmt.Sprintf("(%d/3)", i+1) || m.Answered != want {
			t.Fatalf("part %d answers %d and begins %q", i+1, m.Answered, head)
		}
		bodies += body
	}
	if bodies != text {
		t.Fatal("the mails do not make the text again")
	}
	if err := store.MailReplyText(api, ctx, "jo", strings.Repeat("x", store.MaxMailParts*store.MaxMailBytes+1), "ada", seq); !errors.Is(err, store.ErrMailTooLong) {
		t.Fatalf("a text past %d mails = %v", store.MaxMailParts, err)
	}
	// The first part goes by MailReply, past a Mail that fails: the next
	// two go by Mail, and the error counts from the first failure.
	failing := &failingMail{API: api, n: 1}
	if err := store.MailReplyText(failing, ctx, "jo", text, "ada", seq); err == nil ||
		!strings.Contains(err.Error(), "2 of 3 mails went to jo") {
		t.Fatalf("after two parts = %v", err)
	}
}
