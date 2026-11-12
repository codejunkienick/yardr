package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"rsc.io/qr"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/edgelink"
	"yardr.dev/yardr/internal/hooks"
	"yardr.dev/yardr/internal/rpc"
	"yardr.dev/yardr/internal/standin"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
	"yardr.dev/yardr/internal/yardkey"
)

// devicePairYard is userYard with a listener that serves the devices of its
// store, on an address of its own, as the yard's server says it listens.
func devicePairYard(t *testing.T) (st store.API, own *store.Store, addr string) {
	t.Helper()
	api, own := storetest.NewOwned(t)
	st = userYardOn(t, api)
	users := rpc.NewUserServer(own, func(context.Context) string { return "south" }, nil)
	// As the yard's server does: a sealed redemption is opened with
	// the channel key of the yard's home, made there by the pair
	// command below.
	users.SetChannelKeyFile(config.At(yardEnv(t, st)["YARDR_HOME"]).ChannelKey())
	srv := httptest.NewServer(users.Handler())
	t.Cleanup(srv.Close)
	said(t, &config.Config{Home: yardEnv(t, st)["YARDR_HOME"]}, hooks.Listener{Addr: strings.TrimPrefix(srv.URL, "http://"), From: hooks.FromSetting})
	return st, own, srv.URL
}

var devicePairShown = regexp.MustCompile(`(?m)^  address  (\S+)\n(?:  yard     (\S+)\n)?  code     ([0-9A-Z]{4}-[0-9A-Z]{4})\n(?:  channel  (\S+)\n)?`)

// redeemCode calls the yard at addr with the code, as a device does.
func redeemCode(t *testing.T, addr, code string) (int, rpc.Paired) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code})
	resp, err := http.Post(addr+rpc.PairPath, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got rpc.Paired
	answer, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(answer, &got)
	return resp.StatusCode, got
}

// yardr user device pair opens a pairing and prints the code, how long it
// holds and the address, and never a token: the device that calls the
// address with the code is given it, and is then a device as device add
// makes one. --end ends a pairing, and the code with it.
func TestDevicePairCommand(t *testing.T) {
	t.Parallel()
	st, own, addr := devicePairYard(t)
	ctx := context.Background()
	mustAs(t, st, "jo", "user", "add", "ada")

	out := mustAs(t, st, "jo", "user", "device", "pair", "jo", "phone")
	m := devicePairShown.FindStringSubmatch(out)
	if m == nil || m[1] != addr || m[2] != "" || len(m[4]) != 43 {
		t.Fatalf("device pair shows no code at %s, with no yard and the yard's channel key:\n%s", addr, out)
	}
	until := regexp.MustCompile(`^Pairing is open for 10 minutes, until (\d\d:\d\d:\d\d), for the device phone of jo\. Give the device this yard's address and the code:\n`).FindStringSubmatch(out)
	if until == nil {
		t.Fatalf("device pair does not say how long the code holds:\n%s", out)
	}
	wantText(t, out,
		"The code is good once: a device that scans the QR code is answered inside a sealed channel, and whoever carries the connection reads neither the code nor the device token. 5 wrong codes end the pairing, and so does: ",
		" user device pair --end\n",
		"It then calls as jo until: ", " user device rm jo phone\n")
	if strings.Contains(out, "yardr-dev-") || strings.Contains(out, "█") || strings.Contains(out, "is not the operator") || strings.Contains(out, "plain http") {
		t.Fatalf("device pair, without --qr, for the operator, on this machine:\n%s", out)
	}
	if list := mustAs(t, st, "jo", "user", "device", "list"); !strings.HasPrefix(list, "no devices") {
		t.Fatalf("a device is listed before its code was used:\n%s", list)
	}
	// Opening for another user names the holder's yard user, and nothing
	// of the device or when its code runs out.
	refusedAs(t, st, "jo", "a pairing is already open for jo, try again in a few minutes", "user", "device", "pair", "ada", "laptop")
	refusedAs(t, st, "jo", "a pairing is open already, for the device phone of jo until "+until[1], "user", "device", "pair", "jo", "laptop")

	status, got := redeemCode(t, m[1], m[3])
	if status != http.StatusOK || !deviceToken.MatchString(got.Token) || got.User != "jo" || got.Device != "phone" {
		t.Fatalf("the code at the address = %d %+v", status, got)
	}
	if d, err := own.ResolveDevice(ctx, got.Token, "100.64.0.7"); err != nil || d == nil || d.User != "jo" || d.Name != "phone" {
		t.Fatalf("the token the device was given resolves to %+v, %v", d, err)
	}
	wantText(t, mustAs(t, st, "jo", "user", "device", "list"), "jo    phone   ", " from 100.64.0.7\n")
	for _, args := range [][]string{{"user", "device", "list", "--json"}, {"user", "show", "jo", "--json"}, {"events", "--json"}} {
		if out := mustAs(t, st, "jo", args...); strings.Contains(out, "yardr-dev-") || strings.Contains(out, m[3]) {
			t.Fatalf("yardr %s says something of the token or the code:\n%s", strings.Join(args, " "), out)
		}
	}

	// For a user who is not the operator, at an address of the operator's
	// giving, in JSON: what the QR code holds is there, and no picture.
	var opened devicePaired
	out = mustAs(t, st, "jo", "user", "device", "pair", "ada", "laptop", "--url", "https://mac.example.net/", "--qr", "--json")
	if err := json.Unmarshal([]byte(out), &opened); err != nil || opened.User != "ada" || opened.Device != "laptop" || opened.Pairing == "" ||
		opened.Address != "https://mac.example.net" || opened.Yard != "" || len(opened.ChannelKey) != 43 || opened.ChannelSignature == "" ||
		opened.QR != store.PairingQR("https://mac.example.net", opened.Code, "", opened.ChannelKey) ||
		time.Until(opened.Expires) > store.TTL || time.Until(opened.Expires) < store.TTL-time.Minute || strings.Contains(out, "█") {
		t.Fatalf("device pair --json: %+v, %v\n%s", opened, err, out)
	}
	// The channel key is this yard's, signed by its identity.
	cfg := config.At(yardEnv(t, st)["YARDR_HOME"])
	channel, err := yardkey.LoadChannel(cfg.ChannelKey())
	if err != nil || yardkey.ChannelPublic(channel) != opened.ChannelKey {
		t.Fatalf("the pair's channel key is not the yard's in %s: %v", cfg.ChannelKey(), err)
	}
	identity, err := store.Load(cfg.PeerKey())
	if err != nil {
		t.Fatal(err)
	}
	if err := yardkey.VerifyChannelKey(store.Public(identity), opened.ChannelKey, opened.ChannelSignature); err != nil {
		t.Fatalf("the channel signature does not check against the yard's identity: %v", err)
	}
	if out := mustAs(t, st, "jo", "user", "device", "pair", "--end"); out != "the pairing for the device laptop of ada is ended: its code is good for nothing\n" {
		t.Fatalf("device pair --end:\n%s", out)
	}
	if status, got := redeemCode(t, addr, opened.Code); status != http.StatusUnauthorized || got.Token != "" {
		t.Fatalf("the code of an ended pairing = %d %+v", status, got)
	}
	refusedAs(t, st, "jo", "this yard has no device pairing open", "user", "device", "pair", "--end")
	out = mustAs(t, st, "jo", "user", "device", "pair", "ada", "laptop", "--url", "http://10.0.0.5:8790")
	wantText(t, out, "  address  http://10.0.0.5:8790\n",
		"ada is not an operator, and the device reads every bead in the yard, with its notes, and the mail of ada alone.\n",
		"http://10.0.0.5:8790 is plain http: a code typed there crosses readable, and the token after it; a device that scans the QR code is answered sealed even so.")
	mustAs(t, st, "jo", "user", "device", "pair", "--end")

	refusedAs(t, st, "ada", "only the operator (jo) may add a device; this is ada", "user", "device", "pair", "ada", "watch")
	refusedAs(t, st, "ada", "only the operator (jo) may end a device's pairing; this is ada", "user", "device", "pair", "--end")
	refusedAs(t, st, "jo", "jo has a device phone already", "user", "device", "pair", "jo", "phone")
	refusedAs(t, st, "jo", "user carol: not found", "user", "device", "pair", "carol", "phone")
	refusedAs(t, st, "jo", "the address the device reaches this yard at", "user", "device", "pair", "ada", "watch", "--url", "mac.example.net")
	refusedAs(t, st, "jo", "the address the device reaches this yard at", "user", "device", "pair", "ada", "watch", "--url", "https://mac.example.net/#K7QM-2XPD")
	refusedAs(t, st, "jo", "give the user and the device's name, or --end", "user", "device", "pair", "ada")
	refusedAs(t, st, "jo", "--end ends the device pairing this yard has open: it takes nothing else", "user", "device", "pair", "ada", "watch", "--end")
	if own.DevicePairOpened(ctx) {
		t.Fatal("a refused command left a pairing open")
	}
}

// --qr draws a QR code under the code, and what it holds is the address,
// '#' and the code, and the yard's channel public key as its fourth
// field (the yard field empty where no yard is named): read back module
// by module, the picture is the QR code of exactly that.
func TestDevicePairQR(t *testing.T) {
	t.Parallel()
	st, _, _ := devicePairYard(t)
	out := mustAs(t, st, "jo", "user", "device", "pair", "jo", "phone", "--url", "https://mac.example.net", "--qr")
	m := devicePairShown.FindStringSubmatch(out)
	if m == nil || m[2] != "" {
		t.Fatalf("device pair --qr shows no code, with no yard:\n%s", out)
	}
	channel, err := yardkey.LoadChannel(config.At(yardEnv(t, st)["YARDR_HOME"]).ChannelKey())
	if err != nil {
		t.Fatal(err)
	}
	if m[4] != yardkey.ChannelPublic(channel) {
		t.Fatalf("the pair shows channel %q, want the yard's %q", m[4], yardkey.ChannelPublic(channel))
	}
	holds := m[1] + "#" + m[3] + "##" + m[4]
	if holds != store.PairingQR("https://mac.example.net", m[3], "", m[4]) {
		t.Fatalf("the QR code holds %q", holds)
	}
	want, err := qr.Encode(holds, qr.M)
	if err != nil {
		t.Fatal(err)
	}
	// The picture's lines: the quiet zone around it is part of each, and of
	// their number.
	const quiet = 4
	var rows [][]rune
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "  "); ok && strings.Trim(rest, "█▀▄ ") == "" && len([]rune(rest)) == want.Size+2*quiet {
			rows = append(rows, []rune(rest))
		}
	}
	if len(rows) != (want.Size+2*quiet+1)/2 {
		t.Fatalf("%d lines of a picture for a code of %d modules:\n%s", len(rows), want.Size, out)
	}
	drawn := func(x, y int) bool {
		switch rows[(y+quiet)/2][x+quiet] {
		case '█':
			return true
		case '▀':
			return (y+quiet)%2 == 0
		case '▄':
			return (y+quiet)%2 == 1
		}
		return false
	}
	for y := -quiet; y < want.Size+quiet; y++ {
		for x := -quiet; x < want.Size+quiet; x++ {
			in := x >= 0 && y >= 0 && x < want.Size && y < want.Size
			if drawn(x, y) != (in && want.Black(x, y)) {
				t.Fatalf("the module at %d,%d is drawn %v:\n%s", x, y, drawn(x, y), out)
			}
		}
	}
	// On a terminal it is black on white, whatever the terminal's colours.
	coloured, err := drawQR(holds, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(coloured, "\n"), "\n") {
		if !strings.HasPrefix(line, "  \x1b[30;47m") || !strings.HasSuffix(line, "\x1b[0m") {
			t.Fatalf("a line of the coloured picture is %q", line)
		}
	}
}

// A device that pairs by the new QR redeems the code the QR carries
// inside a Noise handshake to the channel key of its fourth field: the
// fields split at '#' name the address, the code, the empty yard field
// and the key, the answer opens only with the device's own key, and
// the device stands recorded with that key pinned. The same QR text
// replayed buys nothing.
func TestDevicePairSealedByTheQR(t *testing.T) {
	t.Parallel()
	st, own, addr := devicePairYard(t)
	ctx := context.Background()
	mustAs(t, st, "jo", "user", "add", "ada")
	var opened devicePaired
	out := mustAs(t, st, "jo", "user", "device", "pair", "ada", "phone", "--json")
	if err := json.Unmarshal([]byte(out), &opened); err != nil {
		t.Fatalf("device pair --json: %v\n%s", err, out)
	}
	fields := strings.Split(opened.QR, "#")
	if len(fields) != 4 || fields[0] != opened.Address || fields[1] != opened.Code || fields[2] != "" || fields[3] != opened.ChannelKey {
		t.Fatalf("the QR holds %q, want address#code##channel", opened.QR)
	}
	yardPublic, err := yardkey.ParseChannelPublic(fields[3])
	if err != nil {
		t.Fatal(err)
	}
	device, err := yardkey.GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	first, opening, err := yardkey.StartPairingHandshake(device, yardPublic, fields[1])
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"sealed": base64.RawURLEncoding.EncodeToString(first)})
	resp, err := http.Post(addr+rpc.PairPath, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	answer, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.Contains(string(answer), opened.Code) || strings.Contains(string(answer), "yardr-dev-") {
		t.Fatalf("the sealed redemption = %d %s, want 200 with neither the code nor a token readable", resp.StatusCode, answer)
	}
	var sealed struct {
		Sealed string `json:"sealed"`
	}
	if err := json.Unmarshal(answer, &sealed); err != nil {
		t.Fatal(err)
	}
	reply, err := base64.RawURLEncoding.DecodeString(sealed.Sealed)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := opening.OpenAnswer(reply)
	if err != nil {
		t.Fatalf("the device could not open its answer: %v", err)
	}
	var got rpc.Paired
	if err := json.Unmarshal(plain, &got); err != nil || !deviceToken.MatchString(got.Token) || got.User != "ada" || got.Device != "phone" {
		t.Fatalf("the sealed redemption is answered %q (%v)", plain, err)
	}
	devices, err := own.Devices(ctx, "")
	if err != nil || len(devices) != 1 || devices[0].ChannelKey != yardkey.ChannelPublic(device) {
		t.Fatalf("devices = %+v, %v, want ada's phone with the device key pinned", devices, err)
	}
	// The transcript replayed: no second token, and no second device.
	resp, err = http.Post(addr+rpc.PairPath, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	answer, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || strings.Contains(string(answer), "yardr-dev-") {
		t.Errorf("the sealed transcript again = %d %s, want 401 and no token", resp.StatusCode, answer)
	}
	if devices, err := own.Devices(ctx, ""); err != nil || len(devices) != 1 {
		t.Fatalf("devices after the replay = %+v, %v, want still the one", devices, err)
	}
}

// A yard that listens nowhere but whose link to the edge is up opens a
// pairing all the same: the default address is the edge's, beside this
// yard's handle/name at the edge, and the device redeems the code down the
// link, naming the yard handle/name with the call, for the user the edge
// says presents it and for no other. With the link down, no pairing.
func TestDevicePairOverTheLinkWithNoListener(t *testing.T) {
	t.Parallel()
	api, own := storetest.NewOwned(t)
	st := userYardOn(t, api)
	edge, err := standin.NewEdge()
	if err != nil {
		t.Fatal(err)
	}
	edge.Behave(func(b *standin.EdgeBehaviour) { b.Name = "edgeyard" })
	srv := httptest.NewTLSServer(edge)
	t.Cleanup(srv.Close)
	mustEdge(t, st, srv.Client(), "edge", "connect", srv.URL)
	cfg := config.At(yardEnv(t, st)["YARDR_HOME"])
	ctx := context.Background()

	sayLink(t, cfg, edgelink.State{URL: srv.URL, Handle: "mckean", Since: time.Now(), Reason: edgelink.DownPings, Ups: 1})
	refusedAs(t, st, "jo", "this yard has no listener, so no device can call it with a code: ", "user", "device", "pair", "jo", "phone")

	sayLink(t, cfg, edgelink.State{URL: srv.URL, Handle: "mckean", Up: true, Since: time.Now(), Ups: 1})
	yard, err := store.Load(cfg.PeerKey())
	if err != nil {
		t.Fatal(err)
	}
	u := rpc.NewUserServer(own, func(context.Context) string { return "south" }, nil)
	l := &edgelink.Link{
		Edge: store.Edge{URL: srv.URL, Key: edge.PublicKey(), Handle: "mckean"},
		Key:  yard, Handler: u.Handler(), HTTP: srv.Client(),
		Backoff: 10 * time.Millisecond, BackoffMax: 20 * time.Millisecond,
	}
	lctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); l.Run(lctx) }()
	defer func() { cancel(); <-done }()

	out := mustAs(t, st, "jo", "user", "device", "pair", "jo", "phone", "--qr")
	m := devicePairShown.FindStringSubmatch(out)
	if m == nil || m[1] != srv.URL || m[2] != "mckean/edgeyard" {
		t.Fatalf("device pair over the link shows no code at %s, for the yard mckean/edgeyard:\n%s", srv.URL, out)
	}
	if !strings.Contains(out, "█") {
		t.Fatalf("device pair over the link --qr draws no QR code:\n%s", out)
	}
	var link *standin.EdgeLink
	select {
	case link = <-edge.Links():
	case <-time.After(10 * time.Second):
		t.Fatal("the yard's link never came up at the edge")
	}
	body, _ := json.Marshal(map[string]string{"code": m[3]})
	dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dcancel()
	// The edge names the signed-in account that presents the redemption, in
	// a header of the frame it signs, and only the user the pairing was
	// opened for redeems: another user of the yard is refused as a wrong
	// code is, with no device, and the code is still good for jo.
	if _, err := st.AddUser(ctx, "sam", "jo"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, store.WebJWTUsersSetting, "jo@example.net=jo, sam@example.net=sam", "jo"); err != nil {
		t.Fatal(err)
	}
	h := http.Header{"X-Edge-Yard": {m[2]}, rpc.PresenterHeader: {"sam@example.net"}}
	a, err := link.Deliver(dctx, link.Frame(http.MethodPost, rpc.PairPath, h, body, false))
	if err != nil || a.Status != http.StatusForbidden || strings.TrimSpace(string(a.Body)) != `{"refused":"wrong_code"}` {
		t.Fatalf("the code down the link, presented by sam = %+v, %v, want 403 wrong_code", a, err)
	}
	if devices, err := own.Devices(ctx, ""); err != nil || len(devices) != 0 {
		t.Fatalf("devices after the refusal = %+v, %v", devices, err)
	}
	h.Set(rpc.PresenterHeader, "jo@example.net")
	a, err = link.Deliver(dctx, link.Frame(http.MethodPost, rpc.PairPath, h, body, false))
	if err != nil {
		t.Fatal(err)
	}
	var got rpc.Paired
	if err := json.Unmarshal(a.Body, &got); err != nil || a.Status != http.StatusOK || !deviceToken.MatchString(got.Token) {
		t.Fatalf("the code down the link = %d %s, %v", a.Status, a.Body, err)
	}
	if d, err := own.ResolveDevice(ctx, got.Token, "100.64.0.7"); err != nil || d == nil || d.User != "jo" || d.Name != "phone" {
		t.Fatalf("the token the device was given resolves to %+v, %v", d, err)
	}

	// The payload carries the yard's handle/name beside the edge's
	// address, and the QR code holds both with the code.
	var opened devicePaired
	out = mustAs(t, st, "jo", "user", "device", "pair", "jo", "laptop", "--json")
	if err := json.Unmarshal([]byte(out), &opened); err != nil || opened.Address != srv.URL || opened.Yard != "mckean/edgeyard" ||
		opened.QR != srv.URL+"#"+opened.Code+"#mckean/edgeyard#"+opened.ChannelKey || len(opened.ChannelKey) != 43 {
		t.Fatalf("device pair over the link --json: %+v, %v\n%s", opened, err, out)
	}
	if err := yardkey.VerifyChannelKey(store.Public(yard), opened.ChannelKey, opened.ChannelSignature); err != nil {
		t.Fatalf("the channel signature over the link does not check against the yard's key: %v", err)
	}
}

// A device needs an address to call: a yard without a listener opens no
// pairing, and one that listens on every address says to give --url.
func TestDevicePairNeedsAnAddress(t *testing.T) {
	t.Parallel()
	st, own, _ := devicePairYard(t)
	cfg := &config.Config{Home: yardEnv(t, st)["YARDR_HOME"]}
	said(t, cfg, hooks.Listener{Addr: ":8790", From: hooks.FromSetting})
	refusedAs(t, st, "jo", "the listener's address (:8790) is none to give a device: say where the device reaches this yard with --url", "user", "device", "pair", "jo", "phone")
	said(t, cfg, hooks.Listener{})
	refusedAs(t, st, "jo", "this yard has no listener, so no device can call it with a code: ", "user", "device", "pair", "jo", "phone", "--url", "https://mac.example.net")
	if own.DevicePairOpened(context.Background()) {
		t.Fatal("a refused command left a pairing open")
	}
}
