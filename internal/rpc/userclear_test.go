package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/store"
)

// A save of a stage file and a copy of a depot's flows are held to an
// approval's rule for the operator's device token that goes with them: over
// https or from the yard's own machine, from no request whose proxy says it
// came in plain, and from the router's peer never. Down the link from the
// edge a call is taken where the edge could not misuse the token: on a hosted
// yard, or when the call came sealed. hosted is asked for a call that came down
// the link and is not sealed, once, and for no other: not off the link, and not
// for a sealed call, which any yard takes.
func TestStageWriteInTheClear(t *testing.T) {
	const (
		fromProxyInPlain = "the proxy in front of the yard says this request came to it in plain HTTP"
		fromElsewhere    = "this request reached the yard's listener in plain HTTP from another machine"
		notSealed        = "does not cross the edge in a call that is not sealed: this request came down the link from the edge"
		fixes            = "Make it from a sealed device (pairing the device with the QR makes it one), or "
		crossed          = "yardr user device rm"
	)
	for _, c := range []struct {
		name   string
		remote string
		header string // X-Forwarded-Proto, when there is one
		sealed bool
		hosted bool
		want   string // "" where the call is taken, else what its refusal says
		asked  int    // how many times hosted was asked
	}{
		{"from the yard's own machine", "127.0.0.1:40000", "", false, false, "", 0},
		{"from the yard's own machine, over https", "[::1]:40000", "https", false, false, "", 0},
		{"from the yard's own machine, hosted", "127.0.0.1:40000", "", false, true, "", 0},
		{"from the yard's own machine, behind a proxy that says plain", "127.0.0.1:40000", "http", false, false, fromProxyInPlain, 0},
		{"from the yard's own machine, behind a proxy that says plain and https", "127.0.0.1:40000", "https, http", false, false, fromProxyInPlain, 0},
		{"from another machine", "203.0.113.9:40000", "", false, false, fromElsewhere, 0},
		{"from another machine that says https", "203.0.113.9:40000", "https", false, false, fromElsewhere, 0},
		{"from another machine, hosted", "203.0.113.9:40000", "https", false, true, fromElsewhere, 0},
		{"down the link, plain, on a yard that is not hosted", linkRemoteAddr, "", false, false, notSealed, 1},
		{"down the link, plain, on a hosted yard", linkRemoteAddr, "", false, true, "", 1},
		{"down the link, plain, on a hosted yard behind a proxy that says plain", linkRemoteAddr, "http", false, true, fromProxyInPlain, 1},
		{"down the link, sealed, on a yard that is not hosted", linkRemoteAddr, "", true, false, "", 0},
		{"down the link, sealed, on a hosted yard", linkRemoteAddr, "", true, true, "", 0},
		{"down the link, sealed, behind a proxy that says plain", linkRemoteAddr, "http", true, false, fromProxyInPlain, 0},
	} {
		for _, call := range []struct{ name, here string }{
			{"PutStageFile", "yardr flow set-stage"},
			{"CopyRepoFlows", "yardr flow copy-repo"},
		} {
			t.Run(c.name+"/"+call.name, func(t *testing.T) {
				r := httptest.NewRequest(http.MethodPost, "/user/call/"+call.name, nil)
				r.RemoteAddr = c.remote
				if c.header != "" {
					r.Header.Set("X-Forwarded-Proto", c.header)
				}
				if c.sealed {
					r = r.WithContext(context.WithValue(r.Context(), e2eSessionKey{}, &e2eSession{}))
				}
				asked := 0
				got := stageWriteInTheClear(r, call.name, func() bool { asked++; return c.hosted })
				if asked != c.asked {
					t.Errorf("hosted was asked %d times, want %d", asked, c.asked)
				}
				if c.want == "" {
					if got != "" {
						t.Errorf("the call is refused: %q", got)
					}
					return
				}
				for _, want := range []string{c.want, "`" + call.here + "`", crossed} {
					if !strings.Contains(got, want) {
						t.Errorf("the refusal is %q, want it to say %q", got, want)
					}
				}
				if c.want == notSealed {
					for _, want := range []string{fixes, "The edge saw the device's token with it"} {
						if !strings.Contains(got, want) {
							t.Errorf("the refusal is %q, want it to say %q", got, want)
						}
					}
				}
			})
		}
	}
}

// A refusal that gives its fix after "or" reads on from it, so the fix starts
// in lower case: "..., or read it on the yard's machine." The two refusals that
// do are the one for a request from another machine and the one for a request
// behind a proxy that says plain. The refusal down the link has no "or" and
// says the same words as a sentence of its own, where they keep their capital;
// the grant list, the telemetry preview and the telemetry switch's change have
// such a refusal, which makes each of them a constant used both ways.
func TestRefusalsGiveTheFixAfterOrInLowerCase(t *testing.T) {
	const here = "read it on the yard's machine"
	for _, call := range []struct{ name, fix string }{
		{"Teammates", here + "."},
		{"UninviteTeammate", "file it on the yard's machine."},
		{"IdentityHeldPayloads", "read them on the yard's machine."},
		{"HookPayload", here + " (`yardr hook show <seq>`)."},
		{"SourceGrants", here + " (`yardr source grants`)."},
		{"SessionScreen", here + "."},
		{"Line", here + "."},
		{"TelemetryShow", here + " (`yardr telemetry show`)."},
		{"TelemetrySet", "change it on the yard's machine (`yardr telemetry on` or `yardr telemetry off`)."},
	} {
		for _, c := range []struct{ name, remote, proto string }{
			{"from another machine", "203.0.113.9:40000", ""},
			{"behind a proxy that says plain", "127.0.0.1:40000", "http"},
		} {
			t.Run(call.name+"/"+c.name, func(t *testing.T) {
				got := inTheClear(clearRequest(c.remote, c.proto), call.name, "", false, false, false)
				if want := ", or " + call.fix; !strings.Contains(got, want) {
					t.Errorf("the refusal is %q, want it to say %q", got, want)
				}
			})
		}
	}
	// The calls that share the teammates' rule give the same fix down the link,
	// where a proxy that says plain is the one refusal that has an "or".
	for _, call := range []struct{ name, fix string }{
		{"UninviteTeammate", "file it on the yard's machine."},
		{"IdentityHeldPayloads", "read them on the yard's machine."},
		{"HookPayload", here + " (`yardr hook show <seq>`)."},
	} {
		t.Run(call.name+"/down the link behind a proxy that says plain", func(t *testing.T) {
			got := inTheClear(clearRequest(linkRemoteAddr, "http"), call.name, "", false, false, false)
			if want := ", or " + call.fix; !strings.Contains(got, want) {
				t.Errorf("the refusal is %q, want it to say %q", got, want)
			}
		})
	}
	for _, call := range []struct{ name, sentence string }{
		{"SourceGrants", "Read it on the yard's machine (`yardr source grants`)."},
		{"TelemetryShow", "Read it on the yard's machine (`yardr telemetry show`)."},
		{"TelemetrySet", "Change it on the yard's machine (`yardr telemetry on` or `yardr telemetry off`)."},
	} {
		t.Run(call.name+"/down the link", func(t *testing.T) {
			got := inTheClear(clearRequest(linkRemoteAddr, ""), call.name, "", false, false, false)
			if want := "from the edge. " + call.sentence; !strings.Contains(got, want) {
				t.Errorf("the refusal is %q, want it to say %q", got, want)
			}
		})
	}
}

// operatorInTheClear is inTheClear for every call of OperatorCalls it does not
// judge itself: whether the yard is hosted, or has a router, moves no answer
// of Line, ApproveAsk, SessionScreen or any other, down the link or off it,
// and the two calls it does judge never ask for the router.
func TestOperatorInTheClearChangesNoOtherCall(t *testing.T) {
	ctx := context.Background()
	st, u, _ := userYard(t)
	requests := []func() *http.Request{
		func() *http.Request { return clearRequest("127.0.0.1:40000", "") },
		func() *http.Request { return clearRequest("127.0.0.1:40000", "http") },
		func() *http.Request { return clearRequest("203.0.113.9:40000", "") },
		func() *http.Request { return clearRequest("192.0.2.15:40000", "https") },
		func() *http.Request { return clearRequest(linkRemoteAddr, "") },
		func() *http.Request { return clearRequest(linkRemoteAddr, "http") },
	}
	for _, setting := range []struct{ router, hosted string }{{"", ""}, {"192.0.2.15", "true"}, {"192.0.2.15", "false"}} {
		if err := st.SetSetting(ctx, store.RouterAddrSetting, setting.router, "jo"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetSetting(ctx, store.HostedSetting, setting.hosted, "jo"); err != nil {
			t.Fatal(err)
		}
		for name := range OperatorCalls {
			if name == "PutStageFile" || name == "CopyRepoFlows" {
				continue
			}
			for i, request := range requests {
				want := inTheClear(request(), name, setting.router, false, false, false)
				if got := u.operatorInTheClear(request(), name); got != want {
					t.Errorf("%s, request %d, router %q, hosted %q: operatorInTheClear says %q, inTheClear %q", name, i, setting.router, setting.hosted, got, want)
				}
			}
		}
		// The router is no door for the two calls that write what agents are
		// told: its peer is refused as from any other machine, though the
		// grant list is taken from it.
		for _, name := range []string{"PutStageFile", "CopyRepoFlows"} {
			if got := u.operatorInTheClear(clearRequest("192.0.2.15:40000", "https"), name); !strings.Contains(got, "in plain HTTP from another machine") {
				t.Errorf("%s from the router's peer, router %q: %q, want it refused as from another machine", name, setting.router, got)
			}
		}
	}
}

func clearRequest(remote, proto string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/user/call/Any", nil)
	r.RemoteAddr = remote
	if proto != "" {
		r.Header.Set("X-Forwarded-Proto", proto)
	}
	return r
}

// writeYard is a yard of the doc's two writes: a store flow code with a stage
// work, and a depot web whose repository has a flow of its own, with jo, the
// operator, and sam, a member, each with a device.
type writeYard struct {
	st      *store.Store
	u       *UserServer
	home    string
	jo, sam string
	since   int64 // the newest seq the fixture wrote: what comes after is a call's
}

// stageFlowFixtures puts into st the flow code and the dir depot web.
func stageFlowFixtures(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	flow, err := store.ParseFlow("work -> done\ndone terminal\n")
	if err != nil {
		t.Fatal(err)
	}
	flow.Name = "code"
	if _, err := st.PutFlow(ctx, *flow, "jo"); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	for name, text := range map[string]string{
		".yardr/flows/default.flow":     "build -> done\ndone terminal\n",
		".yardr/flows/default/build.md": "Build it from the repository.\n",
	} {
		if err := os.MkdirAll(filepath.Join(repo, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PutDepot(ctx, store.Depot{Name: "web", Kind: store.DepotDir, Path: repo}, "jo"); err != nil {
		t.Fatal(err)
	}
}

func newWriteYard(t *testing.T) *writeYard {
	t.Helper()
	ctx := context.Background()
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "yard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	y := &writeYard{st: st, home: home, u: NewUserServer(st, func(context.Context) string { return "south" }, nil)}
	for _, name := range []string{"jo", "sam"} {
		if _, err := st.AddUser(ctx, name, "jo"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	stageFlowFixtures(t, st)
	for user, token := range map[string]*string{"jo": &y.jo, "sam": &y.sam} {
		d, err := st.AddDevice(ctx, user, "phone", "jo")
		if err != nil {
			t.Fatal(err)
		}
		*token = d.Token
	}
	y.since = headOf(t, st)
	return y
}

func headOf(t *testing.T, st *store.Store) int64 {
	t.Helper()
	head, err := st.Head(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return int64(head)
}

// post makes the call as a request from remote with the token, and with the
// header X-Forwarded-Proto when proto is not "". A body that is a *unread*
// fails the test if the handler reads it.
func (y *writeYard) post(remote, token, name string, body io.Reader, proto string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/user/call/"+name, body)
	r.RemoteAddr = remote
	r.Header.Set("Authorization", "Bearer "+token)
	if proto != "" {
		r.Header.Set("X-Forwarded-Proto", proto)
	}
	w := httptest.NewRecorder()
	y.u.Handler().ServeHTTP(w, r)
	return w
}

// answer is what a call answered: its result, or the words it was refused in.
func answer(t *testing.T, w *httptest.ResponseRecorder) (result json.RawMessage, refused string) {
	t.Helper()
	return answerOf(t, w.Body.Bytes())
}

func answerOf(t *testing.T, raw []byte) (result json.RawMessage, refused string) {
	t.Helper()
	var got struct {
		Result json.RawMessage `json:"result"`
		Error  *wireError      `json:"error"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the answer is %s (%v)", raw, err)
	}
	if got.Error != nil {
		return nil, got.Error.Message
	}
	return got.Result, ""
}

// events are the events of kind that the calls of the test wrote.
func (y *writeYard) events(t *testing.T, kind string) []store.Event {
	t.Helper()
	return eventsOfKindSince(t, y.st, kind, y.since)
}

func eventsOfKindSince(t *testing.T, st *store.Store, kind string, since int64) []store.Event {
	t.Helper()
	evs, err := st.EventsOfKind(context.Background(), kind, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Event
	for _, e := range evs {
		if int64(e.Seq) > since {
			out = append(out, e)
		}
	}
	return out
}

// stageText is the text of the stage file of work in code, "" where there is none.
func (y *writeYard) stageText(t *testing.T) string {
	t.Helper()
	sf, err := store.HomeStageFile(y.home, "code", "work")
	if err != nil {
		t.Fatal(err)
	}
	if sf == nil {
		return ""
	}
	return sf.Text
}

func (y *writeYard) copied(t *testing.T) bool {
	t.Helper()
	_, err := y.st.GetFlow(context.Background(), "web.default")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

const (
	putStage   = `{"flow":"code","stage":"work","text":"Build it.\n","weighed":true}`
	copyFlows  = `{"depot":"web","weighed":true}`
	weightyPut = "saving a stage file is weighty (code): a client sends \"weighed\": true once its person has passed its gate"
	weightyCpy = "copying a depot's repository flows is weighty (code): a client sends \"weighed\": true once its person has passed its gate"
)

// A device of the operator saves a stage file and copies a depot's flows, as
// the card of each is approved: under every bound the call has. It says
// weighed, which its client says once its person has passed its gate, and the
// store holds the write to the operator; its actor is the device's user; it is
// served over https or from the yard's own machine, and down the link from the
// edge on a hosted yard or sealed; and what it writes says which device wrote
// it. A device of anyone else is told there is no such method, whatever the
// call asks, and a pane on the socket still files the card instead
// (TestSocketServesACrewPaneTheYardsRouting).
func TestUserSavesAStageFileAndCopiesFlows(t *testing.T) {
	const phone = "127.0.0.1:40000"
	t.Run("from the yard's own machine", func(t *testing.T) {
		y := newWriteYard(t)
		// weighed is the client's word, and without it nothing is read or written.
		for _, c := range []struct{ name, body, want string }{
			{"PutStageFile", `{"flow":"code","stage":"work","text":"Build it.\n"}`, weightyPut},
			{"PutStageFile", `{"flow":"code","stage":"work","text":"Build it.\n","weighed":false}`, weightyPut},
			{"PutStageFile", `{"flow":"no-flow","stage":"work","text":"Build it.\n"}`, weightyPut},
			{"CopyRepoFlows", `{"depot":"web"}`, weightyCpy},
			{"CopyRepoFlows", `{"depot":"web","weighed":false}`, weightyCpy},
			{"CopyRepoFlows", `{"depot":"no-depot"}`, weightyCpy},
		} {
			if _, refused := answer(t, y.post(phone, y.jo, c.name, strings.NewReader(c.body), "")); refused != c.want {
				t.Errorf("%s %s is refused %q, want %q", c.name, c.body, refused, c.want)
			}
		}
		if y.stageText(t) != "" || y.copied(t) {
			t.Error("a call without weighed wrote")
		}
		// A device of a member is told there is no such method, before its
		// body is read, wherever it calls from.
		for _, name := range []string{"PutStageFile", "CopyRepoFlows"} {
			none, _ := json.Marshal(noSuchMethod(name))
			for _, remote := range []string{phone, "203.0.113.9:40000", linkRemoteAddr} {
				if got := strings.TrimSpace(y.post(remote, y.sam, name, unread{t}, "").Body.String()); got != string(none) {
					t.Errorf("%s from sam's device at %s is answered %s, want %s", name, remote, got, none)
				}
			}
		}
		// Whom the call is made as is the device's user: another name is
		// refused without being said back, and none is the user.
		for _, name := range []string{"PutStageFile", "CopyRepoFlows"} {
			body := strings.Replace(map[string]string{"PutStageFile": putStage, "CopyRepoFlows": copyFlows}[name], `"weighed"`, `"actor":"sam","weighed"`, 1)
			if _, refused := answer(t, y.post(phone, y.jo, name, strings.NewReader(body), "")); refused != `this device calls as jo: "actor" is jo or left out` {
				t.Errorf("%s as another actor is refused %q", name, refused)
			}
		}
		if y.stageText(t) != "" || y.copied(t) {
			t.Error("a call as another actor wrote")
		}
		// Taken from the yard's own machine, as jo.
		raw, refused := answer(t, y.post(phone, y.jo, "PutStageFile", strings.NewReader(putStage), ""))
		var saved store.LineStageFile
		if err := json.Unmarshal(raw, &saved); refused != "" || err != nil || saved.Text != "Build it.\n" || saved.Digest == "" || saved.PriorText != "" {
			t.Fatalf("PutStageFile from the yard's own machine is answered %s (%q, %v), want the stage file saved", raw, refused, err)
		}
		if y.stageText(t) != "Build it.\n" {
			t.Errorf("the stage file reads %q", y.stageText(t))
		}
		// The save is held to the digest the client read: the same device
		// writes over it with its digest, and the text it replaced comes back.
		next := `{"flow":"code","stage":"work","text":"Build it, then say so.\n","expect_digest":"` + saved.Digest + `","actor":"jo","weighed":true}`
		raw, refused = answer(t, y.post(phone, y.jo, "PutStageFile", strings.NewReader(next), ""))
		var again store.LineStageFile
		if err := json.Unmarshal(raw, &again); refused != "" || err != nil || again.PriorText != "Build it.\n" || again.PriorDigest != saved.Digest {
			t.Fatalf("the save held to its digest is answered %s (%q, %v), want it to replace the text it read", raw, refused, err)
		}
		if _, refused := answer(t, y.post(phone, y.jo, "PutStageFile", strings.NewReader(next), "")); !strings.Contains(refused, "not the "+saved.Digest) {
			t.Errorf("the save held to a digest that moved is refused %q", refused)
		}
		raw, refused = answer(t, y.post(phone, y.jo, "CopyRepoFlows", strings.NewReader(copyFlows), ""))
		var copiedFlows store.CopiedFlows
		if err := json.Unmarshal(raw, &copiedFlows); refused != "" || err != nil || len(copiedFlows.Flows) != 1 || copiedFlows.Flows[0].Flow != "web.default" || !y.copied(t) {
			t.Fatalf("CopyRepoFlows from the yard's own machine is answered %s (%q, %v), want web.default copied", raw, refused, err)
		}
		// Each write is marked with the device and written as its user.
		for _, kind := range []string{"flow_set", "flow_pointer_set"} {
			evs := y.events(t, kind)
			if len(evs) == 0 {
				t.Errorf("no %s event", kind)
			}
			for _, e := range evs {
				if e.Actor != "jo" || store.Via(e) != "phone" {
					t.Errorf("%s %d was written by %s via %q, want jo via phone", kind, e.Seq, e.Actor, store.Via(e))
				}
			}
		}
	})

	t.Run("not from the yard's own machine", func(t *testing.T) {
		y := newWriteYard(t)
		const from = "in plain HTTP from another machine"
		if err := y.st.SetSetting(context.Background(), store.RouterAddrSetting, "192.0.2.15", "jo"); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"PutStageFile", "CopyRepoFlows"} {
			for _, c := range []struct{ name, remote, proto, want string }{
				{"another machine", "203.0.113.9:40000", "", from},
				{"another machine that says https", "203.0.113.9:40000", "https", from},
				{"the router's peer, over https", "192.0.2.15:40000", "https", from},
				{"a proxy on the machine that says plain", phone, "http", "the proxy in front of the yard says this request came to it in plain HTTP"},
			} {
				// Refused before the body is read, the token named as at risk.
				_, refused := answer(t, y.post(c.remote, y.jo, name, unread{t}, c.proto))
				if !strings.Contains(refused, c.want) || !strings.Contains(refused, "yardr user device rm") {
					t.Errorf("%s from %s is refused %q, want %q and the device's removal", name, c.name, refused, c.want)
				}
			}
		}
		if y.stageText(t) != "" || y.copied(t) {
			t.Error("a refused call wrote")
		}
	})

	t.Run("down the link on a yard that is not hosted", func(t *testing.T) {
		y := newWriteYard(t)
		for _, name := range []string{"PutStageFile", "CopyRepoFlows"} {
			// Plain: the edge read the token with the call, so the call is
			// refused before its body is read, naming the two fixes.
			_, refused := answer(t, y.post(linkRemoteAddr, y.jo, name, unread{t}, ""))
			for _, want := range []string{"does not cross the edge in a call that is not sealed", "a sealed device (pairing the device with the QR makes it one)", "the yard's machine", "yardr user device rm"} {
				if !strings.Contains(refused, want) {
					t.Errorf("%s down the link, plain, is refused %q, want it to say %q", name, refused, want)
				}
			}
		}
		if y.stageText(t) != "" || y.copied(t) {
			t.Error("a refused call wrote")
		}
	})

	t.Run("down the link on a hosted yard", func(t *testing.T) {
		y := newWriteYard(t)
		if err := y.st.SetSetting(context.Background(), store.HostedSetting, "true", "jo"); err != nil {
			t.Fatal(err)
		}
		// Plain down the link is taken where the service runs the edge, but
		// not when the proxy in front says it came in plain.
		if _, refused := answer(t, y.post(linkRemoteAddr, y.jo, "PutStageFile", unread{t}, "http")); !strings.Contains(refused, "the proxy in front of the yard says this request came to it in plain HTTP") {
			t.Errorf("PutStageFile down the link, plain, behind a proxy that says plain is refused %q", refused)
		}
		if _, refused := answer(t, y.post(linkRemoteAddr, y.jo, "PutStageFile", strings.NewReader(putStage), "")); refused != "" || y.stageText(t) != "Build it.\n" {
			t.Errorf("PutStageFile down the link of a hosted yard is refused %q, the stage file reads %q", refused, y.stageText(t))
		}
		if _, refused := answer(t, y.post(linkRemoteAddr, y.jo, "CopyRepoFlows", strings.NewReader(copyFlows), "")); refused != "" || !y.copied(t) {
			t.Errorf("CopyRepoFlows down the link of a hosted yard is refused %q", refused)
		}
		for _, kind := range []string{"flow_set", "flow_pointer_set"} {
			for _, e := range y.events(t, kind) {
				if e.Actor != "jo" || store.Via(e) != "phone via edge" {
					t.Errorf("%s %d was written by %s via %q, want jo via phone via edge", kind, e.Seq, e.Actor, store.Via(e))
				}
			}
		}
	})
}

// A sealed device of the operator saves a stage file and copies a depot's
// flows down the link of a yard that is not hosted, where a plain call is
// refused: the envelope the edge carries is ciphertext, so the edge reads no
// token, and the call is the one a yard that is not hosted takes. Each write is
// marked with the device and the link.
func TestUserSavesAStageFileAndCopiesFlowsSealed(t *testing.T) {
	y := newPairYard(t)
	stageFlowFixtures(t, y.st)
	d := newE2EDevice(t, y, "jo", "laptop")
	plain := bindFor("", false)
	since := headOf(t, y.st)
	var path string
	for _, c := range []struct{ method, body string }{
		{"PutStageFile", putStage},
		{"CopyRepoFlows", copyFlows},
	} {
		outer, _, status, _, raw := d.call(t, http.MethodPost, "/user/call/"+c.method, nil, []byte(c.body), plain, plain)
		if outer != http.StatusOK || status != http.StatusOK {
			t.Fatalf("%s sealed is answered outer %d, inner %d %s", c.method, outer, status, raw)
		}
		result, refused := answerOf(t, raw)
		if refused != "" {
			t.Fatalf("%s sealed is refused %q, want it taken: a sealed call crossed the edge as ciphertext", c.method, refused)
		}
		if c.method == "PutStageFile" {
			var saved store.LineStageFile
			if err := json.Unmarshal(result, &saved); err != nil || saved.Text != "Build it.\n" {
				t.Fatalf("PutStageFile sealed is answered %s (%v)", result, err)
			}
			path = saved.Path
		}
	}
	if text, err := os.ReadFile(path); err != nil || string(text) != "Build it.\n" {
		t.Errorf("the stage file reads %q, %v", text, err)
	}
	if _, err := y.st.GetFlow(context.Background(), "web.default"); err != nil {
		t.Errorf("web.default is not copied: %v", err)
	}
	for _, kind := range []string{"flow_set", "flow_pointer_set"} {
		evs := eventsOfKindSince(t, y.st, kind, since)
		if len(evs) == 0 {
			t.Errorf("no %s event", kind)
		}
		for _, e := range evs {
			if e.Actor != "jo" || store.Via(e) != "laptop via edge" {
				t.Errorf("%s %d was written by %s via %q, want jo via laptop via edge", kind, e.Seq, e.Actor, store.Via(e))
			}
		}
	}
	// The sealed device is the operator's alone: a member's is told there is no
	// such method, as any other device is, and writes nothing.
	member := newE2EDevice(t, y, "sam", "tablet")
	for _, c := range []struct{ method, body string }{{"PutStageFile", putStage}, {"CopyRepoFlows", copyFlows}} {
		outer, _, status, _, raw := member.call(t, http.MethodPost, "/user/call/"+c.method, nil, []byte(c.body), plain, plain)
		if _, refused := answerOf(t, raw); outer != http.StatusOK || status != http.StatusOK || refused != "no such method "+c.method {
			t.Errorf("%s from a member's sealed device is answered outer %d, inner %d %s, want no such method", c.method, outer, status, raw)
		}
	}
}
