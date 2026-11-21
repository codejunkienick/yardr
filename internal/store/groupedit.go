// The writes of a group: PutGroup, which replaces a group whole, and
// SetGroup, which changes what an edit names and keeps the rest. A write
// that moves what a hold read of the group tells its asker beside its
// group_set (runask_stale.go); the settings either write holds to the
// operator are in define.go.
package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// GroupEdit is what SetGroup changes of a group: the config keys of Set
// written, those of Unset removed, and Limit and Members where they are
// given. What it does not name is kept.
type GroupEdit struct {
	Set     map[string]any `json:"set,omitempty"`
	Unset   []string       `json:"unset,omitempty"`
	Limit   *int           `json:"limit,omitempty"`
	Members *[]string      `json:"members,omitempty"`
}

// GroupChange is a group as SetGroup found it and as it left it.
type GroupChange struct {
	Was Group `json:"was"`
	Is  Group `json:"is"`
}

// PutGroup adds or replaces a group, and logs group_set. The cmd, dir,
// env_file, kind, args, prompt and worktrees settings, and a runner change
// to or from exec, are the operator's alone (checkGroupDefine); the rest is
// anyone's. From a pane the write is recorded as the pane's actor (paneAuthor).
func (s *Store) PutGroup(ctx context.Context, g Group, actor string) error {
	return s.write(func(w *wtx) error {
		// Batch reruns this closure, so it writes a copy.
		g := g
		resolved, err := paneAuthor(w.Tx, callerOf(ctx), actor)
		if err != nil {
			return err
		}
		actor = resolved
		return w.delegatedPutGroup(ctx, &g, actor)
	})
}

// putGroup is PutGroup in a transaction: g is stored as it is given but for
// its subscriptions, which are the stored group's. limitExplicit says the
// caller named this limit: SetGroup with a limit in its edit, which 0
// pauses through. A limit of 0 that is not named is no limit named: the
// default of the group's runner (DefaultLimit), except a replace never
// unpauses, so a paused group put back without a limit stays paused. A
// negative limit is refused. Its config is held to ConfigKeys here, whoever
// writes it (the CLI, the group form, a pack); a key the stored group has
// already, with the same value, is kept unasked.
func putGroup(ctx context.Context, w *wtx, g *Group, actor string, limitExplicit bool) error {
	if g.Config == nil {
		g.Config = map[string]any{}
	}
	if g.Limit < 0 {
		return fmt.Errorf("group %s: a limit is 0 or more", g.Name)
	}
	g.Members = dedupeOrdered(g.Members)
	if len(g.Members) > 0 && g.Runner != "manual" {
		return fmt.Errorf("group %s: members are only for manual groups (this one is %s)", g.Name, g.Runner)
	}
	var old Group
	replaced, err := get(w.Tx, bGroups, g.Name, &old)
	if err != nil {
		return err
	}
	if err := checkGroupDefine(ctx, w, old, *g, actor); err != nil {
		return err
	}
	if g.Limit == 0 && !limitExplicit && (!replaced || old.Limit != 0) {
		// 0 is no limit named: the default of the runner, for a group
		// that is new and for a replaced one, as before. A paused group
		// put back without a limit is the exception: the replace keeps
		// the pause instead of unpausing it.
		g.Limit = DefaultLimit(g.Runner)
	}
	if g.Name == RouteWait && !replaced {
		return fmt.Errorf("group wait: wait ends a route (--else wait), so no group takes the name")
	}
	g.Subscriptions = old.Subscriptions
	kept := old.Config
	if old.Runner != g.Runner {
		kept = nil
	}
	if ps := configProblems(GroupConfig, g.Name, g.Runner, g.Config, kept); len(ps) > 0 {
		return ps[0]
	}
	if len(g.Subscriptions) > 0 && g.Runner != "manual" {
		return fmt.Errorf("group %s has subscriptions, which only manual groups may: unsubscribe it first", g.Name)
	}
	if err := checkPeerGroup(w.Tx, g); err != nil {
		return err
	}
	if err := checkLoginFor(w.Tx, g); err != nil {
		return err
	}
	if g.Runner != "herdr" {
		if err := checkQuotaRoutes(w.Tx, g); err != nil {
			return err
		}
	}
	if err := put(w.Tx, bGroups, g.Name, *g); err != nil {
		return err
	}
	data := map[string]any{"name": g.Name, "runner": g.Runner, "limit": g.Limit}
	if len(g.Members) > 0 {
		data["members"] = g.Members
	}
	if err := w.event("", "group_set", actor, data); err != nil {
		return err
	}
	// The tell rides beside the group_set, not through it: a put that moved
	// what a hold read of the group tells its asker in the same write, as a
	// source put does, and one that moved nothing it reads costs no scan.
	var before *Group
	if replaced {
		before = &old
	}
	return w.tellGroupStale(g.Name, before, g, actor)
}

func (s *Store) GetGroup(ctx context.Context, name string) (*Group, error) {
	g := new(Group)
	err := s.read(func(tx *bolt.Tx) error {
		ok, err := get(tx, bGroups, name, g)
		if err == nil && !ok {
			err = fmt.Errorf("group %s: %w", name, ErrNotFound)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return g, nil
}

// DeleteGroup removes the group and the routes that send work to it, those
// that name it after another group too: what is left of such a route would
// say something its operator did not write. Its
// live sessions end as released and their beads go back to routing in the
// same transaction: a bead owned by a deleted group would otherwise sit in
// its stage until released by hand, since no route can claim it. The
// dispatcher's teardown stops the runner resources. It reports how many
// beads it released. It logs one group_removed, which names the routes that
// went with the group, as depot_removed does; a group that was not there
// logs nothing. Removing a group is the operator's or the yardmaster's to
// do (operatorOrYardmaster).
func (s *Store) DeleteGroup(ctx context.Context, name, actor string) (int, error) {
	released := 0
	err := s.write(func(w *wtx) error {
		resolved, err := paneAuthor(w.Tx, callerOf(ctx), actor)
		if err != nil {
			return err
		}
		actor = resolved
		if err := w.packRemoveGroup(ctx, name, actor); err != nil {
			return err
		}
		// Batch reruns this closure when another call in the batch fails, so
		// the count starts over with each attempt.
		_, released, err = w.deleteGroup(name, actor)
		return err
	})
	return released, err
}

// deleteGroup is DeleteGroup inside the transaction, so that removing a user
// takes the groups that carry their login in the write that removes them
// (deleteUser). The caller has checked who may. It answers the ids of the
// routes removed with the group and how many beads it released; a group that
// is not there is nothing removed and logs nothing.
func (w *wtx) deleteGroup(name, actor string) (routes []string, released int, err error) {
	var before Group
	if ok, err := get(w.Tx, bGroups, name, &before); err != nil {
		return nil, 0, err
	} else if !ok {
		return nil, 0, nil
	}
	drop := []string{}
	if err := each(w.Tx, bRoutes, "", func(k []byte, r *Route) error {
		if r.Names(name) {
			drop = append(drop, string(k))
		}
		return nil
	}); err != nil {
		return nil, 0, err
	}
	for _, k := range drop {
		if err := w.Bucket(bRoutes).Delete([]byte(k)); err != nil {
			return nil, 0, err
		}
	}
	live, err := w.store.liveSessions(w.Tx, name)
	if err != nil {
		return nil, 0, err
	}
	reason := "group " + name + " deleted"
	for _, sess := range live {
		owned := false
		if _, err := w.changeBead(sess.Bead, actor, func(w *wtx, b *beadRow) (string, map[string]any, error) {
			if b.Session == nil || *b.Session != sess.ID {
				return "", nil, nil
			}
			owned = true
			return "released", map[string]any{"reason": reason}, w.endSession(b, "released")
		}); err != nil {
			return nil, 0, err
		}
		if owned {
			released++
			continue
		}
		// The bead moved on without this session; end it so it is not live.
		t := now()
		sess.State, sess.Error, sess.EndedAt = "released", &reason, &t
		if err := putSession(w.Tx, &sess); err != nil {
			return nil, 0, err
		}
	}
	if err := w.Bucket(bGroups).Delete([]byte(name)); err != nil {
		return nil, 0, err
	}
	if err := w.event("", "group_removed", actor, map[string]any{
		"name": name, "routes": drop, "released": released}); err != nil {
		return nil, 0, err
	}
	// The removal tells beside its group_removed, with nothing after it: a
	// hold of the group is stale however it read the group (runask_stale.go).
	return drop, released, w.tellGroupStale(name, &before, nil, actor)
}

// SetGroup changes what e names of the group name and keeps the rest, in
// one transaction: PutGroup writes the whole group, so a caller that read
// it, changed a key and put it back would undo what was written in
// between. The result is held to what PutGroup holds a group to. A group
// that is so already is not written and logs nothing; one that changes
// logs group_set, as PutGroup does. From a session's or a crew member's
// pane the write is recorded as that session's actor or that crew member,
// whatever actor says; from anywhere else, as given (paneAuthor).
func (s *Store) SetGroup(ctx context.Context, name string, e GroupEdit, actor string) (*GroupChange, error) {
	if e.Limit != nil && *e.Limit < 0 {
		return nil, fmt.Errorf("group %s: a limit is 0 or more", name)
	}
	var ch *GroupChange
	err := s.write(func(w *wtx) error {
		resolved, err := paneAuthor(w.Tx, callerOf(ctx), actor)
		if err != nil {
			return err
		}
		d, err := w.delegated(ctx, resolved)
		if err != nil {
			return err
		}
		ch, err = w.setGroup(ctx, name, e, resolved, d)
		return err
	})
	if err != nil {
		return nil, err
	}
	return ch, nil
}

// setGroup is SetGroup inside the write, for an actor already resolved: d
// is the delegated run it is, nil for any other, which the group set card's
// approval is (runask_groupset.go).
func (w *wtx) setGroup(ctx context.Context, name string, e GroupEdit, actor string, d *DelegatedCall) (*GroupChange, error) {
	ch := new(GroupChange)
	if ok, err := get(w.Tx, bGroups, name, &ch.Was); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("group %s: %w", name, ErrNotFound)
	}
	// Read again, not copied: the two share no map or slice.
	if _, err := get(w.Tx, bGroups, name, &ch.Is); err != nil {
		return nil, err
	}
	g := &ch.Is
	var cfgErr error
	if g.Config, cfgErr = MergeConfig(name, g.Config, e.Set, e.Unset); cfgErr != nil {
		return nil, cfgErr
	}
	if e.Limit != nil {
		g.Limit = *e.Limit
	}
	if e.Members != nil {
		g.Members = dedupeOrdered(*e.Members)
	}
	// Compared as stored: a list is the same list whatever type it was
	// given as.
	was, err := json.Marshal(ch.Was)
	if err != nil {
		return nil, err
	}
	if is, err := json.Marshal(g); err != nil {
		return nil, err
	} else if bytes.Equal(was, is) {
		ch.Is = ch.Was
		return ch, nil
	}
	if err := putGroup(ctx, w, g, actor, e.Limit != nil); err != nil {
		return nil, err
	}
	if d != nil {
		if err := w.delegatedGroup(d, ch.Was, *g, true); err != nil {
			return nil, err
		}
	}
	return ch, nil
}

// ParseSets reads key=value words into a config: a value that is valid
// JSON is kept as what it is, anything else as the word. The commands
// that take --set read their values through it, and crew set reads its
// words through it after checking their shape itself.
func ParseSets(sets []string) (map[string]any, error) {
	m := map[string]any{}
	for _, kv := range sets {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--set %q: want key=value", kv)
		}
		var js any
		if err := json.Unmarshal([]byte(v), &js); err == nil {
			m[k] = js
		} else {
			m[k] = v
		}
	}
	return m, nil
}

// MergeConfig is a runner config with the keys of sets written over it and
// those of unset removed; config itself is left as it was. A key to unset
// that is not there is refused, naming whose config it is: a misspelt key
// would otherwise read as removed.
func MergeConfig(whose string, config, sets map[string]any, unset []string) (map[string]any, error) {
	out := make(map[string]any, len(config)+len(sets))
	for k, v := range config {
		out[k] = v
	}
	for _, k := range unset {
		if _, is := out[k]; !is {
			has := strings.Join(slices.Sorted(maps.Keys(config)), ", ")
			if has == "" {
				has = "none"
			}
			return nil, fmt.Errorf("--unset %s: %s has no such setting (it has: %s)", k, whose, has)
		}
		delete(out, k)
	}
	for k, v := range sets {
		out[k] = v
	}
	return out, nil
}

func (s *Store) Groups(ctx context.Context) ([]Group, error) {
	out := []Group{}
	err := s.read(func(tx *bolt.Tx) error {
		return each(tx, bGroups, "", func(_ []byte, g *Group) error {
			out = append(out, *g)
			return nil
		})
	})
	return out, err
}
