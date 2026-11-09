// A delegated pack apply (design yardr-m90n 3.2 and 3.4): the one
// delegated run whose writes no one transaction spans. Its record is
// written "started" before the first object and rewritten "done" or
// "failed" at the end, in the bucket of the other delegated records
// (delegation.go), with the pack's digest, its plan's objects by name,
// each updated object's diff and the complete before-state, inline up to
// MaxDelegationInline and in one blob past it. A record that would pass
// the blob cap refuses before anything writes. The import's own writes
// ride the record (WithDelegatedPack, packDelegated): the leg holds
// them to the objects the started record names.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

type delegatedPackKey struct{}

// WithDelegatedPack records on ctx the started record a delegated pack
// apply writes under: pack.Apply sets it from StartDelegatedPack's answer
// for the import's own calls, and the store holds those calls to the
// record (packDelegated). It never stands alone: the call still carries
// its DelegatedCall (WithDelegation), which the record is checked
// against in every write.
func WithDelegatedPack(ctx context.Context, id uint64) context.Context {
	return context.WithValue(ctx, delegatedPackKey{}, id)
}

// DelegatedPackOf is the started record id the call writes under
// (WithDelegatedPack), ok false for every other call.
func DelegatedPackOf(ctx context.Context) (uint64, bool) {
	id, ok := ctx.Value(delegatedPackKey{}).(uint64)
	return id, ok && id != 0
}

// DelegatedPackStart is what a delegated pack apply records before its
// first object: the pack, the depot it goes into ("" for a pack of kind
// source, into the yard), its digest, and the plan's objects with each
// updated object's diff and complete before-state, as pack.Apply read
// them from what stands. The mappings and grants are the import's, as
// its answers gave them. None of it is taken on the caller's word: the
// start holds the objects and the digest to the store's own reading of
// the pack the binary embeds (packReaderOf, as a pack card is read),
// and each grant to a crew member or group that reading's plan names. A
// pack of the packs repository names its URL in From and is held to what
// the origin serves at its digest instead (delegation_repo.go).
type DelegatedPackStart struct {
	Pack     string                `json:"pack"`
	Depot    string                `json:"depot,omitempty"`
	Digest   string                `json:"digest"`
	From     string                `json:"from,omitempty"` // the packs repository's URL for the pack; none for a pack yardr embeds
	Mappings Mappings              `json:"mappings,omitzero"`
	Grants   []string              `json:"grants,omitempty"`
	Objects  []DelegatedPackObject `json:"objects,omitempty"`
	// held is the record the reading says the run's import leaves
	// (heldImport), which delegatedPackPlan sets and the record keeps. It
	// is unexported: no caller names it.
	held *PackImport
}

// delegatedPackTTL is how long a started delegated pack record carries
// its import's writes: one apply's run, not a standing door. A record
// left started past it — its process died, or its yardmaster kept it —
// lets nothing more through, and still reads as unfinished.
var delegatedPackTTL = time.Hour

// StartDelegatedPack writes the delegated record of a pack apply before
// its first object, with state "started", a pointer on the yardmaster's
// journal and a mail to the operator saying the run has started. The
// call must carry the run's delegation (WithDelegation), held as every
// delegated run is (delegatedIn), and pass ActApplyPack on the delegated
// leg: the card's bounds in runask_pack.go bind cards, not this leg. The
// one bound that replaces them is the blob cap: a record whose
// before-state and diffs together would pass it refuses before anything
// writes. It answers the record's id, which the import writes under
// (WithDelegatedPack) and FinishDelegatedPack completes. A pack of the
// packs repository (start.From) is fetched first, outside the write
// (packRepoReader), and the write holds the run to that reading.
func (s *Store) StartDelegatedPack(ctx context.Context, start DelegatedPackStart, actor string) (uint64, error) {
	repo, err := s.packRepoReader(ctx, start, actor)
	if err != nil {
		return 0, err
	}
	var id uint64
	err = s.write(func(w *wtx) error {
		d, err := w.delegated(ctx, actor)
		if err != nil {
			return err
		}
		if d == nil {
			return fmt.Errorf("pack %s: starting a delegated pack record takes a delegated run (--for <operator> --mail <seq>)", start.Pack)
		}
		if err := w.may(ctx, actor, ActApplyPack, Object{Kind: KindPack, Name: start.Pack, Delegate: true}); err != nil {
			return err
		}
		objects, grants, err := w.delegatedPackPlan(ctx, &start, repo)
		if err != nil {
			return err
		}
		start.Objects, start.Grants = objects, grants
		ym, err := yardmasterIn(w.Tx)
		if err != nil {
			return err
		}
		of := "pack " + start.Pack
		if start.Depot != "" {
			of += " in depot " + start.Depot
		}
		prior, back, err := w.packBack(start, of)
		if err != nil {
			return err
		}
		body := DelegatedBody{Command: d.Command, Pack: start.Pack, Digest: start.Digest, From: start.From, Prior: prior, Objects: start.Objects, Back: back}
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		limit, err := bytesSetting(w.Tx, BlobMaxSetting, DefaultBlobMax)
		if err != nil {
			return err
		}
		if int64(len(raw)) > limit {
			return fmt.Errorf("pack %s: its record of before-state and diffs is %d bytes, over the yard's blob cap of %d (%s): this pack is the operator's to type in a terminal", start.Pack, len(raw), limit, BlobMaxSetting)
		}
		rec := Delegation{Yardmaster: ym, For: d.For, At: now(), Mail: d.Mail, Of: of, State: "started", Grants: start.Grants, Mappings: start.Mappings, Import: start.held, DelegatedBody: body}
		for _, o := range start.Objects {
			rec.Objects = append(rec.Objects, o.Kind+" "+o.Name)
			rec.PackObjects = append(rec.PackObjects, DelegatedPackObject{Kind: o.Kind, Name: o.Name, Action: o.Action, Fields: o.Fields, Changes: o.Changes, Target: o.Target})
		}
		if len(raw) > MaxDelegationInline {
			if rec.Blob, err = w.keepDelegationBlob(raw); err != nil {
				return err
			}
			rec.DelegatedBody = DelegatedBody{}
		}
		if rec.ID, err = w.Bucket(bDelegations).NextSequence(); err != nil {
			return err
		}
		if err := put(w.Tx, bDelegations, seqKey(rec.ID), rec); err != nil {
			return err
		}
		id = rec.ID
		if err := w.delegationPointer(ym, d, rec.ID); err != nil {
			return err
		}
		return w.event("", "mail", "yard", map[string]any{"to": d.For, "to_user": true, "text": delegatedPackMail(rec, body)})
	})
	return id, err
}

// FinishDelegatedPack rewrites the started record id of a delegated pack
// apply to "done", or to "failed" with the objects written before the
// failure and its error, and mails the operator the outcome. The partial
// import stays either way: applying again finishes it. The call carries
// the same delegation the run started under, checked as at the start.
func (s *Store) FinishDelegatedPack(ctx context.Context, id uint64, done bool, written []string, errText string, actor string) error {
	return s.write(func(w *wtx) error {
		d, err := w.delegated(ctx, actor)
		if err != nil {
			return err
		}
		if d == nil {
			return fmt.Errorf("delegated record %d: finishing a delegated pack record takes the delegated run it started under (--for <operator> --mail <seq>)", id)
		}
		rec := new(Delegation)
		ok, err := get(w.Tx, bDelegations, seqKey(id), rec)
		if err != nil {
			return err
		}
		if !ok || rec.State != "started" || rec.Mail != d.Mail || rec.For != d.For {
			return fmt.Errorf("delegated record %d: %w", id, ErrNotFound)
		}
		body, err := w.delegationBody(rec)
		if err != nil {
			return err
		}
		body.Written = written
		rec.State = "done"
		if !done {
			rec.State = "failed"
			body.Error = errText
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		if len(raw) > MaxDelegationInline {
			if rec.Blob, err = w.keepDelegationBlob(raw); err != nil {
				return err
			}
			rec.DelegatedBody = DelegatedBody{}
		} else {
			rec.DelegatedBody, rec.Blob = body, nil
		}
		if err := put(w.Tx, bDelegations, seqKey(id), rec); err != nil {
			return err
		}
		return w.event("", "mail", "yard", map[string]any{"to": rec.For, "to_user": true, "text": delegatedPackMail(*rec, body)})
	})
}

// delegationBody is a record's body, in its blob where it stands there.
func (w *wtx) delegationBody(rec *Delegation) (DelegatedBody, error) {
	if rec.Blob == nil {
		return rec.DelegatedBody, nil
	}
	if w.store == nil {
		return DelegatedBody{}, fmt.Errorf("delegated record %d: its blob needs the store", rec.ID)
	}
	raw, err := os.ReadFile(w.store.BlobPath(rec.Blob.Hash))
	if err != nil {
		return DelegatedBody{}, &OperationalError{Err: err}
	}
	var body DelegatedBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return DelegatedBody{}, fmt.Errorf("delegated record %d: its blob: %w", rec.ID, err)
	}
	return body, nil
}

// delegationPointer leaves the yardmaster's journal its pointer to record
// id: the operator and the id alone (recordDelegated leaves the same).
func (w *wtx) delegationPointer(ym string, d *DelegatedCall, id uint64) error {
	var r CrewMember
	if ok, err := get(w.Tx, bCrew, ym, &r); err != nil {
		return err
	} else if ok && r.Journal != "" {
		pointer := fmt.Sprintf("yardmaster for %s: delegated record %d (an operator reads it: yardr delegation show %d)", d.For, id, id)
		return w.addNoteSession(r.Journal, NoteJournal, pointer, ym, "")
	}
	return nil
}

// delegatedPackPlan holds a delegated pack start to the store's own
// reading of the pack it names: the reading registered on this store
// (packReaderOf), taken inside this write as a pack card's reading is, or,
// for a pack of the packs repository, the reading its fetch answered
// before the write (repo).
// A plan that still asks or refuses — a question unanswered, a grant
// nobody mapped, a hand-edited or non-text file — refuses the start
// whole, as a card refuses it: the CLI's refusal is not the only one.
// The caller's objects must be the reading's complete object list,
// kind, name and action each, exactly once and with nothing beside
// them, and its digest the reading's where the reading carries one;
// each object then gains the fields, changes and target that reading
// says for it. The grants are derived from the run's mappings, or held
// to the reading's own accepted grants where it carries them, never
// taken on the caller's word. Anything else refuses the start whole,
// before any record writes: a caller — the yardmaster over the socket
// among them — no longer chooses which names the leg opens.
func (w *wtx) delegatedPackPlan(ctx context.Context, start *DelegatedPackStart, repo PackReader) ([]DelegatedPackObject, []string, error) {
	read := repo
	if start.From == "" {
		read = w.packReaderOf()
	}
	if read == nil {
		return nil, nil, fmt.Errorf("pack %s: no server has registered its reading of the packs yardr embeds on this store, so no delegated pack record can be written from the binary that runs the yard", start.Pack)
	}
	reading, err := read(ctx, start.Pack, start.Depot, start.Mappings, w.store)
	if err != nil {
		return nil, nil, err
	}
	if reading == nil || reading.Name != start.Pack {
		return nil, nil, fmt.Errorf("pack %s: the server's reading named another pack: nothing was written", start.Pack)
	}
	if reading.Digest == "" || start.Digest != reading.Digest {
		return nil, nil, fmt.Errorf("pack %s: its digest is sha256:%s, and the run named sha256:%s: nothing was written", start.Pack, reading.Digest, start.Digest)
	}
	if start.Depot != reading.Depot {
		return nil, nil, fmt.Errorf("pack %s: its reading is for depot %s, and the run named depot %s: nothing was written", start.Pack, reading.Depot, start.Depot)
	}
	if reading.Plan != nil {
		var asks []string
		for _, lines := range [][]string{reading.Plan.Missing, reading.Plan.Asks, reading.Plan.Refused, reading.Plan.Edited, reading.Plan.NonText} {
			asks = append(asks, lines...)
		}
		if len(asks) > 0 {
			return nil, nil, fmt.Errorf("pack %s: its plan asks or refuses, so a delegated run cannot apply it: %s: nothing was written", start.Pack, strings.Join(asks, "; "))
		}
	}
	type object struct {
		kind, name, action string
		fields             []string
		changes            []AskChange
		target             string
	}
	var derived []object
	if reading.Plan != nil {
		for _, o := range reading.Plan.Objects {
			var fields []string
			for _, c := range o.Changes {
				fields = append(fields, c.Field)
			}
			derived = append(derived, object{o.Kind, o.Name, o.Action, fields, o.Changes, reading.Targets[o.Kind+"\x00"+o.Name]})
		}
	} else {
		named := map[string]bool{}
		for _, s := range reading.Sources {
			named[s.Name] = true
			action := "create"
			var old Source
			if ok, err := get(w.Tx, bSources, s.Name, &old); err != nil {
				return nil, nil, err
			} else if ok {
				action = "change"
				if old.Text == s.Text {
					action = "unchanged"
				}
			}
			derived = append(derived, object{"source", s.Name, action, nil, nil, reading.Targets["source\x00"+s.Name]})
		}
		// A source the pack no longer declares stands dropped where the
		// pack's last import recorded it: the plan lists it so, and the
		// record may name it — for its audit, opening no removal.
		var imp PackImport
		if _, err := get(w.Tx, bPacks, join(start.Depot, start.Pack), &imp); err != nil {
			return nil, nil, err
		}
		for _, po := range imp.Objects {
			if po.Kind == "source" && !named[po.Name] {
				derived = append(derived, object{"source", po.Name, "dropped", nil, nil, ""})
			}
		}
	}
	byKey := map[string]object{}
	planNames := map[string]bool{}
	for _, o := range derived {
		byKey[o.kind+"\x00"+o.name+"\x00"+o.action] = o
		planNames[o.kind+" "+o.name] = true
	}
	seen := map[string]bool{}
	objects := make([]DelegatedPackObject, 0, len(start.Objects))
	for _, o := range start.Objects {
		key := o.Kind + "\x00" + o.Name + "\x00" + o.Action
		d, ok := byKey[key]
		if !ok {
			return nil, nil, fmt.Errorf("pack %s: its plan has no object %s %s (%s) to write, and a delegated record holds only what the pack yardr embeds would write: nothing was written", start.Pack, o.Kind, o.Name, o.Action)
		}
		if seen[key] {
			return nil, nil, fmt.Errorf("pack %s: its plan names object %s %s (%s) once, and the run's record names it twice: nothing was written", start.Pack, o.Kind, o.Name, o.Action)
		}
		seen[key] = true
		o.Fields, o.Changes, o.Target = d.fields, d.changes, d.target
		objects = append(objects, o)
	}
	if len(seen) != len(byKey) {
		missing := make([]string, 0, len(byKey)-len(seen))
		for key, o := range byKey {
			if !seen[key] {
				missing = append(missing, o.kind+" "+o.name+" ("+o.action+")")
			}
		}
		slices.Sort(missing)
		return nil, nil, fmt.Errorf("pack %s: its plan would write %s, and the run's record names none of it: nothing was written", start.Pack, strings.Join(missing, ", "))
	}
	grants := reading.Grants
	if grants == nil {
		grants = delegatedGrantsOf(start.Mappings)
	}
	if !sameWords(start.Grants, grants) {
		return nil, nil, fmt.Errorf("pack %s: its grants are the ones its mappings answer (%s), and the run named %s: nothing was written", start.Pack, or(strings.Join(grants, ", "), "none"), or(strings.Join(start.Grants, ", "), "none"))
	}
	for _, g := range grants {
		// Grants name their grantee in their own words (Delegation.Grants);
		// the plan must name that crew member or group as an object.
		parts := strings.Fields(g)
		var want string
		switch {
		case len(parts) < 3:
			return nil, nil, fmt.Errorf("pack %s: grant %q is no grant a delegated pack record holds: nothing was written", start.Pack, g)
		case parts[0] == "threads":
			want = "crew " + parts[1]
		case parts[0] == "call" && parts[1] == "group" && len(parts) >= 4:
			want = "group " + parts[2]
		case parts[0] == "call" && parts[1] == "crew" && len(parts) >= 5:
			want = "crew " + parts[3]
		default:
			return nil, nil, fmt.Errorf("pack %s: grant %q is no grant a delegated pack record holds: nothing was written", start.Pack, g)
		}
		if !planNames[want] {
			return nil, nil, fmt.Errorf("pack %s: grant %q names %s, which its plan does not: nothing was written", start.Pack, g, want)
		}
	}
	start.held = heldImport(start, reading)
	return objects, grants, nil
}

// delegatedGrantsOf are the grants a delegated pack run's mappings
// answer yes to, in the record's words: each open-threads source for
// the yard crew member it names, and each call for its grantee, the
// pack's names read through the crew and group mappings. No other
// mapping grants anything — an exposure among them, which no mapping
// names — so a caller's grant beside them stands on nothing.
func delegatedGrantsOf(m Mappings) []string {
	var out []string
	for _, g := range m.threadGrants() {
		crew := g.Crew
		if mapped, ok := m.Crew[crew]; ok {
			crew = mapped
		}
		for _, src := range g.Sources {
			out = append(out, "threads "+crew+" "+src)
		}
	}
	for _, g := range m.callGrants() {
		var who string
		if name, isCrew := strings.CutPrefix(g.Who, "crew:"); isCrew {
			if mapped, ok := m.Crew[name]; ok {
				name = mapped
			}
			who = "crew " + name
		} else {
			name := g.Who
			if mapped, ok := m.Groups[name]; ok {
				name = mapped
			}
			who = "group " + name
		}
		for _, call := range g.Calls {
			out = append(out, "call "+who+" "+g.Source+":"+call)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// sameWords says whether two grant lists name the same grants, in
// whatever order each stands.
func sameWords(a, b []string) bool {
	as, bs := slices.Clone(a), slices.Clone(b)
	slices.Sort(as)
	slices.Sort(bs)
	return slices.Equal(as, bs)
}

// packRecord is the started, unexpired delegated pack record a call
// writes under, with its body: nil where the call carries no record a
// delegated run started for the same operator on the same mail.
func (w *wtx) packRecord(ctx context.Context) (*Delegation, DelegatedBody) {
	id, ok := DelegatedPackOf(ctx)
	if !ok {
		return nil, DelegatedBody{}
	}
	rec := new(Delegation)
	if ok, err := get(w.Tx, bDelegations, seqKey(id), rec); err != nil || !ok {
		return nil, DelegatedBody{}
	}
	if rec.State != "started" || now().After(rec.At.Add(delegatedPackTTL)) {
		return nil, DelegatedBody{}
	}
	body, err := w.delegationBody(rec)
	if err != nil {
		return nil, DelegatedBody{}
	}
	return rec, body
}

// packRecordFor is packRecord held to the delegated run of the call:
// the yardmaster, for the operator, on the mail the record cites.
func (w *wtx) packRecordFor(ctx context.Context, actor string) (*Delegation, DelegatedBody) {
	rec, body := w.packRecord(ctx)
	if rec == nil {
		return nil, DelegatedBody{}
	}
	d, err := delegatedIn(ctx, w.Tx, actor)
	if err != nil || d == nil || rec.Mail != d.Mail || rec.For != d.For {
		return nil, DelegatedBody{}
	}
	return rec, body
}

// packObject is the record's object of kind and name the import
// creates or changes, nil where the record names none.
func packObject(rec *Delegation, kind, name string) *DelegatedPackObject {
	for i := range rec.PackObjects {
		if rec.PackObjects[i].Kind == kind && rec.PackObjects[i].Name == name && (rec.PackObjects[i].Action == "create" || rec.PackObjects[i].Action == "change") {
			return &rec.PackObjects[i]
		}
	}
	return nil
}

// packSourceWrite holds a declared source written under a delegated
// pack record to the text the store's reading of the pack carries for
// it, read again under the mappings the record was started with.
func (w *wtx) packSourceWrite(ctx context.Context, name, text string, actor string) error {
	rec, body := w.packRecordFor(ctx, actor)
	if rec == nil {
		return fmt.Errorf("source %s: no started delegated pack record carries this write", name)
	}
	obj := packObject(rec, "source", name)
	if obj == nil {
		return fmt.Errorf("source %s: the delegated pack record %d names no such source to write", name, rec.ID)
	}
	// A pack of the packs repository is not fetched again inside a write:
	// its reading held the text's hash on the record.
	if obj.Target != "" {
		if TextSum(text) != obj.Target {
			return fmt.Errorf("source %s: its text is not the text pack %s carries for it: a delegated pack apply adds only the pack's own declarations", name, body.Pack)
		}
		return nil
	}
	read := w.packReaderOf()
	if read == nil {
		return fmt.Errorf("source %s: no server has registered its reading of the packs yardr embeds on this store", name)
	}
	reading, err := read(ctx, body.Pack, recDepot(rec), rec.Mappings, w.store)
	if err != nil {
		return err
	}
	for _, s := range reading.Sources {
		if s.Name == name {
			if s.Text != text {
				return fmt.Errorf("source %s: its text is not the text pack %s carries for it: a delegated pack apply adds only the pack's own declarations", name, body.Pack)
			}
			return nil
		}
	}
	return fmt.Errorf("source %s: pack %s carries no source of that name", name, body.Pack)
}

// packPeerWrite holds a peer change under a delegated pack record to
// a mapped exposure alone: no trust, no reach, no change to what the
// link carries, and each exposed name one the record's grants name for
// this peer. It does nothing on any other call.
func (w *wtx) packPeerWrite(ctx context.Context, name string, ch PeerAllow, actor string) error {
	if _, pack := DelegatedPackOf(ctx); !pack {
		return nil
	}
	rec, _ := w.packRecordFor(ctx, actor)
	if rec == nil || len(ch.Send)+len(ch.Receive)+len(ch.NoSend)+len(ch.NoReceive)+len(ch.Trust)+len(ch.NoTrust)+len(ch.Reach)+len(ch.NoReach)+len(ch.NoExpose) > 0 || ch.MailLimit != 0 {
		return fmt.Errorf("peer %s: a delegated pack apply changes no link but by a mapped exposure", name)
	}
	for _, who := range ch.Expose {
		if !slices.Contains(rec.Grants, "expose "+who+" "+name) {
			return fmt.Errorf("peer %s: no mapped exposure of %s stands on the delegated pack record", name, who)
		}
	}
	return nil
}

// packCallGrant holds a source grant under a delegated pack record to
// the mapped call itself: this grantee, this source, this call. It
// does nothing on any other call, and never on a grant as a user.
func (w *wtx) packCallGrant(ctx context.Context, src, what string, to Grantee, as, actor string) error {
	if _, pack := DelegatedPackOf(ctx); !pack || as != "" {
		return nil
	}
	rec, _ := w.packRecordFor(ctx, actor)
	if rec == nil || !slices.Contains(rec.Grants, "call "+to.String()+" "+src+":"+what) {
		return fmt.Errorf("source %s: no mapped grant of the call %s to %s stands on the delegated pack record", src, what, to)
	}
	return nil
}

// packFlowWrite holds a flow written under a delegated pack record
// to the flow the record names, as its reading has it where it carries
// a target. It does nothing on any other call.
func (w *wtx) packFlowWrite(ctx context.Context, f Flow, actor string) error {
	if _, pack := DelegatedPackOf(ctx); !pack {
		return nil
	}
	rec, _ := w.packRecordFor(ctx, actor)
	if rec == nil {
		return fmt.Errorf("flow %s: no started delegated pack record carries this write", f.Name)
	}
	obj := packObject(rec, "flow", f.Name)
	if obj == nil {
		return fmt.Errorf("flow %s: the delegated pack record %d names no such flow to write", f.Name, rec.ID)
	}
	if obj.Target != "" {
		var want Flow
		if err := json.Unmarshal([]byte(obj.Target), &want); err != nil {
			return err
		}
		got, wantNorm := f, want
		if err := got.normalize(); err != nil {
			return err
		}
		if err := wantNorm.normalize(); err != nil {
			return err
		}
		if !reflect.DeepEqual(got, wantNorm) {
			return fmt.Errorf("flow %s: a delegated pack apply writes it as its reading has it, not as given: that is the operator's to type in a terminal", f.Name)
		}
	}
	return nil
}

// packPointerWrite holds a flow pointer written under a delegated
// pack record to the pointer the record names. Clearing a pointer
// never rides a record: a delegated run never prunes.
func (w *wtx) packPointerWrite(ctx context.Context, depot, typ, flow, actor string) error {
	if _, pack := DelegatedPackOf(ctx); !pack {
		return nil
	}
	rec, _ := w.packRecordFor(ctx, actor)
	if rec == nil {
		return fmt.Errorf("depot %s: no started delegated pack record carries this write", depot)
	}
	name := FlowPointer{Depot: depot, Type: typ, Flow: flow}.String() + " -> " + flow
	obj := packObject(rec, "pointer", name)
	if flow == "" || obj == nil {
		return fmt.Errorf("depot %s: the delegated pack record %d names no pointer %s to write", depot, rec.ID, name)
	}
	if obj.Target != "" {
		var want FlowPointer
		if err := json.Unmarshal([]byte(obj.Target), &want); err != nil {
			return err
		}
		if want.Depot != depot || want.Type != typ || want.Flow != flow {
			return fmt.Errorf("depot %s: a delegated pack apply points it as its reading has it, not as given: that is the operator's to type in a terminal", depot)
		}
	}
	return nil
}

// routeTarget is a route object of a reading's plan, as its target
// stands on a record: the route the import writes and the ones it
// stands in for.
type routeTarget struct {
	Stage      string   `json:"stage"`
	Depot      string   `json:"depot"`
	Type       string   `json:"type,omitempty"`
	Group      string   `json:"group"`
	Originator string   `json:"originator,omitempty"`
	Delete     []string `json:"delete,omitempty"`
}

// packRouteWrite holds a route added under a delegated pack record
// to the route the record names for its stage, as its reading has it
// where it carries a target.
func (w *wtx) packRouteWrite(ctx context.Context, r Route, actor string) error {
	if _, pack := DelegatedPackOf(ctx); !pack {
		return nil
	}
	rec, _ := w.packRecordFor(ctx, actor)
	if rec == nil {
		return fmt.Errorf("route stage %s: no started delegated pack record carries this write", r.Stage)
	}
	if r.Depot == nil {
		return fmt.Errorf("route stage %s: a delegated pack apply writes no yard default route", r.Stage)
	}
	name := "depot " + *r.Depot + " stage " + r.Stage
	if r.Type != nil && *r.Type != "" {
		name += " type " + *r.Type
	}
	name += " -> " + r.Worker()
	obj := packObject(rec, "route", name)
	if obj == nil {
		return fmt.Errorf("route %s: the delegated pack record %d names no such route to write", name, rec.ID)
	}
	if obj.Target != "" {
		var want routeTarget
		if err := json.Unmarshal([]byte(obj.Target), &want); err != nil {
			return err
		}
		if want.Stage != r.Stage || want.Depot != *r.Depot || want.Type != derefStr(r.Type) || want.Group != r.Group ||
			want.Originator != derefStr(r.Originator) || r.Label != nil || len(r.AndLabels) != 0 || r.BeadPriority != nil ||
			r.Note != "" || r.When != "" || len(r.Else) != 0 {
			return fmt.Errorf("route %s: a delegated pack apply writes it as its reading has it, not as given: that is the operator's to type in a terminal", name)
		}
	}
	return nil
}

// derefStr is a string pointer's value, "" for none.
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// packRouteDelete holds a route removed under a delegated pack
// record to the ones a route of the record stands in for: the target's
// list where it carries one, else a route for the same depot, stage
// and type. It does nothing on any other call, and a route that is
// not there is the caller's to report as before.
func (w *wtx) packRouteDelete(ctx context.Context, id, actor string) error {
	if _, pack := DelegatedPackOf(ctx); !pack {
		return nil
	}
	rec, _ := w.packRecordFor(ctx, actor)
	if rec == nil {
		return fmt.Errorf("route %s: no started delegated pack record carries this write", id)
	}
	var r Route
	ok, err := get(w.Tx, bRoutes, id, &r)
	if err != nil || !ok {
		return err
	}
	for i := range rec.PackObjects {
		o := &rec.PackObjects[i]
		if o.Kind != "route" || (o.Action != "create" && o.Action != "change") {
			continue
		}
		if o.Target != "" {
			var want routeTarget
			if err := json.Unmarshal([]byte(o.Target), &want); err != nil {
				return err
			}
			if slices.Contains(want.Delete, id) {
				return nil
			}
			continue
		}
		if r.Depot != nil && strings.HasPrefix(o.Name, "depot "+*r.Depot+" stage "+r.Stage+" ") &&
			(derefStr(r.Type) == "" || strings.Contains(o.Name, " type "+derefStr(r.Type)+" ")) {
			return nil
		}
	}
	return fmt.Errorf("route %s: the delegated pack record %d names no route it stands in for", id, rec.ID)
}

// packNoPrune refuses a removal under a delegated pack record: a
// delegated run takes no --prune, so it removes nothing, and a removal
// named beside its import is the operator's alone.
func (w *wtx) packNoPrune(ctx context.Context, what, actor string) error {
	if _, pack := DelegatedPackOf(ctx); !pack {
		return nil
	}
	if rec, _ := w.packRecordFor(ctx, actor); rec == nil {
		return fmt.Errorf("%s: no started delegated pack record carries this write", what)
	}
	return fmt.Errorf("%s: a delegated pack apply removes nothing: that is the operator's to type in a terminal", what)
}

// packPutFlow is PutFlow's store of a flow, held under a delegated
// pack record first (packFlowWrite).
func (w *wtx) packPutFlow(ctx context.Context, f Flow, actor string) error {
	if err := w.packFlowWrite(ctx, f, actor); err != nil {
		return err
	}
	return put(w.Tx, bFlows, f.Name, f)
}

// packRemoveFlow is DeleteFlow's gate, held under a delegated pack
// record first (packNoPrune).
func (w *wtx) packRemoveFlow(ctx context.Context, name, actor string) error {
	if err := w.packNoPrune(ctx, "flow "+name, actor); err != nil {
		return err
	}
	return w.may(ctx, actor, ActRemoveFlow, Object{Kind: KindFlow, Name: name})
}

// packDepotPointer is SetFlowPointer's depot check, held under a
// delegated pack record first (packPointerWrite).
func (w *wtx) packDepotPointer(ctx context.Context, depot, typ, flow, actor string) error {
	if err := w.packPointerWrite(ctx, depot, typ, flow, actor); err != nil {
		return err
	}
	if w.Bucket(bDepots).Get([]byte(depot)) == nil {
		return fmt.Errorf("depot %s: %w", depot, ErrNotFound)
	}
	return nil
}

// packRemoveGroup is DeleteGroup's gate, held under a delegated pack
// record first (packNoPrune).
func (w *wtx) packRemoveGroup(ctx context.Context, name, actor string) error {
	if err := w.packNoPrune(ctx, "group "+name, actor); err != nil {
		return err
	}
	return w.may(ctx, actor, ActRemoveGroup, Object{Kind: KindGroup, Name: name})
}

// packCheckRoute is AddRoute's group check, held under a delegated
// pack record first (packRouteWrite).
func (w *wtx) packCheckRoute(ctx context.Context, r Route, actor string) (Group, error) {
	if err := w.packRouteWrite(ctx, r, actor); err != nil {
		return Group{}, err
	}
	return checkRouteGroups(w.Tx, r)
}

// packRemoveRoute is DeleteRoute's gate, held under a delegated pack
// record first (packRouteDelete).
func (w *wtx) packRemoveRoute(ctx context.Context, id, actor string) error {
	if err := w.packRouteDelete(ctx, id, actor); err != nil {
		return err
	}
	return w.may(ctx, actor, ActRemoveRoute, Object{Kind: KindRoute, Name: id})
}

// packRecordWrite holds an import's record written under a delegated
// pack record to that record's pack and depot, to its objects, and, field
// by field, to the record the store's reading of the pack said the run
// leaves (packImportHeld). That is its origin, which a later apply by name
// reads the pack again from: the URL the run fetched it from (a pack of
// the packs repository) or, for a pack yardr embeds, the build the reading
// names, and no credential, for a delegated run takes none. It is also the
// flow, frame, mappings and answers a later apply and every page start
// from, which a caller reaching RecordPack over the socket must not choose.
func (w *wtx) packRecordWrite(ctx context.Context, imp PackImport, actor string) error {
	if _, pack := DelegatedPackOf(ctx); !pack {
		return nil
	}
	rec, body := w.packRecordFor(ctx, actor)
	if rec == nil {
		return fmt.Errorf("pack %s: no started delegated pack record carries this write", imp.Pack)
	}
	if imp.Pack != body.Pack || imp.Depot != recDepot(rec) {
		return fmt.Errorf("pack %s: the delegated pack record %d is of pack %s, not this import", imp.Pack, rec.ID, body.Pack)
	}
	if err := packImportHeld(rec, imp); err != nil {
		return err
	}
	for _, po := range imp.Objects {
		if !recordedObject(rec, po) {
			return fmt.Errorf("pack %s: object %s %s stands on no delegated pack record %d", imp.Pack, po.Kind, po.Name, rec.ID)
		}
	}
	return nil
}

// recordedObject says whether an import record's object po is one the
// delegated record names, each kind in its own words: a file by the
// path it was installed at, a route and a pointer by the depot's
// stage and flow they name, a subscription and a stage map by theirs.
func recordedObject(rec *Delegation, po PackObject) bool {
	switch po.Kind {
	case "file":
		// A file stands in the record under its pack path and in the
		// import's record under the path it was installed at: the two
		// share its last two parts where a flow's name stands between,
		// and always its name.
		for _, o := range rec.Objects {
			name, ok := strings.CutPrefix(o, "file ")
			if ok && (po.Name == name || strings.HasSuffix(po.Name, "/"+name) || strings.HasSuffix(po.Name, "/"+name[strings.LastIndex(name, "/")+1:])) {
				return true
			}
		}
		return false
	case "route":
		name := "route depot " + recDepot(rec) + " stage " + po.Stage
		if po.Type != "" {
			name += " type " + po.Type
		}
		return slices.Contains(rec.Objects, name+" -> "+po.Group)
	case "pointer":
		ptr := FlowPointer{Depot: recDepot(rec), Type: po.Type, Flow: po.Flow}
		return slices.Contains(rec.Objects, "pointer "+ptr.String()+" -> "+po.Flow)
	case "subscription":
		if po.Subscription == nil {
			return false
		}
		return slices.Contains(rec.Objects, "subscription "+po.CrewMember+" "+po.Subscription.String())
	case "map":
		name := "map hook " + po.Name
		if po.CrewMember != "" {
			name += " as " + po.CrewMember
		}
		return slices.Contains(rec.Objects, name) || slices.Contains(rec.Objects, "map hook "+po.Name)
	default:
		return slices.Contains(rec.Objects, po.Kind+" "+po.Name)
	}
}

// recDepot is the depot a delegated pack record goes into, from its Of
// words where its body's depot is not kept apart.
func recDepot(rec *Delegation) string {
	if _, depot, ok := strings.Cut(rec.Of, " in depot "); ok {
		return depot
	}
	return ""
}

// packable is r with the delegated pack leg beside it: a write the
// started record of the call's pack apply names is let through, and any
// other call is judged by r as before. policy.go wraps the operator's
// rules with it; the deeds it can carry are packDelegated's switch.
func packable(r rule) rule {
	return func(ctx context.Context, tx *bolt.Tx, actor string, act Action, obj Object) error {
		if packDelegated(ctx, tx, actor, act, obj) {
			return nil
		}
		return r(ctx, tx, actor, act, obj)
	}
}

// packDelegated is the delegated pack leg beside the operator's: where ctx writes under a started
// delegated pack record for the yardmaster on the operator's cited mail,
// a write the record's plan names is let through, and no other. The
// record is the run's audit; holding each write to its objects keeps a
// started record from opening the yard past the pack it records. A
// started record carries its writes for delegatedPackTTL alone, and
// grants, thread openings, peer exposure and the declared-source fold
// pass only where the record's mapped grants name them, each bound to
// its crew member, group, source or peer — never on the record alone.
func packDelegated(ctx context.Context, tx *bolt.Tx, actor string, act Action, obj Object) bool {
	id, ok := DelegatedPackOf(ctx)
	if !ok {
		return false
	}
	d, err := delegatedIn(ctx, tx, actor)
	if err != nil || d == nil {
		return false
	}
	rec := new(Delegation)
	if ok, err := get(tx, bDelegations, seqKey(id), rec); err != nil || !ok {
		return false
	}
	if rec.State != "started" || rec.Mail != d.Mail || rec.For != d.For || now().After(rec.At.Add(delegatedPackTTL)) {
		return false
	}
	// Only an object the import creates or changes opens a write: a
	// name the plan lists unchanged, dropped or not in the pack stands
	// in the record for its audit and opens nothing.
	has := func(kind, name string) bool { return packObject(rec, kind, name) != nil }
	grant := func(prefix string) bool {
		for _, g := range rec.Grants {
			if strings.HasPrefix(g, prefix) {
				return true
			}
		}
		return false
	}
	switch act {
	case ActChangeGroup, ActGrantCrew:
		return has("group", obj.Name) || has("crew", obj.Name)
	case ActChangeCrew:
		return has("crew", obj.Name)
	case ActAddSource:
		return has("source", obj.Name)
	case ActRemoveSource:
		// A delegated run never prunes, so no removal rides its record.
		return false
	case ActSubscribeCrew:
		for i := range rec.PackObjects {
			o := &rec.PackObjects[i]
			if o.Kind == "subscription" && (o.Action == "create" || o.Action == "change") && strings.HasPrefix(o.Name, obj.Name+" ") {
				return true
			}
		}
	case ActAllowThreads:
		// Its Does names the crew member and each source granted, and
		// every one must be a mapped grant of the record.
		sources, found := strings.CutPrefix(obj.Does, "let "+obj.Name+" open threads in ")
		if !found {
			return false
		}
		for _, src := range strings.Split(sources, ", ") {
			if !grant("threads " + obj.Name + " " + src) {
				return false
			}
		}
		return true
	case ActGrantSource:
		// The call and its grantee are bound in grantSource itself;
		// here the source must be one a mapped call names.
		for _, g := range rec.Grants {
			if parts := strings.Fields(g); len(parts) >= 4 && parts[0] == "call" && strings.HasPrefix(parts[len(parts)-1], obj.Name+":") {
				return true
			}
		}
		return false
	case ActExposeToPeer:
		for _, g := range rec.Grants {
			if parts := strings.Fields(g); len(parts) == 3 && parts[0] == "expose" && parts[2] == obj.Name {
				return true
			}
		}
		return false
	case ActMoveLinks, ActMoveSource:
		// The declared-source fold alone: <source>-declared into a
		// source the plan names.
		return obj.To != "" && obj.Name == obj.To+"-declared" && has("source", obj.To)
	}
	return false
}
