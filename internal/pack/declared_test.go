package pack_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// pushover is the declaration docs/pushover.md gives, which the cases
// below each change in one place.
const pushover = `name: pushover
about: A push to a person's phone through Pushover.
base: https://api.pushover.net/1
credential:
  env: PUSHOVER_TOKEN
calls:
  message:
    kind: update
    about: One push to one person
    params:
      user:  { type: string, pattern: "^[A-Za-z0-9]{30}$" }
      title: { type: string, max: 250 }
      text:  { type: string, max: 1024 }
      link:  { type: string, max: 512, optional: true }
    request:
      method: POST
      path: /messages.json
      body: { token: "{credential}", user: "{user}", title: "{title}", message: "{text}", url: "{link}" }
notify:
  call: message
  address: { param: user, env: PUSHOVER_USER }
  title: title
  text: text
  link: link
`

// smallDeclared is small with a declared source.
const smallDeclared = small + `sources:
  pushover:
    declaration: sources/pushover.yaml
`

func declaredFiles(yaml, declaration string) map[string]string {
	files := smallFiles(yaml)
	files["sources/pushover.yaml"] = declaration
	return files
}

func declared(t *testing.T, p *pack.Plan, name string) pack.DeclaredPlan {
	t.Helper()
	i := slices.IndexFunc(p.Declared, func(d pack.DeclaredPlan) bool { return d.Source == name })
	if i < 0 {
		t.Fatalf("the plan has no declared source %s: %+v", name, p.Declared)
	}
	return p.Declared[i]
}

func declaredNeed(t *testing.T, d pack.DeclaredPlan, what string) pack.Need {
	t.Helper()
	i := slices.IndexFunc(d.Needs, func(n pack.Need) bool { return n.What == what })
	if i < 0 {
		t.Fatalf("the declared source %s says nothing of %s: %+v", d.Source, what, d.Needs)
	}
	return d.Needs[i]
}

// A pack names a declaration file under a source yardr has no adapter for,
// and the file is read as yardr source add reads it.
func TestPackReadsADeclaredSource(t *testing.T) {
	p := load(t, writePack(t, declaredFiles(smallDeclared, pushover)))
	if len(p.Declared) != 1 || len(p.Sources) != 0 {
		t.Fatalf("declared = %+v, sources = %+v", p.Declared, p.Sources)
	}
	d := p.Declared[0]
	if d.Name != "pushover" || d.File != "sources/pushover.yaml" || d.Text != pushover {
		t.Fatalf("declared = %+v", d)
	}
	if d.Declaration == nil || d.Declaration.Notify == nil || d.Declaration.Notify.AddressEnv != "PUSHOVER_USER" {
		t.Fatalf("declaration = %+v", d.Declaration)
	}
	// The declaration goes into the store, not under the yard's home.
	if slices.Contains(p.Files(), "sources/pushover.yaml") {
		t.Fatalf("files = %v", p.Files())
	}
}

// What is wrong with a declared source is named by the file it is in: the
// pack's own for what the pack says, the declaration's for what that says.
func TestDeclaredSourceRefusals(t *testing.T) {
	cases := []struct {
		name        string
		yaml        string // of the pack; "" is smallDeclared
		declaration string // "" is pushover; "-" is no file
		file, key   string
		line        int
		msg         string
	}{
		{name: "a limits key", declaration: strings.Replace(pushover, "calls:\n", "limits:\n  hour: 60\ncalls:\n", 1),
			file: "sources/pushover.yaml", key: "limits", line: 6, msg: "a declaration in a pack has name, about, base, bases, base_template, path_template, credential, check, idempotency, calls, notify and no other key"},
		// limits stays refused in every pack: it is the yard's to ask.
		{name: "a limits command", declaration: pushover + "limits:\n  command: [/opt/q, \"{provider}\"]\n  kinds: { kimi: kimi }\n",
			file: "sources/pushover.yaml", key: "limits", line: 25, msg: "what else a source may have is set in the yard, by its operator"},
		{name: "a limits command alone", declaration: "name: pushover\nlimits:\n  command: [/opt/q, \"{provider}\"]\n  kinds: { kimi: kimi }\n",
			file: "sources/pushover.yaml", key: "limits", line: 2, msg: "what else a source may have is set in the yard, by its operator"},
		// The commands by which the yard speaks in a source's threads, and
		// the source's issues as a bead's link: for a pack of kind source,
		// refused in a pack for a depot.
		{name: "a thread key in a git pack", yaml: strings.Replace(smallDeclared, "name: small", "name: small\nkind: git", 1), declaration: pushover + "thread:\n  reply: [linear/thread, reply]\n  place: team\n",
			file: "sources/pushover.yaml", key: "thread", line: 25, msg: "hear, hook, thread, link, identity, events, git and needs are for a pack of kind source"},
		{name: "a link key", declaration: pushover + "link:\n  host: linear.app\n  issue: '^/x'\n  fixed: y\n  thread: z\n",
			file: "sources/pushover.yaml", key: "link", line: 25, msg: "hear, hook, thread, link, identity, events, git and needs are for a pack of kind source"},
		// What a source sends in: the command that turns a verified body
		// into the envelope. For a pack of kind source, as thread and link.
		{name: "a hook key", declaration: pushover + "hook:\n  bodies: unique\n  normalize: [pushover/normalize]\n",
			file: "sources/pushover.yaml", key: "hook", line: 25, msg: "hear, hook, thread, link, identity, events, git and needs are for a pack of kind source"},
		{name: "an identity key", declaration: pushover + "identity:\n  fold: lower\n",
			file: "sources/pushover.yaml", key: "identity", line: 25, msg: "hear, hook, thread, link, identity, events, git and needs are for a pack of kind source"},
		{name: "an events key", declaration: pushover + "events:\n  runs: [workflow_run]\n",
			file: "sources/pushover.yaml", key: "events", line: 25, msg: "hear, hook, thread, link, identity, events, git and needs are for a pack of kind source"},
		{name: "a git key", declaration: pushover + "git:\n  credential: [pushover/git-credential]\n",
			file: "sources/pushover.yaml", key: "git", line: 25, msg: "hear, hook, thread, link, identity, events, git and needs are for a pack of kind source"},
		{name: "a needs key", declaration: pushover + "needs:\n  - { program: gws, install: [install-gws] }\n",
			file: "sources/pushover.yaml", key: "needs", line: 25, msg: "hear, hook, thread, link, identity, events, git and needs are for a pack of kind source"},
		{name: "a key nobody knows", declaration: pushover + "retries: 3\n",
			file: "sources/pushover.yaml", key: "retries", line: 25, msg: "and no other key"},
		{name: "what yardr source add refuses", declaration: strings.Replace(pushover, "https://api.pushover.net/1", "http://api.pushover.net/1", 1),
			file: "sources/pushover.yaml", key: "base", line: 3, msg: "http:// is for a stand-in on this machine only"},
		// What the file says of its updates is refused as yardr source add
		// refuses it: a value where the name of a variable goes.
		{name: "a value for the name of the update's credential", declaration: strings.Replace(pushover, "  env: PUSHOVER_TOKEN\n", "  env: PUSHOVER_TOKEN\n  update_env: \"s3cr3t value\"\n", 1),
			file: "sources/pushover.yaml", key: "credential.update_env", line: 6, msg: "is no name of a variable"},
		{name: "a key over the credential's header", declaration: strings.Replace(pushover, "  env: PUSHOVER_TOKEN\n", "  env: PUSHOVER_TOKEN\n  header: \"X-Token: {credential}\"\nidempotency: x-token\n", 1),
			file: "sources/pushover.yaml", key: "idempotency", line: 7, msg: "is the header the credential is sent in"},
		{name: "another name in the file", declaration: strings.Replace(pushover, "name: pushover", "name: push", 1),
			file: "pack.yaml", key: "sources.pushover.declaration", line: 24, msg: "sources/pushover.yaml declares the source push, and the pack names it pushover"},
		{name: "no file", declaration: "-",
			file: "pack.yaml", key: "sources.pushover.declaration", line: 24, msg: "declaration sources/pushover.yaml is missing"},
		{name: "a file outside the pack", yaml: strings.Replace(smallDeclared, "sources/pushover.yaml", "../pushover.yaml", 1),
			file: "pack.yaml", key: "sources.pushover.declaration", line: 24, msg: "leaves the pack's directory"},
		{name: "a stage map beside it", yaml: smallDeclared + "    as: yardmaster\n",
			file: "pack.yaml", key: "sources.pushover.as", line: 25, msg: "a declared source has declaration: <file> and nothing else"},
		{name: "as without hears for a name no adapter has", yaml: small + "sources:\n  pushover:\n    as: yardmaster\n",
			file: "pack.yaml", key: "sources.pushover.hears", line: 23, msg: "missing: which stage of which bead type means which status"},
		{name: "a declaration for a source yardr has built in", yaml: small + "sources:\n  token:\n    declaration: sources/pushover.yaml\n",
			file: "pack.yaml", key: "sources.token", line: 23, msg: "a declaration is for a source of another name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := declaredFiles(or(c.yaml, smallDeclared), or(c.declaration, pushover))
			if c.declaration == "-" {
				delete(files, "sources/pushover.yaml")
			}
			dir := writePack(t, files)
			_, err := pack.LoadDir(dir)
			var errs pack.Errors
			if !errors.As(err, &errs) {
				t.Fatalf("err = %v, want the pack's errors", err)
			}
			want := filepath.Join(dir, filepath.FromSlash(c.file))
			if !slices.ContainsFunc(errs, func(e pack.Error) bool {
				return e.File == want && e.Key == c.key && e.Line == c.line && strings.Contains(e.Msg, c.msg)
			}) {
				t.Fatalf("no error at %s:%d %s with %q:\n%v", c.file, c.line, c.key, c.msg, errs)
			}
			// One mistake is one line: the reader's own "unknown key" does
			// not follow the pack's refusal of the same key.
			if n := len(errs); n != 1 {
				t.Fatalf("%d errors for one mistake:\n%v", n, errs)
			}
		})
	}
}

// A crew member may hear any source, one the pack declares included: the
// source is the yard's to have, and the import sets the subscription once
// it has it, skipping it until then.
func TestSubscribeToAnySourceLoads(t *testing.T) {
	yaml := strings.Replace(smallDeclared, "linear: involved", "linear: involved\n      pushover: involved", 1)
	p := load(t, writePack(t, declaredFiles(yaml, pushover)))
	want := []pack.Source{{Name: "linear", Scope: "involved"}, {Name: "pushover", Scope: "involved"}}
	if !slices.Equal(p.Crew[0].Subscribe, want) {
		t.Fatalf("subscribe = %+v, want %+v", p.Crew[0].Subscribe, want)
	}
}

func subscribed(t *testing.T, p *pack.Plan, source string) pack.SourceNeeds {
	t.Helper()
	i := slices.IndexFunc(p.Sources, func(s pack.SourceNeeds) bool { return s.Source == source })
	if i < 0 {
		t.Fatalf("the plan says nothing of source %s: %+v", source, p.Sources)
	}
	return p.Sources[i]
}

// A subscription to a source the yard has nothing from is skipped, and the
// Sources table says why. The next apply sets it once the yard has the
// source, reading the credential's name from its declaration.
func TestSubscribeSkippedUntilTheYardHasTheSource(t *testing.T) {
	st, o := yard(t)
	yaml := strings.Replace(smallDeclared, "linear: involved", "linear: involved\n      acme: involved", 1)
	p := load(t, writePack(t, declaredFiles(yaml, pushover)))

	plan := build(t, st, p, o)
	for _, obj := range plan.Objects {
		if obj.Kind == "subscription" && strings.Contains(obj.Name, "acme") {
			t.Fatalf("the plan subscribes to acme: %+v", obj)
		}
	}
	needs := subscribed(t, plan, "acme").Needs
	if len(needs) != 1 || needs[0].What != "source" || needs[0].Status != "missing" ||
		!strings.Contains(needs[0].Detail, "the yard has no source acme") {
		t.Fatalf("needs = %+v", needs)
	}

	if _, err := st.PutSource(ctx, "acme.yaml", cmdDecl, "me"); err != nil {
		t.Fatal(err)
	}
	plan = build(t, st, p, o)
	if sub := find(t, plan, "subscription", "yardmaster acme involved"); sub.Action != pack.Create {
		t.Fatalf("with the source: %+v", sub)
	}
	needs = subscribed(t, plan, "acme").Needs
	i := slices.IndexFunc(needs, func(n pack.Need) bool { return n.What == "credential" })
	if i < 0 || needs[i].Status != "missing" || !strings.Contains(needs[i].Detail, "ACME_TOKEN") {
		t.Fatalf("credential = %+v", needs)
	}
}

// A subscription to a source the pack declares without a hook key is
// skipped too: nothing could be heard from it.
func TestSubscribeToADeclaredSourceWithoutAHookIsSkipped(t *testing.T) {
	st, o := yard(t)
	yaml := strings.Replace(smallDeclared, "linear: involved", "linear: involved\n      pushover: involved", 1)
	plan := build(t, st, load(t, writePack(t, declaredFiles(yaml, pushover))), o)
	for _, obj := range plan.Objects {
		if obj.Kind == "subscription" && strings.Contains(obj.Name, "pushover") {
			t.Fatalf("the plan subscribes to pushover: %+v", obj)
		}
	}
	needs := subscribed(t, plan, "pushover").Needs
	if len(needs) != 1 || needs[0].What != "hook" || needs[0].Status != "missing" ||
		!strings.Contains(needs[0].Detail, "receives no webhooks") ||
		!strings.Contains(needs[0].Detail, "the pack's declaration") {
		t.Fatalf("needs = %+v", needs)
	}
}

// A source of yardr's own is heard without a hook, an identity or a
// credential: the subscription is planned on a yard that has set nothing
// up (for peer, a yard linked with none), the plan asks nothing of the yard
// for it and does not offer it as a source to set up, and the apply makes
// it. Regression: yardr-bnxv, where both were skipped as sources the yard
// does not have.
func TestSubscribeToYardrsOwnSourcesNeedsNothing(t *testing.T) {
	for _, source := range []string{store.SourceYardr, store.SourcePeer} {
		t.Run(source, func(t *testing.T) {
			st, o := yard(t)
			yaml := strings.Replace(small, "linear: involved", "linear: involved\n      "+source+": all", 1)
			p := load(t, writePack(t, smallFiles(yaml)))

			plan := build(t, st, p, o)
			name := "yardmaster " + source + " all"
			if sub := find(t, plan, "subscription", name); sub.Action != pack.Create {
				t.Fatalf("subscription = %+v", sub)
			}
			if i := slices.IndexFunc(plan.Sources, func(s pack.SourceNeeds) bool { return s.Source == source }); i >= 0 {
				t.Fatalf("the plan names needs for %s: %+v", source, plan.Sources[i])
			}
			if i := slices.IndexFunc(plan.Optional, func(s pack.OptionalSource) bool { return s.Source == source }); i >= 0 {
				t.Fatalf("the plan offers %s as a source to set up: %+v", source, plan.Optional[i])
			}

			apply(t, st, plan, nil)
			r, err := st.GetCrew(ctx, "yardmaster")
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(r.Subscriptions, func(s store.Subscription) bool {
				return s.Source == source && s.Scope == store.ScopeAll && len(s.Where) == 0
			}) {
				t.Fatalf("subscriptions = %+v", r.Subscriptions)
			}
			if sub := find(t, build(t, st, p, o), "subscription", name); sub.Action != pack.Unchanged {
				t.Fatalf("a second plan: %+v", sub)
			}
		})
	}
}

func or(s, otherwise string) string {
	if s == "" {
		return otherwise
	}
	return s
}

// An import adds the declaration to the yard's sources and nothing else of
// it: the credential's file and the user who is told stay to be named, and
// the plan gives the command for each. A second import changes nothing.
func TestApplyAddsADeclaredSource(t *testing.T) {
	st, o := yard(t)
	dir := writePack(t, declaredFiles(smallDeclared, pushover))

	plan := build(t, st, load(t, dir), o)
	d := declared(t, plan, "pushover")
	if d.Action != pack.Create || d.Base != "https://api.pushover.net/1" || !slices.Equal(d.Calls, []string{"message (update)"}) {
		t.Fatalf("declared = %+v", d)
	}
	if n := declaredNeed(t, d, store.SourceEnvFile); n.Status != "missing" || !strings.Contains(n.Detail, "yardr source key pushover gives PUSHOVER_TOKEN, or yardr source set pushover env_file=<file> names a file the operator keeps themselves; an agent asks from its pane: yardr bead ask <bead> - --secret pushover, with the question and where to get it on stdin") {
		t.Fatalf("env_file = %+v", n)
	}
	if n := declaredNeed(t, d, store.UserNotify); n.Status != "missing" || !strings.Contains(n.Detail, "yardr user key <user> pushover gives their PUSHOVER_USER, then yardr user set <user> notify=pushover") {
		t.Fatalf("notify = %+v", n)
	}
	if _, err := st.GetSource(ctx, "pushover"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a plan wrote the source: %v", err)
	}

	res := apply(t, st, plan, nil)
	if !slices.Equal(res.Declared, []string{"pushover"}) {
		t.Fatalf("result = %+v", res)
	}
	src, err := st.GetSource(ctx, "pushover")
	if err != nil || src.Text != pushover || src.EnvFile != "" {
		t.Fatalf("source = %+v, %v", src, err)
	}
	if rec := record(t, st); !slices.ContainsFunc(rec.Objects, func(po store.PackObject) bool { return po.Kind == "source" && po.Name == "pushover" }) {
		t.Fatalf("the source is not on record: %+v", rec.Objects)
	}

	plan = build(t, st, load(t, dir), o)
	if got := find(t, plan, "source", "pushover"); got.Action != pack.Unchanged {
		t.Fatalf("a second import would %s the source", got.Action)
	}
	if c, ch := actions(plan, pack.Create), actions(plan, pack.Change); len(c)+len(ch) != 0 {
		t.Fatalf("a second import would create %v and change %v", c, ch)
	}
	if d := declared(t, plan, "pushover"); len(d.Warnings) != 0 {
		t.Fatalf("a declaration with no select is warned of: %q", d.Warnings)
	}

	// A select name no path walks is warned of in the plan, and the import
	// goes through all the same: the name loads as the exact key it spells.
	typo := strings.Replace(pushover, "    request:\n", "    answer: { select: [id, \"head.*\"] }\n    request:\n", 1)
	if typo == pushover {
		t.Fatal("the test adds no select")
	}
	st, o = yard(t)
	plan = build(t, st, load(t, writePack(t, declaredFiles(smallDeclared, typo))), o)
	want := []string{`answer.select "head.*" of call message is read as that exact key and not walked as a path, since a path has no empty segment, star or bracket. It keeps nothing unless the answer has a key spelled exactly that way`}
	if d := declared(t, plan, "pushover"); !slices.Equal(d.Warnings, want) {
		t.Fatalf("warnings = %q, want %q", d.Warnings, want)
	}
	apply(t, st, plan, nil)
	if src, err := st.GetSource(ctx, "pushover"); err != nil || src.Text != typo {
		t.Fatalf("the import did not keep the declaration as written: %+v, %v", src, err)
	}
}

// A source no call of which is granted, whose key a crew member holds,
// has no env_file need: the server makes no call of it, and its commands
// run with the crew member's key. Only its check call wants the source's
// own file. A grant makes it a need again.
func TestDeclaredSourceNeedsNoEnvFileACrewMemberHolds(t *testing.T) {
	st, o := yard(t)
	has(t, st, "boss", "pushover")
	dir := writePack(t, declaredFiles(smallDeclared, pushover))

	plan := build(t, st, load(t, dir), o)
	d := declared(t, plan, "pushover")
	for _, n := range d.Needs {
		if n.What == store.SourceEnvFile {
			t.Fatalf("env_file is a need though boss holds the key: %+v", d.Needs)
		}
	}
	if n := declaredNeed(t, d, store.UserNotify); n.Status != "missing" {
		t.Fatalf("notify = %+v", n)
	}
	apply(t, st, plan, nil)
	if _, err := st.AddUser(ctx, "ada", "me"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetUser(ctx, "ada", map[string]string{store.UserNotify: "pushover", store.UserEnvFile: "/home/ada/ada.env"}, "ada"); err != nil {
		t.Fatal(err)
	}
	d = declared(t, build(t, st, load(t, dir), o), "pushover")
	if n := declaredNeed(t, d, store.SourceEnvFile); n.Status != "missing" {
		t.Fatalf("env_file, with a user notified through the source = %+v", n)
	}

	// A grant is a call the server makes with the source's file: the need
	// is back.
	if _, err := st.GrantSource(ctx, "pushover", "message", store.Grantee{Crew: "boss"}, "", "me"); err != nil {
		t.Fatal(err)
	}
	d = declared(t, build(t, st, load(t, dir), o), "pushover")
	if n := declaredNeed(t, d, store.SourceEnvFile); n.Status != "missing" {
		t.Fatalf("env_file, with a call granted = %+v", n)
	}
}

// A declaration the yard has under the pack's name is replaced, and the
// plan shows where its calls would go instead: a source is where a
// credential of the yard is sent. Its env_file stays, and so does the user
// who names it, which the plan then says. A region base added, moved or
// dropped is shown too, where nothing else differs.
func TestApplyReplacesADeclaredSourceAndThePlanShowsWhatChanges(t *testing.T) {
	st, o := yard(t)
	if _, err := st.PutSource(ctx, "pushover.yaml", strings.Replace(pushover, "https://api.pushover.net/1", "https://push.example.com/1", 1), "me"); err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(t.TempDir(), "pushover.env")
	if err := os.WriteFile(env, []byte("PUSHOVER_TOKEN=secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetSource(ctx, "pushover", store.SourceEnvFile, env, "me"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddUser(ctx, "ada", "me"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetUser(ctx, "ada", map[string]string{store.UserNotify: "pushover", store.UserEnvFile: "/home/ada/ada.env"}, "ada"); err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(pushover, "      path: /messages.json", "      path: /messages", 1)
	changed = strings.Replace(changed, "calls:\n", "calls:\n  limits: { kind: read, request: { method: GET, path: /apps/limits.json } }\n", 1)
	plan := build(t, st, load(t, writePack(t, declaredFiles(smallDeclared, changed))), o)

	got := find(t, plan, "source", "pushover")
	want := []pack.Diff{
		{Field: "base", Old: "https://push.example.com/1", New: "https://api.pushover.net/1"},
		{Field: "call limits", Old: "", New: "(read) GET /apps/limits.json"},
		{Field: "call message", Old: "(update) POST /messages.json", New: "(update) POST /messages"},
	}
	if got.Action != pack.Change || !slices.Equal(got.Changes, want) {
		t.Fatalf("source = %s %+v, want a change of %+v", got.Action, got.Changes, want)
	}
	d := declared(t, plan, "pushover")
	if n := declaredNeed(t, d, store.SourceEnvFile); n.Status != "ok" || strings.Contains(n.Detail, "secret-token") {
		t.Fatalf("env_file = %+v", n)
	}
	if n := declaredNeed(t, d, store.UserNotify); n.Status != "ok" || n.Detail != "tells ada" {
		t.Fatalf("notify = %+v", n)
	}

	apply(t, st, plan, nil)
	src, err := st.GetSource(ctx, "pushover")
	if err != nil || src.Text != changed || src.EnvFile != env {
		t.Fatalf("source = %+v, %v", src, err)
	}

	// A pack that differs in region bases alone is a change of the bases,
	// not another text: a region's address is where the credential goes.
	// Each side is the bases as compact JSON, "" for none.
	eu, eu2 := "https://eu.push.example.com/1", "https://eu2.push.example.com/1"
	withBases := func(url string) string { return changed + "bases:\n  eu: " + url + "\n" }
	for _, step := range []struct {
		name, text string
		want       []pack.Diff
	}{
		{"added", withBases(eu), []pack.Diff{{Field: "bases", Old: "", New: `{"eu":"` + eu + `"}`}}},
		{"moved", withBases(eu2), []pack.Diff{{Field: "bases", Old: `{"eu":"` + eu + `"}`, New: `{"eu":"` + eu2 + `"}`}}},
		{"dropped", changed, []pack.Diff{{Field: "bases", Old: `{"eu":"` + eu2 + `"}`, New: ""}}},
	} {
		plan = build(t, st, load(t, writePack(t, declaredFiles(smallDeclared, step.text))), o)
		got = find(t, plan, "source", "pushover")
		if got.Action != pack.Change || !slices.Equal(got.Changes, step.want) {
			t.Fatalf("bases %s: source = %s %+v, want a change of %+v", step.name, got.Action, got.Changes, step.want)
		}
		apply(t, st, plan, nil)
	}

	// A template is where the credential may go too, to a host the operator
	// types: a pack that differs in it alone is a change of it.
	tpl, tpl2 := "https://{host}/1", "https://{host}/2"
	withTemplate := func(template string) string { return changed + "base_template: " + template + "\n" }
	for _, step := range []struct {
		name, text string
		want       []pack.Diff
	}{
		{"added", withTemplate(tpl), []pack.Diff{{Field: "base_template", Old: "", New: tpl}}},
		{"moved", withTemplate(tpl2), []pack.Diff{{Field: "base_template", Old: tpl, New: tpl2}}},
		{"dropped", changed, []pack.Diff{{Field: "base_template", Old: tpl2, New: ""}}},
	} {
		plan = build(t, st, load(t, writePack(t, declaredFiles(smallDeclared, step.text))), o)
		got = find(t, plan, "source", "pushover")
		if got.Action != pack.Change || !slices.Equal(got.Changes, step.want) {
			t.Fatalf("base_template %s: source = %s %+v, want a change of %+v", step.name, got.Action, got.Changes, step.want)
		}
		apply(t, st, plan, nil)
	}

	// Every key of the credential that alters a call is a change of its own,
	// beside the credential's line, and never the line that says the pack
	// declares the same calls: the scopes the hosted mint must see granted,
	// whether a path may carry the credential, which of a mint's inputs one
	// holder keeps, which of them is no secret and the command a mint runs.
	// The input marked no secret is the account the hosted mint mints for, so
	// one of its rows moves the mark to the other input, a pack that differs
	// in nothing else. Each side is the key as the line shows it, "" for none,
	// the names of several joined with ", ". A mint's inputs and its command
	// stand under a mint, so those rows follow the one that adds it, which
	// says only that the credential's own line changed. A command is a file
	// beside the declaration, so its rows carry it, and its first and last
	// rows say the file's hash with it.
	scopes, scopes2 := "https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/drive.readonly"
	const account, key = "PUSHOVER_ACCOUNT", "PUSHOVER_KEY"
	mint := "  mint: [" + account + ", " + key + "]\n"
	script := "#!/bin/sh\necho token\n"
	sum := sha256.Sum256([]byte(script))
	sha := "sha256 " + hex.EncodeToString(sum[:])[:12]
	withKeys := func(keys string) string {
		return strings.Replace(changed, "credential:\n  env: PUSHOVER_TOKEN\n", "credential:\n  env: PUSHOVER_TOKEN\n"+keys, 1)
	}
	for _, step := range []struct {
		name, text, script string
		want               []pack.Diff
	}{
		{"mint_scopes added", withKeys("  mint_scopes: [" + scopes + "]\n"), "", []pack.Diff{{Field: "mint_scopes", Old: "", New: scopes}}},
		{"mint_scopes moved", withKeys("  mint_scopes: [" + scopes2 + ", " + scopes + "]\n"), "", []pack.Diff{{Field: "mint_scopes", Old: scopes, New: scopes2 + ", " + scopes}}},
		{"mint_scopes dropped", changed, "", []pack.Diff{{Field: "mint_scopes", Old: scopes2 + ", " + scopes, New: ""}}},
		{"in_path added", withKeys("  in_path: true\n"), "", []pack.Diff{{Field: "in_path", Old: "", New: "true"}}},
		{"in_path dropped", changed, "", []pack.Diff{{Field: "in_path", Old: "true", New: ""}}},
		{"mint inputs added", withKeys(mint), "", []pack.Diff{{Field: "credential", Old: "PUSHOVER_TOKEN", New: "minted from " + account + " and " + key + "; PUSHOVER_TOKEN"}}},
		{"bound added", withKeys("  mint: [{ name: " + account + ", bound: true }, " + key + "]\n"), "", []pack.Diff{{Field: "bound", Old: "", New: account}}},
		{"bound moved", withKeys("  mint: [{ name: " + account + ", bound: true }, { name: " + key + ", bound: true }]\n"), "", []pack.Diff{{Field: "bound", Old: account, New: account + ", " + key}}},
		{"bound dropped", withKeys(mint), "", []pack.Diff{{Field: "bound", Old: account + ", " + key, New: ""}}},
		{"not_secret added", withKeys("  mint: [{ name: " + account + ", secret: false }, " + key + "]\n"), "", []pack.Diff{{Field: "not_secret", Old: "", New: account}}},
		{"not_secret moved", withKeys("  mint: [" + account + ", { name: " + key + ", secret: false }]\n"), "", []pack.Diff{{Field: "not_secret", Old: account, New: key}}},
		{"not_secret second input added", withKeys("  mint: [{ name: " + account + ", secret: false }, { name: " + key + ", secret: false }]\n"), "", []pack.Diff{{Field: "not_secret", Old: key, New: account + ", " + key}}},
		{"not_secret dropped", withKeys(mint), "", []pack.Diff{{Field: "not_secret", Old: account + ", " + key, New: ""}}},
		{"mint_command added", withKeys(mint + "  mint_command: [pushover/mint]\n"), script, []pack.Diff{
			{Field: "mint_command", Old: "", New: "pushover/mint"}, {Field: "file pushover/mint", Old: "", New: sha}}},
		{"mint_command moved", withKeys(mint + "  mint_command: [pushover/mint, --refresh]\n"), script, []pack.Diff{
			{Field: "mint_command", Old: "pushover/mint", New: "pushover/mint --refresh"}}},
		{"mint_command dropped", withKeys(mint), "", []pack.Diff{
			{Field: "mint_command", Old: "pushover/mint --refresh", New: ""}, {Field: "file pushover/mint", Old: sha, New: ""}}},
	} {
		files := declaredFiles(smallDeclared, step.text)
		if step.script != "" {
			files["sources/pushover/mint"] = step.script
		}
		plan = build(t, st, load(t, writePack(t, files)), o)
		got = find(t, plan, "source", "pushover")
		if got.Action != pack.Change || !slices.Equal(got.Changes, step.want) {
			t.Fatalf("%s: source = %s %+v, want a change of %+v", step.name, got.Action, got.Changes, step.want)
		}
		apply(t, st, plan, nil)
	}
}

// A declared source may build a call's body with a pack script, or have a
// call answered by one: the plan hashes the script beside the call, whose
// summary stays its name and kind, lists it among the pack's programs, and a
// replacement whose script changed shows the file's old hash against its
// new one.
func TestDeclaredSourceWithABuiltCall(t *testing.T) {
	const decl = `name: docs
base: https://docs.googleapis.com
credential:
  env: DOCS_TOKEN
calls:
  ping: { kind: read, request: { method: GET, path: /me } }
  write:
    kind: update
    params:
      docId: { type: string, max: 200 }
    request: { method: POST, path: "/v1/documents/{docId}:batchUpdate" }
    build: [docs/build-write]
  lookup:
    kind: read
    params:
      query: { type: string, max: 200 }
    run: [docs/lookup, search]
`
	st, o := yard(t)
	yaml := small + "sources:\n  docs:\n    declaration: sources/docs.yaml\n"
	files := smallFiles(yaml)
	files["sources/docs.yaml"] = decl
	script := "#!/bin/sh\necho '{}'\n"
	files["sources/docs/build-write"] = script
	lookup := "#!/bin/sh\necho '{}'\n"
	files["sources/docs/lookup"] = lookup
	dir := writePack(t, files)
	plan := build(t, st, load(t, dir), o)

	d := declared(t, plan, "docs")
	if !slices.Contains(d.Calls, "write (update)") || !slices.Contains(d.Calls, "lookup (read)") {
		t.Fatalf("the plan's calls = %v, want the built and the run call by name and kind", d.Calls)
	}
	sum := sha256.Sum256([]byte(script))
	var cmd *pack.CommandPlan
	for i := range d.Commands {
		if d.Commands[i].Key == "call.write.build" {
			cmd = &d.Commands[i]
		}
	}
	if cmd == nil || cmd.Program != "docs/build-write" || cmd.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("the plan's commands = %+v, want the build with its sha256", d.Commands)
	}
	ran := sha256.Sum256([]byte(lookup))
	cmd = nil
	for i := range d.Commands {
		if d.Commands[i].Key == "call.lookup.run" {
			cmd = &d.Commands[i]
		}
	}
	if cmd == nil || cmd.Program != "docs/lookup" || cmd.SHA256 != hex.EncodeToString(ran[:]) {
		t.Fatalf("the plan's commands = %+v, want the run with its sha256", d.Commands)
	}
	// What pack check prints as the files that run: the run call's program
	// is one of them, as the build's is.
	if !slices.Contains(plan.Programs, "sources/docs/lookup") || !slices.Contains(plan.Programs, "sources/docs/build-write") {
		t.Fatalf("the plan's programs = %v, want the build's and the run's", plan.Programs)
	}

	apply(t, st, plan, nil)

	// The pack's script changed: the plan shows the changed file.
	changed := "#!/bin/sh\necho '{\"built\":true}'\n"
	if err := os.WriteFile(filepath.Join(dir, "sources", "docs", "build-write"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	plan = build(t, st, load(t, dir), o)
	got := find(t, plan, "source", "docs")
	newer := sha256.Sum256([]byte(changed))
	want := []pack.Diff{{Field: "file docs/build-write", Old: "sha256 " + hex.EncodeToString(sum[:])[:12], New: "sha256 " + hex.EncodeToString(newer[:])[:12]}}
	if got.Action != pack.Change || !reflect.DeepEqual(got.Changes, want) {
		t.Fatalf("after the script changed: %+v", got)
	}
}

// Another text that declares the same is a change too, and said to be one
// of the text alone.
func TestADeclarationThatOnlyReadsDifferentlyIsSaidSo(t *testing.T) {
	st, o := yard(t)
	if _, err := st.PutSource(ctx, "pushover.yaml", "# as the operator added it\n"+pushover, "me"); err != nil {
		t.Fatal(err)
	}
	plan := build(t, st, load(t, writePack(t, declaredFiles(smallDeclared, pushover))), o)
	got := find(t, plan, "source", "pushover")
	want := []pack.Diff{{Field: "declaration", Old: "another text", New: "the pack's, which declares the same calls"}}
	if got.Action != pack.Change || !slices.Equal(got.Changes, want) {
		t.Fatalf("source = %s %+v, want %+v", got.Action, got.Changes, want)
	}
}

// A pack that changes a key the yard hears or speaks through is said to
// change it, with the whole key before and after as one value: a thread that
// now takes files, a hook that verifies another way. "Declares the same
// calls" would hide both, and stays for a text that differs in nothing the
// plan compares. The yard has the pack's declaration, applied once, and each
// row is the pack's file after one edit of it. These keys are for a pack of
// kind source, so it is one.
func TestAChangedHearThreadLinkOrHookKeyIsShown(t *testing.T) {
	st := storetest.New(t)
	o := pack.Options{Home: t.TempDir()}
	files := sourcePackFiles()
	files["acme.yaml"] = hearDecl
	dir := writePack(t, files)
	if _, err := pack.Apply(ctx, st, build(t, st, load(t, dir), o), pack.ApplyOptions{Actor: "jo"}); err != nil {
		t.Fatal(err)
	}

	// The keys of hearDecl as the plan shows them: compact JSON, in the order
	// of the key's fields, the fields it leaves out left out.
	const (
		hear       = `{"field":"team","address":{"env":"ACME_TEAM"}}`
		link       = `{"host":"acme.test","issue":"^/issue/([0-9]+)","fixed":"https://acme.test/issue/{1}","thread":"{1}"}`
		thread     = `{"reply":["acme/thread","reply"],"posted":["acme/thread","posted"],"read":["acme/thread","read"],"open":["acme/thread","open"],"opened":["acme/thread","opened"],"state":["acme/thread","state"],"place":"team","states":["started","review","done"]}`
		hook       = `{"verify":{"scheme":"hmac-sha256","signed":"body","signature":{"header":"Acme-Signature","encoding":"hex"},"delivery":{"header":"Acme-Delivery"}},"bodies":"unique","normalize":["acme/normalize"],"keys":["team"]}`
		threadText = "thread:\n  reply: [acme/thread, reply]\n  posted: [acme/thread, posted]\n  read: [acme/thread, read]\n  place: team\n  open: [acme/thread, open]\n  opened: [acme/thread, opened]\n  state: [acme/thread, state]\n  states: [started, review, done]\n"
	)
	if strings.Count(hearDecl, threadText) != 1 {
		t.Fatal("threadText is not once in hearDecl")
	}
	withoutThread := strings.Replace(hearDecl, threadText, "", 1)
	another := pack.Diff{Field: "declaration", Old: "another text", New: "the pack's, which declares the same calls"}
	cases := []struct {
		name     string
		base     string
		old, new string // the replacement in base
		want     pack.Diff
	}{
		{name: "thread takes files", old: "  states: [started, review, done]\n", new: "  states: [started, review, done]\n  files: true\n",
			want: pack.Diff{Field: "thread", Old: thread, New: strings.TrimSuffix(thread, "}") + `,"files":true}`}},
		{name: "hook verifies with another hash", old: "    scheme: hmac-sha256\n", new: "    scheme: hmac-sha256\n    algorithm: sha1\n",
			want: pack.Diff{Field: "hook", Old: hook, New: strings.Replace(hook, `"delivery"`, `"algorithm":"sha1","delivery"`, 1)}},
		{name: "link reads another path", old: "issue: '^/issue/([0-9]+)'", new: "issue: '^/issues/([0-9]+)'",
			want: pack.Diff{Field: "link", Old: link, New: strings.Replace(link, `"^/issue/`, `"^/issues/`, 1)}},
		{name: "hear narrowed to the operator", old: "  address: { env: ACME_TEAM }\n", new: "  address: { env: ACME_TEAM }\n  from: operator\n",
			want: pack.Diff{Field: "hear", Old: hear, New: strings.TrimSuffix(hear, "}") + `,"from":"operator"}`}},
		{name: "hear dropped", old: "hear:\n  field: team\n  address: { env: ACME_TEAM }\n", new: "",
			want: pack.Diff{Field: "hear", Old: hear, New: ""}},
		{name: "only a comment", old: "name: acme\n", new: "# said again\nname: acme\n", want: another},
		{name: "only the about", old: "about: Acme issues.\n", new: "about: Acme's issues.\n", want: another},
		{name: "only the order of a key's fields", old: "{ header: Acme-Signature, encoding: hex }", new: "{ encoding: hex, header: Acme-Signature }", want: another},
		{name: "empty thread reads as absent", base: withoutThread, old: "hear:\n", new: "thread: {}\nhear:\n", want: another},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := hearDecl
			if c.base != "" {
				base = c.base
				if _, err := st.PutSource(ctx, "acme.yaml", base, "jo"); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(dir, "acme/thread")); err != nil {
					t.Fatal(err)
				}
			}
			if strings.Count(base, c.old) != 1 {
				t.Fatalf("%q is not once in the declaration", c.old)
			}
			text := strings.Replace(base, c.old, c.new, 1)
			if err := os.WriteFile(filepath.Join(dir, "acme.yaml"), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			got := find(t, build(t, st, load(t, dir), o), "source", "acme")
			if got.Action != pack.Change || !slices.Equal(got.Changes, []pack.Diff{c.want}) {
				t.Fatalf("source = %s %+v, want one change %+v", got.Action, got.Changes, c.want)
			}
		})
	}
}

// Adding a source is the operator's. Run by anyone else the source is
// neither added nor recorded, and the rest of the pack is imported but
// left unrecorded, with the run card named; the operator's next apply
// adds it.
func TestDeclaredSourceRefusedLeavesTheRestOfTheImport(t *testing.T) {
	st, o := yard(t)
	if err := st.SetSetting(ctx, store.OperatorSetting, "me", "me"); err != nil {
		t.Fatal(err)
	}
	p := load(t, writePack(t, declaredFiles(smallDeclared, pushover)))

	plan := build(t, st, p, o)
	a := pack.Answers{}
	for _, q := range plan.Questions {
		a[q.ID] = map[string]string{"kind": "claude", "dir": t.TempDir()}[q.Field]
	}
	seedGroups(t, st, plan, a)
	res, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{Answers: a, SourceRefused: map[string]string{"pushover": "not the operator"}, Actor: "yardmaster"})
	if err == nil || !strings.Contains(err.Error(), "may record the import of pack small") ||
		!strings.Contains(err.Error(), "applying again records nothing") || !strings.Contains(err.Error(), "yardr bead ask --new - with Q: Apply pack small to depot web?, C: the operator must approve the pack's import and R: pack apply small --depot web on stdin") {
		t.Fatalf("apply as yardmaster = %+v, %v; want the rest imported and the record refused with the run card", res, err)
	}
	if res == nil || res.Recorded {
		t.Fatalf("result = %+v, want it unrecorded", res)
	}
	if _, err := st.GetSource(ctx, "pushover"); !errors.Is(err, store.ErrNotFound) || len(res.Declared) != 0 {
		t.Fatalf("a refused source was added: %v, %+v", err, res)
	}
	if _, err := st.GetGroup(ctx, "web-builders"); err != nil {
		t.Fatalf("the rest was not imported: %v, %+v", err, res)
	}
	if recs, _ := st.Packs(ctx); len(recs) != 0 {
		t.Fatalf("a crew member's import was recorded: %+v", recs)
	}

	// Without the refusal the store holds the same line, and says whose it is.
	plan = build(t, st, p, o)
	if _, err := pack.Apply(ctx, st, plan, pack.ApplyOptions{Answers: a, Actor: "yardmaster"}); err == nil || !strings.Contains(err.Error(), "adding a source is the operator's (me)") {
		t.Fatalf("err = %v, want the store's refusal", err)
	}

	plan = build(t, st, p, o)
	if got := actions(plan, pack.Create); !slices.Equal(got, []string{"source pushover"}) {
		t.Fatalf("the next plan would create %v", got)
	}
	apply(t, st, plan, nil)
	if _, err := st.GetSource(ctx, "pushover"); err != nil {
		t.Fatal(err)
	}
}

// A pack that drops its declaration leaves the source in the yard unless
// told to prune. A prune removes it, but not while a user is told through
// it, and never one whose declaration was added by hand since.
func TestDroppedDeclaredSourceIsPrunedOnlyWhenAsked(t *testing.T) {
	st, o := yard(t)
	dir := writePack(t, declaredFiles(smallDeclared, pushover))
	apply(t, st, build(t, st, load(t, dir), o), nil)
	if _, err := st.AddUser(ctx, "ada", "me"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetUser(ctx, "ada", map[string]string{store.UserNotify: "pushover", store.UserEnvFile: "/home/ada/ada.env"}, "ada"); err != nil {
		t.Fatal(err)
	}
	edit(t, dir, "sources:\n  pushover:\n    declaration: sources/pushover.yaml\n", "")
	prune := func() *pack.Result {
		t.Helper()
		res, err := pack.Apply(ctx, st, build(t, st, load(t, dir), o), pack.ApplyOptions{Answers: pack.Answers{}, Prune: true, Actor: "me"})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	plan := build(t, st, load(t, dir), o)
	got := find(t, plan, "source", "pushover")
	if got.Action != pack.Dropped || !strings.Contains(got.Held, "user ada is told through it") {
		t.Fatalf("source = %s, held %q", got.Action, got.Held)
	}
	if res := prune(); !slices.Contains(res.Left, "source pushover") {
		t.Fatalf("a held source was pruned: %+v", res)
	}
	if _, err := st.GetSource(ctx, "pushover"); err != nil {
		t.Fatal(err)
	}

	if _, err := st.SetUser(ctx, "ada", map[string]string{store.UserNotify: ""}, "ada"); err != nil {
		t.Fatal(err)
	}
	res := apply(t, st, build(t, st, load(t, dir), o), nil)
	if !slices.Contains(res.Left, "source pushover") {
		t.Fatalf("without --prune the source is left: %+v", res)
	}
	if res := prune(); !slices.Contains(res.Pruned, "source pushover") {
		t.Fatalf("pruned = %+v", res)
	}
	if _, err := st.GetSource(ctx, "pushover"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the source is still there: %v", err)
	}
}

func TestADeclaredSourceAddedByHandSinceIsTheYards(t *testing.T) {
	st, o := yard(t)
	dir := writePack(t, declaredFiles(smallDeclared, pushover))
	apply(t, st, build(t, st, load(t, dir), o), nil)
	if _, err := st.PutSource(ctx, "pushover.yaml", "# mine now\n"+pushover, "me"); err != nil {
		t.Fatal(err)
	}
	edit(t, dir, "sources:\n  pushover:\n    declaration: sources/pushover.yaml\n", "")
	plan := build(t, st, load(t, dir), o)
	if slices.ContainsFunc(plan.Objects, func(o pack.Object) bool { return o.Kind == "source" }) {
		t.Fatalf("the yard's own source is in the plan: %v", actions(plan, pack.Dropped))
	}
}

// A pack's declaration may name a second credential for its updates and the
// header an update's key goes in. The plan asks for an env_file that sets
// both variables, and shows either as a change of a source the yard has.
func TestADeclaredSourceWithACredentialForItsUpdates(t *testing.T) {
	st, o := yard(t)
	if _, err := st.PutSource(ctx, "pushover.yaml", pushover, "me"); err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(t.TempDir(), "pushover.env")
	if err := os.WriteFile(env, []byte("PUSHOVER_TOKEN=secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetSource(ctx, "pushover", store.SourceEnvFile, env, "me"); err != nil {
		t.Fatal(err)
	}
	two := strings.Replace(pushover, "  env: PUSHOVER_TOKEN\n", "  env: PUSHOVER_TOKEN\n  update_env: PUSHOVER_SEND_TOKEN\nidempotency: Idempotency-Key\n", 1)
	plan := build(t, st, load(t, writePack(t, declaredFiles(smallDeclared, two))), o)
	want := []pack.Diff{
		{Field: "credential", Old: "PUSHOVER_TOKEN", New: "PUSHOVER_TOKEN, for an update PUSHOVER_SEND_TOKEN"},
		{Field: "idempotency", Old: "", New: "Idempotency-Key"},
	}
	if got := find(t, plan, "source", "pushover"); got.Action != pack.Change || !slices.Equal(got.Changes, want) {
		t.Fatalf("source = %s %+v, want a change of %+v", got.Action, got.Changes, want)
	}
	d := declared(t, plan, "pushover")
	if n := declaredNeed(t, d, store.SourceEnvFile); n.Status != "missing" || !strings.Contains(n.Detail, "does not set PUSHOVER_SEND_TOKEN") {
		t.Fatalf("env_file, with the first variable alone = %+v", n)
	}
	if err := os.WriteFile(env, []byte("PUSHOVER_TOKEN=secret-token\nPUSHOVER_SEND_TOKEN=secret-send\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d = declared(t, build(t, st, load(t, writePack(t, declaredFiles(smallDeclared, two))), o), "pushover")
	if n := declaredNeed(t, d, store.SourceEnvFile); n.Status != "ok" || !strings.Contains(n.Detail, "for an update, PUSHOVER_SEND_TOKEN is set in") || strings.Contains(n.Detail, "secret") {
		t.Fatalf("env_file, with both = %+v", n)
	}
}

// hookDeclared is a declaration whose credential is the address every call
// goes to, so it names no base.
const hookDeclared = `name: pushover
about: A webhook.
credential:
  env: PUSHOVER_URL
  url: true
calls:
  message:
    kind: update
    params: { text: { type: string, max: 1024 } }
    request: { method: POST, body: { text: "{text}" } }
`

// A declaration whose credential is the address says where its calls go in
// words, since the address is a secret and no file holds it: the plan names
// the variable, asks for it in the env_file, and shows a source the yard has
// that went to a base as a change of both.
func TestADeclaredSourceWhoseCredentialIsTheAddress(t *testing.T) {
	st, o := yard(t)
	plan := build(t, st, load(t, writePack(t, declaredFiles(smallDeclared, hookDeclared))), o)
	d := declared(t, plan, "pushover")
	if d.Base != "the URL in PUSHOVER_URL" || !slices.Equal(d.Calls, []string{"message (update)"}) {
		t.Fatalf("declared = %+v, want its calls under the URL in PUSHOVER_URL", d)
	}
	if n := declaredNeed(t, d, store.SourceEnvFile); n.Status != "missing" || !strings.Contains(n.Detail, "yardr source key pushover gives PUSHOVER_URL") {
		t.Fatalf("env_file = %+v", n)
	}

	if _, err := st.PutSource(ctx, "pushover.yaml", pushover, "me"); err != nil {
		t.Fatal(err)
	}
	plan = build(t, st, load(t, writePack(t, declaredFiles(smallDeclared, hookDeclared))), o)
	got := find(t, plan, "source", "pushover")
	if got.Action != pack.Change {
		t.Fatalf("source = %s %+v, want a change", got.Action, got.Changes)
	}
	for _, want := range []pack.Diff{
		{Field: "base", Old: "https://api.pushover.net/1", New: "the URL in PUSHOVER_URL"},
		{Field: "credential", Old: "PUSHOVER_TOKEN", New: "PUSHOVER_URL, the address every call goes to"},
		{Field: "call message", Old: "(update) POST /messages.json", New: "(update) POST /"},
	} {
		if !slices.Contains(got.Changes, want) {
			t.Errorf("the change lacks %+v: %+v", want, got.Changes)
		}
	}
}
