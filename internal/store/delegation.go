// A delegated run (design yardr-m90n 3): the yardmaster running one of an
// operator's defining commands on that operator's own mail, recorded as
// the yardmaster acting for them and told to them after the fact instead
// of approved before it. The switch is yardmaster_delegation (3.1). The
// leg stands beside the operator's (delegable), never wider: a gate
// whose deed its site marks Delegate passes the yardmaster the
// yardmaster setting names when the call carries a delegation
// (WithDelegation) whose cited mail is an operator's local mail to the
// yardmaster (3.6), and nothing else changes. Every delegated write keeps
// a record in its own bucket, whose one read is an operator's (3.4),
// leaves the yardmaster's journal a pointer to it, and mails the operator
// what ran, in the write that runs the command.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// DelegationSetting is the switch (design yardr-m90n 3.1): whether the
// yardmaster may run the delegated commands for an operator on their
// cited mail. Unset, it reads as hosted reads. It is Alone (settingAsk's
// default), and the run ask's one exception is settingSetAsk's.
const DelegationSetting = "yardmaster_delegation"

// yardmasterDelegation is the switch's entry, added to KnownSettings
// beside the others.
var yardmasterDelegation = Setting{Key: DelegationSetting, Help: "whether the yardmaster may run an operator's defining commands for them on that operator's own mail to it, " +
	"each recorded (yardr delegation show) and mailed to the operator in the write that runs it (true), or not (false): group add and group set of kind, model, dir, worktrees and preset args, " +
	"crew set of the same for a member other than the yardmaster, depot test and depot base, user set notify=, and hook add in its safe shape. " +
	"Unset, it reads as hosted does: on for a hosted yard, off for one you run yourself. An operator turns it off or on with a run ask " +
	"(yardr bead ask --run \"settings set yardmaster_delegation false\"), or on the hosted web's Config page; the yardmaster never changes it",
	Check: func(v string) error {
		if v != "true" && v != "false" {
			return fmt.Errorf("%s %q: true or false", DelegationSetting, v)
		}
		return nil
	}}

// delegationOn reads the switch: as set, else as hosted reads.
func delegationOn(tx *bolt.Tx) (bool, error) {
	var v string
	if ok, err := get(tx, bSettings, DelegationSetting, &v); err != nil || ok {
		return v == "true", err
	}
	if _, err := get(tx, bSettings, HostedSetting, &v); err != nil {
		return false, err
	}
	return v == "true", nil
}

// settingSetAsk is the mark a settings set proposal carries: the switch's
// two values are Askable, so an operator with no terminal turns it off or
// back on by approving a card (3.1); unsetting it, and every other key,
// keep settingAsk's mark.
func settingSetAsk(key, value string) AskClass {
	if key == DelegationSetting && (value == "true" || value == "false") {
		return Askable
	}
	return settingAsk(key)
}

// settingSetWeight weighs a settings set card: turning delegation on is
// trust, so its approval takes the fresh sign-in a strong card takes;
// turning it off narrows, and every other setting is routine as before.
func settingSetWeight(_ *wtx, run AskRun) ([]AskWeight, error) {
	if run.Argv[2] != DelegationSetting || run.Argv[3] != "true" {
		return nil, nil
	}
	return []AskWeight{weighs(WeightTrust, "the yardmaster may run the operator's defining commands for them on their mail, told after they run")}, nil
}

// yardmasterIn is the crew member the yardmaster setting names, the
// default where it is unset.
func yardmasterIn(tx *bolt.Tx) (string, error) {
	var ym string
	if _, err := get(tx, bSettings, YardmasterSetting, &ym); err != nil {
		return "", err
	}
	return or(ym, "yardmaster"), nil
}

// DelegatedCall is what a delegated run's call says of itself: the
// operator it is for, the seq of their mail it runs on, and the command
// as the yardmaster typed it, which the record keeps whole. The store
// holds the first two to its records in the write (delegatedIn); the
// command is the caller's words, beside the changes the store itself
// reads (Delegation.Changes).
type DelegatedCall struct {
	For     string `json:"for"`
	Mail    uint64 `json:"mail"`
	Command string `json:"command,omitempty"`
}

type delegationKey struct{}

// WithDelegation records on ctx that the call is a delegated run: what
// the CLI does for --for and --mail, and the socket's server for the
// header the call crossed with (rpc's DelegationHeader).
func WithDelegation(ctx context.Context, d DelegatedCall) context.Context {
	return context.WithValue(ctx, delegationKey{}, d)
}

// DelegationOf is the delegated run the call of ctx says it is, ok false
// for every other call.
func DelegationOf(ctx context.Context) (d DelegatedCall, ok bool) {
	d, ok = ctx.Value(delegationKey{}).(DelegatedCall)
	return d, ok
}

// maxDelegatedCommand bounds the command words a delegated call carries:
// a terminal line, never a file's worth.
const maxDelegatedCommand = 16 << 10

// delegatedIn is the delegated run ctx carries, held in tx to what opens
// the leg (3.6): the actor is the yardmaster through its pane
// (paneAuthor), the user it is for holds the operator role, and the cited
// event is a mail from that operator to the yardmaster with no mark of
// another yard, a session or a timer on it. It is nil where ctx carries
// none, and where the switch is off: the call is then judged as any call
// of the yardmaster's, in today's words. A delegation that fails any part
// refuses the run whole.
func delegatedIn(ctx context.Context, tx *bolt.Tx, actor string) (*DelegatedCall, error) {
	d, ok := DelegationOf(ctx)
	if !ok {
		return nil, nil
	}
	if on, err := delegationOn(tx); err != nil || !on {
		return nil, err
	}
	resolved, err := paneAuthor(tx, callerOf(ctx), actor)
	if err != nil {
		return nil, err
	}
	ym, err := yardmasterIn(tx)
	if err != nil {
		return nil, err
	}
	if resolved != ym || tx.Bucket(bCrew).Get([]byte(ym)) == nil {
		return nil, fmt.Errorf("a delegated run (--for) is the yardmaster's (%s) alone; this is %s", ym, or(resolved, "nobody"))
	}
	if u, err := getUser(tx, d.For); err != nil || !u.IsOperator() {
		return nil, fmt.Errorf("a delegated run is for an operator, and %s holds no operator role here", or(d.For, "nobody"))
	}
	if len(d.Command) > maxDelegatedCommand {
		return nil, fmt.Errorf("a delegated run's command is %d bytes, over %d", len(d.Command), maxDelegatedCommand)
	}
	if d.Mail == 0 {
		return nil, fmt.Errorf("a delegated run cites the mail of %s's it runs on: --mail <seq>", d.For)
	}
	var e Event
	if ok, err := get(tx, bEvents, seqKey(d.Mail), &e); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("a delegated run cites mail %d, and the yard has no event %d", d.Mail, d.Mail)
	}
	switch {
	case e.Kind != "mail":
		return nil, fmt.Errorf("a delegated run cites mail %d, and event %d is a %s event, not mail", d.Mail, d.Mail, e.Kind)
	case MailReceiver(e) != ym:
		return nil, fmt.Errorf("a delegated run cites mail %d, which is not mail to the yardmaster (%s)", d.Mail, ym)
	case e.Actor != d.For:
		return nil, fmt.Errorf("a delegated run for %s cites mail %d, which is from %s, not from %s", d.For, d.Mail, or(e.Actor, "nobody"), d.For)
	}
	for _, mark := range []string{"from_peer", "from_peer_unlinked", "as", "from_bead", "timer"} {
		if _, marked := e.Data[mark]; marked {
			return nil, fmt.Errorf("a delegated run cites mail %d, which is no local mail of %s's (it carries %s): only an operator's own mail to the yardmaster opens a delegated run", d.Mail, d.For, mark)
		}
	}
	return &d, nil
}

// delegated is delegatedIn in this write.
func (w *wtx) delegated(ctx context.Context, actor string) (*DelegatedCall, error) {
	return delegatedIn(ctx, w.Tx, actor)
}

// delegable is r with the delegated leg beside it: where r refuses a deed
// its site marks Delegate (Object.Delegate), a call that carries a
// delegation passing delegatedIn is let through; any other refusal of r
// stands in r's own words. It widens no other rule, and no other deed.
func delegable(r rule) rule {
	return func(ctx context.Context, tx *bolt.Tx, actor string, act Action, obj Object) error {
		err := r(ctx, tx, actor, act, obj)
		if err == nil || !obj.Delegate {
			return err
		}
		d, derr := delegatedIn(ctx, tx, actor)
		if derr != nil {
			return derr
		}
		if d == nil {
			return err
		}
		return nil
	}
}

// groupDelegates are the group settings the delegated leg carries
// (3.2); cmd, prompt and env_file and a runner change to or from exec
// stay the operator's (checkGroupDefine marks them so).
var groupDelegates = map[string]bool{"kind": true, "model": true, "dir": true, "worktrees": true, "args": true}

// crewDelegates are the crew settings it carries, the directory beside
// them, for a member other than the yardmaster (crewDelegate).
var crewDelegates = map[string]bool{"kind": true, "model": true, "args": true, "dir": true}

// crewDelegate says whether the delegated leg carries key of the crew
// member name: never on the yardmaster's own entry, whose teller would be
// the changed party (3.3).
func (w *wtx) crewDelegate(name, key string) bool {
	ym, err := yardmasterIn(w.Tx)
	return err == nil && crewDelegates[key] && name != ym
}

// presetOnly refuses args that are not exactly one named permission
// preset of kind: a delegated run names a preset (preset=<label>), and
// what runs is a flag set the yard names, never words the yardmaster
// typed (3.2). Args removed are none, which is no preset.
func presetOnly(subject, kind string, args any, has bool) error {
	if !has {
		return nil
	}
	words, _ := args.([]any)
	given := make([]string, 0, len(words))
	for _, w := range words {
		s, ok := w.(string)
		if !ok {
			given = nil
			break
		}
		given = append(given, s)
	}
	if given != nil {
		for _, p := range HarnessPresets[kind] {
			if !slices.Equal(p.Args, given) {
				continue
			}
			if skipsAsking(p.Args) {
				return fmt.Errorf("%s: the %s preset %s skips the agent's permission prompts, and a delegated run never sets one: it is the operator's to type in a terminal", subject, kind, p.Label)
			}
			return nil
		}
	}
	return fmt.Errorf("%s: a delegated run never carries free args: name one of the %s presets as preset=<preset> (%s)",
		subject, or(kind, "kind's"), or(strings.Join(HarnessPresetLabels(kind), ", "), "it has none"))
}

// skipsAsking says whether a preset's args let the agent act without its
// permission prompts: the flags design yardr-m90n 3.5 names for the bypass
// presets and their like, which the delegated leg refuses (3.2). Bead 3
// moves this to a Strong mark on HarnessPresets; until then the flags
// themselves are read, so no such preset runs told-after meanwhile.
func skipsAsking(args []string) bool {
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--dangerously-"), strings.HasPrefix(a, "--trust-tools"):
			return true
		case a == "--always-approve", a == "--yolo", a == "--auto", a == "--trust-all-tools", a == "--disable-approval":
			return true
		}
	}
	return false
}

// delegatedDefine holds the delegated keys of a definition to what the leg
// carries them as (3.2): args one preset of the kind the definition runs
// on, dir and worktrees a place the yard knows. The keys themselves were
// let through by their gates (delegable); this reads their values.
func (w *wtx) delegatedDefine(subject string, before, after map[string]any, keys []string) error {
	b, a := asStored(before), asStored(after)
	kind, _ := a["kind"].(string)
	if kind == "" {
		if _, err := get(w.Tx, bSettings, DefaultKindSetting, &kind); err != nil {
			return err
		}
		if kind == "" {
			setting, _ := SettingOf(DefaultKindSetting)
			kind = setting.Default
		}
	}
	oldKind, hadKind := b["kind"]
	newKind, hasKind := a["kind"]
	kindChanged := hadKind != hasKind || !reflect.DeepEqual(oldKind, newKind)
	oldArgs, hadArgs := b["args"]
	newArgs, hasArgs := a["args"]
	argsChanged := hadArgs != hasArgs || !reflect.DeepEqual(oldArgs, newArgs)
	if kindChanged || argsChanged {
		if err := presetOnly(subject, kind, newArgs, hasArgs); err != nil {
			return err
		}
	}
	for _, key := range keys {
		was, had := b[key]
		is, has := a[key]
		if had == has && reflect.DeepEqual(was, is) {
			continue
		}
		switch key {
		case "dir", "worktrees":
			if dir, ok := is.(string); has && ok {
				if _, err := w.knownDir(subject, key, dir, key+"=<dir>"); err != nil {
					return err
				}
			}
		case "kind":
			if k, ok := is.(string); has && (!ok || HarnessLabels[k] == "") {
				return fmt.Errorf("%s: kind %v is no harness kind the yard knows", subject, is)
			}
		}
	}
	return nil
}

// Delegation is a delegated run's record (3.4): who ran what for whom,
// on which mail, what stood before and how it is taken back. Its one read
// is an operator's (Store.Delegation); everyone else is told it is not
// there. Command, Changes and Back stand inline while they fit in
// MaxDelegationInline, and in one blob past it (Blob), which only
// Store.DelegationBlob serves.
type Delegation struct {
	ID         uint64    `json:"id"`
	Yardmaster string    `json:"yardmaster"`
	For        string    `json:"for"`
	At         time.Time `json:"at"`
	// Mail is the seq of the operator's mail the run cites.
	Mail uint64 `json:"mail"`
	// Of is what the run changed: "group reviewers", "hook linear".
	Of string `json:"of"`
	DelegatedBody
	Blob *BlobRef `json:"blob,omitempty"`
}

// DelegatedBody is the part of a record that may be held in its blob: the
// command as typed, the store's own reading of what changed, and the
// command that takes it back, or the words that say there is none.
type DelegatedBody struct {
	Command string            `json:"command,omitempty"`
	Changes []DelegatedChange `json:"changes,omitempty"`
	Back    string            `json:"back,omitempty"`
}

// DelegatedChange is one setting a delegated run changed, as it was and as
// it is, a value that is no string written as JSON; Had and Has say it was
// and is set at all.
type DelegatedChange struct {
	Key string `json:"key"`
	Was string `json:"was,omitempty"`
	Had bool   `json:"had,omitempty"`
	Is  string `json:"is,omitempty"`
	Has bool   `json:"has,omitempty"`
}

// MaxDelegationInline is how large a record's body stands inline: the
// bound a journal entry lives under, taken only as a size (3.4).
const MaxDelegationInline = MaxJournalBytes

var bDelegations = []byte("delegations") // seq: Delegation

func init() { allBuckets = append(allBuckets, bDelegations) }

// valueWords is a setting's value as a record and a take-back write it.
func valueWords(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// configChanged is what changed between two configs, key by key in order,
// the fields named in extra first.
func configChanged(before, after map[string]any, extra ...DelegatedChange) []DelegatedChange {
	b, a := asStored(before), asStored(after)
	keys := slices.Sorted(maps.Keys(b))
	for k := range a {
		if _, ok := b[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := slices.DeleteFunc(extra, func(c DelegatedChange) bool { return c.Was == c.Is && c.Had == c.Has })
	for _, k := range keys {
		was, had := b[k]
		is, has := a[k]
		if had == has && reflect.DeepEqual(was, is) {
			continue
		}
		out = append(out, DelegatedChange{Key: k, Was: valueWords(was), Had: had, Is: valueWords(is), Has: has})
	}
	return out
}

// configBack is the set command that puts back what changes changed of a
// config: each key that was set to its old value, each that was not
// unset, the fields in fields named as their flags.
func configBack(command string, changes []DelegatedChange, fields map[string]string) string {
	words := []string{"yardr", command}
	for _, c := range changes {
		switch flag := fields[c.Key]; {
		case flag != "":
			words = append(words, flag, shellQuote(c.Was))
		case c.Had:
			words = append(words, shellQuote(c.Key+"="+c.Was))
		default:
			words = append(words, "--unset", c.Key)
		}
	}
	return strings.Join(words, " ")
}

// recordDelegated writes the record of the delegated run d, which changed
// of, with body, in this write: the record in its bucket (its body in one
// blob where it passes MaxDelegationInline), a pointer on the
// yardmaster's journal naming its id alone, and mail to the operator it
// was for. The change's own event carries none of it (3.4).
func (w *wtx) recordDelegated(d *DelegatedCall, of string, body DelegatedBody) error {
	ym, err := yardmasterIn(w.Tx)
	if err != nil {
		return err
	}
	body.Command = d.Command
	rec := Delegation{Yardmaster: ym, For: d.For, At: now(), Mail: d.Mail, Of: of, DelegatedBody: body}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(body.Changes))
	for _, c := range body.Changes {
		keys = append(keys, c.Key)
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
	var r CrewMember
	if ok, err := get(w.Tx, bCrew, ym, &r); err != nil {
		return err
	} else if ok && r.Journal != "" {
		pointer := fmt.Sprintf("yardmaster for %s: delegated record %d (an operator reads it: yardr delegation show %d)", d.For, rec.ID, rec.ID)
		if err := w.addNoteSession(r.Journal, NoteJournal, pointer, ym, ""); err != nil {
			return err
		}
	}
	return w.event("", "mail", "yard", map[string]any{"to": d.For, "to_user": true, "text": delegatedMail(rec, keys)})
}

// delegatedMail is the telling (3.4): what ran for the operator, on which
// of their mails, the record's id, the keys it changed and how it is taken
// back. The command is the caller's words, so the keys are the store's own
// reading beside them: a command that says less than ran is caught by the
// keys. The values that stood before stand in the record alone.
func delegatedMail(rec Delegation, keys []string) string {
	command, back := rec.Command, rec.Back
	if rec.Blob != nil {
		command, back = "(too long for mail: it is in the record)", "in the record"
	}
	if len(command) > MaxMailBytes/2 {
		command = command[:MaxMailBytes/2] + "…"
	}
	text := fmt.Sprintf("yardmaster for %s ran, on your mail %d (record %d, which yardr delegation show %d reads):\n  %s\nchanged: %s",
		rec.For, rec.Mail, rec.ID, rec.ID, or(command, "(no command words were sent)"), rec.Of)
	if len(keys) > 0 {
		text += " (" + strings.Join(keys, ", ") + ")"
	}
	if back != "" {
		text += "\ntaken back by: " + back
	}
	if len(text) > MaxMailBytes {
		text = text[:MaxMailBytes-len("…")] + "…"
	}
	return text
}

// keepDelegationBlob keeps a record's body as one blob in this write,
// under the yard's cap (BlobMaxSetting): the record names it, so the
// sweep keeps it while the record stands, and a write that rolls back
// leaves it unnamed, swept after the grace any unnamed blob gets. It
// takes blobFileMu and not blobMu, which a put holds while it writes the
// store: this write holds the store already.
func (w *wtx) keepDelegationBlob(data []byte) (*BlobRef, error) {
	if w.store == nil {
		return nil, fmt.Errorf("a delegated record's blob needs the store")
	}
	limit, err := bytesSetting(w.Tx, BlobMaxSetting, DefaultBlobMax)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("a delegated run's record is %d bytes, over the yard's blob cap of %d (%s): this run is the operator's to type in a terminal", len(data), limit, BlobMaxSetting)
	}
	sum := sha256.Sum256(data)
	w.store.blobFileMu.Lock()
	defer w.store.blobFileMu.Unlock()
	return w.store.keepBlob(&BlobRef{Hash: hex.EncodeToString(sum[:]), Type: blobType(data), Bytes: int64(len(data))}, data)
}

// delegationFor reads the record id for the reader actor: an operator, the
// one it was for or another alike. Anyone else, the yardmaster that ran
// it and a call that names nobody among them, is told it is not there, as
// a mail's file is refused to a non-party (MailBlob).
func delegationFor(ctx context.Context, tx *bolt.Tx, id uint64, actor string) (*Delegation, error) {
	missing := fmt.Errorf("delegated record %d: %w", id, ErrNotFound)
	resolved, err := paneAuthor(tx, callerOf(ctx), actor)
	if err != nil {
		return nil, err
	}
	if u, err := getUser(tx, resolved); err != nil || !u.IsOperator() {
		return nil, missing
	}
	rec := new(Delegation)
	if ok, err := get(tx, bDelegations, seqKey(id), rec); err != nil {
		return nil, err
	} else if !ok {
		return nil, missing
	}
	return rec, nil
}

// Delegation is the delegated record id, read by actor: an operator's
// alone (delegationFor).
func (s *Store) Delegation(ctx context.Context, id uint64, actor string) (*Delegation, error) {
	var rec *Delegation
	err := s.read(func(tx *bolt.Tx) (err error) {
		rec, err = delegationFor(ctx, tx, id, actor)
		return err
	})
	return rec, err
}

// DelegationBlob is the body the delegated record id holds in its blob,
// read by actor under the record's own check; a record whose body stands
// inline has none, and is told so as not found.
func (s *Store) DelegationBlob(ctx context.Context, id uint64, actor string) (*DelegatedBody, error) {
	rec, err := s.Delegation(ctx, id, actor)
	if err != nil {
		return nil, err
	}
	if rec.Blob == nil {
		return nil, fmt.Errorf("delegated record %d holds no blob: its body stands in it: %w", id, ErrNotFound)
	}
	ref, err := s.Blob(rec.Blob.Hash)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(s.BlobPath(ref.Hash))
	if err != nil {
		return nil, &OperationalError{Err: err}
	}
	body := new(DelegatedBody)
	if err := json.Unmarshal(raw, body); err != nil {
		return nil, fmt.Errorf("delegated record %d: its blob: %w", id, err)
	}
	return body, nil
}

// groupFields are the group's own fields a delegated change records beside
// its config, as group set's flags name them.
var groupFields = map[string]string{"--limit": "--limit", "--members": "--members"}

// delegatedPutGroup is putGroup for PutGroup, with a delegated run held
// and recorded (delegatedGroup).
func (w *wtx) delegatedPutGroup(ctx context.Context, g *Group, actor string) error {
	d, err := w.delegated(ctx, actor)
	if err != nil {
		return err
	}
	var old Group
	had, err := get(w.Tx, bGroups, g.Name, &old)
	if err != nil {
		return err
	}
	if err := putGroup(ctx, w, g, actor, false); err != nil || d == nil {
		return err
	}
	return w.delegatedGroup(d, old, *g, had)
}

// delegatedGroup holds a delegated group add or set to the values the leg
// carries (delegatedDefine) and records it: what changed of old, which
// stood where had, and how it is taken back: a new group is removed as
// the yardmaster removes groups already, a replaced one on another runner
// is added back whole, and any other is set back.
func (w *wtx) delegatedGroup(d *DelegatedCall, old, g Group, had bool) error {
	subject := "group " + g.Name
	if err := w.delegatedDefine(subject, old.Config, g.Config, slices.Sorted(maps.Keys(groupDelegates))); err != nil {
		return err
	}
	fields := []DelegatedChange{
		{Key: "--runner", Was: old.Runner, Had: had, Is: g.Runner, Has: true},
		{Key: "--limit", Was: fmt.Sprint(old.Limit), Had: had, Is: fmt.Sprint(g.Limit), Has: true},
		{Key: "--members", Was: strings.Join(old.Members, ","), Had: had, Is: strings.Join(g.Members, ","), Has: true},
	}
	changes := configChanged(old.Config, g.Config, fields...)
	var back string
	switch {
	case !had:
		back = "yardr group rm " + shellQuote(g.Name)
	case old.Runner != g.Runner:
		words := []string{"yardr group add", shellQuote(g.Name), "--runner", shellQuote(old.Runner), "--limit", fmt.Sprint(old.Limit)}
		if len(old.Members) > 0 {
			words = append(words, "--members", shellQuote(strings.Join(old.Members, ",")))
		}
		for _, k := range slices.Sorted(maps.Keys(asStored(old.Config))) {
			words = append(words, "--set", shellQuote(k+"="+valueWords(asStored(old.Config)[k])))
		}
		back = strings.Join(words, " ")
	default:
		back = configBack("group set "+shellQuote(g.Name), changes, groupFields)
	}
	return w.recordDelegated(d, subject, DelegatedBody{Changes: changes, Back: back})
}

// crewFields are the crew member's own fields a delegated change records
// beside its config, as crew set's flags name them.
var crewFields = map[string]string{"--dir": "--dir", "--on": "--on", "--max-age": "--max-age", "--handoff-grace": "--handoff-grace", "--context-handoff": "--context-handoff"}

// delegatedCrew is putCrew for PutCrew, with a delegated run held to what
// the leg carries and recorded: a member that stands only, since a new one
// arrives with its role text on the crew add card (3.2), and its values
// as delegatedDefine reads them.
func (w *wtx) delegatedCrew(ctx context.Context, def CrewMember, actor string) (*CrewMember, error) {
	d, err := w.delegated(ctx, actor)
	if err != nil || d == nil {
		if err != nil {
			return nil, err
		}
		return w.putCrew(ctx, def, actor, nil)
	}
	var before CrewMember
	if ok, err := get(w.Tx, bCrew, def.Name, &before); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("crew member %s is new, and a new member arrives with its role on the crew add card, never by a delegated run: yardr bead ask --run \"crew add %s ...\"", def.Name, def.Name)
	}
	out, err := w.putCrew(ctx, def, actor, nil)
	if err != nil {
		return nil, err
	}
	subject := "crew member " + def.Name
	if err := w.delegatedDefine(subject, before.Config, out.Config, slices.Sorted(maps.Keys(crewDelegates))); err != nil {
		return nil, err
	}
	if out.Dir != before.Dir && out.Dir != "" {
		if _, err := w.knownDir(subject, "dir", out.Dir, "--dir <dir>"); err != nil {
			return nil, err
		}
	}
	fields := []DelegatedChange{
		{Key: "--dir", Was: before.Dir, Had: true, Is: out.Dir, Has: true},
		{Key: "--on", Was: strings.Join(before.On, ","), Had: true, Is: strings.Join(out.On, ","), Has: true},
		{Key: "--max-age", Was: before.MaxAge, Had: true, Is: out.MaxAge, Has: true},
		{Key: "--handoff-grace", Was: before.HandoffGrace, Had: true, Is: out.HandoffGrace, Has: true},
		{Key: "--context-handoff", Was: before.ContextHandoff, Had: true, Is: out.ContextHandoff, Has: true},
	}
	changes := configChanged(before.Config, out.Config, fields...)
	back := configBack("crew set "+shellQuote(def.Name), changes, crewFields)
	return out, w.recordDelegated(d, subject, DelegatedBody{Changes: changes, Back: back})
}

// delegatedDepot holds a delegated depot write to the bounds the depot
// test card carries (a command of one line, MaxRunDepotTest at most) and
// records it. Its path and kind, and clearing its test command, were
// refused by their gates, which the leg does not carry.
func (w *wtx) delegatedDepot(d *DelegatedCall, was Depot, had bool, r Depot) error {
	if r.Test != was.Test {
		if len(r.Test) > MaxRunDepotTest || claimPlain(r.Test) != r.Test {
			return fmt.Errorf("depot %s: a delegated test command is one line of at most %d bytes, as the depot test card shows one whole: a longer one is the operator's to type in a terminal", r.Name, MaxRunDepotTest)
		}
	}
	var changes []DelegatedChange
	var backs []string
	if !had {
		return w.recordDelegated(d, "depot "+r.Name, DelegatedBody{Changes: []DelegatedChange{{Key: "path", Is: r.Path, Has: true}},
			Back: "removing a depot is the operator's, in a terminal: yardr depot rm " + shellQuote(r.Name)})
	}
	if r.Test != was.Test {
		changes = append(changes, DelegatedChange{Key: "test", Was: was.Test, Had: was.Test != "", Is: r.Test, Has: true})
		if was.Test == "" {
			backs = append(backs, "clearing a depot's test command is the operator's, in a terminal")
		} else {
			backs = append(backs, "yardr depot test "+shellQuote(r.Name)+" "+shellQuote(was.Test))
		}
	}
	if r.Base != was.Base {
		changes = append(changes, DelegatedChange{Key: "base", Was: was.Base, Had: was.Base != "", Is: r.Base, Has: r.Base != ""})
		backs = append(backs, "yardr depot base "+shellQuote(r.Name)+" "+shellQuote(was.Base))
	}
	return w.recordDelegated(d, "depot "+r.Name, DelegatedBody{Changes: changes, Back: strings.Join(backs, "; ")})
}

// delegatedUser is setUser for SetUser, with a delegated run recorded: the
// leg carries notify= alone, so the role, env_file or tell in the same
// command was refused by its gate and the record names that one key.
func (w *wtx) delegatedUser(ctx context.Context, name string, keys []string, set map[string]string, actor string) (*User, error) {
	d, err := w.delegated(ctx, actor)
	if err != nil {
		return nil, err
	}
	var was string
	if prior, err := getUser(w.Tx, name); err == nil {
		was = prior.Notify
	}
	u, err := w.setUser(ctx, name, keys, set, actor, nil)
	if err != nil || d == nil {
		return u, err
	}
	change := DelegatedChange{Key: UserNotify, Was: was, Had: was != "", Is: u.Notify, Has: u.Notify != ""}
	back := "yardr user set " + shellQuote(name) + " " + shellQuote(UserNotify+"="+was)
	return u, w.recordDelegated(d, "user "+name, DelegatedBody{Changes: []DelegatedChange{change}, Back: back})
}

// hookDelegateUse is how a refusal names the hook add the leg carries.
const hookDelegateUse = "yardr hook add <name> --source <declared source> [--unknown unknown|allow|ignore] --for <operator> --mail <seq>"

// delegatedHook is putHook for PutHook, with a delegated run held to the
// hook add card's safe shape (3.2) and recorded: a new hook, its name,
// source and --unknown word, and the secret file the yard owns for it
// (HookSecretFile), its secret still given on its own secret card. A
// field that sets automation or identity in motion refuses the leg
// whole, naming its flag as the card's refusal does, and nothing is
// stored.
func (w *wtx) delegatedHook(ctx context.Context, h Hook, actor string) (*Hook, error) {
	d, err := w.delegated(ctx, actor)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return w.putHook(ctx, h, actor, nil, Alone)
	}
	refused := ""
	switch {
	case len(h.Says) > 0:
		refused = "--says"
	case len(h.Makes) > 0:
		refused = "--makes"
	case len(h.Map) > 0:
		refused = "--map"
	case h.As != "":
		refused = "--as"
	case h.SecretFile != HookSecretFile(w.home, h.Name):
		refused = "--secret-file"
	}
	if refused != "" {
		return nil, fmt.Errorf("a delegated run takes %s, and hook add with %s is the operator's to type in a terminal", hookDelegateUse, refused)
	}
	if w.Bucket(bHooks).Get([]byte(h.Name)) != nil {
		return nil, fmt.Errorf("hook %s stands: a delegated run adds a new hook, and replacing one is the operator's to type in a terminal", h.Name)
	}
	out, err := w.putHook(ctx, h, actor, nil, Alone)
	if err != nil {
		return nil, err
	}
	changes := []DelegatedChange{{Key: "source", Is: out.Source, Has: true}, {Key: "secret_file", Is: out.SecretFile, Has: true}}
	if out.Unknown != "" {
		changes = append(changes, DelegatedChange{Key: "unknown", Is: out.Unknown, Has: true})
	}
	back := "removing a hook is the operator's, in a terminal: yardr hook rm " + shellQuote(h.Name)
	return out, w.recordDelegated(d, "hook "+h.Name, DelegatedBody{Changes: changes, Back: back})
}
