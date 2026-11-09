package cli

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
	"yardr.dev/yardr/internal/version"
	"yardr.dev/yardr/packs"
)

// The yardmaster runs a delegated command from its CLI with --for and
// --mail, naming a preset where args would go; the store keeps the record,
// and yardr delegation show reads it for an operator alone.
func TestDelegatedRunFromTheCLI(t *testing.T) {
	t.Parallel()
	st := userYard(t)
	ctx := context.Background()
	mustAs(t, st, "jo", "user", "add", "ada")
	mustAs(t, st, "jo", "settings", "set", store.DelegationSetting, "true")
	mustAs(t, st, "jo", "group", "add", "builders", "--runner", "herdr", "--set", "kind=claude")
	mustAs(t, st, "jo", "mail", "yardmaster", "give the builders the accept-edits preset")
	head, err := st.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mail := strconv.FormatUint(head, 10)

	if _, err := as(t, st, "yardmaster", "group", "set", "builders", "preset=accept-edits"); err == nil {
		t.Fatal("the yardmaster set a group's args with no delegation")
	}
	if _, err := as(t, st, "yardmaster", "group", "set", "builders", "preset=accept-edits", "--mail", mail); err == nil || !strings.Contains(err.Error(), "goes with --for") {
		t.Errorf("--mail alone: %v", err)
	}
	if _, err := as(t, st, "yardmaster", "group", "set", "builders", `args=["--yolo"]`, "--for", "jo", "--mail", mail); err == nil || !strings.Contains(err.Error(), "never carries free args") {
		t.Errorf("free args on a delegated run: %v", err)
	}
	mustAs(t, st, "yardmaster", "group", "set", "builders", "preset=accept-edits", "--for", "jo", "--mail", mail)
	accept, _ := store.HarnessPreset("claude", "accept edits")
	g, err := st.GetGroup(ctx, "builders")
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	for _, a := range g.Config["args"].([]any) {
		args = append(args, a.(string))
	}
	if !reflect.DeepEqual(args, accept.Args) {
		t.Errorf("builders' args = %v, want the accept-edits preset's %v", args, accept.Args)
	}

	show := mustAs(t, st, "jo", "delegation", "show", "1")
	for _, want := range []string{"record 1: yardmaster for jo", "on mail " + mail + " from jo", "ran: yardr group set builders preset=accept-edits", "args: (unset) -> ", "taken back by: yardr group set builders --unset args"} {
		if !strings.Contains(show, want) {
			t.Errorf("delegation show lacks %q:\n%s", want, show)
		}
	}
	for _, reader := range []string{"ada", "yardmaster"} {
		if out, err := as(t, st, reader, "delegation", "show", "1"); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("%s reads record 1: %q, %v", reader, out, err)
		}
	}

	// depot test and depot base change one field each, for the operator at
	// a terminal and for the yardmaster's delegated run alike.
	mustAs(t, st, "jo", "depot", "add", "docs", t.TempDir(), "--kind", "dir")
	mustAs(t, st, "jo", "depot", "test", "docs", "make test")
	mustAs(t, st, "yardmaster", "depot", "test", "docs", "sh .yardr/test", "--for", "jo", "--mail", mail)
	if d, err := st.GetDepot(ctx, "docs"); err != nil || d.Test != "sh .yardr/test" {
		t.Errorf("docs = %+v, %v", d, err)
	}
	if show := mustAs(t, st, "jo", "delegation", "show", "2"); !strings.Contains(show, "taken back by: yardr depot test docs 'make test'") {
		t.Errorf("the depot test's record:\n%s", show)
	}
	if err := st.PutGroup(ctx, store.Group{Name: "inherited", Runner: "herdr"}, "jo"); err != nil {
		t.Fatal(err)
	}
	mustAs(t, st, "yardmaster", "group", "set", "inherited", "preset=accept-edits", "--for", "jo", "--mail", mail)
	mustAs(t, st, "yardmaster", "group", "add", "reviewers", "--runner", "herdr", "--set", "preset=accept-edits", "--for", "jo", "--mail", mail)
	// The record's words keep the flags the command was given, as typed
	// (preset= is read after the words are kept), and not --for, --mail or the
	// --actor the helper adds.
	if show := mustAs(t, st, "jo", "delegation", "show", "4"); !strings.Contains(show, "\nran: yardr group add reviewers --runner herdr --set preset=accept-edits\n") {
		t.Errorf("the delegated group add's record:\n%s", show)
	}
	if _, err := as(t, st, "yardmaster", "hook", "add", "delegated", "--source", "token", "--secret-file", t.TempDir(), "--for", "jo", "--mail", mail); err == nil || !strings.Contains(err.Error(), "--secret-file is the operator's") {
		t.Errorf("explicit secret file in a delegated hook add: %v", err)
	}
}

// A delegated run's record keeps the command as typed: its path, its words and
// each flag it was given, quoted as a terminal reads them, the flags by name
// and a repeated flag's values in the order given. A flag it was not given
// stays out though it has a default. So do --for and --mail, which the record
// keeps apart, and the root's --actor, --json and --home, which say who ran the
// command and how it printed.
func TestDelegatedCommandWords(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		path []string // the command, under yardr
		args []string // what follows it, as the shell hands it over
		want string
	}{
		{"two flags", []string{"pack", "apply"}, []string{"starter", "--depot", "web", "--bind", "designers=claude"},
			"yardr pack apply starter --bind designers=claude --depot web"},
		{"a repeated flag keeps each value in the order given", []string{"group", "add"},
			[]string{"reviewers", "--runner", "herdr", "--set", "kind=claude", "--set", "model=opus"},
			"yardr group add reviewers --runner herdr --set kind=claude --set model=opus"},
		{"a list flag given as one list is a pair for each value", []string{"group", "add"},
			[]string{"reviewers", "--members", "ada,hal"},
			"yardr group add reviewers --members ada --members hal"},
		{"a value that needs quoting, an empty one among them", []string{"group", "add"},
			[]string{"reviewers", "--runner", "", "--set", "prompt=it's a prompt", "--set", "worktrees=~/my work"},
			`yardr group add reviewers --runner '' --set 'prompt=it'\''s a prompt' --set 'worktrees=~/my work'`},
		{"a word that needs quoting", []string{"depot", "test"}, []string{"shed", "make test"},
			"yardr depot test shed 'make test'"},
		{"a flag not given stays out", []string{"group", "add"}, []string{"reviewers"},
			"yardr group add reviewers"},
		{"--for, --mail and the root's flags stay out", []string{"depot", "test"},
			[]string{"shed", "make test", "--for", "jo", "--mail", "7", "--actor", "yardmaster", "--json", "--home", "/yard"},
			"yardr depot test shed 'make test'"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cmd, _, err := (&app{}).rootCmd().Find(c.path)
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.ParseFlags(c.args); err != nil {
				t.Fatal(err)
			}
			if got := commandWords(cmd, cmd.Flags().Args()); got != c.want {
				t.Errorf("commandWords = %q, want %q", got, c.want)
			}
		})
	}
}

// A delegated pack apply from the CLI takes a pack yardr embeds with its
// mappings alone: the yardmaster applies telegram for jo on her mail and
// the record stands done; a directory, another flag, an unknown name and
// a plan that asks each refuse, and a record left started reads as
// unfinished.
func TestDelegatedPackApplyFromTheCLI(t *testing.T) {
	t.Parallel()
	// The delegated pack record id must cross the socket with each write,
	// so exercise this command through the same RPC client the CLI uses.
	// The store behind it stands with the reading of the packs yardr
	// embeds the server registers: telegram's declaration whole, and
	// starter with the digest the started-record case below names.
	rpcAPI, own := storetest.NewRPCOwned(t)
	// The build the server's reading names: the command's own, until a step
	// stands the server on another.
	var serverBuild atomic.Value
	serverBuild.Store(version.Describe())
	reader := func(ctx context.Context, name string, depot string, _ store.Mappings, yard store.API) (*store.PackReading, error) {
		build := serverBuild.Load().(string)
		switch name {
		case "telegram", "pushover":
			sub, err := fs.Sub(packs.FS, name)
			if err != nil {
				return nil, err
			}
			p, err := pack.Load(sub, packs.Embedded)
			if err != nil {
				return nil, err
			}
			plan, err := pack.Build(ctx, yard, p, pack.Options{})
			if err != nil {
				return nil, err
			}
			text, err := fs.ReadFile(packs.FS, name+"/"+name+".yaml")
			if err != nil {
				return nil, err
			}
			askPlan := &store.AskPlan{}
			for _, o := range plan.Objects {
				askPlan.Objects = append(askPlan.Objects, store.AskPlanObject{Kind: o.Kind, Name: o.Name, Action: o.Action})
			}
			// The record the plan leaves of its import is the plan's own, as the
			// server's reading derives it, and the label the one the command
			// loads a pack yardr embeds under.
			imp := plan.ImportRecord(pack.ApplyOptions{})
			return &store.PackReading{Name: name, Kind: "source", Digest: plan.Digest, Plan: askPlan, Import: &imp,
				Label: packs.Embedded + " in yardr " + build, Build: build,
				Sources: []store.AskSource{{Name: name, Text: string(text)}}}, nil
		case "starter":
			return &store.PackReading{Name: "starter", Kind: "git", Depot: depot, Digest: "abc", Plan: &store.AskPlan{}}, nil
		}
		return nil, store.ErrNotFound
	}
	own.SetPackReader(reader)
	// The packs repository is a stand-in serving pushover and starter: the
	// store's fetching holds a pack to its URL and its pin as the server's
	// does (pack.LoadRepository), and reads it as the double above reads
	// them.
	origin := serveRepositoryPacks(t, "pushover", "starter")
	own.SetPackFetcher(func(_ context.Context, from, digest string) (store.PackReader, error) {
		if _, err := pack.LoadRepository(origin, from, digest); err != nil {
			return nil, err
		}
		return reader, nil
	})
	st := userYardOn(t, rpcAPI)
	ctx := context.Background()
	mustAs(t, st, "jo", "settings", "set", store.DelegationSetting, "true")
	mustAs(t, st, "jo", "mail", "yardmaster", "apply telegram for me")
	head, err := st.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mail := strconv.FormatUint(head, 10)

	// As the yardmaster types it, the import does the rest and adds no
	// source: only the delegated run applies the pack whole.
	if out, err := as(t, st, "yardmaster", "pack", "apply", "telegram"); err != nil || !strings.Contains(out, "was not added") {
		t.Errorf("pack apply as typed: %q, %v", out, err)
	}
	if _, err := st.GetSource(ctx, "telegram"); err == nil {
		t.Fatal("telegram was applied without a delegation")
	}
	for words, want := range map[string]string{
		"pack apply telegram --yes":                               "no other flag, not --yes",
		"pack apply nosuchpack":                                   "embeds no pack of that name",
		"pack apply " + t.TempDir():                               "applies a pack yardr embeds",
		"pack apply telegram --sha256 " + strings.Repeat("a", 64): "no other flag, not --sha256",
	} {
		args := append(strings.Fields(words), "--for", "jo", "--mail", mail)
		if out, err := as(t, st, "yardmaster", args...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %q, %v; want %q", words, out, err, want)
		}
	}
	// The command applies a pack of the build it is, which the server's
	// reading names: where the server runs another, as in the seconds between
	// an install and its restart, the start refuses naming both builds, with
	// nothing written, and the apply goes through once they are one.
	serverBuild.Store("other")
	if out, err := as(t, st, "yardmaster", "pack", "apply", "telegram", "--for", "jo", "--mail", mail); err == nil || !strings.Contains(err.Error(), "two builds") ||
		!strings.Contains(err.Error(), "embedded in yardr "+version.Describe()) || !strings.Contains(err.Error(), "embedded in yardr other") {
		t.Errorf("pack apply against a server of another build: %q, %v", out, err)
	}
	serverBuild.Store(version.Describe())
	if _, err := st.GetSource(ctx, "telegram"); err == nil {
		t.Fatal("telegram was applied against a server of another build")
	}
	if _, err := st.Delegation(ctx, 1, "jo"); err == nil {
		t.Fatal("a refused delegated pack apply left a record")
	}

	mustAs(t, st, "yardmaster", "pack", "apply", "telegram", "--for", "jo", "--mail", mail)
	if _, err := st.GetSource(ctx, "telegram"); err != nil {
		t.Fatalf("telegram was not applied: %v", err)
	}
	rec, err := st.Delegation(ctx, 1, "jo")
	if err != nil || rec.State != "done" || rec.DelegatedBody.Pack != "telegram" || rec.DelegatedBody.Digest == "" {
		t.Fatalf("record 1 = %+v, %v; want telegram done with its digest", rec, err)
	}
	if show := mustAs(t, st, "jo", "delegation", "show", "1"); !strings.Contains(show, "state: done") || !strings.Contains(show, "pack telegram") {
		t.Errorf("delegation show 1:\n%s", show)
	}

	// A record left started reads as unfinished, never done.
	mustAs(t, st, "jo", "mail", "yardmaster", "and starter in web")
	head, err = st.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dctx := store.WithDelegation(ctx, store.DelegatedCall{For: "jo", Mail: head, Command: "yardr pack apply starter --depot web"})
	id, err := st.StartDelegatedPack(dctx, store.DelegatedPackStart{Pack: "starter", Depot: "web", Digest: "abc"}, "yardmaster")
	if err != nil {
		t.Fatal(err)
	}
	if show := mustAs(t, st, "jo", "delegation", "show", strconv.FormatUint(id, 10)); !strings.Contains(show, "state: started (unfinished)") {
		t.Errorf("a started record's show:\n%s", show)
	}

	// A plan that asks refuses whole and leaves no record.
	if err := st.PutDepot(ctx, store.Depot{Name: "web", Path: t.TempDir(), Base: "main"}, "jo"); err != nil {
		t.Fatal(err)
	}
	mail = strconv.FormatUint(head, 10)
	if out, err := as(t, st, "yardmaster", "pack", "apply", "starter", "--depot", "web", "--for", "jo", "--mail", mail); err == nil || !strings.Contains(err.Error(), "its plan asks") {
		t.Errorf("starter with no mappings: %q, %v", out, err)
	}
	if _, err := st.Delegation(ctx, id+1, "jo"); err == nil {
		t.Error("an asking plan left a record")
	}

	// A pack of the packs repository is named by its URL and pinned. An
	// apply with no pin, with a URL of another origin, with another digest
	// or with another flag refuses, and leaves no record; the pinned apply
	// of the repository's own pack runs and the record names where it came
	// from.
	sub, err := fs.Sub(packs.FS, "pushover")
	if err != nil {
		t.Fatal(err)
	}
	pushover, err := pack.Load(sub, packs.Embedded)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := pack.Build(ctx, st, pushover, pack.Options{})
	if err != nil {
		t.Fatal(err)
	}
	from := origin + "pushover/"
	pin := "sha256:" + plan.Digest
	starterSub, err := fs.Sub(packs.FS, "starter")
	if err != nil {
		t.Fatal(err)
	}
	starterPack, err := pack.Load(starterSub, packs.Embedded)
	if err != nil {
		t.Fatal(err)
	}
	starterPlan, err := pack.Build(ctx, st, starterPack, pack.Options{Depot: "web", Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	mustAs(t, st, "jo", "mail", "yardmaster", "and pushover, from the packs repository")
	if head, err = st.Head(ctx); err != nil {
		t.Fatal(err)
	}
	mail = strconv.FormatUint(head, 10)
	asRepository := func(args ...string) (string, error) {
		a := appOn(st)
		a.packsOrigin = origin
		return run(t, a.rootCmd(), append(args, "--for", "jo", "--mail", mail, "--actor", "yardmaster")...)
	}
	for words, want := range map[string]string{
		"pack apply " + from: "applied pinned",
		"pack apply " + strings.Replace(from, "yardr-packs", "other", 1) + " --sha256 " + pin: "is not in the packs repository",
		"pack apply " + from + " --sha256 " + strings.Repeat("a", 64):                         "is not the one pinned",
		"pack apply " + from + " --sha256 " + pin + " --yes":                                  "no other flag, not --yes",
		"pack apply " + from + " --sha256 " + pin + " --auth packs":                           "no other flag, not --auth",
		"pack apply " + origin + "pushover/roles/ --sha256 " + pin:                            "want one pack's name",
		// A plan that still asks refuses whole, as an embedded one does.
		"pack apply " + origin + "starter/ --sha256 sha256:" + starterPlan.Digest + " --depot web": "its plan asks",
	} {
		if out, err := asRepository(strings.Fields(words)...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %q, %v; want %q", words, out, err, want)
		}
	}
	if _, err := st.GetSource(ctx, "pushover"); err == nil {
		t.Fatal("a refused repository apply added the source")
	}
	if _, err := st.Delegation(ctx, id+1, "jo"); err == nil {
		t.Fatal("a refused repository apply left a record")
	}
	if out, err := asRepository("pack", "apply", strings.TrimSuffix(from, "/"), "--sha256", plan.Digest); err != nil || !strings.Contains(out, "pushover") {
		t.Fatalf("pushover from the repository: %q, %v", out, err)
	}
	if _, err := st.GetSource(ctx, "pushover"); err != nil {
		t.Fatalf("pushover was not applied: %v", err)
	}
	rec, err = st.Delegation(ctx, id+1, "jo")
	if err != nil || rec.State != "done" || rec.DelegatedBody.From != from || rec.DelegatedBody.Digest != plan.Digest || rec.DelegatedBody.Prior != "" {
		t.Fatalf("record %d = %+v, %v; want pushover done from %s", id+1, rec, err, from)
	}
	for _, want := range []string{"from: " + from, "prior digest: none known", "pack pushover, digest " + pin, "state: done"} {
		if show := mustAs(t, st, "jo", "delegation", "show", strconv.FormatUint(id+1, 10)); !strings.Contains(show, want) {
			t.Errorf("delegation show lacks %q:\n%s", want, show)
		}
	}
}

// serveRepositoryPacks serves the packs yardr embeds under names as packs of
// a stand-in packs repository, at <origin><name>/, and answers the origin.
func serveRepositoryPacks(t *testing.T, names ...string) string {
	t.Helper()
	mux := http.NewServeMux()
	for _, name := range names {
		dir := t.TempDir()
		sub, err := fs.Sub(packs.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.CopyFS(dir, sub); err != nil {
			t.Fatal(err)
		}
		prefix := "/yardr-packs/main/" + name + "/"
		mux.Handle(prefix, http.StripPrefix(prefix, http.FileServer(http.Dir(dir))))
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/yardr-packs/main/"
}
