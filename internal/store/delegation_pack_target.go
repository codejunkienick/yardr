package store

// The value half of a delegated pack record (delegation_pack.go): a
// write the record names is held to what the store's reading of the
// pack would write, not to the object's name alone. Where the reading
// carries the object whole (DelegatedPackObject.Target, built by the
// server from the plan and its mapping answers), the write must be
// that object, the answered fields alone filling what the target
// leaves open. Where it carries none, the changes the reading prints
// for the object are the bound: a changed field's new value must be
// theirs, and a field they do not name must stand as it stood.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// changeNew is the new value the reading's changes name for field,
// ok false where they change no such field.
func changeNew(o *DelegatedPackObject, field string) (string, bool) {
	for _, c := range o.Changes {
		if c.Field == field {
			return c.New, true
		}
	}
	return "", false
}

// planWords is a value as a plan's changes print it (the server's
// planValue): the words a changed field's new value is held to where
// no target stands.
func planWords(v any) string { return fmt.Sprintf("%v", v) }

// packFileArg says whether a prompt or script path the import sets
// names a file of the pack, as the record's file objects name them: the
// installed path, or the file's own name for a depot's role.
func packFileArg(rec *Delegation, value string) bool {
	for _, o := range rec.PackObjects {
		if o.Kind == "file" && (value == o.Name || strings.HasSuffix(value, "/"+o.Name)) {
			return true
		}
	}
	return false
}

// cmdArg is the command word of an exec group's cmd: its variable
// assignments skipped, its quotes taken off.
func cmdArg(cmd string) string {
	for _, word := range strings.Fields(cmd) {
		if strings.Contains(word, "=") && !strings.HasPrefix(word, "/") && !strings.HasPrefix(word, "'") && !strings.HasPrefix(word, "\"") {
			continue
		}
		return strings.Trim(word, "'\"")
	}
	return ""
}

// changedKeys are the config keys whose stored value differs between
// before and after, a key either side lacks among them.
func changedKeys(before, after map[string]any) []string {
	b, a := asStored(before), asStored(after)
	var out []string
	for k := range a {
		if was, ok := b[k]; !ok || !reflect.DeepEqual(was, a[k]) {
			out = append(out, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// answeredKey says whether a config key is one an import's answers
// fill in where the plan leaves it open: the agent's kind, model and
// permission arguments. A target that names none of them leaves the
// key to its answer; one that names it holds the write to it.
func answeredKey(k string) bool { return k == "kind" || k == "model" || k == "args" }

// openKeyStands says whether an answered key a target leaves open may
// take the value after gives it: on a new object its answer fills it
// (a new object's args are held to the presets apart); on one that
// stands it stays as it stood, since answers fill only a new object
// and the plain delegated leg never sets free args (presetOnly).
func openKeyStands(had bool, before, after map[string]any, k string) bool {
	if !had {
		return true
	}
	was, wasSet := asStored(before)[k]
	is, isSet := asStored(after)[k]
	return wasSet == isSet && reflect.DeepEqual(was, is)
}

// packGroupWrite holds a group written under a delegated pack record
// to what an embedded pack carries for it: with a target, its runner,
// limit, members and config as the reading has them; without one, the
// fields and changed values its changes name, a new one only kind,
// model, preset args, prompt, cmd, dir and worktrees. Its prompt and
// script name files of the pack. Anything else is refused as the
// plain delegated leg refuses it.
func (w *wtx) packGroupWrite(ctx context.Context, g *Group, actor string) error {
	rec, _ := w.packRecordFor(ctx, actor)
	if rec == nil {
		return fmt.Errorf("group %s: no started delegated pack record carries this write", g.Name)
	}
	obj := packObject(rec, "group", g.Name)
	if obj == nil {
		return fmt.Errorf("group %s: the delegated pack record %d names no such group to write", g.Name, rec.ID)
	}
	var old Group
	had, err := get(w.Tx, bGroups, g.Name, &old)
	if err != nil {
		return err
	}
	if obj.Target != "" {
		var want Group
		if err := json.Unmarshal([]byte(obj.Target), &want); err != nil {
			return err
		}
		switch {
		case g.Runner != want.Runner:
			return fmt.Errorf("group %s: a delegated pack apply writes its runner as %s, not %s: that is the operator's to type in a terminal", g.Name, want.Runner, g.Runner)
		case g.Limit != want.Limit:
			return fmt.Errorf("group %s: a delegated pack apply writes its limit as %d, not %d: that is the operator's to type in a terminal", g.Name, want.Limit, g.Limit)
		case !slices.Equal(g.Members, want.Members) && !(len(g.Members) == 0 && len(want.Members) == 0):
			return fmt.Errorf("group %s: a delegated pack apply writes its members as %s, not %s: that is the operator's to type in a terminal", g.Name, strings.Join(want.Members, ","), strings.Join(g.Members, ","))
		}
		b := asStored(want.Config)
		for _, k := range changedKeys(want.Config, g.Config) {
			if _, named := b[k]; named || !answeredKey(k) || !openKeyStands(had, old.Config, g.Config, k) {
				return fmt.Errorf("group %s: a delegated pack apply writes %s as its reading has it, not as given: that is the operator's to type in a terminal", g.Name, k)
			}
		}
	} else {
		allowed := map[string]bool{}
		if had {
			for _, f := range obj.Fields {
				allowed[f] = true
			}
			if len(obj.Fields) == 0 && len(obj.Changes) == 0 {
				allowed = map[string]bool{"prompt": true, "cmd": true, "limit": true, "runner": true, "members": true}
			}
			for _, c := range obj.Changes {
				allowed[c.Field] = true
			}
		} else {
			allowed = map[string]bool{"kind": true, "model": true, "args": true, "prompt": true, "cmd": true, "dir": true, "worktrees": true}
		}
		for _, k := range changedKeys(old.Config, g.Config) {
			if had && !allowed[k] {
				return fmt.Errorf("group %s: a delegated pack apply does not change %s on it: that is the operator's to type in a terminal", g.Name, k)
			}
			if !had && !allowed[k] {
				return fmt.Errorf("group %s: a delegated pack apply does not set %s on it: that is the operator's to type in a terminal", g.Name, k)
			}
			if had {
				if nv, ok := changeNew(obj, k); ok && planWords(asStored(g.Config)[k]) != nv {
					return fmt.Errorf("group %s: a delegated pack apply writes %s as %s, not as given: that is the operator's to type in a terminal", g.Name, k, nv)
				}
			}
		}
		if had && g.Runner != old.Runner {
			if nv, ok := changeNew(obj, "runner"); !allowed["runner"] || (ok && nv != g.Runner) || (!ok && len(obj.Changes) > 0) {
				return fmt.Errorf("group %s: a delegated pack apply does not change its runner: that is the operator's to type in a terminal", g.Name)
			}
		}
		if had && g.Limit != old.Limit {
			nv, ok := changeNew(obj, "limit")
			if !ok || nv != fmt.Sprint(g.Limit) {
				return fmt.Errorf("group %s: a delegated pack apply writes its limit as it stood, not %d: that is the operator's to type in a terminal", g.Name, g.Limit)
			}
		}
		if had && !slices.Equal(g.Members, old.Members) && !(len(g.Members) == 0 && len(old.Members) == 0) {
			nv, ok := changeNew(obj, "members")
			if !ok || nv != planWords(g.Members) {
				return fmt.Errorf("group %s: a delegated pack apply writes its members as they stood, not as given: that is the operator's to type in a terminal", g.Name)
			}
		}
	}
	if !had {
		if v, ok := asStored(g.Config)["args"]; ok {
			kind, _ := asStored(g.Config)["kind"].(string)
			if err := presetOnly("group "+g.Name, kind, v, true); err != nil {
				return err
			}
		}
	}
	if v, ok := asStored(g.Config)["prompt"].(string); ok && v != "" && !packFileArg(rec, v) {
		return fmt.Errorf("group %s: its prompt %s is no file of the pack: a delegated pack apply sets only the pack's own roles", g.Name, v)
	}
	if v, ok := asStored(g.Config)["cmd"].(string); ok && v != "" && !packFileArg(rec, cmdArg(v)) {
		return fmt.Errorf("group %s: its cmd runs %s, which is no script of the pack: a delegated pack apply sets only the pack's own scripts", g.Name, cmdArg(v))
	}
	return nil
}

// subscriptionTarget is one subscription operation as a delegated
// pack's reading names it: the crew member, the subscription to add and,
// for a scope change, the one it replaces.
type subscriptionTarget struct {
	Crew   string        `json:"crew"`
	Add    *Subscription `json:"add,omitempty"`
	Remove *Subscription `json:"remove,omitempty"`
}

// packSubscriptionChanges holds a crew member's subscription list under a
// delegated pack record to the exact additions and removals its plan names.
func (w *wtx) packSubscriptionChanges(ctx context.Context, name string, before, after []Subscription, actor string) error {
	if _, pack := DelegatedPackOf(ctx); !pack {
		return nil
	}
	rec, _ := w.packRecordFor(ctx, actor)
	if rec == nil {
		return fmt.Errorf("crew member %s: no started delegated pack record carries this subscription write", name)
	}
	removed := subscriptionDifference(before, after)
	added := subscriptionDifference(after, before)
	for _, change := range []struct {
		values []Subscription
		add    bool
	}{{removed, false}, {added, true}} {
		for _, sub := range change.values {
			allowed := false
			for i := range rec.PackObjects {
				o := &rec.PackObjects[i]
				if o.Kind != "subscription" || (o.Action != "create" && o.Action != "change") {
					continue
				}
				var want subscriptionTarget
				if err := json.Unmarshal([]byte(o.Target), &want); err != nil {
					return err
				}
				if want.Crew != name {
					continue
				}
				candidate := want.Remove
				if change.add {
					candidate = want.Add
				}
				if candidate != nil && candidate.same(sub) {
					allowed = true
					break
				}
			}
			if !allowed {
				word := "remove"
				if change.add {
					word = "add"
				}
				return fmt.Errorf("crew member %s: a delegated pack apply does not %s subscription %s: that is the operator's to type in a terminal", name, word, sub.String())
			}
		}
	}
	return nil
}

// subscriptionDifference returns each subscription in after that has no
// equal entry in before, keeping duplicate entries matched one at a time.
func subscriptionDifference(after, before []Subscription) []Subscription {
	used := make([]bool, len(before))
	var out []Subscription
	for _, candidate := range after {
		found := false
		for i, old := range before {
			if !used[i] && old.same(candidate) {
				used[i], found = true, true
				break
			}
		}
		if !found {
			out = append(out, candidate)
		}
	}
	return out
}

// crewTarget is a crew member object of a reading's plan, as its
// target stands on a record: the definition the import writes.
type crewTarget struct {
	Config         map[string]any `json:"config"`
	Dir            string         `json:"dir"`
	On             []string       `json:"on"`
	Runner         string         `json:"runner"`
	MaxAge         string         `json:"max_age"`
	HandoffGrace   string         `json:"handoff_grace"`
	ContextHandoff string         `json:"context_handoff,omitempty"`
}

// packCrewWrite holds a crew member written under a delegated pack
// record to what an embedded pack carries for it: with a target, its
// config, directory and what it hears as the reading has them;
// without one, an existing member only its prompt and what it hears
// as its changes name, a new one only kind, model, preset args and
// prompt. A new member's directory is the yard's home, where a
// delegated run's answers put it (packAnswers, mappingAnswers). The
// yardmaster's own entry is held to the same fields as any other, so
// naming it in a pack opens none of its defining keys.
func (w *wtx) packCrewWrite(ctx context.Context, def *CrewMember, actor string) error {
	rec, _ := w.packRecordFor(ctx, actor)
	if rec == nil {
		return fmt.Errorf("crew member %s: no started delegated pack record carries this write", def.Name)
	}
	obj := packObject(rec, "crew", def.Name)
	if obj == nil {
		return fmt.Errorf("crew member %s: the delegated pack record %d names no such crew member to write", def.Name, rec.ID)
	}
	var old CrewMember
	had, err := get(w.Tx, bCrew, def.Name, &old)
	if err != nil {
		return err
	}
	if obj.Target != "" {
		var want crewTarget
		if err := json.Unmarshal([]byte(obj.Target), &want); err != nil {
			return err
		}
		switch {
		case want.Runner != "" && def.Runner != want.Runner:
			return fmt.Errorf("crew member %s: a delegated pack apply writes its runner as %s, not %s: that is the operator's to type in a terminal", def.Name, want.Runner, def.Runner)
		case def.Dir != want.Dir:
			return fmt.Errorf("crew member %s: a delegated pack apply writes its directory as %s, not %s: that is the operator's to type in a terminal", def.Name, want.Dir, def.Dir)
		case !slices.Equal(def.On, want.On) && !(len(def.On) == 0 && len(want.On) == 0):
			return fmt.Errorf("crew member %s: a delegated pack apply writes what it hears as %s, not as given: that is the operator's to type in a terminal", def.Name, strings.Join(want.On, ","))
		case want.MaxAge != def.MaxAge || want.HandoffGrace != def.HandoffGrace || want.ContextHandoff != def.ContextHandoff:
			return fmt.Errorf("crew member %s: a delegated pack apply writes its rotation as its reading has it, not as given: that is the operator's to type in a terminal", def.Name)
		}
		b := asStored(want.Config)
		for _, k := range changedKeys(want.Config, def.Config) {
			if _, named := b[k]; named || !answeredKey(k) || !openKeyStands(had, old.Config, def.Config, k) {
				return fmt.Errorf("crew member %s: a delegated pack apply writes %s as its reading has it, not as given: that is the operator's to type in a terminal", def.Name, k)
			}
		}
	} else {
		allowed := map[string]bool{"prompt": true}
		if !had {
			allowed = map[string]bool{"kind": true, "model": true, "args": true, "prompt": true}
		}
		for _, k := range changedKeys(old.Config, def.Config) {
			if !allowed[k] {
				return fmt.Errorf("crew member %s: a delegated pack apply does not change %s on it: that is the operator's to type in a terminal", def.Name, k)
			}
			if had {
				if nv, ok := changeNew(obj, k); ok && planWords(asStored(def.Config)[k]) != nv {
					return fmt.Errorf("crew member %s: a delegated pack apply writes %s as %s, not as given: that is the operator's to type in a terminal", def.Name, k, nv)
				}
			}
		}
		if had && (def.Dir != old.Dir || def.Runner != old.Runner || def.MaxAge != old.MaxAge || def.HandoffGrace != old.HandoffGrace || def.ContextHandoff != old.ContextHandoff) {
			return fmt.Errorf("crew member %s: a delegated pack apply does not change its dir, runner or rotation: that is the operator's to type in a terminal", def.Name)
		}
		if had && !slices.Equal(def.On, old.On) && !(len(def.On) == 0 && len(old.On) == 0) {
			nv, ok := changeNew(obj, "on")
			if !ok || nv != planWords(def.On) {
				return fmt.Errorf("crew member %s: a delegated pack apply writes what it hears as it stood, not as given: that is the operator's to type in a terminal", def.Name)
			}
		}
		if !had && def.Dir != "" && w.home != "" && def.Dir != w.home {
			return fmt.Errorf("crew member %s: a delegated pack apply works a new member in the yard's home, not %s: that is the operator's to type in a terminal", def.Name, def.Dir)
		}
	}
	if !had {
		if v, ok := asStored(def.Config)["args"]; ok {
			kind, _ := asStored(def.Config)["kind"].(string)
			if err := presetOnly("crew member "+def.Name, kind, v, true); err != nil {
				return err
			}
		}
	}
	if v, ok := asStored(def.Config)["prompt"].(string); ok && v != "" && !packFileArg(rec, v) {
		return fmt.Errorf("crew member %s: its prompt %s is no file of the pack: a delegated pack apply sets only the pack's own roles", def.Name, v)
	}
	return nil
}

// mapTarget is a stage map object of a reading's plan, as its target
// stands on a record: what the import sets on its hook.
type mapTarget struct {
	Hook      string            `json:"hook"`
	Source    string            `json:"source"`
	As        string            `json:"as"`
	Map       map[string]string `json:"map"`
	Says      []Say             `json:"says,omitempty"`
	Makes     []Make            `json:"makes,omitempty"`
	DropSays  []Say             `json:"drop_says,omitempty"`
	DropMakes []Make            `json:"drop_makes,omitempty"`
}

// mergedLines merges a hook's table as a pack import does (pack's
// merged): the pack's lines keep their places, its dropped lines go,
// its new lines go where it wrote them among those that stand.
func mergedLines[L fmt.Stringer](have, drop, want []L) []L {
	lineSet := func(lines []L) map[string]bool {
		out := make(map[string]bool, len(lines))
		for _, l := range lines {
			out[l.String()] = true
		}
		return out
	}
	dropped, wanted, on := lineSet(drop), lineSet(want), lineSet(have)
	var fill []L
	add := [][]L{nil}
	seen := map[string]bool{}
	for _, l := range want {
		k := l.String()
		switch {
		case seen[k]:
		case on[k]:
			fill = append(fill, l)
			add = append(add, nil)
		default:
			add[len(add)-1] = append(add[len(add)-1], l)
		}
		seen[k] = true
	}
	out := make([]L, 0, len(have)+len(want))
	n := 0
	placed := map[string]bool{}
	for _, l := range have {
		switch k := l.String(); {
		case wanted[k]:
			if placed[k] {
				continue
			}
			placed[k] = true
			if n < len(fill) {
				out = append(append(out, add[n]...), fill[n])
				n++
			}
		case dropped[k]:
		default:
			out = append(out, l)
		}
	}
	return append(out, add[len(fill)]...)
}

// mapWords is a hook's map as its change line prints it, sorted.
func mapWords(m map[string]string) string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	slices.Sort(out)
	return strings.Join(out, " ")
}

// packHookWrite holds a hook written under a delegated pack record to
// the stage map the record names for it: with a target, its as, map,
// says and makes as the import would leave them among what the hook
// has; without one, each as its change line names it. Its source,
// secret file and unmapped-sender policy never ride a record.
func (w *wtx) packHookWrite(ctx context.Context, h *Hook, actor string) error {
	rec, _ := w.packRecordFor(ctx, actor)
	if rec == nil {
		return fmt.Errorf("hook %s: no started delegated pack record carries this write", h.Name)
	}
	var obj *DelegatedPackObject
	for i := range rec.PackObjects {
		o := &rec.PackObjects[i]
		if o.Kind == "map" && (o.Name == "hook "+h.Name || o.Name == "hook "+h.Name+" as "+h.As) && (o.Action == "create" || o.Action == "change") {
			obj = o
		}
	}
	if obj == nil {
		return fmt.Errorf("hook %s: the delegated pack record %d names no stage map for it", h.Name, rec.ID)
	}
	var was Hook
	had, err := get(w.Tx, bHooks, h.Name, &was)
	if err != nil {
		return err
	}
	if !had {
		return fmt.Errorf("hook %s: %w", h.Name, ErrNotFound)
	}
	if h.Source != was.Source || h.SecretFile != was.SecretFile || h.Unknown != was.Unknown {
		return fmt.Errorf("hook %s: a delegated pack apply does not change its source, secret file or unmapped-sender policy: that is the operator's to type in a terminal", h.Name)
	}
	if obj.Target != "" {
		var want mapTarget
		if err := json.Unmarshal([]byte(obj.Target), &want); err != nil {
			return err
		}
		as, mp := was.As, was.Map
		if want.As != "" {
			as = want.As
			if want.Map != nil {
				mp = want.Map
			}
		}
		says, makes := was.Says, was.Makes
		if len(want.Says) > 0 || len(want.DropSays) > 0 {
			says = mergedLines(was.Says, want.DropSays, want.Says)
		}
		if len(want.Makes) > 0 || len(want.DropMakes) > 0 {
			makes = mergedLines(was.Makes, want.DropMakes, want.Makes)
		}
		switch {
		case h.As != as:
			return fmt.Errorf("hook %s: a delegated pack apply acts as %s on it, not %s: that is the operator's to type in a terminal", h.Name, as, h.As)
		case !maps.Equal(h.Map, mp):
			return fmt.Errorf("hook %s: a delegated pack apply writes its map as its reading has it, not as given: that is the operator's to type in a terminal", h.Name)
		case !SameSays(h.Says, says):
			return fmt.Errorf("hook %s: a delegated pack apply writes its says as its reading has them, not as given: that is the operator's to type in a terminal", h.Name)
		case !SameMakes(h.Makes, makes):
			return fmt.Errorf("hook %s: a delegated pack apply writes its makes as its reading has them, not as given: that is the operator's to type in a terminal", h.Name)
		}
		return nil
	}
	if h.As != was.As {
		if nv, ok := changeNew(obj, "as"); !ok || nv != h.As {
			return fmt.Errorf("hook %s: a delegated pack apply acts as the hook does, not %s: that is the operator's to type in a terminal", h.Name, h.As)
		}
	}
	if !maps.Equal(h.Map, was.Map) {
		if nv, ok := changeNew(obj, "map"); !ok || nv != mapWords(h.Map) {
			return fmt.Errorf("hook %s: a delegated pack apply writes its map as it stood, not as given: that is the operator's to type in a terminal", h.Name)
		}
	}
	if !SameSays(h.Says, was.Says) {
		if nv, ok := changeNew(obj, "says"); !ok || nv != strings.Join(SayLines(h.Says), "; ") {
			return fmt.Errorf("hook %s: a delegated pack apply writes its says as they stood, not as given: that is the operator's to type in a terminal", h.Name)
		}
	}
	if !SameMakes(h.Makes, was.Makes) {
		if nv, ok := changeNew(obj, "makes"); !ok || nv != strings.Join(MakeLines(h.Makes), "; ") {
			return fmt.Errorf("hook %s: a delegated pack apply writes its makes as they stood, not as given: that is the operator's to type in a terminal", h.Name)
		}
	}
	return nil
}

// packHookSpeakAs leaves a delegated map's exact speak-as permission to
// packHookWrite; every other hook write uses the ordinary policy gate.
func (w *wtx) packHookSpeakAs(ctx context.Context, actor, name string) error {
	if _, pack := DelegatedPackOf(ctx); pack {
		return nil
	}
	return w.may(ctx, actor, ActSpeakAs, Object{Kind: KindCrew, Name: name})
}
