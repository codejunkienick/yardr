package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// GiveSecret answers the secret ask ask on the bead id with values, the
// names it asked for to the values they take: the values are written into
// the file the source's credential is read from (KeySource), and the answer
// is a note of kind answer that holds the names and never a value. It
// answers where the credential is set now, as KeySource does.
//
// It refuses an ask that is not on the bead, asks for no secret or was
// declined; names that are not exactly the ones asked for; whoever may not
// set that source's credential (maySource: the operator, and never the pane
// of an agent); and a source whose calls go elsewhere than the ask's card
// says. No refusal repeats a name or a value that was sent.
//
// An ask for a user's own address in the source (AskSecret.User) is given
// the same way into that user's env file (keyUser), by whoever may say
// where the user is told (mayTell: the user and the operator, and never the
// pane of an agent). It sets no notify: that the user is told through the
// source is a second thing, and theirs to say.
//
// An ask for a user's own credential in the source (AskSecret.As) is given
// the same way into that user's env file (keyUser for a credential), by
// the user or the operator for them, as their address is (mayTell). The
// source needs no notify key: a source that is called as a user need tell
// nobody.
//
// An ask for a crew member's own credential in the source (AskSecret.Crew)
// is given the same way into that crew member's env file
// (giveCrewSecret), by the operator alone: the crew member's running
// session does not see the new value, which takes effect when it next
// starts. It restarts nothing: that is a second thing, and the card and
// the answer say when the value takes effect.
//
// An ask for a harness API key (AskSecret.Harness) is given the same way
// into the operator's key file of the kind (giveHarnessKey), by the
// operator alone: no source declares it, so the kind's harness file holds
// the names instead, and it takes effect as each session of the kind on
// the operator's login next starts.
//
// An ask for a hook's signing secret (AskSecret.Hook) is given the same
// way into the hook's secret file the yard owns (giveHookSecret), by the
// operator alone: a hook that stands is rotated only where it still
// serves the source and names that file. For a new hook, hook add is a
// second ask; for a standing hook, this ask rotates the secret in place.
//
// An ask for a Google account's sign-in (AskSecret.Google) is given the
// same way into that account's credentials file (giveGoogle), by the
// operator for the yard's own account and by the person alone for
// their own: the pasted export is checked as an authorized_user JSON
// and written 0600, and no refusal or answer repeats it.
//
// An ask for a waiting login's input (AskSecret.Login) is handed to the
// login that waits under its reference (LoginWait) and goes to no file: the
// answer note holds the names and never the value, as any giving does, and
// is refused when no login waits under it, or when the login that waits is
// another user's than the ask is for (giveLogin). Giving is the operator's,
// as a source's is (maySource); for a user's own login (AskSecret.As) it is
// that user's or the operator's for them (mayTell), as for their own
// credential.
//
// Giving again is allowed while the ask is neither declined nor taken
// back, as a newer pick replaces an older one: each giving is a note with
// the names.
//
// The answer note is put in the transaction that the file's rename ends
// (keySource), so the store never says "gave" of a value the file does not
// hold. The other way round is not held: when that transaction fails after
// the rename the value is in the file, the ask is open and has no answer
// (*KeyNotRecorded), and giving again answers it. A login's value is handed
// before its note for the same reason in reverse: the wait ends with the
// handing, so a note that failed after it leaves the login answered and the
// ask open, where the other order leaves the ask answered and the login
// waiting.
//
// It is the one call of the operator's that a device may make, and only a
// device of the operator, or of the user whose own address or credential
// is asked for (rpc.OperatorCalls): the note and the events of such a
// giving say how it came, as a device's other writes do (writeVia).
//
// Every secret ask is weighty (weight.go: it gives the yard a credential),
// so a giving is made only by a call that says its person saw the weight
// first (weighed). Without it the giving is refused before anything is
// written (weighedRefusal): no file, no note, and the ask stays open. The
// yard checks what the client says and no more.
//
// A giving that carries a fresh sign-in (WithFreshWitness, the hosted
// web's) is judged by that sign-in as a strong card's approval is
// (freshGivenRefusal), and its answer's event records the fresh sign-in
// (addGiven) where it came through no device. A giving that carries none
// behaves exactly as it always has, on a device or a terminal alike.
func (s *Store) GiveSecret(ctx context.Context, id string, ask uint64, values map[string]string, actor string, weighed bool) (*SourceKeyed, error) {
	var k sourceKey
	var lg loginSent
	err := s.writeVia(ctx, func(w *wtx) error { return w.giveSecret(ctx, s, s.home, id, ask, values, actor, weighed, &k, &lg) })
	if err != nil {
		return nil, k.failed(err)
	}
	if lg.sent {
		return &SourceKeyed{Given: lg.given, Login: lg.id}, nil
	}
	return k.keyed(s.home), nil
}

// giveSecret is GiveSecret inside the transaction: the checks, the answer
// note, and last the file (keySource), after whose rename nothing may
// follow but the commit. A batch may run it twice for one call (write):
// the note of the run that is rolled back goes with it, and the second run
// finds the file's lines and writes the same content. A login's handing
// runs once: the second run finds lg set and writes the note only.
func (w *wtx) giveSecret(ctx context.Context, s *Store, home, id string, ask uint64, values map[string]string, actor string, weighed bool, k *sourceKey, lg *loginSent) error {
	resolved, err := paneAuthor(w.Tx, callerOf(ctx), actor)
	if err != nil {
		return err
	}
	actor = resolved
	row, err := w.loadBead(id)
	if err != nil {
		return err
	}
	notes, err := beadNotes(w.Tx, row.UUID)
	if err != nil {
		return err
	}
	asked, err := givable(row.ID, notes, ask, values, now())
	if err != nil {
		return err
	}
	sec := asked.Secret
	// Before the note is put: an agent's giving writes nothing, not even
	// what the transaction would take back.
	if sec.Google != "" {
		err = w.may(ctx, actor, ActGiveGoogleCredentials, Object{Kind: KindUser, Name: sec.Google, To: sec.As})
	} else if sec.Harness != "" {
		err = w.may(ctx, actor, ActGiveHarnessKey, Object{Kind: KindHarness, Name: sec.Harness, To: sec.Crew})
	} else if sec.User != "" {
		err = w.may(ctx, actor, ActTell, Object{Kind: KindUser, Name: sec.User})
	} else if sec.Login != "" && sec.As != "" {
		err = w.may(ctx, actor, ActAnswerLogin, Object{Kind: KindUser, Name: sec.As})
	} else if sec.As != "" {
		err = w.may(ctx, actor, ActTell, Object{Kind: KindUser, Name: sec.As})
	} else if sec.Crew != "" {
		err = w.may(ctx, actor, ActGiveCrewCredential, Object{Kind: KindCrew, Name: sec.Crew})
	} else if sec.Hook != "" {
		err = w.may(ctx, actor, ActGiveHookSecret, Object{Kind: KindHook, Name: sec.Hook})
	} else if sec.Login != "" {
		err = w.may(ctx, actor, ActAnswerSourceLogin, Object{Kind: KindLogin, Name: sec.Login})
	} else {
		err = w.may(ctx, actor, ActSetSourceKey, Object{Kind: KindSource, Name: sec.Source})
	}
	if err != nil {
		return err
	}
	// Weighed again, as an approval weighs every card: an ask written
	// before secrets were weighed showed none, and is given no more. It
	// is weighty, so it is given by a client that says its person saw it.
	weight := secretWeight(*sec)
	if err := weightStale(ask, storedWeight(notes, ask), weight); err != nil {
		return err
	}
	if err := weighedRefusal(ask, weight, weighed); err != nil {
		return err
	}
	if err := w.freshGivenRefusal(ask, weight, actor); err != nil {
		return err
	}
	if sec.Google != "" {
		return w.giveGoogle(row, ask, sec, values, actor, k)
	}
	if sec.Harness != "" {
		return w.giveHarnessKey(ctx, home, row, ask, sec, values, actor, k)
	}
	if sec.Login != "" {
		return w.giveLogin(s, row, ask, sec, asked.To, values, actor, lg)
	}
	var src Source
	if ok, err := get(w.Tx, bSources, sec.Source, &src); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("ask %d: source %s: %w", ask, sec.Source, ErrNotFound)
	}
	d, err := src.Declaration()
	if err != nil {
		return err
	}
	d = src.Resolved(d)
	if host := sourceHost(d); host != sec.Host {
		return fmt.Errorf("ask %d says the secret is for the source %s at %s, and the source's calls go to %s now: the ask is from before the source was replaced, and is to be asked again",
			ask, sec.Source, sec.Host, host)
	}
	// Before the note is put: a URL-credential value that is no address
	// is refused here, as the writers below refuse it, so a refused value
	// leaves neither the note nor the file. A user's own address and a
	// hook's secret are not the credential and pass unchecked.
	if sec.Hook == "" && sec.User == "" {
		if err := checkURLCredential(d, sec.Source, values); err != nil {
			return err
		}
	}
	text, err := json.Marshal(Answer{Ask: ask, Given: sec.Env})
	if err != nil {
		return err
	}
	if _, err := w.addGiven(row, string(text), actor); err != nil {
		return err
	}
	if sec.Hook != "" {
		return w.giveHookSecret(home, sec, &src, values, k)
	}
	if sec.User != "" {
		return w.keyUser(ctx, home, sec.User, sec.Source, values, actor, false, k)
	}
	if sec.As != "" {
		return w.keyUser(ctx, home, sec.As, sec.Source, values, actor, true, k)
	}
	if sec.Crew != "" {
		return w.giveCrewSecret(ctx, home, sec, &src, values, actor, k)
	}
	return w.keySource(ctx, home, sec.Source, values, actor, k)
}

// giveHookSecret is giveSecret for a hook's signing secret: the value
// goes to the hook's secret file the yard owns (HookSecretFile), in the
// order keySource keeps: the answer note is put first and the file's
// rename last, so the store never says "gave" of a value the file does
// not hold, and a commit that fails after the rename leaves the value
// in the file with the ask open (KeyNotRecorded, through k). It refuses
// a hook that has been added, moved to another source or repointed to a
// custom file since the ask, as askedHookSecret refuses one that
// stands so. It clears no Blocked: the receiver reads the file on every
// delivery and clears it there.
func (w *wtx) giveHookSecret(home string, sec *AskSecret, src *Source, values map[string]string, k *sourceKey) error {
	var standing Hook
	had, err := get(w.Tx, bHooks, sec.Hook, &standing)
	if err != nil {
		return err
	}
	switch {
	case had && standing.Source != sec.Source:
		return fmt.Errorf("hook %s is in the yard already: it serves the source %s, not %s", sec.Hook, standing.Source, sec.Source)
	case had && standing.SecretFile != HookSecretFile(home, sec.Hook):
		return fmt.Errorf("hook %s is in the yard already: its signing secret is the file it names, changed in a terminal", sec.Hook)
	}
	d, err := src.Declaration()
	if err != nil {
		return err
	}
	if d.Hook == nil {
		return fmt.Errorf("source %s: its declaration has no hook key now, so there is no signing secret to give", sec.Source)
	}
	k.src, k.hook, k.names = src, sec.Hook, slices.Clone(sec.Env)
	file, renamed, err := writeHookSecret(home, sec.Hook, values[sec.Env[0]])
	k.file, k.renamed = file, k.renamed || renamed
	if err != nil {
		return err
	}
	if afterKeyRename != nil {
		return afterKeyRename()
	}
	return nil
}

// crewEnvFile is where the yard keeps what a source needs of the crew
// member name when nobody named a file for them: <home>/crew/<name>.env,
// beside the store and not in it. A name with a path separator in it
// could name a file outside that directory or another member's, so the
// yard chooses no file for it.
func crewEnvFile(home, name string) (string, error) {
	if strings.ContainsAny(name, `/\\`) {
		return "", fmt.Errorf("crew member %s has no env_file, and the yard chooses no file for a name with a path separator in it: yardr crew set %s env_file=<file> first", name, name)
	}
	return filepath.Join(home, CrewDir, name+".env"), nil
}

// giveCrewSecret is giveSecret for a crew member's own credential in the
// source: the values go to the crew member's env file, in the order
// keySource keeps: the answer note is put first and the file's rename
// last, so the store never says "gave" of a value the file does not
// hold, and a commit that fails after the rename leaves the value in the
// file with the ask open (KeyNotRecorded, through k). With no env_file
// set the file is crewEnvFile, which is then set as yardr crew set sets
// it, through the same write. The crew member the ask names must still
// be the yard's, and the names are held to the source's credential as it
// stands now, since its declaration may have changed since the ask. The
// running session is restarted by nothing here: it reads its environment
// when its next session starts.
func (w *wtx) giveCrewSecret(ctx context.Context, home string, sec *AskSecret, src *Source, values map[string]string, actor string, k *sourceKey) error {
	var r CrewMember
	if ok, err := get(w.Tx, bCrew, sec.Crew, &r); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("crew member %s: %w", sec.Crew, ErrNotFound)
	}
	d, err := src.Declaration()
	if err != nil {
		return err
	}
	if d.Credential == nil {
		return fmt.Errorf("source %s: its declaration names no credential now, so there is no secret to give", sec.Source)
	}
	// givable held the names to the ask's; the declaration is the
	// writer's to check, without saying back what was sent.
	names := make([]string, 0, len(values))
	for n := range values {
		if !slices.Contains(d.Credential.Variables(), n) {
			return fmt.Errorf("source %s: %s is no variable of its credential now (its declaration names %s)", sec.Source, n, d.Credential.Names())
		}
		names = append(names, n)
	}
	sort.Strings(names)
	if err := checkURLCredential(d, sec.Source, values); err != nil {
		return err
	}
	saved, file, err := w.crewSecretFile(ctx, home, &r, actor)
	if err != nil {
		return err
	}
	k.src, k.crew, k.names = src, saved, names
	resolved, renamed, err := WriteEnvFile(file, values)
	k.file = resolved
	k.renamed = k.renamed || renamed
	if err != nil {
		return fmt.Errorf("crew member %s: %w", sec.Crew, err)
	}
	if afterKeyRename != nil {
		return afterKeyRename()
	}
	return nil
}

// crewSecretFile is the env file a secret for the crew member r is written
// to: the env_file it has, or crewEnvFile when it has none, which is then
// set as yardr crew set sets it, through the same write. It answers the
// member as the write leaves it.
func (w *wtx) crewSecretFile(ctx context.Context, home string, r *CrewMember, actor string) (*CrewMember, string, error) {
	file, _ := r.Config["env_file"].(string)
	if file != "" {
		return r, file, nil
	}
	// The server chooses the path: nothing a caller sends is one.
	file, err := crewEnvFile(home, r.Name)
	if err != nil {
		return nil, "", err
	}
	def := *r
	def.Config = make(map[string]any, len(r.Config)+1)
	for key, value := range r.Config {
		def.Config[key] = value
	}
	def.Config["env_file"] = file
	def.Revision = r.Revision
	saved, err := w.putCrew(ctx, def, actor, nil)
	if err != nil {
		return nil, "", err
	}
	return saved, file, nil
}

// HarnessDir is the directory of a yard's home where the yard keeps the
// API key of each harness kind it was given, one file per login user
// (HarnessKeyFile).
const HarnessDir = "harness"

// HarnessKeyFile is where the yard keeps the API key of the harness kind
// for the sessions that burn user's login: <home>/harness/<kind>/<user>.env,
// beside the store and not in it. The kind comes first, so a user's name
// is one file's name and never a directory's. Every session of the kind on
// that login reads it as it starts (the runner), so a key given once
// reaches the crew, the groups and the conversations that burn the login,
// as the machine's login of the kind does. A kind or a name that could
// name a file elsewhere (a path separator, a dot name) has no file.
func HarnessKeyFile(home, kind, user string) (string, error) {
	for _, part := range []string{kind, user} {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, `/\`) {
			return "", fmt.Errorf("the yard keeps no harness key file for kind %q and user %q: neither may be empty, a dot name or hold a path separator", kind, user)
		}
	}
	return filepath.Join(home, HarnessDir, kind, user+".env"), nil
}

// giveHarnessKey is giveSecret for a harness API key: the values go to the
// operator's key file of the kind (HarnessKeyFile), in the order keySource
// keeps: the answer note is put first and the file's rename last, so the
// store never says "gave" of a value the file does not hold, and a commit
// that fails after the rename leaves the value in the file with the ask
// open (KeyNotRecorded, through k). No source declares the names: the
// kind's harness file holds them as it stands now. The file is the
// operator's because the led login asks the operator, and an unset
// login_for is the operator's (Group.EffectiveLogin): a group that burns
// another user's login reads that user's file, which this never writes.
// No crew member's env_file is set. The running sessions are restarted by
// nothing here: each reads the file when its next session starts.
func (w *wtx) giveHarnessKey(ctx context.Context, home string, row *beadRow, ask uint64, sec *AskSecret, values map[string]string, actor string, k *sourceKey) error {
	if w.Bucket(bCrew).Get([]byte(sec.Crew)) == nil {
		return fmt.Errorf("crew member %s: %w", sec.Crew, ErrNotFound)
	}
	loginEnv := HarnessLoginEnv[sec.Harness]
	if _, ok := HarnessLabels[sec.Harness]; !ok || len(loginEnv) == 0 {
		return fmt.Errorf("harness %s: its file names no login_env now, so there is no API key to give", sec.Harness)
	}
	h := Harness{Kind: sec.Harness, Label: HarnessLabels[sec.Harness], LoginEnv: loginEnv}
	// givable held the names to the ask's; the harness file is the
	// writer's to check, without saying back what was sent.
	names := make([]string, 0, len(values))
	for n := range values {
		if !slices.Contains(h.LoginEnv, n) {
			return fmt.Errorf("harness %s: %s is no variable of its API key now (its file names %s)", sec.Harness, n, strings.Join(h.LoginEnv, ", "))
		}
		names = append(names, n)
	}
	sort.Strings(names)
	operator, err := operatorIn(w.Tx)
	if err != nil {
		return err
	}
	if operator == "" {
		return fmt.Errorf("a harness API key is kept for the operator's login, and this yard has none: yardr settings set operator <name>")
	}
	file, err := HarnessKeyFile(home, h.Kind, operator)
	if err != nil {
		return err
	}
	text, err := json.Marshal(Answer{Ask: ask, Given: sec.Env})
	if err != nil {
		return err
	}
	if _, err := w.addGiven(row, string(text), actor); err != nil {
		return err
	}
	k.harness, k.login, k.names = &h, operator, names
	resolved, renamed, err := WriteEnvFile(file, values)
	k.file = resolved
	k.renamed = k.renamed || renamed
	if err != nil {
		return fmt.Errorf("harness %s: %w", h.Kind, err)
	}
	if afterKeyRename != nil {
		return afterKeyRename()
	}
	return nil
}

// giveLogin is giveSecret for an ask for a waiting login's input: the value
// is handed to the login that waits under sec's reference and the answer
// note holds the names, to no file. The login must be the one of the user the
// ask is for: the user whose own login it is (sec.As), else the one it is
// addressed to (to), the operator by askedLogin. The reference alone ties a
// wait to nobody, so a wait that is another user's is refused here and left
// waiting for the ask that is its own. The handing runs once per call: a
// batch rerun finds lg set and writes the note only. No refusal repeats a
// value that was sent.
func (w *wtx) giveLogin(s *Store, row *beadRow, ask uint64, sec *AskSecret, to string, values map[string]string, actor string, lg *loginSent) error {
	if !lg.sent {
		for _, n := range sec.Env {
			if err := loginValueError(ask, n, values[n]); err != nil {
				return err
			}
		}
		// givable held the names to the ask's: the one value is the one's.
		forUser := or(sec.As, to)
		owner, waits, handed := s.handLogin(sec.Login, forUser, values[sec.Env[0]])
		switch {
		case !waits:
			return fmt.Errorf("ask %d names the waiting login %s, and no login waits under it: the login ended, or its wait ran out, and its next input need names the reference to ask for", ask, sec.Login)
		case !handed:
			// Only a yard that names no operator has a wait with no owner.
			whose := owner + "'s"
			if owner == "" {
				whose = "nobody's, because this yard names no operator (yardr settings set operator <name>)"
			}
			return fmt.Errorf("ask %d is for %s's login, and the login waiting under %s is %s: a code is handed to the login of the user it is for, and that login keeps waiting for the ask that is its own", ask, forUser, sec.Login, whose)
		}
		lg.sent, lg.id, lg.given = true, sec.Login, slices.Clone(sec.Env)
	}
	text, err := json.Marshal(Answer{Ask: ask, Given: sec.Env})
	if err != nil {
		return err
	}
	_, err = w.addGiven(row, string(text), actor)
	return err
}

// givable decides whether values may be given to the ask seq among notes,
// the notes of the bead id, at the time at, and answers the ask, whose Secret
// is what it asks for and whose To is who it is asked of. The names of values
// are held to the ask's here and to the source's declaration by keySource or
// keyUser; the values are the writer's to check (WriteEnvFile,
// loginValueError for a waiting login).
func givable(id string, notes []Note, seq uint64, values map[string]string, at time.Time) (*Ask, error) {
	var r *ResolvedAsk
	resolved := ResolveAsks(notes, at)
	for i := range resolved {
		if resolved[i].Note.Seq == seq {
			r = &resolved[i]
		}
	}
	switch {
	case r == nil && slices.ContainsFunc(notes, func(n Note) bool { return n.Seq == seq }):
		return nil, fmt.Errorf("note %d on bead %s is not an ask", seq, id)
	case r == nil:
		return nil, fmt.Errorf("bead %s has no ask %d", id, seq)
	case r.AskErr != nil:
		return nil, r.AskErr
	case r.Ask.Secret == nil:
		return nil, fmt.Errorf("ask %d asks for no secret: it is answered with yardr bead answer %s %d <n> [words], or --words", seq, id, seq)
	case r.State == AskDeclined:
		return nil, fmt.Errorf("ask %d was declined: %s", seq, declinedLine(*r.Current))
	case r.State == AskTakenBack:
		return nil, fmt.Errorf("ask %d is asked no more: %s", seq, takenBackLine(*r.Current))
	}
	sec := r.Ask.Secret
	// What was sent is not said back: a client that put a value where a
	// name goes would read it in the refusal.
	for n := range values {
		if !slices.Contains(sec.Env, n) {
			return nil, fmt.Errorf("ask %d asks for %s, and was given a name it did not ask for", seq, sec.Names())
		}
	}
	for _, n := range sec.Env {
		if _, ok := values[n]; !ok {
			return nil, fmt.Errorf("ask %d asks for %s: %s was not given", seq, sec.Names(), n)
		}
	}
	return &r.Ask, nil
}
