package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
)

// pairAsk puts a device pair ask for user on the yard's bead, asked of
// to, and returns its seq. The address is the command's, since no
// server owns these stores to give one of its own.
func pairAsk(t *testing.T, st *store.Store, bead, user, device, to string) uint64 {
	t.Helper()
	seq, err := st.AddAsk(context.Background(), bead, store.Ask{Question: "Pair the new device?", To: to,
		Context: []string{"the device arrived"},
		Run:     &store.AskRun{Argv: []string{"user", "device", "pair", user, device, "--url", "https://mac.example.net"}}}, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

// A member's own device is newly served ApproveAsk for a device pair
// ask of their own: approving it opens the pairing and answers the
// transient once to that device. Another user's pairing is answered as
// for a method the yard does not have, and the operator's device
// approves a member's pairing as any approval. The member then ends
// that pairing from their own device; another user's device cannot.
func userApprovesOwnPairing(t *testing.T) {
	ctx := context.Background()
	y := newSecretYard(t)
	seq := pairAsk(t, y.st, "yd-crm", "sam", "watch", "sam")
	body := fmt.Sprintf(`{"bead":"yd-crm","ask":%d,"weighed":true}`, seq)
	w := y.post("ApproveAsk", onTheMachine, y.sam, strings.NewReader(body))
	var got struct {
		Result *store.AskApproved `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Result == nil {
		t.Fatalf("sam's own approval answered %d %s (%v)", w.Code, w.Body, err)
	}
	p := got.Result.Pairing
	if p == nil || p.User != "sam" || p.Device != "watch" || p.Code == "" {
		t.Fatalf("the approval answered %+v", got.Result)
	}
	if strings.Contains(got.Result.Result, p.Code) {
		t.Fatalf("the recorded result holds the code: %q", got.Result.Result)
	}
	if !y.st.DevicePairOpened(ctx) {
		t.Fatal("the approval opened no pairing")
	}
	if _, err := y.st.DevicePairRedeem(ctx, p.Code); err != nil {
		t.Fatalf("the answered code does not pair: %v", err)
	}
	// sam's device approving a pairing of jo's: not there.
	seq = pairAsk(t, y.st, "yd-crm", "jo", "laptop", "jo")
	body = fmt.Sprintf(`{"bead":"yd-crm","ask":%d,"weighed":true}`, seq)
	w = y.post("ApproveAsk", onTheMachine, y.sam, strings.NewReader(body))
	if !strings.Contains(w.Body.String(), "no such method") {
		t.Fatalf("sam's device approving jo's pairing answered %s", w.Body)
	}
	// The operator's device approves a member's pairing asked of it.
	seq = pairAsk(t, y.st, "yd-crm", "sam", "laptop", "jo")
	body = fmt.Sprintf(`{"bead":"yd-crm","ask":%d,"weighed":true}`, seq)
	w = y.post("ApproveAsk", onTheMachine, y.sam, strings.NewReader(body))
	if !strings.Contains(w.Body.String(), "no such method") {
		t.Fatalf("sam's device approving a pairing asked of jo answered %s", w.Body)
	}
	w = y.post("ApproveAsk", onTheMachine, y.jo, strings.NewReader(body))
	got.Result = nil
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Result == nil || got.Result.Pairing == nil {
		t.Fatalf("the operator's approval answered %d %s (%v)", w.Code, w.Body, err)
	}
	// The member can end the pairing opened by an operator's approval
	// from their own device, and cannot end another user's pairing.
	w = y.post("DevicePairCloseSelf", onTheMachine, y.sam, strings.NewReader(`{"user":"sam"}`))
	if w.Code != 200 || strings.Contains(w.Body.String(), "error") {
		t.Fatalf("sam ending their approval-opened pairing answered %d %s", w.Code, w.Body)
	}
	if y.st.DevicePairOpened(ctx) {
		t.Fatal("the pairing stands after its user ended it")
	}
	if _, err := y.st.DevicePairOpen(ctx, "jo", "watch", "jo"); err != nil {
		t.Fatal(err)
	}
	w = y.post("DevicePairCloseSelf", onTheMachine, y.sam, strings.NewReader(`{"user":"jo"}`))
	if !strings.Contains(w.Body.String(), "calls as sam") {
		t.Fatalf("sam's device ending jo's pairing answered %s", w.Body)
	}
	if !y.st.DevicePairOpened(ctx) {
		t.Fatal("a refused ending ended the pairing")
	}
}
