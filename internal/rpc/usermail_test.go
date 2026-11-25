package rpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
	"yardr.dev/yardr/internal/store"
)

// seePolicy is a store.Policy of one function.
type seePolicy func(actor string, act store.Action, obj store.Object) error

func (f seePolicy) May(_ context.Context, _ *bolt.Tx, actor string, act store.Action, obj store.Object) error {
	return f(actor, act, obj)
}

// mailYard is userYard with sam's device too: two users, jo the operator
// and sam, a device each.
func mailYard(t *testing.T) (*store.Store, *UserServer, string, string) {
	t.Helper()
	st, u, jo := userYard(t)
	sam, err := st.AddDevice(context.Background(), "sam", "tablet", "jo")
	if err != nil {
		t.Fatal(err)
	}
	return st, u, jo, sam.Token
}

func mustHead(t *testing.T, st *store.Store) uint64 {
	t.Helper()
	head, err := st.Head(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return head
}

// A device that subscribes from the first event is sent its own mail and
// none between two others: the mail, read receipts and reactions it is a
// party to arrive, and what two others wrote, read or reacted to does
// not. A bead's events and a hook delivery still arrive, and the event that
// its files are in with it: those are the yard's own news, not mail.
func TestUserStreamShowsOnlyOwnMail(t *testing.T) {
	ctx := context.Background()
	st, u, joToken, samToken := mailYard(t)
	base := listen(t, u, time.Minute)
	since := mustHead(t, st)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.Mail(ctx, "yardmaster", "crew business", "jo"))
	crewMail := mustHead(t, st)
	must(st.Mail(ctx, "sam", "for sam", "jo"))
	toSam := mustHead(t, st)
	must(st.Mail(ctx, "jo", "for jo", "sam"))
	fromSam := mustHead(t, st)
	must(st.AckUserInbox(ctx, "jo", fromSam, "jo"))
	joRead := mustHead(t, st)
	must(st.AckUserInbox(ctx, "sam", toSam, "sam"))
	samRead := mustHead(t, st)
	must(st.React(ctx, crewMail, "👍", "jo"))
	crewReact := mustHead(t, st)
	must(st.React(ctx, toSam, "👀", "sam"))
	samReact := mustHead(t, st)
	must(st.AddNote(ctx, "yd-shed", "painted", "jo"))
	noted := mustHead(t, st)
	hooked, _, err := st.RecordHook(ctx, "linear", map[string]any{"type": "note", "text": "hello"}, []byte("{}"), nil)
	must(err)
	head := mustHead(t, st)
	if hooked != head {
		t.Fatalf("the hook delivery is seq %d, want the head %d", hooked, head)
	}
	// Its files are fetched after it is answered, and recorded with an event
	// of their own.
	photo, err := st.PutBlob(ctx, []byte("\x89PNG\r\n\x1a\n the shed from the phone"))
	must(err)
	must(st.RecordDeliveryFiles(ctx, hooked, []store.BlobRef{{Hash: photo.Hash}}))
	filed := mustHead(t, st)
	head = filed

	// read is the events after since on token's stream from the first
	// event, by seq.
	read := func(token string) map[uint64]store.Event {
		t.Helper()
		lines, resp := follow(t, base, token, 0)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the stream is status %d", resp.StatusCode)
		}
		got := map[uint64]store.Event{}
		for {
			var e store.Event
			if err := json.Unmarshal([]byte(next(t, lines)), &e); err != nil {
				t.Fatal(err)
			}
			if e.Seq > since {
				got[e.Seq] = e
			}
			if e.Seq == head {
				return got
			}
		}
	}

	sams := read(samToken)
	for _, tc := range []struct {
		name string
		seq  uint64
		want bool
	}{
		{"mail between two others", crewMail, false},
		{"mail to sam", toSam, true},
		{"mail from sam", fromSam, true},
		{"jo's read receipt", joRead, false},
		{"sam's read receipt", samRead, true},
		{"reaction between two others", crewReact, false},
		{"sam's reaction", samReact, true},
		{"a bead's note", noted, true},
		{"a hook delivery", hooked, true},
		{"a hook delivery's files", filed, true},
	} {
		_, has := sams[tc.seq]
		if has != tc.want {
			t.Errorf("sam's stream carries %s: %v, want %v", tc.name, has, tc.want)
		}
	}
	// What sam is sent of its own mail is the mail itself, body and all.
	if e := sams[toSam]; e.Data["text"] != "for sam" {
		t.Errorf("sam's mail arrives as %v, want its text", e.Data)
	}

	jos := read(joToken)
	for _, tc := range []struct {
		name string
		seq  uint64
		want bool
	}{
		{"jo's mail to the crew", crewMail, true},
		{"jo's mail to sam", toSam, true},
		{"sam's mail to jo", fromSam, true},
		{"jo's read receipt", joRead, true},
		{"sam's read receipt", samRead, false},
		{"jo's reaction", crewReact, true},
		{"sam's reaction", samReact, true},
		{"a hook delivery", hooked, true},
		{"a hook delivery's files", filed, true},
	} {
		_, has := jos[tc.seq]
		if has != tc.want {
			t.Errorf("jo's stream carries %s: %v, want %v", tc.name, has, tc.want)
		}
	}
}

// A device reads a note's file, a kept hook delivery's, its own mail's
// files, and a file its own user put within the hour whose mail or note
// is on its way; another user's mail file is 404, as it is in the web
// view, and so is a file another user just put, anything the yard never
// held, a name that is no hash, and a file put long ago that nothing
// names.
func TestUserBlobsServeOnlyWhatTheCallerMayRead(t *testing.T) {
	ctx := context.Background()
	st, u, joToken, samToken := mailYard(t)
	h := u.Handler()

	put := func(data string) string {
		t.Helper()
		ref, err := st.PutBlob(ctx, []byte(data))
		if err != nil {
			t.Fatal(err)
		}
		return ref.Hash
	}
	noteFile := put("\x89PNG\r\n\x1a\n the shed on its note")
	ownFile := put("\xff\xd8\xff\xe0 the fence by mail")
	otherFile := put("GIF89a the crew's chart")
	if err := st.AddNote(ctx, "yd-shed", "hung the picture", "jo",
		store.BlobFile{Hash: noteFile, Name: "shed.png"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Mail(ctx, "sam", "for sam", "jo",
		store.BlobFile{Hash: ownFile, Name: "fence.jpg"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Mail(ctx, "yardmaster", "crew business", "jo",
		store.BlobFile{Hash: otherFile, Name: "chart.gif"}); err != nil {
		t.Fatal(err)
	}
	// The fresh file goes up as sam's device puts it: through the
	// authenticated call, so the yard knows whose upload it is.
	freshReply := call(t, h, samToken, "PutBlob", `{"data":"`+
		base64.StdEncoding.EncodeToString([]byte("put this moment, named by nothing yet"))+`"}`)
	fresh, _, _ := strings.Cut(strings.TrimPrefix(freshReply, `{"result":{"hash":"`), `"`)
	if len(fresh) != 64 {
		t.Fatalf("PutBlob = %s", freshReply)
	}
	old := put("put long ago, named by nothing")
	// A photo a kept hook delivery carried, fetched and recorded as the
	// receiver records it, put long ago: the delivery names it still.
	hookFile := put("\x89PNG\r\n\x1a\n the shed from the phone")
	seq, _, err := st.RecordHook(ctx, "telegram", map[string]any{"source": "telegram", "type": "message", "text": "[photo]"}, []byte(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordDeliveryFiles(ctx, seq, []store.BlobRef{{Hash: hookFile}}); err != nil {
		t.Fatal(err)
	}
	aged := time.Now().Add(-2 * store.BlobGrace)
	for _, hash := range []string{old, hookFile} {
		if err := os.Chtimes(st.BlobPath(hash), aged, aged); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name  string
		token string
		hash  string
		want  int
	}{
		{"a note's file", samToken, noteFile, http.StatusOK},
		{"own mail in", samToken, ownFile, http.StatusOK},
		{"own mail out", joToken, ownFile, http.StatusOK},
		{"another user's fresh mail file", samToken, otherFile, http.StatusNotFound},
		{"the sender reads it", joToken, otherFile, http.StatusOK},
		{"a file just put", samToken, fresh, http.StatusOK},
		{"a file another user just put", joToken, fresh, http.StatusNotFound},
		{"a hook delivery's file", samToken, hookFile, http.StatusOK},
		{"a hook delivery's file to another device", joToken, hookFile, http.StatusOK},
		{"a file put long ago, named by nothing", samToken, old, http.StatusNotFound},
		{"a hash the yard never held", samToken, strings.Repeat("4", 64), http.StatusNotFound},
		{"a name that is no hash", samToken, "nope", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := ask(h, http.MethodGet, "/user/blobs/"+tc.hash, tc.token, nil)
			if w.Code != tc.want {
				t.Errorf("GET /user/blobs/%s = %d, want %d", tc.hash, w.Code, tc.want)
			}
		})
	}
	if w := ask(h, http.MethodGet, "/user/blobs/"+noteFile, samToken, nil); w.Code != http.StatusOK ||
		w.Body.String() != "\x89PNG\r\n\x1a\n the shed on its note" {
		t.Errorf("a note's file = %d, %q", w.Code, w.Body.String())
	}
	// The hash a device reads a delivery's file by is named by DeliveryFiles,
	// to a device of either user, as the delivery is sent to both; a
	// delivery whose files were never fetched reads none.
	bare, _, err := st.RecordHook(ctx, "telegram", map[string]any{"source": "telegram", "type": "message", "text": "[voice]"}, []byte(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	held, err := st.Blob(hookFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{samToken, joToken} {
		var got struct{ Result []store.BlobRef }
		if err := json.Unmarshal([]byte(call(t, h, token, "DeliveryFiles", fmt.Sprintf(`{"seq":%d}`, seq))), &got); err != nil || len(got.Result) != 1 || got.Result[0] != *held {
			t.Errorf("DeliveryFiles(%d) = %+v, %v, want [%+v]", seq, got.Result, err, held)
		}
		if got := call(t, h, token, "DeliveryFiles", fmt.Sprintf(`{"seq":%d}`, bare)); got != `{"result":null}` {
			t.Errorf("DeliveryFiles(%d) of a delivery whose files were not fetched = %s, want none", bare, got)
		}
	}
}

// deviceEvent sends a mail, a read receipt and a reaction only where the
// caller is its sender or its receiver: row by row, for sam's device
// against mail of every shape the log holds.
func TestUserDeviceEventShowsOnlyPartiesMail(t *testing.T) {
	_, u, _ := userYard(t)
	ctx := context.Background()
	sam := &Caller{User: "sam", SaidBy: "tablet", device: true}
	mail := func(actor string, data map[string]any) store.Event {
		return store.Event{Kind: "mail", Actor: actor, Data: data}
	}
	for _, tc := range []struct {
		name string
		e    store.Event
		want bool
	}{
		{"mail to sam", mail("jo", map[string]any{"to": "sam", "to_user": true, "text": "hi"}), true},
		{"mail from sam", mail("sam", map[string]any{"to": "yardmaster", "text": "hi"}), true},
		{"mail from sam's pane", mail("sam", map[string]any{"to": "jo", "to_user": true, "from_bead": "yd-shed"}), true},
		{"mail between two others", mail("jo", map[string]any{"to": "yardmaster", "text": "hi"}), false},
		{"mail sam sent to a linked yard", mail("sam", map[string]any{"to": "clerk", "to_peer": "north", "text": "hi"}), true},
		{"mail another sent to a linked yard", mail("jo", map[string]any{"to": "clerk", "to_peer": "north", "text": "hi"}), false},
		{"mail in from a linked yard to sam", mail("al@north", map[string]any{"to": "sam", "to_user": true, "from": "al", "from_peer": "north"}), true},
		{"mail in from a linked yard to another", mail("al@north", map[string]any{"to": "jo", "to_user": true, "from": "al", "from_peer": "north"}), false},
		{"a timer's mail to sam", mail("jo", map[string]any{"to": "sam", "to_user": true, "timer": "t1"}), true},
		{"sam's read receipt", store.Event{Kind: "mail_read", Actor: "sam", Data: map[string]any{"reader": "sam", "through": uint64(9)}}, true},
		{"another's read receipt", store.Event{Kind: "mail_read", Actor: "jo", Data: map[string]any{"reader": "jo", "through": uint64(9)}}, false},
		{"sam's inbox read", store.Event{Kind: "inbox_read", Actor: "sam", Data: map[string]any{"reader": "sam"}}, true},
		{"crew inbox read", store.Event{Kind: "inbox_read", Actor: "yardmaster", Data: map[string]any{"reader": "yardmaster"}}, false},
		{"sam's inbox shown", store.Event{Kind: "inbox_shown", Actor: "sam", Data: map[string]any{"reader": "sam"}}, true},
		{"crew inbox shown", store.Event{Kind: "inbox_shown", Actor: "yardmaster", Data: map[string]any{"reader": "yardmaster"}}, false},
		{"sam's reaction", store.Event{Kind: "reaction", Actor: "sam", Data: map[string]any{"seq": uint64(3), "emoji": "👀", "to": "jo"}}, true},
		{"a reaction on sam's mail", store.Event{Kind: "reaction", Actor: "jo", Data: map[string]any{"seq": uint64(4), "emoji": "👍", "to": "sam"}}, true},
		{"a reaction between two others", store.Event{Kind: "reaction", Actor: "jo", Data: map[string]any{"seq": uint64(5), "emoji": "👍", "to": "yardmaster"}}, false},
		{"a bead's event", store.Event{Kind: "noted", Actor: "jo", Data: map[string]any{"note": "hi"}}, true},
		{"a hook delivery", store.Event{Kind: "hook", Actor: "hook:linear", Data: map[string]any{"hook": "linear", "text": "hi"}}, true},
		{"a hook delivery's files", store.Event{Kind: store.HookFiles, Actor: "hook:linear", Data: map[string]any{"seq": uint64(7)}}, true},
	} {
		if _, keep := u.deviceEvent(ctx, sam, tc.e); keep != tc.want {
			t.Errorf("sam is sent %s: %v, want %v", tc.name, keep, tc.want)
		}
	}
}

// deviceEvent drops every event of a bead the policy refuses the caller
// to see, and keeps the rest: the stream tells no bead a listing would
// not. Under the core every device is sent every bead's events.
func TestUserDeviceEventDropsABeadThePolicyHides(t *testing.T) {
	st, u, _ := userYard(t)
	ctx := context.Background()
	sam := &Caller{User: "sam", SaidBy: "tablet", device: true}
	shed, other := "yd-shed", "yd-other"
	noted := func(bead *string) store.Event {
		return store.Event{Kind: "noted", Actor: "jo", Bead: bead, Data: map[string]any{"note": "hi"}}
	}
	if _, keep := u.deviceEvent(ctx, sam, noted(&shed)); !keep {
		t.Fatal("sam is not sent the shed's note under the core")
	}
	st.SetPolicy(seePolicy(func(actor string, act store.Action, obj store.Object) error {
		if act == store.ActSee && obj.Kind == store.KindBead && obj.Name == shed {
			return fmt.Errorf("hidden from %s", actor)
		}
		return nil
	}))
	if _, keep := u.deviceEvent(ctx, sam, noted(&shed)); keep {
		t.Error("sam is sent the shed's note with yd-shed refused, want it dropped")
	}
	if _, keep := u.deviceEvent(ctx, sam, noted(&other)); !keep {
		t.Error("sam is not sent another bead's note with yd-shed refused, want it kept")
	}
	if _, keep := u.deviceEvent(ctx, sam, noted(nil)); !keep {
		t.Error("sam is not sent a bead-less event with yd-shed refused, want it kept")
	}
}

// A device answers one message of its user's mail, served as its user
// with the actor left out: the reply goes to whoever wrote it and is
// linked to the letter, and the answer names both. A number that is no
// mail of theirs — missing, what is not mail, and another's — is refused
// alike, in the blind words with the code error, never not-found.
func TestUserDeviceRepliesToItsPersonsMail(t *testing.T) {
	st, u, jo, _ := mailYard(t)
	ctx := context.Background()
	h := u.Handler()
	if err := st.Mail(ctx, "jo", "when is the freeze over?", "sam"); err != nil {
		t.Fatal(err)
	}
	box, err := st.UserInbox(ctx, "jo", 0, "jo")
	if err != nil || len(box.Mail) != 1 {
		t.Fatalf("jo's inbox = %+v, %v", box, err)
	}
	seq := box.Mail[0].Seq
	got := call(t, h, jo, "ReplyMail", fmt.Sprintf(`{"mail":%d,"message":"Friday."}`, seq))
	if !strings.Contains(got, `"to":"sam"`) || !strings.Contains(got, fmt.Sprintf(`"reply_to":%d`, seq)) {
		t.Fatalf("ReplyMail = %s", got)
	}
	sams, err := st.UserInbox(ctx, "sam", 0, "sam")
	if err != nil || len(sams.Mail) != 1 || sams.Mail[0].From != "jo" ||
		sams.Mail[0].Text != "Friday." || sams.Mail[0].Answered != seq {
		t.Fatalf("sam's inbox = %+v, %v", sams, err)
	}
	if err := st.Mail(ctx, "sam", "the shed is painted", "jo"); err != nil {
		t.Fatal(err)
	}
	sams, err = st.UserInbox(ctx, "sam", 0, "sam")
	if err != nil || len(sams.Mail) != 2 {
		t.Fatalf("sam's inbox = %+v, %v", sams, err)
	}
	evs, err := st.Events(ctx, "", 500)
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
	missing := refusal(t, call(t, h, jo, "ReplyMail", `{"mail":999999,"message":"Friday."}`))
	if want := "error\nI can't find message 999999 among your mail; read_my_mail lists the numbers"; missing != want {
		t.Fatalf("a number that matches nothing was answered %q, want %q", missing, want)
	}
	for _, n := range []uint64{built, sams.Mail[1].Seq} {
		body := fmt.Sprintf(`{"mail":%d,"message":"Friday."}`, n)
		want := fmt.Sprintf("error\nI can't find message %d among your mail; read_my_mail lists the numbers", n)
		if got := refusal(t, call(t, h, jo, "ReplyMail", body)); got != want {
			t.Errorf("ReplyMail %s = %q, want the blind words: %q", body, got, want)
		}
	}
}

// A device reads one message of its user's mail with its exchange, served
// as its user with the user left out: the anchor with its ancestors and
// replies, oldest first, each with its text and the number it answers.
// Another's letter is refused in the blind words with the code error,
// never not-found, and the device acks the named letter alone: older
// unread stays unread.
func TestUserDeviceReadsItsPersonsExchange(t *testing.T) {
	st, u, jo, _ := mailYard(t)
	ctx := context.Background()
	h := u.Handler()
	if err := st.Mail(ctx, "jo", "when is the freeze over?", "sam"); err != nil {
		t.Fatal(err)
	}
	box, err := st.UserInbox(ctx, "jo", 0, "jo")
	if err != nil || len(box.Mail) != 1 {
		t.Fatalf("jo's inbox = %+v, %v", box, err)
	}
	first := box.Mail[0].Seq
	if _, err := st.ReplyMail(ctx, first, "Friday.", "jo"); err != nil {
		t.Fatal(err)
	}
	sams, err := st.UserInbox(ctx, "sam", 0, "sam")
	if err != nil || len(sams.Mail) != 1 {
		t.Fatalf("sam's inbox = %+v, %v", sams, err)
	}
	second := sams.Mail[0].Seq
	got := call(t, h, jo, "MailExchange", fmt.Sprintf(`{"seq":%d}`, first))
	for _, want := range []string{
		`"text":"when is the freeze over?"`,
		`"text":"Friday."`,
		fmt.Sprintf(`"seq":%d`, first),
		fmt.Sprintf(`"seq":%d`, second),
		fmt.Sprintf(`"answered":%d`, first),
		`"truncated":false`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("MailExchange = %s, want %s", got, want)
		}
	}
	if asked, answered := strings.Index(got, "when is the freeze over?"), strings.Index(got, "Friday."); asked < 0 || answered < 0 || asked > answered {
		t.Fatalf("MailExchange is not oldest first: %s", got)
	}
	if err := st.Mail(ctx, "yardmaster", "crew business", "sam"); err != nil {
		t.Fatal(err)
	}
	head, err := st.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []uint64{999999, head} {
		body := fmt.Sprintf(`{"seq":%d}`, n)
		want := fmt.Sprintf("error\nI can't find message %d among your mail; read_my_mail lists the numbers", n)
		if got := refusal(t, call(t, h, jo, "MailExchange", body)); got != want {
			t.Errorf("MailExchange %s = %q, want the blind words: %q", body, got, want)
		}
	}
	if err := st.Mail(ctx, "jo", "second note", "sam"); err != nil {
		t.Fatal(err)
	}
	if got := call(t, h, jo, "AckUserMail", fmt.Sprintf(`{"seq":%d}`, first)); got != "{}" {
		t.Fatalf("AckUserMail = %s, want nothing", got)
	}
	box, err = st.UserInbox(ctx, "jo", 0, "jo")
	if err != nil || len(box.Mail) != 2 {
		t.Fatalf("jo's inbox = %+v, %v", box, err)
	}
	for _, l := range box.Mail {
		if l.Read == (l.Seq != first) {
			t.Fatalf("jo's inbox after the single ack = %+v", box.Mail)
		}
	}
}
