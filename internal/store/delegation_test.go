package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"yardr.dev/yardr/internal/store"
)

// delegationYard is operatorYard with delegation switched on, the group
// builders, the depot shed and a directory under the yard's home, and the
// operator jo's mail to the yardmaster that a delegated run cites.
type delegationYard struct {
	api  store.API
	own  *store.Store
	ctx  context.Context
	mail uint64
	dir  string
}

func newDelegationYard(t *testing.T) *delegationYard {
	t.Helper()
	api, own := operatorYard(t)
	y := &delegationYard{api: api, own: own, ctx: context.Background(), dir: filepath.Join(own.HomeDir(), "work")}
	if err := os.MkdirAll(y.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := api.SetSetting(y.ctx, store.DelegationSetting, "true", "jo"); err != nil {
		t.Fatal(err)
	}
	if err := api.PutGroup(y.ctx, store.Group{Name: "builders", Runner: "herdr", Config: map[string]any{"kind": "claude", "model": "sonnet"}}, "jo"); err != nil {
		t.Fatal(err)
	}
	if err := api.PutDepot(y.ctx, store.Depot{Name: "shed", Path: t.TempDir(), Base: "origin/main", Test: "make test"}, "jo"); err != nil {
		t.Fatal(err)
	}
	for file, decl := range map[string]string{"pushover.yaml": sourceFile, "linear.yaml": hookedSource} {
		if _, err := api.PutSource(y.ctx, file, decl, "jo"); err != nil {
			t.Fatal(err)
		}
	}
	y.mail = y.mailFrom(t, "jo", "yardmaster")
	return y
}

// hookedSource declares a source whose deliveries a hook takes, signed
// with a secret from a file.
const hookedSource = `name: linear
base: https://api.linear.app
credential: { env: LINEAR_API_KEY }
hook:
  verify:
    scheme: hmac-sha256
    signed: body
    signature: { header: Linear-Signature, encoding: hex }
    delivery: { header: Linear-Delivery }
  bodies: unique
  normalize: [linear/normalize]
  keys: [team]
calls:
  viewer: { kind: read, request: { method: GET, path: /viewer } }
`

// mailFrom sends one local mail and answers its seq.
func (y *delegationYard) mailFrom(t *testing.T, from, to string) uint64 {
	t.Helper()
	if err := y.api.Mail(y.ctx, to, "please shape the yard", from); err != nil {
		t.Fatal(err)
	}
	head, err := y.api.Head(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return head
}

// logEvent logs an event of kind by jo, as a door of the yard would, and
// answers its seq: a mail carrying a mark no local mail of a test's could.
func (y *delegationYard) logEvent(t *testing.T, kind string, data map[string]any) uint64 {
	t.Helper()
	if err := y.own.LogEvent(y.ctx, "", kind, "jo", data); err != nil {
		t.Fatal(err)
	}
	head, err := y.api.Head(y.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return head
}

// askWeight is the weight the ask seq on bead was stored with.
func (y *delegationYard) askWeight(t *testing.T, bead string, seq uint64) []store.AskWeight {
	t.Helper()
	notes, err := y.api.Notes(y.ctx, bead)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if n.Seq == seq && n.Kind == store.NoteAsk {
			a, err := store.ParseAsk(n)
			if err != nil {
				t.Fatal(err)
			}
			return a.Weight
		}
	}
	t.Fatalf("no ask %d on %s", seq, bead)
	return nil
}

// as is ctx carrying a delegated run for jo on mail.
func (y *delegationYard) as(mail uint64, command string) context.Context {
	return store.WithDelegation(y.ctx, store.DelegatedCall{For: "jo", Mail: mail, Command: command})
}

// latestRecord is the newest delegated record as jo reads it, by the id
// the newest mail to jo names.
func (y *delegationYard) latestRecord(t *testing.T) *store.Delegation {
	t.Helper()
	for id := uint64(64); id > 0; id-- {
		if d, err := y.api.Delegation(y.ctx, id, "jo"); err == nil {
			return d
		}
	}
	t.Fatal("no delegated record")
	return nil
}

// The delegated commands of the design's 3.2 each run from the yardmaster
// for the operator on their cited mail, and each leaves its record, a
// journal pointer naming the record's id alone and a mail to the operator
// naming the record, while the change's own event carries the keys a
// terminal's run of it carries and no more.
func TestDelegatedRuns(t *testing.T) {
	t.Parallel()
	accept, _ := store.HarnessPreset("claude", "accept edits")
	cases := []struct {
		name, command, of string
		run               func(y *delegationYard, ctx context.Context) error
		event             string
	}{
		{"group set of model, worktrees and preset args", "yardr group set builders model=opus worktrees=~/work preset=accept-edits", "group builders",
			func(y *delegationYard, ctx context.Context) error {
				_, err := y.api.SetGroup(ctx, "builders", store.GroupEdit{Set: map[string]any{"model": "opus", "worktrees": y.dir, "args": accept.Args}}, "yardmaster")
				return err
			}, "group_set"},
		{"group add", "yardr group add reviewers --runner herdr --set kind=codex --set model=opus", "group reviewers",
			func(y *delegationYard, ctx context.Context) error {
				return y.api.PutGroup(ctx, store.Group{Name: "reviewers", Runner: "herdr", Limit: 1, Config: map[string]any{"kind": "codex", "model": "opus", "worktrees": y.dir}}, "yardmaster")
			}, "group_set"},
		{"group runner change taking-back a manual group", "yardr group add reviewers --runner herdr --set kind=claude", "group reviewers",
			func(y *delegationYard, ctx context.Context) error {
				if _, err := y.api.GetGroup(y.ctx, "reviewers"); errors.Is(err, store.ErrNotFound) {
					if err := y.api.PutGroup(y.ctx, store.Group{Name: "reviewers", Runner: "manual", Limit: 7, Members: []string{"jo", "ada"}}, "jo"); err != nil {
						return err
					}
				} else if err != nil {
					return err
				}
				return y.api.PutGroup(ctx, store.Group{Name: "reviewers", Runner: "herdr", Limit: 1, Config: map[string]any{"kind": "claude"}}, "yardmaster")
			}, "group_set"},
		{"crew set of kind, model and preset args", "yardr crew set clerk kind=claude model=opus preset=accept-edits", "crew member clerk",
			func(y *delegationYard, ctx context.Context) error {
				r, err := y.api.GetCrew(y.ctx, "clerk")
				if err != nil {
					return err
				}
				r.Config = map[string]any{"kind": "claude", "model": "opus", "args": accept.Args}
				_, err = y.api.PutCrew(ctx, *r, "yardmaster")
				return err
			}, "crew_defined"},
		{"depot test", "yardr depot test shed 'sh .yardr/test'", "depot shed",
			func(y *delegationYard, ctx context.Context) error {
				d, err := y.api.GetDepot(y.ctx, "shed")
				if err != nil {
					return err
				}
				d.Test = "sh .yardr/test"
				return y.api.PutDepot(ctx, *d, "yardmaster")
			}, "depot_updated"},
		{"depot base", "yardr depot base shed origin/trunk", "depot shed",
			func(y *delegationYard, ctx context.Context) error {
				d, err := y.api.GetDepot(y.ctx, "shed")
				if err != nil {
					return err
				}
				d.Base = "origin/trunk"
				return y.api.PutDepot(ctx, *d, "yardmaster")
			}, "depot_updated"},
		{"user set notify=", "yardr user set jo notify=pushover", "user jo",
			func(y *delegationYard, ctx context.Context) error {
				_, err := y.api.SetUser(ctx, "jo", map[string]string{store.UserNotify: "pushover"}, "yardmaster")
				return err
			}, "user_set"},
		{"hook add in the safe shape", "yardr hook add tracker --source token", "hook tracker",
			func(y *delegationYard, ctx context.Context) error {
				_, err := y.api.PutHook(ctx, store.Hook{Name: "tracker", Source: "token", SecretFile: store.HookSecretFile(y.own.HomeDir(), "tracker")}, "yardmaster")
				return err
			}, "hook_added"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			y := newDelegationYard(t)
			// Without the delegation each defining command refuses the
			// yardmaster as before.
			if err := c.run(y, y.ctx); err == nil {
				t.Fatalf("without a delegation the yardmaster ran %s", c.name)
			}
			if err := c.run(y, y.as(y.mail, c.command)); err != nil {
				t.Fatalf("delegated %s: %v", c.name, err)
			}
			rec := y.latestRecord(t)
			if rec.For != "jo" || rec.Yardmaster != "yardmaster" || rec.Mail != y.mail || rec.Of != c.of || rec.Command != c.command {
				t.Errorf("record = %+v, want for jo on mail %d of %s with the command whole", rec, y.mail, c.of)
			}
			if rec.Back == "" || len(rec.Changes) == 0 {
				t.Errorf("record holds no before-values or no taking-back: %+v", rec)
			}
			if c.name == "group runner change taking-back a manual group" && !strings.Contains(rec.Back, "--members jo,ada") {
				t.Errorf("manual group's members missing from taking-back command: %q", rec.Back)
			}
			// The journal's pointer names the record and nothing the run
			// carried.
			ym, err := y.api.GetCrew(y.ctx, "yardmaster")
			if err != nil {
				t.Fatal(err)
			}
			notes, err := y.api.Notes(y.ctx, ym.Journal)
			if err != nil || len(notes) == 0 {
				t.Fatalf("journal notes = %v, %v", notes, err)
			}
			pointer := notes[len(notes)-1].Text
			if !strings.Contains(pointer, "yardmaster for jo") || !strings.Contains(pointer, "record "+itoa(rec.ID)) || strings.Contains(pointer, c.command) || strings.Contains(pointer, itoa(y.mail)+" ") {
				t.Errorf("journal pointer = %q, want only the record's id under yardmaster for jo", pointer)
			}
			// The operator is mailed the telling, naming the record and the
			// cited mail.
			evs, err := y.api.Events(y.ctx, "", 50)
			if err != nil {
				t.Fatal(err)
			}
			var told bool
			var change *store.Event
			for i, e := range evs {
				if e.Kind == "mail" && e.Actor == "yard" && store.MailReceiver(e) == "jo" {
					text, _ := e.Data["text"].(string)
					told = strings.Contains(text, "record "+itoa(rec.ID)) && strings.Contains(text, "your mail "+itoa(y.mail))
				}
				if e.Kind == c.event {
					change = &evs[i]
				}
			}
			if !told {
				t.Errorf("no mail to jo names record %d and mail %d", rec.ID, y.mail)
			}
			if change == nil {
				t.Fatalf("no %s event", c.event)
			}
			for k := range change.Data {
				if strings.Contains(k, "delegat") || k == "for" || k == "mail" || k == "command" {
					t.Errorf("the change's event carries %s: %v", k, change.Data)
				}
			}
		})
	}
}

func itoa(n uint64) string { return strconv.FormatUint(n, 10) }

// The command a delegated run carries is the caller's words, which a
// caller of the socket writes as it likes. The telling names the keys the
// store read as changed beside them, so a command that says less than ran
// does not hide what ran from the operator it tells.
func TestDelegatedMailNamesChangedKeys(t *testing.T) {
	t.Parallel()
	y := newDelegationYard(t)
	accept, _ := store.HarnessPreset("claude", "accept edits")
	ctx := y.as(y.mail, "yardr group set builders model=opus")
	if _, err := y.api.SetGroup(ctx, "builders", store.GroupEdit{Set: map[string]any{"model": "opus", "args": accept.Args}}, "yardmaster"); err != nil {
		t.Fatal(err)
	}
	evs, err := y.api.Events(y.ctx, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for _, e := range evs {
		if e.Kind == "mail" && e.Actor == "yard" && store.MailReceiver(e) == "jo" {
			text, _ = e.Data["text"].(string)
		}
	}
	if !strings.Contains(text, "changed: group builders (args, model)") {
		t.Errorf("telling = %q, want the keys the store read as changed", text)
	}
}

// A delegated run is made only on an operator's own local mail to the
// yardmaster, checked in the write: each citation that fails refuses the
// run whole, and nothing is written.
func TestDelegatedCitation(t *testing.T) {
	t.Parallel()
	y := newDelegationYard(t)
	fromMember := y.mailFrom(t, "ada", "yardmaster")
	toClerk := y.mailFrom(t, "jo", "clerk")
	notMail := y.logEvent(t, "note", map[string]any{"to": "yardmaster"})
	marked := map[string]uint64{}
	for _, mark := range []string{"from_peer", "from_peer_unlinked", "as", "timer", "from_bead"} {
		marked[mark] = y.logEvent(t, "mail", map[string]any{"to": "yardmaster", "text": "do it", mark: "north"})
	}
	cases := map[string]struct {
		d    store.DelegatedCall
		want string
	}{
		"no mail cited":              {store.DelegatedCall{For: "jo"}, "cites the mail of jo's"},
		"a seq that is no event":     {store.DelegatedCall{For: "jo", Mail: 999999}, "has no event 999999"},
		"a seq that is no mail":      {store.DelegatedCall{For: "jo", Mail: notMail}, "a note event, not mail"},
		"for a member":               {store.DelegatedCall{For: "ada", Mail: fromMember}, "ada holds no operator role"},
		"mail from a member":         {store.DelegatedCall{For: "jo", Mail: fromMember}, "is from ada, not from jo"},
		"mail to another":            {store.DelegatedCall{For: "jo", Mail: toClerk}, "not mail to the yardmaster"},
		"mail from another yard":     {store.DelegatedCall{For: "jo", Mail: marked["from_peer"]}, "carries from_peer"},
		"mail from an unlinked peer": {store.DelegatedCall{For: "jo", Mail: marked["from_peer_unlinked"]}, "carries from_peer_unlinked"},
		"mail trusted as a user":     {store.DelegatedCall{For: "jo", Mail: marked["as"]}, "carries as"},
		"a timer's mail":             {store.DelegatedCall{For: "jo", Mail: marked["timer"]}, "carries timer"},
		"a bead's session's mail":    {store.DelegatedCall{For: "jo", Mail: marked["from_bead"]}, "carries from_bead"},
	}
	for name, c := range cases {
		ctx := store.WithDelegation(y.ctx, c.d)
		_, err := y.api.SetGroup(ctx, "builders", store.GroupEdit{Set: map[string]any{"kind": "codex"}}, "yardmaster")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
	modelCtx := store.WithDelegation(y.ctx, store.DelegatedCall{For: "jo", Mail: marked["from_peer_unlinked"]})
	_, err := y.api.SetGroup(modelCtx, "builders", store.GroupEdit{Set: map[string]any{"model": "opus"}}, "yardmaster")
	if err == nil || !strings.Contains(err.Error(), "carries from_peer_unlinked") {
		t.Errorf("a model-only delegated write with an unlinked-peer citation: %v", err)
	}
	// A delegation from anyone but the yardmaster refuses too: the clerk,
	// and the operator at a terminal naming themselves.
	for _, actor := range []string{"clerk", "jo"} {
		_, err := y.api.SetGroup(y.as(y.mail, ""), "builders", store.GroupEdit{Set: map[string]any{"kind": "codex"}}, actor)
		if err == nil || !strings.Contains(err.Error(), "is the yardmaster's (yardmaster) alone") {
			t.Errorf("a delegation by %s: err = %v", actor, err)
		}
	}
	g, err := y.api.GetGroup(y.ctx, "builders")
	if err != nil || g.Config["kind"] != "claude" {
		t.Errorf("after the refusals builders = %+v, %v; want kind claude still", g, err)
	}
	if _, err := y.api.Delegation(y.ctx, 1, "jo"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a refused run left a record: %v", err)
	}
	// A mail a device wrote is its user's word, marked "via" the device and
	// with none of the marks above, so a stolen device's mail opens a run as
	// the operator's own does. docs/users.md says so in its stolen-device
	// table, and a check that refused "via" would make that wrong.
	byDevice := y.logEvent(t, "mail", map[string]any{"to": "yardmaster", "text": "do it", "via": "phone"})
	if _, err := y.api.SetGroup(y.as(byDevice, ""), "builders", store.GroupEdit{Set: map[string]any{"model": "opus"}}, "yardmaster"); err != nil {
		t.Errorf("a delegated run citing a mail written via a device: %v", err)
	}
}

// With the switch off, or unset on a yard that is not hosted, a delegated
// call opens no leg: each command refuses the yardmaster in the words it
// refuses without one. Unset on a hosted yard, it reads on.
func TestDelegationSwitch(t *testing.T) {
	t.Parallel()
	y := newDelegationYard(t)
	set := func(ctx context.Context) error {
		_, err := y.api.SetGroup(ctx, "builders", store.GroupEdit{Set: map[string]any{"kind": "codex"}}, "yardmaster")
		return err
	}
	today := set(y.ctx)
	if today == nil {
		t.Fatal("the yardmaster changed a group's kind with no delegation")
	}
	for _, value := range []string{"false", ""} {
		if err := y.api.SetSetting(y.ctx, store.DelegationSetting, value, "jo"); err != nil {
			t.Fatal(err)
		}
		if err := set(y.as(y.mail, "")); err == nil || err.Error() != today.Error() {
			t.Errorf("switch %q: err = %v, want today's %q", value, err, today)
		}
	}
	if err := y.api.SetSetting(y.ctx, store.HostedSetting, "true", "jo"); err != nil {
		t.Fatal(err)
	}
	if err := set(y.as(y.mail, "")); err != nil {
		t.Errorf("unset on a hosted yard, delegation reads on: %v", err)
	}
}

// What the leg does not carry refuses it in today's words, or in the
// leg's where a value is held: free args, env_file, env_command, prompt,
// cmd, the exec runner, a key the list names not, the yardmaster's own
// entry, a new crew member, the switch itself, and a hook add with an
// effectful field or a secret file of the caller's, each naming its flag.
func TestDelegatedRefusals(t *testing.T) {
	t.Parallel()
	y := newDelegationYard(t)
	ctx := y.as(y.mail, "")
	setGroup := func(set map[string]any) error {
		_, err := y.api.SetGroup(ctx, "builders", store.GroupEdit{Set: set}, "yardmaster")
		return err
	}
	setCrew := func(name string, cfg map[string]any) error {
		r, err := y.api.GetCrew(y.ctx, name)
		if err != nil {
			return err
		}
		r.Config = cfg
		_, err = y.api.PutCrew(ctx, *r, "yardmaster")
		return err
	}
	hookAdd := func(h store.Hook) error {
		h.Source = "token"
		if h.SecretFile == "" {
			h.SecretFile = store.HookSecretFile(y.own.HomeDir(), h.Name)
		}
		_, err := y.api.PutHook(ctx, h, "yardmaster")
		return err
	}
	secret := filepath.Join(t.TempDir(), "mine.secret")
	cases := map[string]struct {
		run  func() error
		want string
	}{
		"free args":           {func() error { return setGroup(map[string]any{"args": []any{"--yolo"}}) }, "never carries free args"},
		"group env_file":      {func() error { return setGroup(map[string]any{"env_file": "/x.env"}) }, "change env_file on group builders"},
		"group prompt":        {func() error { return setGroup(map[string]any{"prompt": "/roles/b.md"}) }, "change prompt on group builders"},
		"group cmd":           {func() error { return setGroup(map[string]any{"cmd": "make"}) }, "change cmd on group builders"},
		"worktrees outside":   {func() error { return setGroup(map[string]any{"worktrees": t.TempDir()}) }, "outside the yard's known places"},
		"crew env_command":    {func() error { return setCrew("clerk", map[string]any{"env_command": "pass x"}) }, "change env_command on crew member clerk"},
		"crew prompt":         {func() error { return setCrew("clerk", map[string]any{"prompt": "/roles/c.md"}) }, "change prompt on crew member clerk"},
		"crew free args":      {func() error { return setCrew("clerk", map[string]any{"kind": "claude", "args": []any{"--x"}}) }, "never carries free args"},
		"group bypass preset": {func() error { return setGroup(map[string]any{"args": []any{"--dangerously-skip-permissions"}}) }, "preset bypass skips the agent's permission prompts"},
		"crew bypass preset": {func() error {
			return setCrew("clerk", map[string]any{"kind": "codex", "args": []any{"--dangerously-bypass-approvals-and-sandbox"}})
		}, "preset bypass skips the agent's permission prompts"},
		"group kimi auto preset": {func() error {
			return setGroup(map[string]any{"kind": "kimi", "args": []any{"--auto"}})
		}, "preset auto skips the agent's permission prompts"},
		"the yardmaster's own":       {func() error { return setCrew("yardmaster", map[string]any{"kind": "codex"}) }, "change kind on crew member yardmaster"},
		"the yardmaster's own model": {func() error { return setCrew("yardmaster", map[string]any{"model": "opus"}) }, "change model on crew member yardmaster"},
		"the yardmaster's own dir": {func() error {
			r, err := y.api.GetCrew(y.ctx, "yardmaster")
			if err != nil {
				return err
			}
			r.Dir = y.dir
			_, err = y.api.PutCrew(ctx, *r, "yardmaster")
			return err
		}, "change dir on crew member yardmaster"},
		"a new crew member": {func() error {
			_, err := y.api.PutCrew(ctx, store.CrewMember{Name: "scribe", Runner: "herdr"}, "yardmaster")
			return err
		}, "arrives with its role on the crew add card"},
		"an exec group": {func() error {
			return y.api.PutGroup(ctx, store.Group{Name: "merger", Runner: "exec", Limit: 1}, "yardmaster")
		}, "change runner on group merger"},
		"the switch itself": {func() error {
			return y.api.SetSetting(ctx, store.DelegationSetting, "false", "yardmaster")
		}, "may change a setting"},
		"user role": {func() error {
			_, err := y.api.SetUser(ctx, "ada", map[string]string{store.UserRole: store.RoleOperator}, "yardmaster")
			return err
		}, "only"},
		// The exclusions that have a card (design yardr-m90n 3.3, bead 4)
		// are the operator's to approve, never the yardmaster's to run on
		// a cited mail, whatever the switch says.
		"a device removal": {func() error {
			_, err := y.api.RemoveDevice(ctx, "ada", "phone", "yardmaster")
			return err
		}, "may remove a device of ada"},
		"a source removal": {func() error { return y.api.DeleteSource(ctx, "linear", "yardmaster") }, "removing a source"},
		"a records move": {func() error {
			_, err := y.api.MoveSourceRecords(ctx, "linear", "pushover", "yardmaster")
			return err
		}, "move a source's records"},
		"a links move": {func() error {
			_, err := y.api.MoveLinkSource(ctx, "linear", "pushover", "yardmaster")
			return err
		}, "move links from one source to another"},
		"a hook removal": {func() error { return y.api.DeleteHook(ctx, "linear", "yardmaster") }, "remove a hook"},
		"notify with env_file": {func() error {
			_, err := y.api.SetUser(ctx, "jo", map[string]string{store.UserNotify: "pushover", store.UserEnvFile: "/x.env"}, "yardmaster")
			return err
		}, "where jo is told"},
		"clearing a test command": {func() error {
			d, _ := y.api.GetDepot(y.ctx, "shed")
			d.Test = ""
			return y.api.PutDepot(ctx, *d, "yardmaster")
		}, "change a depot's test command"},
		"a depot's path": {func() error {
			d, _ := y.api.GetDepot(y.ctx, "shed")
			d.Path = t.TempDir()
			return y.api.PutDepot(ctx, *d, "yardmaster")
		}, "change a depot's path"},
		"hook --says":        {func() error { return hookAdd(store.Hook{Name: "h1", Says: []store.Say{{Outcome: "done"}}}) }, "with --says is the operator's"},
		"hook --makes":       {func() error { return hookAdd(store.Hook{Name: "h2", Makes: []store.Make{{Type: "task"}}}) }, "with --makes is the operator's"},
		"hook --map":         {func() error { return hookAdd(store.Hook{Name: "h3", Map: map[string]string{"task:review": "started"}}) }, "with --map is the operator's"},
		"hook --as":          {func() error { return hookAdd(store.Hook{Name: "h4", As: "clerk"}) }, "with --as is the operator's"},
		"hook --secret-file": {func() error { return hookAdd(store.Hook{Name: "h5", SecretFile: secret}) }, "with --secret-file is the operator's"},
	}
	// Every preset the table marks Strong is refused, on a group and on a
	// crew member: the leg reads the mark, so a preset added later is held
	// without a case of its own.
	for kind, presets := range store.HarnessPresets {
		for _, p := range presets {
			if !p.Strong {
				continue
			}
			args := make([]any, len(p.Args))
			for i, a := range p.Args {
				args[i] = a
			}
			want := fmt.Sprintf("preset %s skips the agent's permission prompts", p.Label)
			cases["group "+kind+" "+p.Label] = struct {
				run  func() error
				want string
			}{func() error { return setGroup(map[string]any{"kind": kind, "args": args}) }, want}
			cases["crew "+kind+" "+p.Label] = struct {
				run  func() error
				want string
			}{func() error { return setCrew("clerk", map[string]any{"kind": kind, "args": args}) }, want}
		}
	}
	for name, c := range cases {
		if err := c.run(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
	if _, err := y.api.SetGroup(y.ctx, "builders", store.GroupEdit{Set: map[string]any{"args": []any{"--yolo"}}}, "jo"); err != nil {
		t.Fatalf("seed free args as the operator: %v", err)
	}
	if err := setGroup(map[string]any{"kind": "codex"}); err == nil || !strings.Contains(err.Error(), "never carries free args") {
		t.Errorf("changing a group's kind carries forward free args: %v", err)
	}
	hooks, err := y.api.Hooks(y.ctx)
	if err != nil || len(hooks) != 0 {
		t.Errorf("a refused hook add stored %v, %v", hooks, err)
	}
	if _, err := y.api.Delegation(y.ctx, 1, "jo"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a refused run left a record: %v", err)
	}
}

// The record's one read is an operator's: the one it was for and another
// alike; a member, the yardmaster that ran it and a call that names
// nobody are told it is not there. A record past the inline bound holds
// its body in one blob, served only by DelegationBlob under the same
// check, never by NoteBlob, and kept by the sweep while the record stands.
func TestDelegationRecordReads(t *testing.T) {
	t.Parallel()
	y := newDelegationYard(t)
	if _, err := y.api.AddUser(y.ctx, "bo", "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.api.SetUser(y.ctx, "bo", map[string]string{store.UserRole: store.RoleOperator}, "jo"); err != nil {
		t.Fatal(err)
	}
	long := "yardr group set builders model=opus # " + strings.Repeat("x", 2*store.MaxDelegationInline)
	if _, err := y.api.SetGroup(y.as(y.mail, long), "builders", store.GroupEdit{Set: map[string]any{"model": "opus"}}, "yardmaster"); err != nil {
		t.Fatal(err)
	}
	rec, err := y.api.Delegation(y.ctx, 1, "jo")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Blob == nil || rec.Command != "" {
		t.Fatalf("a record past %d bytes = %+v, want its body in a blob", store.MaxDelegationInline, rec)
	}
	for _, reader := range []string{"jo", "bo"} {
		if _, err := y.api.Delegation(y.ctx, 1, reader); err != nil {
			t.Errorf("operator %s reads the record: %v", reader, err)
		}
		body, err := y.api.DelegationBlob(y.ctx, 1, reader)
		if err != nil || body.Command != long || body.Back == "" {
			t.Errorf("operator %s reads the blob: %+v, %v", reader, body, err)
		}
	}
	for _, reader := range []string{"ada", "yardmaster", "clerk", ""} {
		if _, err := y.api.Delegation(y.ctx, 1, reader); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%q reads the record: %v, want not found", reader, err)
		}
		if _, err := y.api.DelegationBlob(y.ctx, 1, reader); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%q reads the blob: %v, want not found", reader, err)
		}
	}
	if _, err := y.api.NoteBlob(y.ctx, rec.Blob.Hash); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("NoteBlob serves a record's blob: %v", err)
	}
	old := time.Now().Add(-2 * store.BlobGrace)
	if err := os.Chtimes(y.own.BlobPath(rec.Blob.Hash), old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := y.api.Prune(y.ctx, store.Retention{}, "jo"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(blobFiles(t, y.own), rec.Blob.Hash) {
		t.Error("the sweep took a blob its record names")
	}
}

// The switch's run ask is proposable for false and for true, anyone may
// file it, and the operator approves it: true weighs trust, so it takes
// the strong gate; unsetting it and every other Alone setting stay
// unproposable.
func TestDelegationSwitchAsk(t *testing.T) {
	t.Parallel()
	y := newDelegationYard(t)
	b, err := y.api.CreateBead(y.ctx, store.NewBead{Title: "the switch"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	ask := func(run, asker string) (uint64, error) {
		return y.api.AddAsk(y.ctx, b.ID, store.Ask{Question: "switch it?", To: "jo", Context: []string{"the yardmaster's hands"}, Run: &store.AskRun{Argv: strings.Fields(run)}}, asker)
	}
	for _, run := range []string{"settings set yardmaster_delegation \"\"", "settings set hosted false", "settings set operator ada"} {
		if _, err := ask(run, "ada"); err == nil {
			t.Errorf("%s was proposed", run)
		}
	}
	off, err := ask("settings set yardmaster_delegation false", "yardmaster")
	if err != nil {
		t.Fatalf("the yardmaster files the off ask: %v", err)
	}
	if _, err := y.api.ApproveAsk(y.ctx, b.ID, off, "jo", false, nil); err != nil {
		t.Fatalf("jo approves off with no weight: %v", err)
	}
	on, err := ask("settings set yardmaster_delegation true", "ada")
	if err != nil {
		t.Fatalf("ada files the on ask: %v", err)
	}
	if w := y.askWeight(t, b.ID, on); !store.StrongWeight(w) {
		t.Errorf("turning delegation on weighs %v, want a strong weight", w)
	}
	if _, err := y.api.ApproveAsk(y.ctx, b.ID, on, "ada", true, nil); err == nil {
		t.Error("a member approved the on ask")
	}
	if _, err := y.api.ApproveAsk(y.ctx, b.ID, on, "jo", false, nil); err == nil {
		t.Error("the on ask was approved without its weight seen")
	}
	if _, err := y.api.ApproveAsk(y.ctx, b.ID, on, "jo", true, nil); err != nil {
		t.Fatalf("jo approves on at a terminal: %v", err)
	}
	settings, err := y.api.Settings(y.ctx)
	if err != nil || settings[store.DelegationSetting] != "true" {
		t.Errorf("after approval the switch is %q, %v", settings[store.DelegationSetting], err)
	}
}

// On a delegated run the approval is the operator's own cited mail, so the
// run skips the card the same command carries when anyone else proposes
// it. These are the commands whose card weight the leg skips; their cards
// stay, weighed as before, for every other proposer (docs/users.md).
func TestDelegationSkipsCardWeight(t *testing.T) {
	t.Parallel()
	y := newDelegationYard(t)
	b, err := y.api.CreateBead(y.ctx, store.NewBead{Title: "cards"}, "jo")
	if err != nil {
		t.Fatal(err)
	}
	skipped := []struct {
		run   []string
		test  string
		class string
	}{
		{[]string{"group", "add", "reviewers", "--runner", "herdr", "--set", "kind=codex"}, "", store.WeightCode},
		{[]string{"hook", "add", "tracker", "--source", "linear"}, "", store.WeightTrust},
		{[]string{"depot", "test", "shed"}, "sh .yardr/test", store.WeightCode},
		{[]string{"depot", "base", "shed", "origin/trunk"}, "", ""},
	}
	for _, s := range skipped {
		seq, err := y.api.AddAsk(y.ctx, b.ID, store.Ask{Question: "may I?", To: "jo", Context: []string{"a card of today's"}, Run: &store.AskRun{Argv: s.run, Test: s.test}}, "ada")
		if err != nil {
			t.Fatalf("%v is no card: %v", s.run, err)
		}
		got := ""
		if w := y.askWeight(t, b.ID, seq); len(w) > 0 {
			got = w[0].Class
		}
		if got != s.class {
			t.Errorf("the card of %v weighs %q, want %q", s.run, got, s.class)
		}
	}
}
