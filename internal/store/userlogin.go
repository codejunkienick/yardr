package store

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// A person with a login of their own is made of records that name them
// (docs/groups.md, "A person with a login of their own"): a group whose
// login_for is the user, the routes that take their beads or send work to
// that group, and the crew members that serve them a conversation. yardr
// user add --login makes them, and removing the user takes them in the same
// write: a route left behind would send beads for the name to a group whose
// login belongs to no one, and a name is reusable, so the first's login must
// not be the second's.

// UserLogin is what carries a user's own login in the yard, by name and id.
type UserLogin struct {
	// Groups are the groups whose login_for is the user.
	Groups []string `json:"groups,omitempty"`
	// EnvFiles are the env_file of those groups, each once: where the
	// login's folder is named. They stay on disk when the user goes.
	EnvFiles []string `json:"env_files,omitempty"`
	// Routes are the ids of the routes that take the user's beads (their
	// originator) or name one of those groups.
	Routes []string `json:"routes,omitempty"`
	// Crew are the crew members that serve the user (for=) or keep a
	// conversation session for them (env_for_<user>).
	Crew []string `json:"crew,omitempty"`
}

// Empty reports whether the yard holds nothing of the user's login.
func (l UserLogin) Empty() bool {
	return len(l.Groups) == 0 && len(l.Routes) == 0 && len(l.Crew) == 0
}

// LoginOf reads what carries user's own login off the yard's groups, routes
// and crew: the same sets removing the user takes (deleteUser). Each list is
// sorted.
func LoginOf(user string, groups []Group, routes []Route, crew []CrewMember) UserLogin {
	var l UserLogin
	for _, g := range groups {
		if g.LoginFor() != user {
			continue
		}
		l.Groups = append(l.Groups, g.Name)
		if file, _ := g.Config["env_file"].(string); file != "" && !slices.Contains(l.EnvFiles, file) {
			l.EnvFiles = append(l.EnvFiles, file)
		}
	}
	for _, r := range routes {
		if (r.Originator != nil && *r.Originator == user) || slices.ContainsFunc(l.Groups, r.Names) {
			l.Routes = append(l.Routes, r.ID)
		}
	}
	for _, c := range crew {
		if slices.Contains(c.Served(), user) || hasEnvFor(c.Config, user) {
			l.Crew = append(l.Crew, c.Name)
		}
	}
	sort.Strings(l.Groups)
	sort.Strings(l.EnvFiles)
	sort.Strings(l.Routes)
	sort.Strings(l.Crew)
	return l
}

// Said is what removing user takes of their own login, in sentences, and
// what stays: the card of user rm shows them before and yardr user rm after,
// so that both say it in the same words. Nothing for a user with no login of
// their own.
func (l UserLogin) Said(user string) []string {
	var out []string
	if len(l.Groups) > 0 {
		out = append(out, "Groups that carry their login go with them: "+strings.Join(l.Groups, ", ")+".")
	}
	if len(l.Routes) > 0 {
		out = append(out, fmt.Sprintf("Their routes go with them (%s).", countNoun(len(l.Routes), "route")))
	}
	if len(l.Crew) > 0 {
		out = append(out, fmt.Sprintf("They are taken out of for= and env_for_%s of %s.", user, strings.Join(l.Crew, ", ")))
	}
	if len(l.EnvFiles) > 0 {
		out = append(out, "Their login stays on disk, in the folder the env file "+strings.Join(l.EnvFiles, ", ")+" names; no command erases it yet.")
	}
	return out
}

func hasEnvFor(cfg map[string]any, user string) bool {
	_, ok := cfg[EnvForPrefix+user]
	return ok
}

// SignInMeta is the metadata key of the bead a person's own sign-in stands on
// (yardr user add --login, yardr user login); its value is the harness kind
// being signed in. The command makes the bead before it serves the person's
// conversation and closes it when the sign-in ends, whatever the end, so the
// bead is open for exactly as long as the sign-in is. That is the one trace
// of the sign-in the daemon can read: the command is a process of its own,
// and the code it asks for comes and goes with it.
// SignInQuestion is the question of the ask for the person's code, and
// the title of the bead the ask stands on.
const SignInQuestion = "The code the sign-in page showed"

const SignInMeta = "sign_in"

// SignInWithin is how long a sign-in can be open: the login ends within its
// own timeouts (a quarter of an hour, docs/doctor.md) and the one call that
// proves it takes seconds. A bead open for longer is what a command killed
// outright left, and holds nothing back.
const SignInWithin = time.Hour

// SigningIn reports whether beads hold an open sign-in of user to the harness
// kind at now: their own login is being made, by the person, and no one else
// can log in a session that sits at its login screen on it.
func SigningIn(beads []Bead, user, kind string, now time.Time) bool {
	_, ok := SigningInBead(beads, user, kind, now)
	return ok
}

// SigningInBead is the bead of user's open sign-in to the harness kind at
// now, the earliest of them where several stand: SigningIn says whether
// one stands, and a refusal names this bead. A bead left open past
// SignInWithin by a command killed outright holds nothing back.
func SigningInBead(beads []Bead, user, kind string, now time.Time) (Bead, bool) {
	var found *Bead
	for i := range beads {
		b := &beads[i]
		of, _ := b.Metadata[SignInMeta].(string)
		if !b.open() || b.Originator != user || of != kind || now.Sub(b.CreatedAt) >= SignInWithin {
			continue
		}
		if found == nil || b.CreatedAt.Before(found.CreatedAt) {
			found = b
		}
	}
	if found == nil {
		return Bead{}, false
	}
	return *found, true
}

// SignInStands words the sign-in of user that already stands open on
// bead: it ends by itself within SignInWithin, so a person whose sign-in
// hung knows when another sign-in can be asked for. The card's check and
// the server's carrier refuse a second sign-in in these words.
func SignInStands(user string, bead Bead) string {
	kind, _ := bead.Metadata[SignInMeta].(string)
	return fmt.Sprintf("%s already has a sign-in to %s open on bead %s, and it ends by itself within the hour", user, kind, bead.ID)
}

// ClaudeFolderEnv is the variable that names the folder a Claude login
// lands in, in the env file of a group that carries one.
const ClaudeFolderEnv = "CLAUDE_CONFIG_DIR"

// LoginFolderEnvs are the harnesses a led login leads, each with the
// variable that names the folder its login lands in. Only claude is
// wired: the others either have no login a program leads or are logged
// in on their own screen.
var LoginFolderEnvs = map[string]string{"claude": ClaudeFolderEnv}

// LoginUserName is a user's name as a folder takes it: one path element.
var LoginUserName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// UserLoginPlan is what giving a user a login of their own will make,
// worked out from the yard as it stands before anything is written, so
// that a refusal leaves no person half made. The CLI (user add --login,
// user login) and the user login run card share it, so the two plan the
// same setup.
type UserLoginPlan struct {
	User, Harness string
	// Mirrors are the herdr groups the person gets a copy of, each named
	// <user>-<group>, in source-group name order.
	Mirrors []Group
	// Folder is where their login lands, and Env the file that names it,
	// both under the home.
	Folder, Env string
	// Routes are the routes to add: a twin of each route of the
	// operator's that sends work to a mirrored group first, for the
	// person's beads.
	Routes []Route
	// Crew is the crew member that talks with them, and Serving the
	// names it serves with them added, "" where it serves everyone
	// already.
	Crew    string
	Serving string
}

// PlanUserLogin works out what giving user a login of harness makes, and
// refuses what cannot be made, writing nothing. lookPath finds the
// harness's executable: the CLI gives its own, the card and the carrier
// the system's. ym is the yardmaster crew member named by yardmaster,
// nil where the yard has none standing under that name.
func PlanUserLogin(home, user, harness string, groups []Group, routes []Route, depots []Depot, users []User, yardmaster string, ym *CrewMember, lookPath func(string) (string, error)) (*UserLoginPlan, error) {
	if !slices.Contains(HerdrKinds, harness) {
		return nil, fmt.Errorf("--login %s is no harness kind; herdr starts: %s", harness, strings.Join(HerdrKinds, ", "))
	}
	if _, ok := LoginFolderEnvs[harness]; !ok {
		return nil, fmt.Errorf("--login %s: only claude is wired (its login folder is one a program names with CLAUDE_CONFIG_DIR, and its sign-in one yardr leads)", harness)
	}
	// The folder and the env file are named by the user, and a user's
	// name may hold path separators: the yard chooses no path for one
	// that does.
	if !LoginUserName.MatchString(user) {
		return nil, fmt.Errorf("user %q: the login folder is named by the user, so --login takes letters, digits, '.', '_' and '-', starting with a letter or digit", user)
	}
	p := &UserLoginPlan{User: user, Harness: harness,
		Folder: filepath.Join(home, "logins", user), Env: filepath.Join(home, "groups", user+".env")}
	// A removed user's folder stays on disk, with their login and their
	// transcripts, and a name is reusable: the first's login must not be
	// the second's. Moving it away is the operator's to say.
	for _, path := range []string{p.Folder, p.Env} {
		if _, err := os.Lstat(path); err == nil {
			return nil, fmt.Errorf("%s exists, and a login that is there already is not taken over for a user added now (a removed user's stays on disk): move it away, then add %s again", path, user)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if err := UserLoginEnvAvailable(home, p.Env, groups, users); err != nil {
		return nil, err
	}
	byName := make(map[string]Group, len(groups))
	for _, g := range groups {
		byName[g.Name] = g
	}
	seenGroups := map[string]bool{}
	for _, r := range routes {
		// The operator's routes to a herdr group: one with an originator
		// is already for someone, and a twin of it would be for no one;
		// one to a group that starts no agents takes every originator's
		// beads already, so it gains no twin either. A depot template
		// resolves to one exact group per depot, and each group gets a
		// depot-scoped twin.
		if r.Originator != nil {
			continue
		}
		candidates := []Route{r}
		if IsTemplatedGroup(r.Worker()) {
			candidates = nil
			for _, depot := range depots {
				resolved := r.ResolveForDepot(depot.Name)
				g, ok := byName[resolved.Worker()]
				if !ok || g.Runner != "herdr" {
					continue
				}
				depotName := depot.Name
				resolved.Depot = &depotName
				candidates = append(candidates, resolved)
			}
		}
		for _, candidate := range candidates {
			g, ok := byName[candidate.Worker()]
			if !ok || g.Runner != "herdr" {
				continue
			}
			if !seenGroups[g.Name] {
				seenGroups[g.Name] = true
				p.Mirrors = append(p.Mirrors, g)
			}
			twin := candidate
			twin.ID, twin.Group, twin.When, twin.Else, twin.Originator = "", user+"-"+g.Name, "", nil, &user
			twin.Note = user + "'s own login (yardr user add --login)"
			p.Routes = append(p.Routes, twin)
		}
	}
	if len(p.Mirrors) == 0 {
		return nil, fmt.Errorf("no route of the operator's sends work to a herdr group, so there is no stage to route %s's beads to: routes to scripts and people take every originator's beads already, and gain no twin; route a herdr group first (yardr route add)", user)
	}
	slices.SortFunc(p.Mirrors, func(a, b Group) int { return strings.Compare(a.Name, b.Name) })
	for _, g := range p.Mirrors {
		name := user + "-" + g.Name
		if _, exists := byName[name]; exists {
			return nil, fmt.Errorf("group %s exists: a person's groups are made new, so that no login of another's is run under their name", name)
		}
	}
	if ym == nil {
		return nil, fmt.Errorf("the yardmaster talks with %s on their own login, and crew member %s: %w", user, yardmaster, ErrNotFound)
	}
	p.Crew = yardmaster
	// Serving everyone already serves them; a list gains the name once.
	if served := ym.Served(); len(served) > 0 && !slices.Contains(served, user) {
		p.Serving = strings.Join(append(served, user), ",")
	}
	// Found before anything is made: a person made and no sign-in to
	// lead is the half-made person this plan is for.
	if _, err := lookPath(Executable(harness)); err != nil {
		label := HarnessLabels[harness]
		if label == "" {
			label = harness
		}
		return nil, fmt.Errorf("%s is not on PATH, so there is no sign-in to lead: see %s's own documentation; it must be on PATH as %s", Executable(harness), label, Executable(harness))
	}
	return p, nil
}

// UserLoginEnvAvailable refuses to create the login's env file where a
// group or another user's credential already names that path. The file
// need not exist yet for its path to be in use: creating it would change
// what that group or user reads.
func UserLoginEnvAvailable(home, path string, groups []Group, users []User) error {
	want, err := loginPathIdentity(path)
	if err != nil {
		return fmt.Errorf("login env file %s: %w", path, err)
	}
	for _, g := range groups {
		file, _ := g.Config["env_file"].(string)
		if file == "" {
			continue
		}
		have, err := loginPathIdentity(file)
		if err != nil {
			return fmt.Errorf("group %s env_file %s: %w", g.Name, file, err)
		}
		if have == want {
			return fmt.Errorf("login env file %s is already named by group %s; move that group's env_file first", path, g.Name)
		}
	}
	for _, u := range users {
		file := u.EnvFile
		if file == "" && !strings.ContainsAny(u.Name, `/\`) {
			file = filepath.Join(home, "users", u.Name+".env")
		}
		if file == "" {
			continue
		}
		have, err := loginPathIdentity(file)
		if err != nil {
			return fmt.Errorf("user %s env_file %s: %w", u.Name, file, err)
		}
		if have == want {
			return fmt.Errorf("login env file %s is already named by user %s; move that user's env_file first", path, u.Name)
		}
	}
	return nil
}

// loginPathIdentity resolves existing symlinks in the path and its
// parents, while still naming the not-yet-created env file by its
// resolved parent.
func loginPathIdentity(path string) (string, error) {
	p, err := ExpandPath(path)
	if err != nil {
		return "", err
	}
	p = filepath.Clean(p)
	var missing []string
	for current := p; ; current = filepath.Dir(current) {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return p, nil
		}
		missing = append(missing, filepath.Base(current))
	}
}

// UserLoginGroup is the group a person's login runs under for source,
// one of the mirrored groups: a copy of it (its prompt, arguments, model
// and the rest) with the plan's kind, the person's login and their env
// file. Its limit is 1, as a new agent group's is: twelve people at the
// limit of a shared builders would overrun the machine, and the operator
// raises one with group set.
func (p *UserLoginPlan) UserLoginGroup(source Group) Group {
	cfg := maps.Clone(source.Config)
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfg["kind"], cfg["login_for"], cfg["env_file"] = p.Harness, p.User, p.Env
	return Group{Name: p.User + "-" + source.Name, Runner: "herdr", Config: cfg, Limit: 1}
}

// EnvLine is the plan's env file as one line: the folder variable naming
// the folder the login lands in.
func (p *UserLoginPlan) EnvLine() string {
	return LoginFolderEnvs[p.Harness] + "=" + p.Folder + "\n"
}

// MirrorNames are the names of the groups the plan makes, in the order
// it makes them.
func (p *UserLoginPlan) MirrorNames() []string {
	names := make([]string, len(p.Mirrors))
	for i, g := range p.Mirrors {
		names[i] = p.User + "-" + g.Name
	}
	return names
}

// UserLoginEnv checks the groups whose login_for is user and returns
// their sorted first group, their single shared env_file and all of
// their sorted names. When no group carries user's login it returns
// empty values and a nil error. Groups naming different env files, or
// any group with no env_file, are refused so the CLI, the card and the
// carrier agree on what one sign-in can lead.
func UserLoginEnv(user string, groups []Group) (firstGroup, envFile string, groupNames []string, err error) {
	var theirs []Group
	for _, g := range groups {
		if g.LoginFor() == user {
			theirs = append(theirs, g)
		}
	}
	if len(theirs) == 0 {
		return "", "", nil, nil
	}
	slices.SortFunc(theirs, func(a, b Group) int { return strings.Compare(a.Name, b.Name) })
	groupNames = make([]string, len(theirs))
	files := map[string]bool{}
	for i, g := range theirs {
		groupNames[i] = g.Name
		file, _ := g.Config["env_file"].(string)
		files[file] = true
	}
	if len(files) != 1 {
		return "", "", groupNames, fmt.Errorf("several groups carry %s's login with different env files (%s), and a person signs in once: yardr harness login claude --group <group> leads one of them", user, strings.Join(groupNames, ", "))
	}
	envFile, _ = theirs[0].Config["env_file"].(string)
	if envFile == "" {
		return "", "", groupNames, fmt.Errorf("group %s carries %s's login with no env_file, so there is no login folder to sign in to", strings.Join(groupNames, ", "), user)
	}
	return theirs[0].Name, envFile, groupNames, nil
}

// SignInBead is the bead a sign-in of user to harness stands on, as the
// CLI and the user login carrier both open it: a question of the user's,
// carrying SignInMeta, which stays open for as long as the sign-in does
// (SigningIn).
func SignInBead(user, harness string) NewBead {
	return NewBead{For: user, Title: SignInQuestion, Type: TypeQuestion,
		Body:     "The sign-in of " + user + " to Claude waits for the code its page shows.",
		Metadata: map[string]any{SignInMeta: harness}}
}

// userLogin is LoginOf over what the transaction holds.
func (w *wtx) userLogin(user string) (UserLogin, error) {
	var groups []Group
	if err := each(w.Tx, bGroups, "", func(_ []byte, g *Group) error {
		groups = append(groups, *g)
		return nil
	}); err != nil {
		return UserLogin{}, err
	}
	var routes []Route
	if err := each(w.Tx, bRoutes, "", func(_ []byte, r *Route) error {
		routes = append(routes, *r)
		return nil
	}); err != nil {
		return UserLogin{}, err
	}
	var crew []CrewMember
	if err := each(w.Tx, bCrew, "", func(_ []byte, c *CrewMember) error {
		crew = append(crew, *c)
		return nil
	}); err != nil {
		return UserLogin{}, err
	}
	return LoginOf(user, groups, routes, crew), nil
}

// dropLogin takes what carries user's own login out of the yard, in the
// caller's write: the groups (through deleteGroup, so their live sessions are
// released and the routes naming them go), the routes that take the user's
// beads wherever they lead (a subsidy route is theirs too), and the user from
// each crew member's for= and its env_for_<user>. The login folder and its
// env file are files, and stay: they hold the person's transcripts, and no
// command erases them yet. It answers what it took.
func (w *wtx) dropLogin(user, actor string) (UserLogin, error) {
	l, err := w.userLogin(user)
	if err != nil || l.Empty() {
		return l, err
	}
	for _, g := range l.Groups {
		if _, _, err := w.deleteGroup(g, actor); err != nil {
			return l, err
		}
	}
	// Deleting a key that deleteGroup took with its group is no error.
	for _, id := range l.Routes {
		if err := w.Bucket(bRoutes).Delete([]byte(id)); err != nil {
			return l, err
		}
	}
	for _, name := range l.Crew {
		var c CrewMember
		if ok, err := get(w.Tx, bCrew, name, &c); err != nil {
			return l, err
		} else if !ok {
			continue
		}
		before := cloneCrew(&c)
		unserve(c.Config, user)
		c.Revision = newRevision()
		if err := put(w.Tx, bCrew, name, c); err != nil {
			return l, err
		} else if err := w.tellCrewStale(name, before, &c, actor); err != nil {
			return l, err
		}
	}
	return l, nil
}

// unserve takes user out of a crew config: from its for= list, which keeps
// its form (a string of commas or a list), and its env_for_<user> key. A list
// the removal empties is dropped whole: an empty for= serves everyone, which
// is what a crew member with none named does.
func unserve(cfg map[string]any, user string) {
	delete(cfg, EnvForPrefix+user)
	rest := slices.DeleteFunc(servedList(cfg), func(u string) bool { return u == user })
	switch cfg[CrewForKey].(type) {
	case nil:
		return
	case string:
		if len(rest) == 0 {
			delete(cfg, CrewForKey)
			return
		}
		cfg[CrewForKey] = strings.Join(rest, ",")
	default:
		if len(rest) == 0 {
			delete(cfg, CrewForKey)
			return
		}
		list := make([]any, len(rest))
		for i, u := range rest {
			list[i] = u
		}
		cfg[CrewForKey] = list
	}
}
