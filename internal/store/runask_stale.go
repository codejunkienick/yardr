// A run ask that went stale tells its asker, so the card is asked again
// before the person meets a dead one. An approval is held to what the card
// saw (AskRun.Seen), and a source the card named may change under it: the
// approval is then refused and records nothing, so without this tell the
// asker learns of it only from the person who met the dead card. The tell
// fires when the source changes, not when the approval is refused: a
// tap-time tell reaches the asker after the person already met it.
//
// The tell is transition-only, from the before and after bytes of the write
// that changed the source: an ask fresh before the write and stale after it
// gets one staleness event, noted on its bead and delivered to its asker
// through the answer-delivery path (deliverAskStale). An ask already stale
// gets none more, and an ask born fresh by construction (its Seen written
// from the current bytes in the same transaction) gets none at creation.
// No new durable state: the event row is the standard event write.
//
// The families beside sources (a group, a peer and a crew member here; a
// user follows on the same machinery) tell the same way through tellXStale:
// a Seen key may be written by more than one fingerprint function, so the
// tell compares the set of fingerprints a hold could carry before the write
// with the set it could carry after it, and tells exactly the holds that
// cross from one set out of the other.
package store

import (
	"fmt"
	"maps"
	"slices"
)

// tellSourceStale tells the askers of the open run asks naming the source
// name whose source digest moved between before and after, the source as the
// write found it and as it leaves it (nil for none: a source added or
// removed). Actor is who made the change, for the event and its sentence.
//
// It recomputes the ask's "source <name>" Seen against the new bytes with
// the row's own digest function (staleFP), and the same for the pack-target
// key a named depot's card holds for a mapped source. Where the fingerprints
// are one no ask can have transitioned, so the scan is skipped: a limit-only
// set, a same-text put and a value-only keying cost no scan. Otherwise every
// open bead's open run asks are read, as retireMappingCards reads them, and
// each ask fresh before the write and stale after gets one ask_stale event
// on its bead, naming the ask and what changed. The event goes through
// w.event in the transaction, so a batch that runs the write twice ends the
// same.
func (w *wtx) tellSourceStale(name string, before, after *Source, actor string) error {
	key := "source " + name
	target := packTargetKey + "source " + name
	beforeFP, err := w.staleFP(name, before, false)
	if err != nil {
		return err
	}
	afterFP, err := w.staleFP(name, after, false)
	if err != nil {
		return err
	}
	beforeTarget, err := w.staleFP(name, before, true)
	if err != nil {
		return err
	}
	afterTarget, err := w.staleFP(name, after, true)
	if err != nil {
		return err
	}
	if beforeFP == afterFP && beforeTarget == afterTarget {
		return nil
	}
	for _, uuid := range keys(w.Tx, bBeadOpen, "") {
		notes, err := beadNotes(w.Tx, uuid)
		if err != nil {
			return err
		}
		for _, r := range ResolveAsks(notes, now()) {
			if r.AskErr != nil || r.Ask.Run == nil {
				continue
			}
			if r.State != AskOpen && r.State != AskAside {
				continue
			}
			if w.approving != nil && uuid == w.approving.bead && r.Note.Seq == w.approving.seq {
				continue
			}
			moved := ""
			if fp, ok := r.Ask.Run.Seen[key]; ok && fp == beforeFP && fp != afterFP {
				moved = key
			} else if fp, ok := r.Ask.Run.Seen[target]; ok && fp == beforeTarget && fp != afterTarget {
				moved = target
			}
			if moved == "" {
				continue
			}
			if err := w.event(uuid, "ask_stale", actor, map[string]any{
				"ask": r.Note.Seq, "source": name, "seen": moved,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// tellKey is one Seen key a family tell watches: the key a hold stores what
// it saw of the thing under, and every fingerprint function that writes that
// key. A key needs every one of them: a hold carries the fingerprint of the
// card that wrote it, in whichever function's words, and a write moves the
// key for that hold only when its own fingerprint crosses. The functions
// read a standing thing alone; a thing that stands not is the nil set of
// tellXStale, never a function's answer.
type tellKey[T any] struct {
	key string
	fns []func(*T) string
}

// tellXStale tells the askers of the open and put-aside holds naming the
// thing kind and name whose Seen moved between before and after, the thing
// as the write found it and as it leaves it (nil for none: a thing added,
// removed or renamed away). Actor is who made the change, for the event and
// its sentence. Keys are the tellable keys of the thing, each with every
// fingerprint function that writes it, so that a later family's wider list
// (a crew member's four combinations under one key) slots in without a new
// comparison.
//
// For each key the tell builds the before-set and the after-set of
// fingerprints across all of the key's functions. A standing thing
// contributes its functions' fingerprints; one that stands not contributes
// the fixed set {"", seen("no <kind>", name)} with no read of the yard, which
// is what a hold written against its absence can carry (nothing, or the
// absence in words). Where the two sets are one for every key no hold can
// have transitioned, so the scan is skipped: a subscription-only write and
// a same-value put cost no scan. Otherwise every open bead's holds are
// read, and each hold fresh before the write and stale after it — its
// stored fingerprint for a key in the before-set but not the after-set —
// gets one ask_stale event on its bead, naming the ask, the kind and name
// and the key that moved. A hold born fresh (its fingerprint in the
// after-set), one already stale (in neither set) and one that never named
// the key get none. The event goes through w.event in the transaction, so
// a batch that runs the write twice ends the same, and a hold that names
// the thing under several keys still gets one event.
func (w *wtx) tellXStale[T any](kind, name string, before, after *T, actor string, watched []tellKey[T]) error {
	type keySets struct {
		key           string
		before, after map[string]bool
	}
	sets := make([]keySets, len(watched))
	moved := false
	for i, k := range watched {
		fingerprints := func(thing *T) map[string]bool {
			if thing == nil {
				return map[string]bool{"": true, seen("no "+kind, name): true}
			}
			out := make(map[string]bool, len(k.fns))
			for _, fn := range k.fns {
				out[fn(thing)] = true
			}
			return out
		}
		sets[i] = keySets{key: k.key, before: fingerprints(before), after: fingerprints(after)}
		if !maps.Equal(sets[i].before, sets[i].after) {
			moved = true
		}
	}
	if !moved {
		return nil
	}
	for _, uuid := range keys(w.Tx, bBeadOpen, "") {
		notes, err := beadNotes(w.Tx, uuid)
		if err != nil {
			return err
		}
		for _, r := range ResolveAsks(notes, now()) {
			if r.AskErr != nil {
				continue
			}
			if r.State != AskOpen && r.State != AskAside {
				continue
			}
			if w.approving != nil && uuid == w.approving.bead && r.Note.Seq == w.approving.seq {
				continue
			}
			held := heldSeen(r.Ask)
			if held == nil {
				continue
			}
			key := ""
			for _, s := range sets {
				if fp, ok := held[s.key]; ok && s.before[fp] && !s.after[fp] {
					key = s.key
					break
				}
			}
			if key == "" {
				continue
			}
			if err := w.event(uuid, "ask_stale", actor, map[string]any{
				"ask": r.Note.Seq, "kind": kind, "name": name, "seen": key,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// approvingAsk is the ask a write is approving as it runs its command: its
// bead, by UUID, and its seq. A tell in that write leaves it alone — the
// write answers it in the same transaction, so by the commit it is no open
// hold gone stale, however its own command moved what it read.
type approvingAsk struct {
	bead string
	seq  uint64
}

// heldSeen is what an ask holds of what it saw, whichever card carries it:
// a run card's, a depot-push card's or a setup card's Seen, and nil for an
// ask that holds none (a question in words), which no family tell can stale.
func heldSeen(a Ask) map[string]string {
	switch {
	case a.Run != nil:
		return a.Run.Seen
	case a.DepotPush != nil:
		return a.DepotPush.Seen
	case a.Setup != nil:
		return a.Setup.Seen
	}
	return nil
}

// depotPushGroupFingerprint is what a depot-push card keeps of a group: its
// runner, its command with every push assignment taken out, and the rest of
// its config whole (depotpush.go). It is computed on the config as stored,
// as the card reads it back, so a put still in memory compares with it.
func depotPushGroupFingerprint(g Group) string {
	c := asStored(g.Config)
	cmd, _ := c["cmd"].(string)
	other := make(map[string]any, len(c))
	for k, v := range c {
		if k != "cmd" {
			other[k] = v
		}
	}
	return seen(g.Runner, cmdRest(cmd), other)
}

// granteeGroupFingerprint is what a grant card and a setup card keep of a
// group: its runner and its config whole, as granteeSeen reads them back.
func granteeGroupFingerprint(g Group) string {
	return seen(g.Runner, asStored(g.Config))
}

// tellGroupStale tells the askers of the holds naming the group name whose
// "group <name>" Seen moved between before and after, the group as the write
// found it and as it leaves it (nil for none). The key is written by three
// fingerprint functions — the group set card's strong few, the depot-push
// card's and the grant cards' — and the tell carries all three, so a write
// tells exactly the holds whose own reading of the group it moved: a change
// of the push peer alone, which neither of the other two readings names in
// full, still tells the holds that read the config whole and none whose
// reading stands.
func (w *wtx) tellGroupStale(name string, before, after *Group, actor string) error {
	return w.tellXStale("group", name, before, after, actor, []tellKey[Group]{{
		key: "group " + name,
		fns: []func(*Group) string{
			func(g *Group) string { return groupSetFingerprint(*g) },
			func(g *Group) string { return depotPushGroupFingerprint(*g) },
			func(g *Group) string { return granteeGroupFingerprint(*g) },
		},
	}})
}

// clonePeer is a copy of the link p that shares nothing a peer write
// mutates: the carries, expose, trust and reach lists and maps, and the
// intakes and identities a write replaces in place. A tell's before is
// one, taken as the write found the link, so the after the write leaves
// cannot move it under it.
func clonePeer(p *Peer) *Peer {
	if p == nil {
		return nil
	}
	out := *p
	out.Send, out.Receive = slices.Clone(p.Send), slices.Clone(p.Receive)
	out.Expose = slices.Clone(p.Expose)
	out.Trust, out.Reach = maps.Clone(p.Trust), maps.Clone(p.Reach)
	out.Intakes, out.Identities = slices.Clone(p.Intakes), slices.Clone(p.Identities)
	return &out
}

// tellPeerStale tells the askers of the holds naming the peer name whose
// Seen moved between before and after, the link as the write found it and
// as it leaves it (nil for none: a link added, removed or renamed away).
// The keys are every one a card holds of a link: its key whole, its
// intakes, its identities and its address, what each of its crew members
// is in a source, and what it carries, exposes, trusts and reaches. The
// crew-in-source keys are built from the identities either side holds,
// each with the one fingerprint function that writes it, so a write tells
// exactly the holds whose own reading of the link it moved.
func (w *wtx) tellPeerStale(name string, before, after *Peer, actor string) error {
	watched := []tellKey[Peer]{
		{key: "peer " + name, fns: []func(*Peer) string{func(p *Peer) string { return p.Key }}},
		{key: "peer " + name + " intakes", fns: []func(*Peer) string{func(p *Peer) string { return seen(p.Intakes) }}},
		{key: "peer " + name + " identities", fns: []func(*Peer) string{func(p *Peer) string { return seen(p.Identities) }}},
		{key: "peer " + name + " address", fns: []func(*Peer) string{func(p *Peer) string { return seen(p.URL) }}},
	}
	paired := map[string]bool{}
	for _, side := range []*Peer{before, after} {
		if side == nil {
			continue
		}
		for _, id := range side.Identities {
			crew, source := id.Crew, id.Source
			if paired[crew+"\x00"+source] {
				continue
			}
			paired[crew+"\x00"+source] = true
			watched = append(watched, tellKey[Peer]{
				key: "peer " + name + " " + crew + " in " + source,
				fns: []func(*Peer) string{func(p *Peer) string {
					cur, had := peerIdentityHeld(p, crew, source)
					return seen(cur.ID, had)
				}},
			})
		}
	}
	watched = append(watched,
		tellKey[Peer]{key: "peer " + name + " carries", fns: []func(*Peer) string{func(p *Peer) string { return seen(p.Send, p.Receive) }}},
		tellKey[Peer]{key: "peer " + name + " exposes", fns: []func(*Peer) string{func(p *Peer) string { return seen(p.Expose) }}},
		tellKey[Peer]{key: "peer " + name + " trusts", fns: []func(*Peer) string{func(p *Peer) string { return seen(p.Trust) }}},
		tellKey[Peer]{key: "peer " + name + " reaches", fns: []func(*Peer) string{func(p *Peer) string { return seen(p.Reach) }}},
	)
	return w.tellXStale("peer", name, before, after, actor, watched)
}

// cloneCrew is a copy of the member r that shares nothing a crew write
// mutates: its config, its identities and its lists. A tell's before is
// one, taken as the write found the member, so the after the write leaves
// cannot move it under it.
func cloneCrew(r *CrewMember) *CrewMember {
	if r == nil {
		return nil
	}
	out := *r
	out.Config = maps.Clone(r.Config)
	out.Identities = maps.Clone(r.Identities)
	out.On, out.Subscriptions = slices.Clone(r.On), slices.Clone(r.Subscriptions)
	return &out
}

// crewBefore is the member a crew write found, cloned, or nil where it
// stood not: the write then adds it, and a hold written against its
// absence is what the tell can reach.
func crewBefore(ok bool, r *CrewMember) *CrewMember {
	if !ok {
		return nil
	}
	return cloneCrew(r)
}

// crewClones is every crew member as it stands, cloned by name: the
// before a write that rewrites members it does not name one by one (a
// source fold, a peer rename) tells from.
func (w *wtx) crewClones() (map[string]*CrewMember, error) {
	out := map[string]*CrewMember{}
	err := each(w.Tx, bCrew, "", func(_ []byte, r *CrewMember) error {
		out[r.Name] = cloneCrew(r)
		return nil
	})
	return out, err
}

// crewSetSeen is what a crew set card keeps of a member: its runner and
// the prompt, kind and flags it runs, with the model and the directory
// only on a card that sets one of them (runask_crewset.go). The tell
// computes all four of the card's combinations, as a hold carries the
// one its own card wrote.
func crewSetSeen(r *CrewMember, model, dir bool) string {
	c := asStored(r.Config)
	held := []any{r.Runner, c["prompt"], c["kind"], c["args"]}
	if model {
		held = append(held, c["model"])
	}
	if dir {
		held = append(held, r.Dir)
	}
	return seen(held...)
}

// granteeCrewFingerprint is what a grant card and a setup card keep of a
// crew member: its runner, its config whole and its directory, as
// granteeSeen reads them back.
func granteeCrewFingerprint(r *CrewMember) string {
	return seen(r.Runner, r.Config, r.Dir)
}

// tellCrewStale tells the askers of the holds naming the crew member name
// whose "crew <name>" Seen moved between before and after, the member as
// the write found it and as it leaves it (nil for none: a member added,
// removed or renamed away). The key is written by the crew set card's
// four combinations, by the grant and setup cards' whole reading, and by
// the identity card's reading of one source, so the tell carries them
// all, the identity readings one per source either side holds, and tells
// exactly the holds whose own reading of the member it moved. A write
// that moves only what no reading names — a session's runtime, its
// subscriptions, the prompt file a card holds by its digest — moves no
// fingerprint here and costs no scan.
func (w *wtx) tellCrewStale(name string, before, after *CrewMember, actor string) error {
	watched := []tellKey[CrewMember]{{
		key: "crew " + name,
		fns: []func(*CrewMember) string{
			func(r *CrewMember) string { return crewSetSeen(r, false, false) },
			func(r *CrewMember) string { return crewSetSeen(r, true, false) },
			func(r *CrewMember) string { return crewSetSeen(r, false, true) },
			func(r *CrewMember) string { return crewSetSeen(r, true, true) },
			func(r *CrewMember) string { return granteeCrewFingerprint(r) },
		},
	}}
	sources := map[string]bool{}
	for _, side := range []*CrewMember{before, after} {
		if side == nil {
			continue
		}
		for source := range side.Identities {
			sources[source] = true
		}
	}
	for _, source := range slices.Sorted(maps.Keys(sources)) {
		watched[0].fns = append(watched[0].fns, func(r *CrewMember) string {
			cur := r.Identities[source]
			return seen(cur.ID, cur.Key)
		})
	}
	if after == nil {
		// Removing a member stales an open identity hold even for a source
		// where the member held no identity yet (seen("", "")).
		watched[0].fns = append(watched[0].fns, func(*CrewMember) string {
			return seen("", "")
		})
	}
	return w.tellXStale("crew", name, before, after, actor, watched)
}

// staleFP is the fingerprint a run ask holds for the source name under the
// source key (target false) or the pack-target key (target true), when the
// yard holds src of it, nil for none. A source that stands uses the row's
// own function (sourceSeen). One that stands not is "" where no command
// copies stand, else seen("no source", inv) as the add and apply cards hold
// it, and always "" under the target key as packTargetsSeen holds it.
func (w *wtx) staleFP(name string, src *Source, target bool) (string, error) {
	if src != nil {
		return w.sourceSeen(src)
	}
	if target {
		return "", nil
	}
	inv, _, err := w.inventoryOf(name, false)
	if err != nil {
		return "", err
	}
	if len(inv) == 0 {
		return "", nil
	}
	return seen("no source", inv), nil
}

// deliverAskStale delivers the ask_stale event e on row to its ask's asker,
// as an answer is delivered (deliver): the sentence reads in words wherever
// it lands, the asker hears it without subscribing when they are a crew
// member or a user of this yard and are not the one who changed the thing,
// and the asking session routes a crew asker's tell to its slice. An asker
// with no inbox hears nothing: nobody stands to ask again. A corrupt ask
// reference delivers to the subscriptions alone, as a corrupt answer does.
// A source tell names its source; a family tell names its kind and name,
// and the sentence names whichever the event carries.
func (w *wtx) deliverAskStale(e *Event, row *beadRow, out, told map[string]any) error {
	var seq uint64
	switch n := e.Data["ask"].(type) {
	case uint64:
		seq = n
	case float64:
		seq = uint64(n)
	default:
		return nil
	}
	var n Note
	if ok, err := get(w.Tx, bNotes, join(row.UUID, seqKey(seq)), &n); err != nil {
		return err
	} else if !ok || n.Kind != NoteAsk {
		return nil
	}
	kind, _ := e.Data["kind"].(string)
	name, _ := e.Data["name"].(string)
	if kind == "" {
		// A source tell, whose event names no kind: its kind is source.
		kind = "source"
		name, _ = e.Data["source"].(string)
	}
	moved, _ := e.Data["seen"].(string)
	e.Data["answer"] = staleAskSentence(e.Actor, row.ID, seq, kind, name, moved)
	if n.Session != "" {
		e.Data["ask_session"] = n.Session
	}
	if asker := n.Author; asker != e.Actor {
		if w.Bucket(bCrew).Get([]byte(asker)) != nil {
			prev, _ := out[asker].(string)
			out[asker] = best(prev, ReasonAnswered)
		} else if w.Bucket(bUsers).Get([]byte(asker)) != nil {
			prev, _ := told[asker].(string)
			told[asker] = best(prev, ReasonAnswered)
		}
	}
	return nil
}

// staleAskSentence is a stale ask as one line of an inbox: who changed the
// thing of kind and name, which ask is stale, and to ask again. A source
// reads through it with kind source, in the words its tells have always had.
func staleAskSentence(actor, bead string, seq uint64, kind, name, moved string) string {
	return oneLine(fmt.Sprintf("%s changed the %s %s: ask %d on %s is stale (%s moved). Ask again.", actor, kind, name, seq, bead, moved))
}
