package pack_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/storetest"
)

// delegatedPackYard is a yard with delegation on (newDelegationYard's
// recipe), depot web, and the delegated context a yardmaster's pack
// apply runs under, citing jo's mail. It also answers the setter for
// the plan the store's reading of the pack is held to: a test sets the
// plan it built, as the server's reading derives the plan of the pack
// yardr embeds, before it applies it.
func delegatedPackYard(t *testing.T) (store.API, pack.Options, context.Context, func(*pack.Plan)) {
	t.Helper()
	return delegatedPackYardAt(t, "")
}

// delegatedPackYardAt is delegatedPackYard for a pack of a stand-in packs
// repository at origin: the store has the fetching of that repository
// registered as the server registers its own (the real LoadRepository
// holds the pack to its URL and its digest) and no reading of an embedded
// pack, so a write that read the pack again inside the store's write
// would find none. An empty origin is a yard whose pack is an embedded
// one's reading.
func delegatedPackYardAt(t *testing.T, origin string) (store.API, pack.Options, context.Context, func(*pack.Plan)) {
	t.Helper()
	st, own := storetest.NewOwned(t)
	var current *pack.Plan
	reading := func(_ context.Context, name string, depot string, _ store.Mappings, _ store.API) (*store.PackReading, error) {
		if current == nil || current.Pack != name {
			return nil, errors.New("pack " + name + ": yardr embeds no pack of that name")
		}
		r := &store.PackReading{Name: name, Kind: current.Kind, Depot: depot, Digest: current.Digest, Plan: &store.AskPlan{}, Targets: map[string]string{}}
		if origin == "" {
			r.Label = "embedded in yardr test"
		}
		for _, o := range current.Objects {
			ao := store.AskPlanObject{Kind: o.Kind, Name: o.Name, Action: o.Action}
			for _, c := range o.Changes {
				ao.Changes = append(ao.Changes, store.AskChange{Field: c.Field, Old: oString(c.Old), New: oString(c.New)})
			}
			r.Plan.Objects = append(r.Plan.Objects, ao)
			if o.Kind == "source" && o.Source != nil {
				r.Sources = append(r.Sources, store.AskSource{Name: o.Source.Name, Text: o.Source.Text})
			}
			if o.Kind == "subscription" && (o.Action == pack.Create || o.Action == pack.Change) {
				target, err := json.Marshal(o.Subscription)
				if err != nil {
					return nil, err
				}
				r.Targets[o.Kind+"\x00"+o.Name] = string(target)
			}
		}
		return r, nil
	}
	if origin == "" {
		own.SetPackReader(reading)
	} else {
		own.SetPackFetcher(func(_ context.Context, from, digest string) (store.PackReader, error) {
			if _, err := pack.LoadRepository(origin, from, digest); err != nil {
				return nil, err
			}
			return func(ctx context.Context, name string, depot string, m store.Mappings, yard store.API) (*store.PackReading, error) {
				r, err := reading(ctx, name, depot, m, yard)
				if err != nil {
					return nil, err
				}
				for _, src := range r.Sources {
					r.Targets["source\x00"+src.Name] = store.TextSum(src.Text)
				}
				return r, nil
			}, nil
		})
	}
	if err := st.SetSetting(ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddUser(ctx, "ada", "jo"); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"yardmaster", "clerk"} {
		if _, err := st.PutCrew(ctx, store.CrewMember{Name: r, Runner: "herdr"}, "jo"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PutDepot(ctx, store.Depot{Name: "web", Base: "main"}, "jo"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, store.DelegationSetting, "true", "jo"); err != nil {
		t.Fatal(err)
	}
	if err := st.Mail(ctx, "yardmaster", "apply the pack for me", "jo"); err != nil {
		t.Fatal(err)
	}
	mail, err := st.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dctx := store.WithDelegation(ctx, store.DelegatedCall{For: "jo", Mail: mail, Command: "yardr pack apply starter --depot web"})
	return st, pack.Options{Depot: "web", Home: t.TempDir()}, dctx, func(p *pack.Plan) { current = p }
}

// oString is a plan change's value as a card prints it.
func oString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// packModes are the ways a delegated apply takes its pack: an embedded pack
// the yard's binary holds, or a stand-in packs repository serving a pack
// pinned as the command loads it.
var packModes = []string{"embedded", "repository"}

// delegatedYardFrom is a delegated yard and the pack in dir taken the way
// mode says: the loader answers the pack for the options the plan is built
// with, pinned to the digest the directory has where the pack is the
// repository's.
func delegatedYardFrom(t *testing.T, mode, name, dir string) (store.API, pack.Options, context.Context, func(*pack.Plan), func(pack.Options) *pack.Pack) {
	t.Helper()
	origin := ""
	if mode == "repository" {
		origin, _ = serveRepository(t, name, dir)
	}
	st, o, dctx, setPlan := delegatedPackYardAt(t, origin)
	return st, o, dctx, setPlan, func(o pack.Options) *pack.Pack {
		t.Helper()
		if origin == "" {
			p := load(t, dir)
			p.Dir = "embedded in yardr test"
			return p
		}
		p, err := pack.LoadRepository(origin, origin+name+"/", build(t, st, load(t, dir), o).Digest)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
}

// repositoryRun is a delegated apply of a pack of the stand-in repository,
// pinned to digest: the pack as the command loads it, planned against the
// yard and applied under the delegated run.
func repositoryRun(t *testing.T, st store.API, o pack.Options, dctx context.Context, setPlan func(*pack.Plan), origin, name, digest string) (*pack.Plan, error) {
	t.Helper()
	p, err := pack.LoadRepository(origin, origin+name+"/", digest)
	if err != nil {
		return nil, err
	}
	plan := build(t, st, p, o)
	setPlan(plan)
	_, err = pack.Apply(dctx, st, plan, pack.ApplyOptions{Answers: answered(t, plan, answers), Actor: "yardmaster"})
	return plan, err
}

// withLinearAs declares the pack's linear source in the yard as jo:
// withLinear's actor is the operator of its own yards, and this one's
// operator is jo.
func withLinearAs(t *testing.T, st store.API) {
	t.Helper()
	if _, err := st.PutSource(ctx, "linear.yaml", linearDecl, "jo"); err != nil {
		t.Fatal(err)
	}
}

// latestDelegated is the newest delegated record as jo reads it.
func latestDelegated(t *testing.T, st store.API) *store.Delegation {
	t.Helper()
	for id := uint64(64); id > 0; id-- {
		if d, err := st.Delegation(ctx, id, "jo"); err == nil {
			return d
		}
	}
	t.Fatal("no delegated record")
	return nil
}

// packTelling is the newest mail the yard sent jo.
func packTelling(t *testing.T, st store.API) string {
	t.Helper()
	evs, err := st.Events(ctx, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	for _, e := range evs {
		if e.Kind == "mail" && e.Actor == "yard" && store.MailReceiver(e) == "jo" {
			text, _ = e.Data["text"].(string)
		}
	}
	return text
}

// delegatedBody is a record's body, from its blob where it stands there.
func delegatedBody(t *testing.T, st store.API, rec *store.Delegation) store.DelegatedBody {
	t.Helper()
	if rec.Blob == nil {
		return rec.DelegatedBody
	}
	body, err := st.DelegationBlob(ctx, rec.ID, "jo")
	if err != nil {
		t.Fatal(err)
	}
	return *body
}

// A delegated apply past 4 KiB of before-state holds the complete prior
// state in its record's blob: setting a changed file and a changed group
// back from the blob restores what stood.
func TestDelegatedApplyPastInlineBeforeStateRestores(t *testing.T) {
	st, o, dctx, setPlan := delegatedPackYard(t)
	withLinearAs(t, st)
	dir := copyStarter(t)
	role := filepath.Join(dir, "roles", "builder.md")
	v1, err := os.ReadFile(role)
	if err != nil {
		t.Fatal(err)
	}
	v1 = append(v1, []byte("\n"+strings.Repeat("the first builder role. ", 300))...)
	if err := os.WriteFile(role, v1, 0o644); err != nil {
		t.Fatal(err)
	}
	applyAs(t, st, build(t, st, load(t, dir), o), answers, "jo")
	// The pack's next word changes the builders' limit, as a pack upgrade
	// would: the group as it stands (limit 5) is the record's before-state.
	manifest := filepath.Join(dir, "pack.yaml")
	text, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(strings.Replace(string(text), "limit: 5", "limit: 2", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	v2 := append([]byte("the second builder role\n"), []byte(strings.Repeat("changed. ", 100))...)
	if err := os.WriteFile(role, v2, 0o644); err != nil {
		t.Fatal(err)
	}

	p := load(t, dir)
	p.Dir = "embedded in yardr test"
	plan := build(t, st, p, o)
	setPlan(plan)
	installed := find(t, plan, "file", "roles/builder.md").File.To
	// What stands is the installed text, with the pack's names filled in:
	// that whole text is the before-state the record must hold.
	stood, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	res, err := pack.Apply(dctx, st, plan, pack.ApplyOptions{Answers: answered(t, plan, answers), Actor: "yardmaster"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Recorded || res.Changed == 0 {
		t.Fatalf("delegated import = %+v", res)
	}
	rec := latestDelegated(t, st)
	if rec.State != "done" || rec.Blob == nil {
		t.Fatalf("record = state %q blob %v, want done with the before-state in a blob", rec.State, rec.Blob)
	}
	body := delegatedBody(t, st, rec)
	if body.Pack != "starter" || body.Digest != plan.Digest {
		t.Errorf("record holds pack %q digest %q, want starter %q", body.Pack, body.Digest, plan.Digest)
	}
	var fileObj, groupObj *store.DelegatedPackObject
	for i := range body.Objects {
		switch body.Objects[i].Kind + " " + body.Objects[i].Name {
		case "file roles/builder.md":
			fileObj = &body.Objects[i]
		case "group web-builders":
			groupObj = &body.Objects[i]
		}
	}
	if fileObj == nil || !fileObj.Had || fileObj.Before != string(stood) || fileObj.Diff == "" {
		t.Fatalf("file object in the record holds %d before bytes, want its diff and the complete %d bytes that stood", len(fileObj.Before), len(stood))
	}
	if groupObj == nil || !groupObj.Had || !strings.Contains(groupObj.Before, `"limit":5`) {
		t.Fatalf("group object in the record = %+v, want the group as it stood", groupObj)
	}
	if _, err := st.Delegation(ctx, rec.ID, "ada"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("ada reads the record: %v", err)
	}
	for _, want := range []string{"state: started", "state: done"} {
		if !strings.Contains(packTelling(t, st), want) && want == "state: done" {
			t.Errorf("the telling lacks %q", want)
		}
	}

	// Setting each object back from the blob restores what stood.
	if data, err := os.ReadFile(installed); err != nil || string(data) == string(stood) {
		t.Fatalf("installed role after the apply = %d bytes, %v; want the second text installed", len(data), err)
	}
	if err := os.WriteFile(installed, []byte(fileObj.Before), 0o644); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(installed); err != nil || string(data) != string(stood) {
		t.Errorf("restored role = %d bytes, %v; want what stood", len(data), err)
	}
	var g store.Group
	if err := json.Unmarshal([]byte(groupObj.Before), &g); err != nil {
		t.Fatal(err)
	}
	if err := st.PutGroup(ctx, g, "jo"); err != nil {
		t.Fatal(err)
	}
	if got, err := st.GetGroup(ctx, "web-builders"); err != nil || got.Limit != 5 {
		t.Errorf("restored builders = %+v, %v; want limit 5 as it stood", got, err)
	}
}

// A write that fails on the second route object leaves a failed record naming
// what was written, the before-state and an outcome mail, with the
// partial import standing: applying again finishes it. A pack of the
// packs repository is the same run, its record naming where it came from.
func TestDelegatedApplySecondObjectFailureLeavesFailedRecord(t *testing.T) {
	for _, mode := range packModes {
		t.Run(mode, func(t *testing.T) {
			st, o, dctx, setPlan, pk := delegatedYardFrom(t, mode, "starter", copyStarter(t))
			withLinearAs(t, st)
			plan := build(t, st, pk(o), o)
			setPlan(plan)
			var routes []string
			for _, obj := range plan.Objects {
				if obj.Kind == "route" && (obj.Action == pack.Create || obj.Action == pack.Change) {
					routes = append(routes, obj.Name)
				}
			}
			if len(routes) < 2 {
				t.Fatalf("plan has %d route writes, want at least 2", len(routes))
			}
			_, err := pack.Apply(dctx, &failing{API: st, left: 2}, plan, pack.ApplyOptions{Answers: answered(t, plan, answers), Actor: "yardmaster"})
			if err == nil || !strings.Contains(err.Error(), "the disk is full") {
				t.Fatalf("err = %v", err)
			}
			if !strings.Contains(err.Error(), "route "+routes[1]) {
				t.Errorf("failed object = %v, want second route %s", err, routes[1])
			}
			if strings.Contains(err.Error(), "nothing is recorded") {
				t.Errorf("delegated apply failure says it left no record: %v", err)
			}
			rec := latestDelegated(t, st)
			body := delegatedBody(t, st, rec)
			if rec.State != "failed" || !strings.Contains(body.Error, "the disk is full") || len(body.Written) == 0 {
				t.Fatalf("record = state %q body %+v, want failed naming what was written", rec.State, body)
			}
			if (body.From != "") != (mode == "repository") {
				t.Errorf("record names %q as where the pack came from in mode %s", body.From, mode)
			}
			if len(body.Objects) == 0 {
				t.Error("the failed record holds no before-state")
			}
			if !strings.Contains(strings.Join(body.Written, "\n"), "route "+routes[0]) || strings.Contains(strings.Join(body.Written, "\n"), "route "+routes[1]) {
				t.Errorf("written objects = %v, want first route %s but not failed route %s", body.Written, routes[0], routes[1])
			}
			if text := packTelling(t, st); !strings.Contains(text, "state: failed") {
				t.Errorf("the telling = %q, want the failed outcome", text)
			}
			if recs, err := st.Packs(ctx); err != nil || len(recs) != 0 {
				t.Errorf("import records = %v, %v; want the partial import unrecorded", recs, err)
			}

			plan2 := build(t, st, pk(o), o)
			setPlan(plan2)
			res, err := pack.Apply(dctx, st, plan2, pack.ApplyOptions{Answers: answered(t, plan2, answers), Actor: "yardmaster"})
			if err != nil {
				t.Fatal(err)
			}
			if !res.Recorded {
				t.Fatalf("applying again = %+v", res)
			}
			if rec := latestDelegated(t, st); rec.State != "done" {
				t.Errorf("after applying again the newest record is %q, want done", rec.State)
			}
			if groups, err := st.Groups(ctx); err != nil || len(groups) != 6 {
				t.Errorf("groups = %v, %v; want the finished import's 6", groups, err)
			}
		})
	}
}

// A fold failure after RecordPack also leaves a failed record and a
// mail, and keeps the partial import, for a pack of the packs repository
// as for an embedded pack. The repository's source is written from its
// text's hash on the record: the store has no reading of the pack to read
// the text from inside the write.
func TestDelegatedApplyFoldFailureLeavesFailedRecord(t *testing.T) {
	for _, mode := range packModes {
		t.Run(mode, func(t *testing.T) {
			dir := writePack(t, sourcePackFiles())
			st, _, dctx, setPlan, pk := delegatedYardFrom(t, mode, "acme-pack", dir)
			if err := st.SetIdentity(ctx, "yardmaster", "acme", store.Identity{ID: "u-one"}, "jo"); err != nil {
				t.Fatal(err)
			}
			if err := st.SetIdentity(ctx, "yardmaster", "acme-declared", store.Identity{ID: "u-two"}, "jo"); err != nil {
				t.Fatal(err)
			}
			o := pack.Options{Home: t.TempDir()}
			plan := build(t, st, pk(o), o)
			setPlan(plan)
			_, err := pack.Apply(dctx, st, plan, pack.ApplyOptions{Actor: "yardmaster"})
			if err == nil || !strings.Contains(err.Error(), "its records were not folded in") {
				t.Fatalf("err = %v", err)
			}
			rec := latestDelegated(t, st)
			if rec.State != "failed" {
				t.Errorf("record state = %q, want failed", rec.State)
			}
			if text := packTelling(t, st); !strings.Contains(text, "state: failed") {
				t.Errorf("the telling = %q, want the failed outcome", text)
			}
			if _, err := st.GetSource(ctx, "acme"); err != nil {
				t.Errorf("the source was not kept: %v", err)
			}
			if recs, err := st.Packs(ctx); err != nil || len(recs) != 1 {
				t.Errorf("import records = %v, %v; want the import kept and recorded", recs, err)
			}
		})
	}
}

// A plan that asks refuses a delegated apply whole: no record, and
// nothing written. A record that would pass the blob cap refuses the
// same way, as a pack of the packs repository does.
func TestDelegatedApplyAskingPlanAndOverCapRefuse(t *testing.T) {
	for _, mode := range packModes {
		t.Run(mode, func(t *testing.T) {
			st, o, dctx, setPlan, pk := delegatedYardFrom(t, mode, "starter", copyStarter(t))
			withLinearAs(t, st)
			plan := build(t, st, pk(o), o)
			setPlan(plan)
			if _, err := pack.Apply(dctx, st, plan, pack.ApplyOptions{Actor: "yardmaster"}); err == nil || !strings.Contains(err.Error(), "has no answer") {
				t.Fatalf("an asking plan's err = %v", err)
			}
			if _, err := st.Delegation(ctx, 1, "jo"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("an asking plan left a record: %v", err)
			}
			if groups, err := st.Groups(ctx); err != nil || len(groups) != 0 {
				t.Errorf("groups = %v, %v; want nothing written", groups, err)
			}

			if err := st.SetSetting(ctx, store.BlobMaxSetting, "128", "jo"); err != nil {
				t.Fatal(err)
			}
			if _, err := pack.Apply(dctx, st, plan, pack.ApplyOptions{Answers: answered(t, plan, answers), Actor: "yardmaster"}); err == nil || !strings.Contains(err.Error(), "blob cap") {
				t.Fatalf("an over-cap apply's err = %v", err)
			}
			if _, err := st.Delegation(ctx, 1, "jo"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("an over-cap apply left a record: %v", err)
			}
			if groups, err := st.Groups(ctx); err != nil || len(groups) != 0 {
				t.Errorf("groups = %v, %v; want nothing written", groups, err)
			}
		})
	}
}

// A pack of the packs repository applied pinned records the URL it came
// from and the digest that stood before it, its before-state past 4 KiB in
// the record's blob as an embedded pack's is; the next apply, pinned to
// that prior digest, restores what stood, where the origin still serves
// those bytes; and a pin on bytes the origin no longer serves refuses before
// anything is written.
func TestDelegatedRepositoryApplyRestoresThePriorDigest(t *testing.T) {
	dir := copyStarter(t)
	origin, _ := serveRepository(t, "starter", dir)
	from := origin + "starter/"
	st, o, dctx, setPlan := delegatedPackYardAt(t, origin)
	withLinearAs(t, st)
	role := filepath.Join(dir, "roles", "builder.md")
	manifest := filepath.Join(dir, "pack.yaml")
	pin := func() string { return build(t, st, load(t, dir), o).Digest }
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) []byte {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	manifest1, role0 := read(manifest), read(role)

	// The first install: nothing stood before it, and the record says so.
	role1 := append(role0, []byte("\n"+strings.Repeat("the first builder role. ", 300))...)
	write(role, role1)
	digest1 := pin()
	plan1, err := repositoryRun(t, st, o, dctx, setPlan, origin, "starter", digest1)
	if err != nil {
		t.Fatal(err)
	}
	installed := find(t, plan1, "file", "roles/builder.md").File.To
	stood := read(installed)
	rec1 := latestDelegated(t, st)
	body1 := delegatedBody(t, st, rec1)
	if rec1.State != "done" || body1.From != from || body1.Digest != digest1 || body1.Prior != "" || !strings.Contains(body1.Back, "no earlier delegated run") {
		t.Fatalf("first record = state %q, %+v; want done from %s with no prior digest", rec1.State, body1, from)
	}

	// The pack's next word changes the builders' limit and the role: the
	// record holds the digest that stood, the complete before-state past
	// 4 KiB in its blob, and the words that take the run back.
	write(manifest, []byte(strings.Replace(string(manifest1), "limit: 5", "limit: 2", 1)))
	write(role, []byte("the second builder role\n"))
	digest2 := pin()
	if _, err := repositoryRun(t, st, o, dctx, setPlan, origin, "starter", digest2); err != nil {
		t.Fatal(err)
	}
	rec2 := latestDelegated(t, st)
	body2 := delegatedBody(t, st, rec2)
	if rec2.State != "done" || rec2.Blob == nil || body2.From != from || body2.Prior != digest1 {
		t.Fatalf("second record = state %q blob %v, %+v; want done, in a blob, over %s", rec2.State, rec2.Blob, body2, digest1)
	}
	for _, want := range []string{"yardr pack apply " + from, "--sha256 sha256:" + digest1, "--depot web"} {
		if !strings.Contains(body2.Back, want) {
			t.Errorf("the taking-back words lack %q: %s", want, body2.Back)
		}
	}
	var fileObj *store.DelegatedPackObject
	for i := range body2.Objects {
		if body2.Objects[i].Kind == "file" && body2.Objects[i].Name == "roles/builder.md" {
			fileObj = &body2.Objects[i]
		}
	}
	if fileObj == nil || !fileObj.Had || fileObj.Before != string(stood) || fileObj.Diff == "" {
		t.Fatalf("the role in the record = %+v, want its diff and the complete %d bytes that stood", fileObj, len(stood))
	}
	if text := packTelling(t, st); !strings.Contains(text, "from "+from) || !strings.Contains(text, "sha256:"+digest1) {
		t.Errorf("the telling = %q, want where it came from and the digest that stood", text)
	}
	if g, err := st.GetGroup(ctx, "web-builders"); err != nil || g.Limit != 2 {
		t.Fatalf("builders after the second apply = %+v, %v; want limit 2", g, err)
	}

	// The origin serves the second pack, so a pin on the first refuses
	// before anything is written.
	if _, err := repositoryRun(t, st, o, dctx, setPlan, origin, "starter", body2.Prior); err == nil || !strings.Contains(err.Error(), "is not the one pinned") {
		t.Fatalf("a pin on bytes the origin does not serve: err = %v", err)
	}
	if latestDelegated(t, st).ID != rec2.ID || string(read(installed)) != "the second builder role\n" {
		t.Error("a refused pin wrote a record or a file")
	}

	// The origin serves the first pack again: applying pinned to the
	// recorded prior digest restores what stood.
	write(manifest, manifest1)
	write(role, role1)
	if _, err := repositoryRun(t, st, o, dctx, setPlan, origin, "starter", body2.Prior); err != nil {
		t.Fatal(err)
	}
	if got := read(installed); string(got) != string(stood) {
		t.Errorf("restored role = %d bytes, want the %d that stood", len(got), len(stood))
	}
	if g, err := st.GetGroup(ctx, "web-builders"); err != nil || g.Limit != 5 {
		t.Errorf("restored builders = %+v, %v; want limit 5 as it stood", g, err)
	}
	if body3 := delegatedBody(t, st, latestDelegated(t, st)); body3.Digest != digest1 || body3.Prior != digest2 {
		t.Errorf("third record = %+v, want the first digest over the second", body3)
	}
}
