package store

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
)

// A group's name is how the yard's records reach it: its routes, the beads it
// owns and the sessions that work them, what its sessions may call of a
// source, the subscriptions that filter on it, what its arrivals have cost
// and the pack imports that made it or took it. Renaming it changes the name
// in every one of them, in the write that changes the group's own, so no
// record is left naming a group that is not there. Adding the group again,
// moving each route and group rm do it in many writes, and the last of them
// releases the beads the group's live sessions hold.
//
// The event log, the notes and the sessions that ended keep the name the
// group had when they were written: they are what happened, and the group's
// record says nothing of what it was called. A timer names no group.

// ActRenameGroup is renaming a group: the operator's alone, as renaming a
// peer is, and one a run ask may propose, so that the yardmaster asks and the
// operator approves. It is declared and registered here and not beside the
// other actions because policy.go stands at the file-size ceiling the test
// gate holds (.yardr/test).
const ActRenameGroup Action = "rename a group"

func init() { coreRules[ActRenameGroup] = ruleOperator }

// DepotGroupPrefix is what a pack import puts before the name of a group it
// makes for depot: the depot's name and a dash. pack.GroupName reads it from
// here, so the names a rename may not take (importerNamespace) and the ones
// the importer makes are one rule.
func DepotGroupPrefix(depot string) string { return depot + "-" }

// DepotUnserved is a depot a yard default stops serving when a group is
// renamed: the default names its group by the depot template, which only the
// old name resolved to, and the template is not renamed with the group (it
// names the groups of every depot, and so no one of them).
type DepotUnserved struct {
	Route Route  `json:"route"`
	Depot string `json:"depot"`
}

// GroupRenamed is what a rename of a group moved, as the command and the
// card tell it. Every count is of what changed, and what a rename leaves
// alone is not in it: the group's sessions go on running, so Sessions is how
// many did, and none is released.
type GroupRenamed struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Routes are the ids of the routes that name the group, as their first
	// group or after it; their conditions stay with it.
	Routes []string `json:"routes,omitempty"`
	// Beads are the open beads the group owned, and Sessions the live
	// sessions that work them.
	Beads    int `json:"beads"`
	Sessions int `json:"sessions"`
	// Grants are the grants of source calls the group's sessions held.
	Grants int `json:"grants"`
	// Subscriptions are who holds a subscription to the yard's own events
	// that filters on the group: crew members, users and groups.
	Subscriptions []string `json:"subscriptions,omitempty"`
	// Members are the groups that list the group among their members: a
	// script group counts as itself where the members of a stage's group
	// may move its beads, so the list names it.
	Members []string `json:"members,omitempty"`
	// Packs are the pack imports that recorded the group, as the pack's name
	// and, for one imported into a depot, " in <depot>".
	Packs []string `json:"packs,omitempty"`
	// Unserved are the depots a yard default stops serving.
	Unserved []DepotUnserved `json:"unserved,omitempty"`
}

// String is what yardr group rename prints, and what its run ask answers: the
// rename, and what followed it.
func (r GroupRenamed) String() string {
	lines := []string{fmt.Sprintf("group %s is %s now: the old name is free, and reaches nothing", r.From, r.To)}
	if len(r.Routes) > 0 {
		lines = append(lines, "routes that name it now: "+strings.Join(r.Routes, ", "))
	}
	lines = append(lines, "its arrival estimates from the old name stand under the new one")
	lines = append(lines, fmt.Sprintf("its open beads (%d) and live sessions (%d) go on under the new name; none is released", r.Beads, r.Sessions))
	if r.Grants > 0 {
		lines = append(lines, fmt.Sprintf("the grants of source calls that its sessions held (%d) moved with it", r.Grants))
	}
	if len(r.Subscriptions) > 0 {
		lines = append(lines, "subscriptions that filtered on it follow: "+strings.Join(r.Subscriptions, ", "))
	}
	if len(r.Members) > 0 {
		lines = append(lines, "groups whose members listed it list the new name: "+strings.Join(r.Members, ", "))
	}
	if len(r.Packs) > 0 {
		lines = append(lines, "pack imports that recorded it: "+strings.Join(r.Packs, ", "))
	}
	for _, u := range r.Unserved {
		lines = append(lines, fmt.Sprintf("default %s (%s -> %s) no longer serves %s: no group %s", u.Route.ID, u.Route.Stage, u.Route.GroupList(), u.Depot, r.From))
	}
	return strings.Join(lines, "\n")
}

// groupRename is a rename as it is read from the yard's records, before
// anything is written: each record that names the group, already as it will
// be. The card says what it holds and the write puts it, so that the two
// cannot disagree about what a rename moves.
type groupRename struct {
	from, to string
	// group is the group's record under its new name.
	group  Group
	routes []Route
	// beads are the open beads the group owns or claimed the stay of.
	beads []*beadRow
	// sessions are the group's live sessions, and the ended ones the daemon
	// is not done with, whose teardown reads the group's settings.
	sessions []Session
	// live is how many of sessions are live.
	live int
	// grants are the group's grants of source calls, as they stand.
	grants []SourceGrant
	// staleGrants and staleETA are the keys of the grants and ETA cells that
	// a group once called to left behind: removing a group takes neither.
	staleGrants, staleETA []string
	eta                   []etaMove
	// crew and users are the holders of a subscription that filters on the
	// group, as they will be, and groups the other groups that hold one or
	// list the group among their members, whose names are in subscribed and
	// listed. The group's own subscriptions are group's, changed when own
	// says so.
	crew       []CrewMember
	users      []User
	groups     []Group
	subscribed []string
	listed     []string
	own        bool
	sent       []peerSent
	packs      []packMove
	// unserved are the depots a yard default stops serving.
	unserved []DepotUnserved
}

// etaMove is one ETA cell of the group, to be kept under its new name.
type etaMove struct {
	from, to string
	raw      []byte
}

// packMove is the record of a pack import that names the group, as it will
// be, and the key it is kept under.
type packMove struct {
	key string
	rec PackImport
}

// importerNamespace is the known depot whose pack imports would name a group
// as name does, "" where none would: the groups an import makes for a depot
// are DepotGroupPrefix's, so a group that is none of an import's, with such
// a name, would be taken for the pack's by its next import.
func (w *wtx) importerNamespace(name string) string {
	for _, depot := range keys(w.Tx, bDepots, "") {
		if strings.HasPrefix(name, DepotGroupPrefix(depot)) {
			return depot
		}
	}
	return ""
}

// checkGroupRename reads the group name and holds its new name to what the
// yard holds one to: a name in use is refused in words of its own, since a
// rename never replaces a group, and one a pack import would make for a
// depot is not free for another. The rest is a new group's rule (group add's
// card holds it): the shape, wait, a user's or a crew member's name.
func (w *wtx) checkGroupRename(name, to string) (*Group, error) {
	var g Group
	if ok, err := get(w.Tx, bGroups, name, &g); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("group %s: %w", name, ErrNotFound)
	}
	switch {
	case to == name:
		return nil, fmt.Errorf("group %s has that name already", name)
	case w.Bucket(bGroups).Get([]byte(to)) != nil:
		return nil, fmt.Errorf("group %s is there already: a rename never replaces a group. Remove that one first (yardr group rm %s), or pick another name", to, to)
	}
	if depot := w.importerNamespace(to); depot != "" {
		return nil, fmt.Errorf("group %s: %s is the name pack imports give the groups they make for depot %s, so a group called that would be taken for the pack's by its next import: choose a name that does not begin with %s", to, to, depot, DepotGroupPrefix(depot))
	}
	if err := w.groupAddName(to); err != nil {
		return nil, err
	}
	return &g, nil
}

// planGroupRename reads every record that names the group name, and what
// each becomes under to. It writes nothing.
func (w *wtx) planGroupRename(name, to string) (*groupRename, error) {
	g, err := w.checkGroupRename(name, to)
	if err != nil {
		return nil, err
	}
	p := &groupRename{from: name, to: to, group: *g}
	p.group.Name = to
	if err := p.readRoutes(w); err != nil {
		return nil, err
	}
	if err := p.readBeads(w); err != nil {
		return nil, err
	}
	if err := p.readSessions(w); err != nil {
		return nil, err
	}
	if err := p.readGrants(w); err != nil {
		return nil, err
	}
	p.readETA(w)
	if err := p.readSubscriptions(w); err != nil {
		return nil, err
	}
	if err := p.readSent(w); err != nil {
		return nil, err
	}
	if err := p.readPacks(w); err != nil {
		return nil, err
	}
	if p.unserved, err = w.renameUnserved(name, to); err != nil {
		return nil, err
	}
	return p, nil
}

// Routes name a group first or in a chain after it. The condition a group
// carries in one stays with its entry, and none names a group (it names a
// window of a provider or of a source), so the name is all that changes.
func (p *groupRename) readRoutes(w *wtx) error {
	return each(w.Tx, bRoutes, "", func(_ []byte, r *Route) error {
		if !r.Names(p.from) {
			return nil
		}
		if r.Group == p.from {
			r.Group = p.to
		}
		for i := range r.Else {
			if r.Else[i].Name == p.from {
				r.Else[i].Name = p.to
			}
		}
		p.routes = append(p.routes, *r)
		return nil
	})
}

// Only an open bead can be owned, and its stay claimed by the group: a
// closed bead's group is cleared when it closes. A bead's claim is kept
// beside its owner for the arrival estimates, which pair a stay with the
// group that still owns it, so both are changed or the stay would end as one
// whose claimant had let go of it.
func (p *groupRename) readBeads(w *wtx) error {
	for _, u := range keys(w.Tx, bBeadOpen, "") {
		row := new(beadRow)
		if ok, err := get(w.Tx, bBeads, u, row); err != nil {
			return err
		} else if !ok {
			continue
		}
		owned := row.Group != nil && *row.Group == p.from
		if !owned && row.StayClaimGroup != p.from {
			continue
		}
		if owned {
			to := p.to
			row.Group = &to
		}
		if row.StayClaimGroup == p.from {
			row.StayClaimGroup = p.to
		}
		p.beads = append(p.beads, row)
	}
	return nil
}

// A session's group is the live index's key, and the daemon reads the
// group's settings through it for as long as it has anything to do with the
// session: the live ones, and the ended ones it has yet to stop, clean up
// after or count. Any other ended session is what happened and keeps the
// name it ran under.
func (p *groupRename) readSessions(w *wtx) error {
	live, err := w.store.liveSessions(w.Tx, p.from)
	if err != nil {
		return err
	}
	p.sessions, p.live = live, len(live)
	for _, id := range keys(w.Tx, bSessPend, "") {
		var sess Session
		if ok, err := get(w.Tx, bSessions, id, &sess); err != nil {
			return err
		} else if ok && sess.Group == p.from && !sess.live() {
			p.sessions = append(p.sessions, sess)
		}
	}
	return nil
}

// A grant is kept under its grantee's name. Grants for a group called to are
// ones an earlier group of the name left, since removing a group takes none:
// they were not given to this group, and are dropped so that it does not
// take them with the name.
func (p *groupRename) readGrants(w *wtx) error {
	return each(w.Tx, bSourceGrants, "", func(k []byte, g *SourceGrant) error {
		switch g.Group {
		case p.from:
			p.grants = append(p.grants, *g)
		case p.to:
			p.staleGrants = append(p.staleGrants, string(k))
		}
		return nil
	})
}

// An ETA cell is kept under its group, so the group's arrival estimates
// would start over under a new name; they follow it, replacing what a group
// of the name left behind.
func (p *groupRename) readETA(w *wtx) {
	p.staleETA = keys(w.Tx, bETACells, join(p.to, ""))
	for _, k := range keys(w.Tx, bETACells, join(p.from, "")) {
		p.eta = append(p.eta, etaMove{from: k, to: join(p.to, k[len(p.from)+1:]),
			raw: bytes.Clone(w.Bucket(bETACells).Get([]byte(k)))})
	}
}

// renamedGroupFilter puts to in place of every name in the group filter of
// each subscription to the yard's own events that names it, and whether any did.
// A source's other keys named group are its own, so no other is touched.
// The filter's map is replaced and not written into: another subscription
// may share it.
func renamedGroupFilter(subs []Subscription, name, to string) bool {
	changed := false
	for i, sub := range subs {
		if sub.Source != SourceYardr {
			continue
		}
		vals := WhereValues(sub.Where["group"])
		found := false
		for j := range vals {
			if vals[j] == name {
				vals[j], found = to, true
			}
		}
		if !found {
			continue
		}
		where := map[string]string{}
		for k, v := range sub.Where {
			where[k] = v
		}
		where["group"] = JoinWhere(vals)
		subs[i].Where, changed = where, true
	}
	return changed
}

// A crew member's, a user's or a group's subscription may filter the yard's
// events on a group, as --where group=<name> does, and it hears nothing of
// the group under a name it no longer has. The group's own is among them
// and is changed with its record.
func (p *groupRename) readSubscriptions(w *wtx) error {
	if err := each(w.Tx, bCrew, "", func(_ []byte, r *CrewMember) error {
		if renamedGroupFilter(r.Subscriptions, p.from, p.to) {
			r.Revision = newRevision()
			p.crew = append(p.crew, *r)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := each(w.Tx, bUsers, "", func(_ []byte, u *User) error {
		if renamedGroupFilter(u.Subscriptions, p.from, p.to) {
			p.users = append(p.users, *u)
		}
		return nil
	}); err != nil {
		return err
	}
	p.own = renamedGroupFilter(p.group.Subscriptions, p.from, p.to)
	return p.readGroups(w)
}

// The other groups hold subscriptions as the rest do, and also list who may
// move the beads of the stages they work: a session of the exec runner counts
// as its group where the list names that group (memberAllowed), so a script
// group's name is in the lists and follows it, or its script is no member of
// them the moment it is renamed. Another runner's name in a list is a user's
// or a crew member's, who is no group's session, and stays.
func (p *groupRename) readGroups(w *wtx) error {
	script := p.group.Runner == "exec"
	return each(w.Tx, bGroups, "", func(_ []byte, g *Group) error {
		if g.Name == p.from {
			return nil
		}
		subscribed := renamedGroupFilter(g.Subscriptions, p.from, p.to)
		listed := script && slices.Contains(g.Members, p.from)
		if listed {
			members := slices.Clone(g.Members)
			members[slices.Index(members, p.from)] = p.to
			g.Members = dedupeOrdered(members)
		}
		if !subscribed && !listed {
			return nil
		}
		p.groups = append(p.groups, *g)
		if subscribed {
			p.subscribed = append(p.subscribed, g.Name)
		}
		if listed {
			p.listed = append(p.listed, g.Name)
		}
		return nil
	})
}

// A bead sent to another yard is recorded with the group that sent it, whose
// settings fetching its branch back reads (PeerFetches); until the stay is
// settled the name is in use.
func (p *groupRename) readSent(w *wtx) error {
	return each(w.Tx, bPeerSent, "", func(_ []byte, rec *peerSent) error {
		if rec.Group == p.from && rec.Settled == nil {
			rec.Group = p.to
			p.sent = append(p.sent, *rec)
		}
		return nil
	})
}

// A pack import records the groups it made or took, by the name the pack
// gives each and the yard's name for it, and what each came to: the group
// objects, the routes' groups, the group whose settings another took, and
// the answers about calls its sessions may make. The next import of the pack
// reads the record to know which group is the pack's (groupName): a record
// that kept the old name would find no group by it, and make one by the
// depot's own name for it, so the old name would come back beside the
// renamed group.
func (p *groupRename) readPacks(w *wtx) error {
	return each(w.Tx, bPacks, "", func(k []byte, rec *PackImport) error {
		if p.renamedIn(rec) {
			p.packs = append(p.packs, packMove{key: string(k), rec: *rec})
		}
		return nil
	})
}

// renamedIn puts the new name in rec, where it names the group, and whether
// it did.
func (p *groupRename) renamedIn(rec *PackImport) bool {
	changed := false
	for pack, name := range rec.Groups {
		if name == p.from {
			rec.Groups[pack], changed = p.to, true
		}
	}
	for i := range rec.Objects {
		o := &rec.Objects[i]
		switch {
		case o.Kind == "group" && o.Name == p.from:
			o.Name, changed = p.to, true
		case o.Kind == "route" && o.Group == p.from:
			o.Group, changed = p.to, true
		}
		if o.Binds == p.from {
			o.Binds, changed = p.to, true
		}
		if o.Subscription != nil {
			subs := []Subscription{*o.Subscription}
			if renamedGroupFilter(subs, p.from, p.to) {
				o.Subscription, changed = &subs[0], true
			}
		}
	}
	was, now := Grantee{Group: p.from}.String(), Grantee{Group: p.to}.String()
	if answers, ok := rec.Calls[was]; ok {
		delete(rec.Calls, was)
		rec.Calls[now], changed = answers, true
	}
	return changed
}

// renameUnserved are the depots a yard default stops serving once the group
// is called to: a default names its group by the depot template, which is
// not renamed, so the depot it resolved to the old name for finds no group
// by it. group rm names the same where it removes the group.
func (w *wtx) renameUnserved(name, to string) ([]DepotUnserved, error) {
	var routes []Route
	if err := each(w.Tx, bRoutes, "", func(_ []byte, r *Route) error {
		routes = append(routes, *r)
		return nil
	}); err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(routes, Route.Templated) {
		return nil, nil
	}
	have := map[string]Group{}
	if err := each(w.Tx, bGroups, "", func(_ []byte, g *Group) error {
		have[g.Name] = *g
		return nil
	}); err != nil {
		return nil, err
	}
	renamed := map[string]Group{to: {Name: to}}
	for n, g := range have {
		if n != name {
			renamed[n] = g
		}
	}
	operator := w.settingValue(OperatorSetting)
	var out []DepotUnserved
	for _, depot := range keys(w.Tx, bDepots, "") {
		was := map[string]bool{}
		for _, u := range UnservedRoutes(routes, depot, operator, have) {
			was[u.Route.ID] = true
		}
		for _, u := range UnservedRoutes(routes, depot, operator, renamed) {
			if !was[u.Route.ID] {
				out = append(out, DepotUnserved{Route: u.Route, Depot: depot})
			}
		}
	}
	return out, nil
}

// report is what the plan moves, as the command and the card tell it.
func (p *groupRename) report() *GroupRenamed {
	out := &GroupRenamed{From: p.from, To: p.to, Beads: len(p.beads), Sessions: p.live, Grants: len(p.grants),
		Unserved: p.unserved}
	for _, r := range p.routes {
		out.Routes = append(out.Routes, r.ID)
	}
	for _, r := range p.crew {
		out.Subscriptions = append(out.Subscriptions, r.Name)
	}
	for _, u := range p.users {
		out.Subscriptions = append(out.Subscriptions, u.Name)
	}
	out.Subscriptions = append(out.Subscriptions, p.subscribed...)
	out.Members = p.listed
	if p.own {
		out.Subscriptions = append(out.Subscriptions, p.to)
	}
	// A crew member and a manual group may share a name, and both hold
	// subscriptions: the name is told once.
	slices.Sort(out.Subscriptions)
	out.Subscriptions = slices.Compact(out.Subscriptions)
	for _, m := range p.packs {
		in := ""
		if m.rec.Depot != "" {
			in = " in " + m.rec.Depot
		}
		out.Packs = append(out.Packs, m.rec.Pack+in)
	}
	return out
}

// apply writes the plan, in the transaction that read it. Its order is what
// keeps a name from holding two records: what a group of the new name left
// behind goes before what is moved to it.
func (p *groupRename) apply(w *wtx) error {
	if err := w.Bucket(bGroups).Delete([]byte(p.from)); err != nil {
		return err
	}
	if err := put(w.Tx, bGroups, p.to, p.group); err != nil {
		return err
	}
	for _, r := range p.routes {
		if err := put(w.Tx, bRoutes, r.ID, r); err != nil {
			return err
		}
	}
	// A new revision and no new time or actor: the bead is the same one, in
	// the same stay and last changed by whom it was, but its record reads
	// differently, and whoever keeps a bead by its revision (the dispatcher's
	// stays, a client's page) must read it again. It is no mintBead, whose
	// open index is of the status, which a rename leaves.
	for _, row := range p.beads {
		row.Revision = newRevision()
		if err := put(w.Tx, bBeads, row.UUID, row); err != nil {
			return err
		}
	}
	for _, sess := range p.sessions {
		// putSession writes the live index under the session's group as it
		// is now, so the entry it has under the old one goes first, or the
		// session would be listed in both.
		if sess.live() {
			if err := w.Bucket(bSessLive).Delete(sess.liveKey()); err != nil {
				return err
			}
		}
		sess.Group = p.to
		if err := putSession(w.Tx, &sess); err != nil {
			return err
		}
	}
	for _, k := range p.staleGrants {
		if err := w.Bucket(bSourceGrants).Delete([]byte(k)); err != nil {
			return err
		}
	}
	for _, g := range p.grants {
		if err := w.Bucket(bSourceGrants).Delete([]byte(grantKey(g.Source, g.Grantee, g.As))); err != nil {
			return err
		}
		g.Group = p.to
		if err := put(w.Tx, bSourceGrants, grantKey(g.Source, g.Grantee, g.As), g); err != nil {
			return err
		}
	}
	for _, k := range p.staleETA {
		if err := w.Bucket(bETACells).Delete([]byte(k)); err != nil {
			return err
		}
	}
	for _, m := range p.eta {
		if err := w.Bucket(bETACells).Delete([]byte(m.from)); err != nil {
			return err
		}
		if err := w.Bucket(bETACells).Put([]byte(m.to), m.raw); err != nil {
			return err
		}
	}
	for _, r := range p.crew {
		if err := put(w.Tx, bCrew, r.Name, r); err != nil {
			return err
		}
	}
	for _, u := range p.users {
		if err := put(w.Tx, bUsers, u.Name, u); err != nil {
			return err
		}
	}
	for _, g := range p.groups {
		if err := put(w.Tx, bGroups, g.Name, g); err != nil {
			return err
		}
	}
	for _, rec := range p.sent {
		if err := put(w.Tx, bPeerSent, rec.Bead, rec); err != nil {
			return err
		}
	}
	for _, m := range p.packs {
		if err := put(w.Tx, bPacks, m.key, m.rec); err != nil {
			return err
		}
	}
	return nil
}

// RenameGroup changes the name of the group name to to. Everything that names
// the group follows in this one write, and nothing it holds is released: its
// live sessions go on, under the new name, with the beads they work, so the
// runner is not asked for anything. That is its routes, with their further
// groups and conditions; the beads it owns; its settings, members, limit and
// subscriptions, which are its record; what its sessions may call of a
// source; the subscriptions that filter on it; what its arrivals have cost;
// and the pack imports that recorded it, so that the next import of the pack
// finds the renamed group and not the old name. The old name is free
// afterwards. The log, the notes and the sessions that ended keep it.
// Renaming is the operator's alone, as the name is what a script, a role
// prompt or a person reaches a group by (ActRenameGroup). It logs one
// group_renamed.
func (s *Store) RenameGroup(ctx context.Context, name, to, actor string) (*GroupRenamed, error) {
	var out *GroupRenamed
	err := s.write(func(w *wtx) error {
		resolved, err := paneAuthor(w.Tx, callerOf(ctx), actor)
		if err != nil {
			return err
		}
		out, err = w.renameGroup(ctx, name, to, resolved, nil)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// renameGroup is RenameGroup inside the transaction, so that an approved run
// ask renames the group in the write that records its approval: by is then
// the ask it was approved on, which the event carries.
func (w *wtx) renameGroup(ctx context.Context, name, to, actor string, by *approvedOn) (*GroupRenamed, error) {
	if err := w.may(ctx, actor, ActRenameGroup, Object{Kind: KindGroup, Name: name, Ask: Askable}); err != nil {
		return nil, err
	}
	p, err := w.planGroupRename(name, to)
	if err != nil {
		return nil, err
	}
	if err := p.apply(w); err != nil {
		return nil, err
	}
	out := p.report()
	data := map[string]any{"from": name, "to": to, "beads": out.Beads, "sessions": out.Sessions, "grants": out.Grants}
	if len(out.Routes) > 0 {
		data["routes"] = out.Routes
	}
	if len(out.Subscriptions) > 0 {
		data["subscriptions"] = out.Subscriptions
	}
	if len(out.Members) > 0 {
		data["members"] = out.Members
	}
	if len(out.Packs) > 0 {
		data["packs"] = out.Packs
	}
	if by != nil {
		data["approved"] = by
	}
	if err := w.event("", "group_renamed", actor, data); err != nil {
		return nil, err
	}
	return out, nil
}
