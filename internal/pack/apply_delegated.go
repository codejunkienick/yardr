package pack

// A delegated pack apply (design yardr-m90n 3.4): where the call carries
// a delegated run (store.DelegationOf), Apply records the import in the
// run's delegated record around its own writes, since no one store
// transaction spans them. The record is written "started" before the
// first object, with the pack's digest, its objects by name, each updated
// object's diff and the complete before-state read from what stands; it
// is rewritten "done" or "failed" at the end, a failure naming the
// objects written before it. A record that would pass the blob cap
// refuses before anything writes (store.StartDelegatedPack). The
// partial import stays either way, and applying again finishes it. A
// pack of the packs repository (Plan.Dir a URL) is the same run, with the
// URL beside its digest and, in the record, the digest that stood before.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"yardr.dev/yardr/internal/store"
)

// delegatedRun is one delegated apply's record in flight: the store its
// writes ride, the record's id, and the objects written so far. A nil
// delegatedRun is an ordinary apply: every method on it does nothing.
type delegatedRun struct {
	st      store.API
	id      uint64
	actor   string
	ctx     context.Context
	written []string
}

// beginDelegated starts the record of a delegated apply, after check has
// passed and before anything writes: a plan check refuses, a plan that
// asks included, leaves no record, as an ordinary apply leaves none. It
// answers nil where the call carries no delegated run.
func (p *Plan) beginDelegated(ctx context.Context, st store.API, o ApplyOptions, ck *checked) (*delegatedRun, error) {
	if _, ok := store.DelegationOf(ctx); !ok {
		return nil, nil
	}
	objects, err := p.delegatedObjects(ctx, st, o, ck)
	if err != nil {
		return nil, err
	}
	mappings := o.DelegatedMappings
	if mappings.IsZero() {
		mappings = store.Mappings{Flow: p.Flow, Crew: p.Crew, Groups: p.Groups}
	}
	// A pack read from a URL is the packs repository's, and the store
	// fetches it again itself to hold the run to what the origin serves:
	// the URL is told, not trusted (store.PackFetcher).
	from := ""
	if IsURL(p.Dir) {
		from = p.Dir
	}
	// The source this command read the pack from is also what its record of
	// the import will name (ImportRecord). A pack yardr embeds is read under
	// a label that names this build, and the store holds the record to its
	// own reading's: told here, the start refuses a command that is not the
	// server's build before anything writes, not at the record's write.
	id, err := st.StartDelegatedPack(ctx, store.DelegatedPackStart{Pack: p.Pack, Depot: p.Depot, Digest: p.Digest, From: from, Source: p.Dir,
		Mappings: mappings, Grants: delegatedGrants(o), Objects: objects}, o.Actor)
	if err != nil {
		return nil, err
	}
	return &delegatedRun{st: st, id: id, actor: o.Actor, ctx: store.WithDelegatedPack(ctx, id)}, nil
}

// delegatedGrants are the grants the import's answers say yes to, in
// the record's words (store.Delegation.Grants): the store lets a grant
// of the import through only where one of these names it.
func delegatedGrants(o ApplyOptions) []string {
	var out []string
	for crew, sources := range o.OpenThreads {
		for src, yes := range sources {
			if yes {
				out = append(out, "threads "+crew+" "+src)
			}
		}
	}
	for who, calls := range o.Calls {
		for key, yes := range calls {
			if yes {
				out = append(out, "call "+who+" "+key)
			}
		}
	}
	for crew, peers := range o.Expose {
		for peer, yes := range peers {
			if yes {
				out = append(out, "expose "+crew+" "+peer)
			}
		}
	}
	slices.Sort(out)
	return out
}

// context is the context the import's writes ride: the started record's,
// or ctx itself on an ordinary apply.
func (r *delegatedRun) context(ctx context.Context) context.Context {
	if r == nil {
		return ctx
	}
	return r.ctx
}

// wrote records one written object of the import.
func (r *delegatedRun) wrote(o Object) {
	if r != nil {
		r.written = append(r.written, o.Kind+" "+o.Name)
	}
}

// finish rewrites the record done, or failed with what the import wrote
// before err: a failure after RecordPack, folding a declared source's
// records in among them, keeps the import and still fails the record.
func (r *delegatedRun) finish(ctx context.Context, err error) error {
	if r == nil {
		return nil
	}
	text := ""
	if err != nil {
		text = err.Error()
	}
	return r.st.FinishDelegatedPack(ctx, r.id, err == nil, r.written, text, r.actor)
}

// delegatedObjects are the plan's objects as the record holds them: each
// by name with its action, and each the import would create or change
// with its diff and the complete before-state read from what stands.
func (p *Plan) delegatedObjects(ctx context.Context, st store.API, o ApplyOptions, ck *checked) ([]store.DelegatedPackObject, error) {
	out := make([]store.DelegatedPackObject, 0, len(p.Objects))
	for i := range p.Objects {
		obj := &p.Objects[i]
		rec := store.DelegatedPackObject{Kind: obj.Kind, Name: obj.Name, Action: obj.Action}
		if (obj.Action == Create || obj.Action == Change) && !obj.refused(o) {
			diff, before, had, err := p.delegatedBefore(ctx, st, obj, ck)
			if err != nil {
				return nil, err
			}
			rec.Diff, rec.Before, rec.Had = diff, before, had
		}
		out = append(out, rec)
	}
	return out, nil
}

// delegatedBefore is one updated object's diff and complete before-state:
// a file's old text against the text the import installs, and any other
// object's changed fields, with the stored object whole as JSON, so an
// operator sets it back without opening anything else. A created object
// had nothing standing. A file whose old or new bytes are not text cannot
// be held in the record, and refuses before anything writes.
func (p *Plan) delegatedBefore(ctx context.Context, st store.API, obj *Object, ck *checked) (diff, before string, had bool, err error) {
	switch obj.Kind {
	case "file":
		var old []byte
		if data, rerr := os.ReadFile(obj.File.To); rerr == nil {
			old, had = data, true
		} else if !errors.Is(rerr, os.ErrNotExist) {
			return "", "", false, rerr
		}
		text := ck.files[obj.File.To]
		if had && !utf8.Valid(old) || !utf8.Valid(text) {
			return "", "", false, fmt.Errorf("pack %s: file %s is not text, so a delegated record cannot hold what stood: this pack is the operator's to type in a terminal", p.Pack, obj.Name)
		}
		return store.Unified(string(old), string(text)), string(old), had, nil
	case "group":
		g, gerr := st.GetGroup(ctx, obj.Name)
		if errors.Is(gerr, store.ErrNotFound) {
			return changesDiff(obj.Changes), "", false, nil
		}
		return storedBefore(g, gerr, obj)
	case "crew":
		r, gerr := st.GetCrew(ctx, obj.Name)
		if errors.Is(gerr, store.ErrNotFound) {
			return changesDiff(obj.Changes), "", false, nil
		}
		return storedBefore(r, gerr, obj)
	case "flow":
		f, gerr := st.GetFlow(ctx, obj.Name)
		if errors.Is(gerr, store.ErrNotFound) {
			return changesDiff(obj.Changes), "", false, nil
		}
		return storedBefore(f, gerr, obj)
	case "source":
		s, gerr := st.GetSource(ctx, obj.Name)
		if errors.Is(gerr, store.ErrNotFound) {
			return changesDiff(obj.Changes), "", false, nil
		}
		return storedBefore(s, gerr, obj)
	case "map":
		h, gerr := st.GetHook(ctx, obj.Map.Hook)
		if errors.Is(gerr, store.ErrNotFound) {
			return changesDiff(obj.Changes), "", false, nil
		}
		return storedBefore(h, gerr, obj)
	default:
		old := map[string]any{}
		for _, c := range obj.Changes {
			old[c.Field] = c.Old
		}
		raw, merr := json.Marshal(old)
		if merr != nil {
			return "", "", false, merr
		}
		return changesDiff(obj.Changes), string(raw), obj.Action == Change, nil
	}
}

// storedBefore is a stored object's diff and before-state: its changed
// fields, and the object whole as JSON.
func storedBefore(v any, gerr error, obj *Object) (string, string, bool, error) {
	if gerr != nil {
		return "", "", false, gerr
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", "", false, err
	}
	return changesDiff(obj.Changes), string(raw), true, nil
}

// changesDiff is an object's changed fields as the pack card prints them.
func changesDiff(changes []Diff) string {
	parts := make([]string, len(changes))
	for i, c := range changes {
		parts[i] = fmt.Sprintf("%s: %v -> %v", c.Field, c.Old, c.New)
	}
	return strings.Join(parts, "; ")
}
