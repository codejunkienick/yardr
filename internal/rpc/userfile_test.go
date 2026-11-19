package rpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
)

// A device that could not create work files one piece for its own user:
// a task with no stage named, starting where its depot's flow starts.
// Another user's is refused (TestUserCallsAreMadeAsTheUser), and a
// project that is missing, names none or names several files nothing and
// answers listing the projects.
func TestUserDeviceFilesItsPersonsWork(t *testing.T) {
	st, u, token := userYard(t)
	ctx := context.Background()
	h := u.Handler()
	// No depot at all: the same refusal, and nothing filed.
	if got := call(t, h, token, "FileBead", `{"title":"Paint the fence"}`); !strings.Contains(got, "Your projects are: none yet") {
		t.Fatalf("FileBead with no depot = %s", got)
	}
	if err := st.PutDepot(ctx, store.Depot{Name: "yard", Kind: store.DepotDir, Path: t.TempDir()}, "jo"); err != nil {
		t.Fatal(err)
	}
	flow, err := store.ParseFlow("inbox -> new\nnew -> merged\nmerged terminal\n")
	if err != nil {
		t.Fatal(err)
	}
	flow.Name = "default"
	if _, err := st.PutFlow(ctx, *flow, "jo"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFlowPointer(ctx, "yard", "", "default", "jo"); err != nil {
		t.Fatal(err)
	}
	// The one depot needs no project, and the actor left out is the
	// device's user: the work is jo's, at the flow's first stage.
	filed := fileResult(t, call(t, h, token, "FileBead", `{"title":"Paint the fence"}`))
	if filed.Type != "task" || filed.Stage != "inbox" || filed.Originator != "jo" ||
		filed.Depot == nil || *filed.Depot != "yard" {
		t.Fatalf("filed %+v, want jo's task in yard's first stage", filed)
	}
	tablet, err := st.AddDevice(ctx, "sam", "tablet", "jo")
	if err != nil {
		t.Fatal(err)
	}
	sams := fileResult(t, call(t, h, tablet.Token, "FileBead", `{"title":"Sam's errand","project":"yard"}`))
	if sams.Originator != "sam" {
		t.Fatalf("sam's device filed %+v", sams)
	}
	// Several depots: missing names none, a named one files there, and
	// one that names none or names several files nothing, each listing
	// the projects.
	for _, depot := range []string{"shop", "yardr"} {
		if err := st.PutDepot(ctx, store.Depot{Name: depot, Kind: store.DepotDir, Path: t.TempDir()}, "jo"); err != nil {
			t.Fatal(err)
		}
	}
	named, err := st.ListBeads(ctx, store.BeadFilter{All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ body, want string }{
		{`{"title":"Stock the shelves"}`, "Your projects are:"},
		{`{"title":"Stock the shelves","project":"storage"}`, "Your projects are: shop, yard, yardr"},
		{`{"title":"Stock the shelves","project":"yar"}`, "Your projects are: shop, yard, yardr"},
	} {
		if got := call(t, h, token, "FileBead", tc.body); !strings.Contains(got, tc.want) ||
			!strings.Contains(got, "Tell me which one you mean") {
			t.Errorf("FileBead %s = %s", tc.body, got)
		}
	}
	if again, _ := st.ListBeads(ctx, store.BeadFilter{All: true}); len(again) != len(named) {
		t.Errorf("the refusals filed %d beads", len(again)-len(named))
	}
	inShop := fileResult(t, call(t, h, token, "FileBead", `{"title":"Stock the shelves","project":"shop"}`))
	if *inShop.Depot != "shop" {
		t.Fatalf("filed in %s, want shop", *inShop.Depot)
	}
	// A note on the filed work leaves its waiting question open: the
	// question is still listed, and the piece's view shows the note.
	if _, err := st.AddAsk(ctx, filed.ID, store.Ask{Question: "Which paint?", To: "jo", Context: []string{"the gate and the fence"}}, "yardmaster"); err != nil {
		t.Fatal(err)
	}
	if got := call(t, h, token, "AddNote", `{"bead":"`+filed.ID+`","text":"the gate first"}`); got != `{}` {
		t.Fatalf("AddNote = %s", got)
	}
	if got := call(t, h, token, "UserAsks", `{}`); !strings.Contains(got, filed.ID) {
		t.Errorf("UserAsks no longer lists the question on %s: %s", filed.ID, got)
	}
	if got := call(t, h, token, "Bead", `{"bead":"`+filed.ID+`"}`); !strings.Contains(got, "the gate first") {
		t.Errorf("the piece's view shows no note: %s", got)
	}
}

// A device withdraws and rewords a request its user filed, served as
// its user with the actor left out, under the same rules as the MCP
// tools. Another's request and a name that matches nothing are refused
// alike, in the blind words with the code error, never not-found.
func TestUserDeviceWithdrawsAndRewordsItsPersonsFiling(t *testing.T) {
	st, u, token := userYard(t)
	ctx := context.Background()
	h := u.Handler()
	if err := st.PutDepot(ctx, store.Depot{Name: "yard", Kind: store.DepotDir, Path: t.TempDir()}, "jo"); err != nil {
		t.Fatal(err)
	}
	filed := fileResult(t, call(t, h, token, "FileBead", `{"title":"Paint the fence"}`))
	// The actor left out is the device's user: jo's request is reworded,
	// and the answer says the words changed.
	got := call(t, h, token, "RewordBead", `{"bead":"`+filed.ID+`","title":"Paint the fence green"}`)
	if !strings.Contains(got, `"title":"Paint the fence green"`) || !strings.Contains(got, `"changed":true`) {
		t.Fatalf("RewordBead = %s", got)
	}
	// And withdrawn, closed with the reason given.
	got = call(t, h, token, "WithdrawBead", `{"bead":"`+filed.ID+`","reason":"the fence is gone"}`)
	if !strings.Contains(got, `"status":"closed"`) || !strings.Contains(got, `"close_reason":"the fence is gone"`) {
		t.Fatalf("WithdrawBead = %s", got)
	}
	if b, err := st.GetBead(ctx, filed.ID); err != nil || b.Status != "closed" {
		t.Fatalf("the withdrawn request is %+v, %v", b, err)
	}
	// Another's and a name that matches nothing are refused alike, in
	// the blind words with the code error.
	sams, err := st.CreateBead(ctx, store.NewBead{Title: "Sam's errand", For: "sam"}, "sam")
	if err != nil {
		t.Fatal(err)
	}
	missing := refusal(t, call(t, h, token, "RewordBead", `{"bead":"yardr-nosuch","title":"A new title"}`))
	if !strings.HasPrefix(missing, "error\nI can't find that among the requests you filed") {
		t.Fatalf("a name that matches nothing was answered %q", missing)
	}
	for _, c := range []struct{ method, body string }{
		{"RewordBead", `{"bead":"` + sams.ID + `","title":"A new title"}`},
		{"WithdrawBead", `{"bead":"` + sams.ID + `","reason":"no longer wanted"}`},
		{"WithdrawBead", `{"bead":"yardr-nosuch","reason":"no longer wanted"}`},
	} {
		if got := refusal(t, call(t, h, token, c.method, c.body)); got != missing {
			t.Errorf("%s %s = %q, want the words for a name that matches nothing: %q", c.method, c.body, got, missing)
		}
	}
	if b, err := st.GetBead(ctx, sams.ID); err != nil || b.Status != "open" || b.Title != "Sam's errand" {
		t.Errorf("sam's request after the refusals is %+v, %v", b, err)
	}
}

// refusal is the code and the message of a refused reply, failing the
// test on one that was answered.
func refusal(t *testing.T, reply string) string {
	t.Helper()
	var r struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(reply), &r); err != nil {
		t.Fatal(err)
	}
	if r.Error.Code == "" {
		t.Fatalf("refused nothing: %s", reply)
	}
	return r.Error.Code + "\n" + r.Error.Message
}

// fileResult is the filed bead of a FileBead reply, failing the test on
// a refusal.
func fileResult(t *testing.T, reply string) store.Bead {
	t.Helper()
	var r struct {
		Result store.Bead `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(reply), &r); err != nil {
		t.Fatal(err)
	}
	if r.Error != nil {
		t.Fatalf("FileBead refused: %s", r.Error.Message)
	}
	return r.Result
}

// A device lists its user's own work alone: open work whole, finished
// work inside the window, and nothing of anyone else's. The window
// defaults to the last 24 hours; an earlier since widens it, and the
// call keeps no cursor.
func TestUserDeviceListsItsPersonsWork(t *testing.T) {
	st, u, token := userYard(t)
	ctx := context.Background()
	h := u.Handler()
	if err := st.PutDepot(ctx, store.Depot{Name: "yard", Kind: store.DepotDir, Path: t.TempDir()}, "jo"); err != nil {
		t.Fatal(err)
	}
	filed := fileResult(t, call(t, h, token, "FileBead", `{"title":"Paint the fence"}`))
	finished, err := st.CreateBead(ctx, store.NewBead{Title: "Swept already", For: "jo"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CloseBead(ctx, finished.ID, "done", store.Expect{}, "jo"); err != nil {
		t.Fatal(err)
	}
	sams, err := st.CreateBead(ctx, store.NewBead{Title: "Sam's errand", For: "sam"}, "sam")
	if err != nil {
		t.Fatal(err)
	}
	// By default, the last 24 hours: the just-filed work at its flow's
	// first stage, the finished work, and nothing of sam's.
	got := call(t, h, token, "OwnBeads", `{}`)
	for _, want := range []string{filed.ID, finished.ID, `"id":"yd-shed"`} {
		if !strings.Contains(got, want) {
			t.Errorf("OwnBeads = %s, want %s among it", got, want)
		}
	}
	if strings.Contains(got, sams.ID) {
		t.Errorf("OwnBeads lists sam's work: %s", got)
	}
	// A since past every close lists the open work alone.
	got = call(t, h, token, "OwnBeads", `{"since":"`+time.Now().Add(time.Hour).UTC().Format(time.RFC3339)+`"}`)
	if strings.Contains(got, finished.ID) {
		t.Errorf("OwnBeads with a later since lists finished work: %s", got)
	} else if !strings.Contains(got, filed.ID) {
		t.Errorf("OwnBeads with a later since leaves out open work: %s", got)
	}
	// No cursor: the same call answers the same.
	first := call(t, h, token, "OwnBeads", `{}`)
	if again := call(t, h, token, "OwnBeads", `{}`); again != first {
		t.Errorf("a second call answers differently:\n%s\n%s", first, again)
	}
	tablet, err := st.AddDevice(ctx, "sam", "tablet", "jo")
	if err != nil {
		t.Fatal(err)
	}
	if got := call(t, h, tablet.Token, "OwnBeads", `{}`); !strings.Contains(got, sams.ID) || strings.Contains(got, filed.ID) {
		t.Errorf("sam's device lists %s, want sam's work alone", got)
	}
}
