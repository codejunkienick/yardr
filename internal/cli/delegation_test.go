package cli

import (
	"context"
	"io/fs"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
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
	if _, err := as(t, st, "yardmaster", "hook", "add", "delegated", "--source", "token", "--secret-file", t.TempDir(), "--for", "jo", "--mail", mail); err == nil || !strings.Contains(err.Error(), "--secret-file is the operator's") {
		t.Errorf("explicit secret file in a delegated hook add: %v", err)
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
	own.SetPackReader(func(ctx context.Context, name string, depot string, _ store.Mappings, yard store.API) (*store.PackReading, error) {
		switch name {
		case "telegram":
			sub, err := fs.Sub(packs.FS, "telegram")
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
			text, err := fs.ReadFile(packs.FS, "telegram/telegram.yaml")
			if err != nil {
				return nil, err
			}
			askPlan := &store.AskPlan{}
			for _, o := range plan.Objects {
				askPlan.Objects = append(askPlan.Objects, store.AskPlanObject{Kind: o.Kind, Name: o.Name, Action: o.Action})
			}
			return &store.PackReading{Name: "telegram", Kind: "source", Digest: plan.Digest, Plan: askPlan,
				Sources: []store.AskSource{{Name: "telegram", Text: string(text)}}}, nil
		case "starter":
			return &store.PackReading{Name: "starter", Kind: "git", Depot: depot, Digest: "abc", Plan: &store.AskPlan{}}, nil
		}
		return nil, store.ErrNotFound
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
}
