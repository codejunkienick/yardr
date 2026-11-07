package edgelink

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/rpc"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
	"yardr.dev/yardr/internal/yardkey"
)

// The call header the link registers by is rpc's envelope header: the
// two spellings are one.
func TestE2ECallHeaderIsRPCs(t *testing.T) {
	if e2eCallHeader != rpc.E2ECallHeader {
		t.Fatalf("the link registers %q, rpc opens %q", e2eCallHeader, rpc.E2ECallHeader)
	}
}

// A frame's enveloped call claim is registered before its goroutine
// starts: a second live frame cannot take the same call id, and the
// id is free again once its frame's handler has returned.
func TestE2ERegistrationHoldsOneLiveCall(t *testing.T) {
	l := &Link{}
	frame := func(id string) frameIn {
		header := http.Header{}
		header.Set(e2eCallHeader, "call-one")
		return frameIn{Type: "request", ID: id, Path: rpc.E2EPath, Header: header}
	}
	if !l.registerE2E(frame("f1")) {
		t.Fatal("the first frame was refused")
	}
	if l.registerE2E(frame("f2")) {
		t.Fatal("a second live frame took a registered call id")
	}
	l.untrack("f1")
	if !l.registerE2E(frame("f2")) {
		t.Fatal("the call id stayed registered after its frame returned")
	}
	if !l.registerE2E(frameIn{Type: "request", ID: "f3", Path: "/hooks/x"}) {
		t.Fatal("a frame with no call claim was refused")
	}
}

// A stand-in device and a real yard exchange a whole call down the
// link, sealed end to end: the frame is checked first, the envelope
// opened after it in its serving goroutine, the inner call served as
// the device's user with no token anywhere, and the outer head the
// link carries back shows only the envelope's own status. A call whose
// marker the frame carried differently than the device sealed fails
// its opening and its call.
func TestLinkOpensAnEnvelopeAfterTheFrameCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, own := storetest.NewOwned(t)
	if _, err := own.AddUser(ctx, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	if err := own.SetSetting(ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := own.AddUser(ctx, "sam", "jo"); err != nil {
		t.Fatal(err)
	}
	channelPath := filepath.Join(t.TempDir(), "channel.key")
	yardChannel, _, err := yardkey.EnsureChannel(channelPath)
	if err != nil {
		t.Fatal(err)
	}
	deviceKey, err := yardkey.GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	opened, err := own.DevicePairOpen(ctx, "sam", "phone", "jo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := own.DevicePairRedeemSealed(ctx, opened.Code, yardkey.ChannelPublic(deviceKey)); err != nil {
		t.Fatal(err)
	}
	u := rpc.NewUserServer(own, func(context.Context) string { return "south" }, nil)
	u.SetChannelKeyFile(channelPath)
	y := linkTo(t, func(l *Link) { l.Handler = u.Handler() }, nil)
	l := y.next(t)

	// The handshake founds the session, down the link like a call.
	first, opening, err := yardkey.StartChannelHandshake(deviceKey, yardChannel.Public)
	if err != nil {
		t.Fatal(err)
	}
	answer := deliver(t, l, l.Frame(http.MethodPost, rpc.E2EPath, nil, first, false))
	if answer.Status != http.StatusOK {
		t.Fatalf("the handshake down the link = %+v", answer)
	}
	session, err := opening.OpenAnswer(answer.Body)
	if err != nil {
		t.Fatal(err)
	}
	binding := &yardkey.FirstBinding{Path: rpc.E2EPath}

	call := func(t *testing.T, seal, wire *yardkey.FirstBinding) (int, http.Header, []byte, []byte) {
		t.Helper()
		head, err := json.Marshal(struct {
			Method string      `json:"method"`
			Path   string      `json:"path"`
			Header http.Header `json:"header"`
		}{Method: http.MethodGet, Path: "/user/me"})
		if err != nil {
			t.Fatal(err)
		}
		plain := append([]byte{0, 0, 0, byte(len(head))}, head...)
		id, err := yardkey.NewCallID()
		if err != nil {
			t.Fatal(err)
		}
		run, err := session.SealWhole(id, seal, plain)
		if err != nil {
			t.Fatal(err)
		}
		header := http.Header{rpc.E2ECallHeader: {base64.RawURLEncoding.EncodeToString(id)}}
		if wire != nil && wire.Present {
			header.Set(rpc.E2EStreamHeader, wire.Marker)
		}
		a := deliver(t, l, l.Frame(http.MethodPost, rpc.E2EPath, header, run, false))
		return a.Status, a.Header, a.Body, id
	}

	status, outerHeader, sealedAnswer, id := call(t, binding, binding)
	if status != http.StatusOK {
		t.Fatalf("the enveloped call down the link = %d", status)
	}
	if outerHeader.Get("Content-Type") != "application/octet-stream" || strings.Contains(string(sealedAnswer), "sam") {
		t.Errorf("the outer answer head %v over a body that reads %q", outerHeader, sealedAnswer)
	}
	openedAnswer, err := session.OpenWhole(sealedAnswer, id, binding)
	if err != nil {
		t.Fatalf("the device cannot open the yard's answer: %v", err)
	}
	if !strings.Contains(string(openedAnswer), `"user":"sam"`) || !strings.Contains(string(openedAnswer), `"status":200`) {
		t.Errorf("the inner answer = %s", openedAnswer)
	}

	// The marker dropped between the device and the yard: the device
	// sealed it present, the frame carries none, and the first record
	// fails its opening. Nothing is served.
	marked := &yardkey.FirstBinding{Path: rpc.E2EPath, Marker: rpc.E2EStreamLongLived, Present: true}
	if status, _, _, _ := call(t, marked, binding); status != http.StatusBadRequest {
		t.Errorf("a call whose marker was dropped on the way = %d, want 400", status)
	}
	// And the honest call after it still opens.
	if status, _, _, _ := call(t, binding, binding); status != http.StatusOK {
		t.Errorf("the honest call after it = %d", status)
	}

	// The outer path rewritten on the way: the same sealed run carried
	// to another path is no envelope there, is served nothing, and is
	// answered as that path answers a caller with no token.
	head, err := json.Marshal(struct {
		Method string      `json:"method"`
		Path   string      `json:"path"`
		Header http.Header `json:"header"`
	}{Method: http.MethodGet, Path: "/user/me"})
	if err != nil {
		t.Fatal(err)
	}
	plain := append([]byte{0, 0, 0, byte(len(head))}, head...)
	id, err = yardkey.NewCallID()
	if err != nil {
		t.Fatal(err)
	}
	run, err := session.SealWhole(id, binding, plain)
	if err != nil {
		t.Fatal(err)
	}
	header := http.Header{rpc.E2ECallHeader: {base64.RawURLEncoding.EncodeToString(id)}}
	if a := deliver(t, l, l.Frame(http.MethodPost, "/user/call/Head", header, run, false)); a.Status != http.StatusUnauthorized {
		t.Errorf("the sealed run on a rewritten outer path = %+v, want 401 unserved", a)
	}
}
