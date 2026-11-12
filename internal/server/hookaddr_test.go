package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/hooks"
	"yardr.dev/yardr/internal/rpc"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/yardkey"
	"yardr.dev/yardr/internal/yards"
)

// freeAddr is a loopback address nothing listens on now. It stays free only
// until another process takes it, so a test that hands one to a server, which
// listens on it later, starts that server with startOnFreeAddr.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// setHookAddr gives the yard in c the hook_addr setting while no server runs
// on it, as an earlier server's yardr settings set left it. A yard whose test
// named an operator first takes a setting from that operator only, so it is
// set by them: startOnFreeAddr sets it after the test's own setup, and again
// for each try.
func setHookAddr(t *testing.T, c *config.Config, addr string) {
	t.Helper()
	st, err := store.Open(c.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	by := "me"
	all, err := st.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if op := all[store.OperatorSetting]; op != "" {
		by = op
	}
	if err := st.SetSetting(context.Background(), hooks.FromSetting, addr, by); err != nil {
		t.Fatal(err)
	}
}

func listens(addr string) bool {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// freeAddrTries is how many addresses startOnFreeAddr and proposeEndedPreview
// try. A port taken in the gap is rare, so a second try nearly always lands; a
// receiver or a preview that cannot listen for another reason fails the test
// after a few tries rather than looping.
const freeAddrTries = 3

// startOnFreeAddr starts a server for c whose hook receiver listens on a
// free loopback address, and returns the address. set puts each address
// where the server reads it: the hook_addr setting, or c.HookAddr. The
// address is free when freeAddr gives it but the server listens on it later,
// and another process can take it between; the server then records why in
// its state and serves on without the receiver (listenHooks), so a server
// whose state says it does not listen is stopped and started again on a
// fresh address.
func startOnFreeAddr(t *testing.T, c *config.Config, set func(addr string)) string {
	t.Helper()
	var last hooks.Listener
	var known bool
	for range freeAddrTries {
		addr := freeAddr(t)
		set(addr)
		done, stop := startStoppable(t, &runner{c: c})
		if last, known = hooks.ReadListener(c); known && last.Listening() {
			return addr
		}
		// The store and the socket are the next server's: wait for this one to
		// have let go of them.
		stop()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("server did not stop")
		}
	}
	t.Fatalf("no hook receiver listened in %d tries; the last server said %+v (known %v)", freeAddrTries, last, known)
	return ""
}

// The server takes webhooks on the yard's hook_addr setting when the
// variable is not set where it starts, and says so in its state; with no
// address at all it runs no receiver and says that.
func TestHookAddrFromTheSetting(t *testing.T) {
	c := home(t)
	addr := startOnFreeAddr(t, c, func(addr string) { setHookAddr(t, c, addr) })
	l, known := hooks.ReadListener(c)
	if !known || !l.Listening() || l.Addr != addr || l.From != hooks.FromSetting {
		t.Fatalf("listener = %+v (known %v)", l, known)
	}
	if !listens(addr) {
		t.Fatalf("nothing listens on %s", addr)
	}
	if got, err := listenerAddress(c); err != nil || got != "http://"+addr {
		t.Fatalf("pairing address = %q, %v; want the listening address", got, err)
	}

	none := home(t)
	start(t, &runner{c: none})
	if l, known := hooks.ReadListener(none); !known || l.Addr != "" || l.From != "" || l.Listening() {
		t.Fatalf("listener without an address = %+v (known %v)", l, known)
	}
	if _, err := listenerAddress(none); err == nil {
		t.Fatal("a yard with no listener got a pairing address")
	}
}

// YARDR_HOOK_ADDR, set where the server starts, wins over the setting.
func TestHookAddrVariableWins(t *testing.T) {
	c := home(t)
	// The setting's address is one the server must not use, so it is held for
	// the whole test and nothing else can take it: a server that read it in
	// place of the variable would be refused it and say so in its state,
	// which is checked below. Dialling it would prove nothing.
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	setHookAddr(t, c, held.Addr().String())
	variable := startOnFreeAddr(t, c, func(addr string) { c.HookAddr = addr })
	l, known := hooks.ReadListener(c)
	if !known || !l.Listening() || l.Addr != variable || l.From != hooks.FromVariable {
		t.Fatalf("listener = %+v (known %v)", l, known)
	}
	if !listens(variable) {
		t.Fatalf("nothing listens on the variable %s", variable)
	}
}

// Two yards given one address: the second server cannot listen, logs an
// event and says why in its state, and serves its yard all the same. The
// first keeps the address. A server that has stopped leaves no state.
func TestHookAddrTaken(t *testing.T) {
	first, second := home(t), home(t)
	addr := freeAddr(t)
	setHookAddr(t, first, addr)
	setHookAddr(t, second, addr)
	start(t, &runner{c: first})
	_, stop := startStoppable(t, &runner{c: second})

	if l, _ := hooks.ReadListener(first); !l.Listening() {
		t.Fatalf("first = %+v", l)
	}
	l, known := hooks.ReadListener(second)
	if !known || l.Listening() || l.Addr != addr || l.From != hooks.FromSetting || !strings.Contains(l.Error, "address already in use") {
		t.Fatalf("second = %+v (known %v)", l, known)
	}
	if _, err := listenerAddress(second); err == nil {
		t.Fatal("a yard whose receiver failed got a pairing address")
	}
	ctx := context.Background()
	cl := rpc.NewClient(second.Socket())
	evs, err := cl.Events(ctx, "", 50)
	if err != nil {
		t.Fatalf("the second server does not serve its yard: %v", err)
	}
	var failed *store.Event
	for i, e := range evs {
		if e.Kind == "hook_listener_failed" {
			failed = &evs[i]
		}
	}
	if failed == nil || failed.Data["addr"] != addr || failed.Data["from"] != hooks.FromSetting ||
		!strings.Contains(failed.Data["error"].(string), "address already in use") {
		t.Fatalf("events = %+v", evs)
	}
	if _, text := hooks.Listens(l, known, addr); !strings.Contains(text, "could not listen on "+addr) ||
		!strings.Contains(text, "yardr settings set hook_addr <host:port>") {
		t.Fatalf("text = %s", text)
	}

	stop()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, known := hooks.ReadListener(second); !known {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the stopped server's state is still read as a running one's")
		}
	}
}

// A server whose address was taken before it could listen is started again on
// a fresh one. The first address is taken here, as another process would take
// it between freeAddr and the server's listen, so that the server's listen on
// it is refused whatever the machine is doing; the helper then lands on
// another address and the server listens there.
func TestStartOnFreeAddrTriesAgain(t *testing.T) {
	c := home(t)
	var tried []string
	addr := startOnFreeAddr(t, c, func(addr string) {
		tried = append(tried, addr)
		// Taking it can lose the very race it stands for; the address is then
		// taken all the same, and a listen that failed for another reason shows
		// in the count below.
		if len(tried) == 1 {
			if taker, err := net.Listen("tcp", addr); err == nil {
				t.Cleanup(func() { taker.Close() })
			}
		}
		setHookAddr(t, c, addr)
	})
	if len(tried) != 2 || addr != tried[1] {
		t.Fatalf("addresses tried = %v, returned %s; want two, and the second", tried, addr)
	}
	l, known := hooks.ReadListener(c)
	if !known || !l.Listening() || l.Addr != addr || !listens(addr) {
		t.Fatalf("listener = %+v (known %v); listens on %s: %v", l, known, addr, listens(addr))
	}
}

// The listener that takes webhooks takes the yard's peers too, under
// /peers/, beside /hooks/: a peer's signed message is taken there, and a
// yard that is no peer is refused.
func TestHookListenerTakesPeers(t *testing.T) {
	c := home(t)
	addr := startOnFreeAddr(t, c, func(addr string) { setHookAddr(t, c, addr) })
	ctx := context.Background()
	cl := rpc.NewClient(c.Socket())
	defer cl.Close(ctx)

	if _, _, err := yardkey.Ensure(c.PeerKey()); err != nil {
		t.Fatal(err)
	}
	own, err := store.Load(c.PeerKey())
	if err != nil {
		t.Fatal(err)
	}
	north, _, err := yardkey.Ensure(filepath.Join(t.TempDir(), "north.key"))
	if err != nil {
		t.Fatal(err)
	}
	northEnc, northEncPub, _, err := yardkey.EnsureEnc(filepath.Join(t.TempDir(), "north-enc.key"))
	if err != nil {
		t.Fatal(err)
	}
	_, ownEncPub, _, err := yardkey.EnsureEnc(c.PeerEncKey())
	if err != nil {
		t.Fatal(err)
	}
	stranger, _, err := yardkey.Ensure(filepath.Join(t.TempDir(), "stranger.key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.AddPeer(ctx, store.Peer{Name: "north", Key: store.Public(north), Enc: northEncPub}, "me"); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.AllowPeer(ctx, "north", store.PeerAllow{Receive: []string{"ping"}}, "me"); err != nil {
		t.Fatal(err)
	}
	me := store.Peer{Name: "yard", Key: store.Public(own), Enc: ownEncPub, URL: "http://" + addr}
	m := store.PeerMessage{ID: "m1", Kind: "ping", Body: []byte(`{}`)}
	if err := (&yards.Caller{Key: north, Enc: northEnc}).Push(ctx, me, m); err != nil {
		t.Fatalf("north's message: %v", err)
	}
	if err := (&yards.Caller{Key: stranger, Enc: northEnc}).Push(ctx, me, m); err == nil || !strings.Contains(err.Error(), "answered 401") {
		t.Fatalf("a stranger's message: %v", err)
	}
	evs, err := cl.Events(ctx, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	received := 0
	for _, e := range evs {
		if e.Kind == "peer_message_received" && e.Data["peer"] == "north" && e.Data["id"] == "m1" {
			received++
		}
	}
	if received != 1 {
		t.Fatalf("events = %+v", evs)
	}
	// The hooks are still served beside it, and nothing else is.
	for path, want := range map[string]int{"/hooks/none": http.StatusNotFound, "/call/Peers": http.StatusNotFound, "/": http.StatusNotFound} {
		resp, err := http.Post("http://"+addr+path, "application/json", strings.NewReader("[]"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("POST %s: %d, want %d", path, resp.StatusCode, want)
		}
	}
}

// The listener serves a user's device under /user/ beside the hooks and the
// peers: with the device's token /user/me says who it is and a call of the
// doc is answered, without it the path is 401, and a method that is not of
// the doc is not there even with it. A yard with no device has no /user/.
func TestUserDeviceOnTheListener(t *testing.T) {
	c := home(t)
	st, err := store.Open(c.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := st.AddUser(ctx, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "name", "south", "jo"); err != nil {
		t.Fatal(err)
	}
	d, err := st.AddDevice(ctx, "jo", "phone", "jo")
	if err != nil {
		t.Fatal(err)
	}
	st.Close(ctx)
	addr := startOnFreeAddr(t, c, func(addr string) { setHookAddr(t, c, addr) })

	ask := func(addr, method, path, token string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, "http://"+addr+path, strings.NewReader("{}"))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(body))
	}
	if code, body := ask(addr, http.MethodGet, "/user/me", d.Token); code != http.StatusOK || body != `{"user":"jo","said_by":"phone","yard":"south","yardmaster":"yardmaster"}` {
		t.Errorf("GET /user/me with the token = %d %s", code, body)
	}
	if code, body := ask(addr, http.MethodPost, "/user/call/GetUser", d.Token); code != http.StatusOK || !strings.Contains(body, `"name":"jo"`) {
		t.Errorf("POST /user/call/GetUser with the token = %d %s", code, body)
	}
	if code, body := ask(addr, http.MethodPost, "/user/call/AddUser", d.Token); code != http.StatusOK || !strings.Contains(body, "no such method AddUser") {
		t.Errorf("POST /user/call/AddUser with the token = %d %s", code, body)
	}
	for _, path := range []string{"/user", "/user/me", "/user/call/GetUser", "/user/events"} {
		if code, _ := ask(addr, http.MethodGet, path, ""); code != http.StatusUnauthorized {
			t.Errorf("%s without a token = %d, want 401", path, code)
		}
	}
	// The socket's own paths are still not on the port.
	if code, _ := ask(addr, http.MethodPost, "/call/GetUser", d.Token); code != http.StatusNotFound {
		t.Errorf("POST /call/GetUser on the listener = %d, want 404", code)
	}

	none := home(t)
	bare := startOnFreeAddr(t, none, func(addr string) { setHookAddr(t, none, addr) })
	if code, _ := ask(bare, http.MethodGet, "/user/me", d.Token); code != http.StatusNotFound {
		t.Errorf("/user/me in a yard with no device = %d, want 404", code)
	}
}

// A device is enrolled on the listener of a running yard: the operator
// opens a pairing over the socket, and the code posted to /user/pair is
// answered with the token the device then calls with. The yard had no
// device, so /user/ was not there before, and the path is not there for a
// code once the pairing was used.
func TestUserDevicePairedOnTheListener(t *testing.T) {
	c := home(t)
	st, err := store.Open(c.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := st.AddUser(ctx, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	st.Close(ctx)
	addr := startOnFreeAddr(t, c, func(addr string) { setHookAddr(t, c, addr) })
	cl := rpc.NewClient(c.Socket())
	defer cl.Close(ctx)

	post := func(path, token, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, "http://"+addr+path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		answer, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(answer))
	}
	if code, _ := post(rpc.PairPath, "", `{"code":"K7QM-2XPD"}`); code != http.StatusNotFound {
		t.Fatalf("POST %s with no pairing open = %d, want 404", rpc.PairPath, code)
	}
	opened, err := cl.DevicePairOpen(ctx, "jo", "phone", "jo")
	if err != nil {
		t.Fatal(err)
	}
	if code, body := post(rpc.PairPath, "", `{"code":"0000-0000"}`); code != http.StatusForbidden || body != `{"refused":"wrong_code"}` {
		t.Errorf("a wrong code = %d %s, want 403 wrong_code", code, body)
	}
	code, body := post(rpc.PairPath, "", `{"code":"`+opened.Code+`"}`)
	var got rpc.Paired
	if err := json.Unmarshal([]byte(body), &got); code != http.StatusOK || err != nil || got.User != "jo" || got.Device != "phone" {
		t.Fatalf("the code = %d %s (%v)", code, body, err)
	}
	if code, body := post("/user/call/GetUser", got.Token, `{}`); code != http.StatusOK || !strings.Contains(body, `"name":"jo"`) {
		t.Errorf("POST /user/call/GetUser with the token the device was given = %d %s", code, body)
	}
	if code, _ := post(rpc.PairPath, "", `{"code":"`+opened.Code+`"}`); code != http.StatusUnauthorized {
		t.Errorf("the code again = %d, want 401", code)
	}
	// A call records the device's last use only when no other write of the
	// yard is in flight: a daemon pass, or the pairing's own write, can be,
	// and the store then answers from its read and leaves the stamp to the
	// device's next call (ResolveDevice, yardr-wpgc). So the stamp lands on
	// some call soon, not on a given one, and the device calls until it has.
	waitFor(t, "a call with the device's token to record its last use", func() bool {
		if code, body := post("/user/call/GetUser", got.Token, `{}`); code != http.StatusOK || !strings.Contains(body, `"name":"jo"`) {
			t.Fatalf("POST /user/call/GetUser with the token the device was given, again = %d %s", code, body)
		}
		devices, err := cl.Devices(ctx, "jo")
		if err != nil || len(devices) != 1 || devices[0].Name != "phone" {
			t.Fatalf("devices = %+v, %v", devices, err)
		}
		return devices[0].LastUsed != nil
	})
	// The plain answer holds no voucher.
	if got.Voucher != "" || strings.Contains(body, "voucher") {
		t.Errorf("the plain answer holds a voucher: %s", body)
	}

	// The server makes its channel key for the approval's QR before the
	// first device pairs; the sealed answer carries a voucher signed by the
	// yard's identity and names the device that paired.
	identity, _, err := yardkey.Ensure(c.PeerKey())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.AddUser(ctx, "sam", "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.AddUser(ctx, "kim", "jo"); err != nil {
		t.Fatal(err)
	}
	b, err := cl.CreateBead(ctx, store.NewBead{Title: "Pair a device"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	seq, err := cl.AddAsk(ctx, b.ID, store.Ask{Question: "Pair the tablet?", To: "jo",
		Context: []string{"the tablet arrived"},
		Run:     &store.AskRun{Argv: []string{"user", "device", "pair", "sam", "tablet"}}}, "kim")
	if err != nil {
		t.Fatal(err)
	}
	approved, err := cl.ApproveAsk(ctx, b.ID, seq, "jo", true, nil)
	if err != nil || approved == nil || approved.Pairing == nil {
		t.Fatalf("the operator's approval = %+v, %v", approved, err)
	}
	p := approved.Pairing
	if p.User != "sam" || p.Device != "tablet" || p.Address != "http://"+addr || p.ChannelKey == "" {
		t.Fatalf("the first-device approval answered %+v", p)
	}
	channelPublic, err := yardkey.ParseChannelPublic(p.ChannelKey)
	if err != nil {
		t.Fatalf("the first-device approval's channel key = %q (%v)", p.ChannelKey, err)
	}
	channel, _, err := yardkey.EnsureChannel(c.ChannelKey())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(channelPublic, channel.Public) {
		t.Fatal("the first-device approval carries a channel key different from the listener's")
	}
	device, err := yardkey.GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	first, opening, err := yardkey.StartPairingHandshake(device, channelPublic, p.Code)
	if err != nil {
		t.Fatal(err)
	}
	sealedBody, _ := json.Marshal(map[string]string{"sealed": base64.RawURLEncoding.EncodeToString(first)})
	code, body = post(rpc.PairPath, "", string(sealedBody))
	var sealed struct {
		Sealed string `json:"sealed"`
	}
	if err := json.Unmarshal([]byte(body), &sealed); code != http.StatusOK || err != nil {
		t.Fatalf("the sealed code = %d %s (%v)", code, body, err)
	}
	reply, err := base64.RawURLEncoding.DecodeString(sealed.Sealed)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := opening.OpenAnswer(reply)
	if err != nil {
		t.Fatalf("the device could not open its answer: %v", err)
	}
	var paired rpc.Paired
	if err := json.Unmarshal(plain, &paired); err != nil || paired.User != "sam" || paired.Device != "tablet" || paired.Voucher == "" {
		t.Fatalf("the sealed answer = %s (%v), want the tablet's token and a voucher", plain, err)
	}
	v, err := yardkey.VerifyVoucher(store.Public(identity), paired.Voucher, time.Now())
	if err != nil || v.User != "sam" || v.Device != "tablet" || v.Yard != paired.Yard || v.Yard == "" {
		t.Errorf("the voucher verifies as %+v (%v) against the yard's identity, want sam's tablet at %q", v, err, paired.Yard)
	}
}
