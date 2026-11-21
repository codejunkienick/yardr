package server

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/store"
)

// The server's carrying out of an approved user login card: under the
// apply lock, in the order below, as the operator who approved. Where no
// group carries the user's login it makes the first-time setup (the
// folder and the env file, the mirrored groups, the twin routes), opens
// the sign-in's bead BEFORE it serves the yardmaster's conversation (so
// store.SigningIn keeps the daemon from offering an operator login card
// for the new seat), and updates the yardmaster; where one does it opens
// the bead alone and writes nothing else. Either way it launches
// `yardr user login <name> --bead <id>` detached and completes at once:
// a sign-in waits up to a quarter of an hour for the person's code, and
// one carrying out runs at a time, so it never runs inside the carry.
// A setup that fails is undone whole (what this run made, in reverse),
// so the card may be filed again; and no second sign-in starts while
// one stands open, at the card or here.

func isUserLoginCarry(argv []string) bool { return store.IsUserLoginCarry(argv) }

// carryUserLoginClaimed runs a claimed user login carrying out under the
// apply lock, as carryClaimed does for an apply.
func (c *carrier) carryUserLoginClaimed(ctx context.Context, rec *store.Carry, run *store.AskRun) (bool, error) {
	release, err := config.AcquireApplyLock(c.home)
	if err != nil {
		return true, fmt.Errorf("carrying out ask %d on %s: the apply lock: %w", rec.Ask, rec.Bead, err)
	}
	defer release()
	result, failure := c.carryUserLogin(ctx, rec, run)
	if failure != "" {
		if err := c.st.FailCarry(ctx, rec.Bead, rec.Ask, c.boot, failure); err != nil {
			return true, err
		}
		c.log.Warn("carrying out a user login ask failed", "bead", rec.Bead, "ask", rec.Ask, "failure", failure)
		return true, nil
	}
	if err := c.st.CompleteCarry(ctx, rec.Bead, rec.Ask, c.boot, result); err != nil {
		return true, err
	}
	c.log.Info("carried out a user login ask", "bead", rec.Bead, "ask", rec.Ask, "command", run.Command())
	return true, nil
}

// userLoginFail words a user login carrying failure where nothing of
// a setup was made, or where a whole setup stands on purpose (a sign-in
// that was not launched): what happened, and to ask again.
func userLoginFail(why string) string {
	return why + " Ask again if it is still wanted."
}

// userLoginMade is what one carrying out itself made of a first-time
// setup, in the order it made it: the folder (its Mkdir succeeded), the
// env file (its exclusive create succeeded), each mirrored group
// (PutGroup succeeded) and each twin route (the id AddRoute returned).
// A path or record that stood before is never on it, so undoing it
// takes nothing of another's.
type userLoginMade struct {
	folder string
	env    string
	groups []string
	routes []string
}

// undoUserLogin takes back what made holds, in reverse: the routes,
// the groups, the env file, then the folder. The folder goes with
// os.Remove, not RemoveAll: a folder made a moment ago is empty, and
// one that is not is left standing and named. It answers what still
// stands, each named as it stands (a route by its id, a group by its
// name, a file by its path); an empty answer means nothing stands.
func (c *carrier) undoUserLogin(ctx context.Context, made userLoginMade, actor string) []string {
	var left []string
	for i := len(made.routes) - 1; i >= 0; i-- {
		if err := c.st.DeleteRoute(ctx, made.routes[i], actor); err != nil {
			left = append(left, "route "+made.routes[i])
		}
	}
	for i := len(made.groups) - 1; i >= 0; i-- {
		if _, err := c.st.DeleteGroup(ctx, made.groups[i], actor); err != nil {
			left = append(left, "group "+made.groups[i])
		}
	}
	if made.env != "" {
		if err := os.Remove(made.env); err != nil && !errors.Is(err, os.ErrNotExist) {
			left = append(left, "the env file "+made.env)
		}
	}
	if made.folder != "" {
		if err := os.Remove(made.folder); err != nil && !errors.Is(err, os.ErrNotExist) {
			left = append(left, "the login folder "+made.folder)
		}
	}
	return left
}

// userLoginMadeFail words a setup failure after its undo: where the
// undo emptied what the run made, nothing stands and the card may be
// filed again, with no taking back named; where an undo step itself
// failed, exactly what still stands is named, and only then is the
// card's way back named for it.
func userLoginMadeFail(name, back, why string, left []string) string {
	if len(left) == 0 {
		return why + " Nothing stands from it, so the card may be filed again."
	}
	if back == "" {
		back = "yardr user rm " + name
	}
	return why + fmt.Sprintf(" What still stands: %s. Take back what stands with %s.", strings.Join(left, ", "), back)
}

func (c *carrier) carryUserLogin(ctx context.Context, rec *store.Carry, run *store.AskRun) (string, string) {
	name := run.Argv[2]
	back := run.Back
	u, err := c.st.GetUser(ctx, name)
	if err != nil {
		return "", userLoginFail(fmt.Sprintf("user %s does not stand: %v.", name, err))
	}
	groups, err := c.st.Groups(ctx)
	if err != nil {
		return "", userLoginFail(fmt.Sprintf("the yard's groups are not read: %v.", err))
	}
	routes, err := c.st.Routes(ctx)
	if err != nil {
		return "", userLoginFail(fmt.Sprintf("the yard's routes are not read: %v.", err))
	}
	depots, err := c.st.Depots(ctx)
	if err != nil {
		return "", userLoginFail(fmt.Sprintf("the yard's depots are not read: %v.", err))
	}
	users, err := c.st.Users(ctx)
	if err != nil {
		return "", userLoginFail(fmt.Sprintf("the yard's users are not read: %v.", err))
	}
	crew, err := c.st.Crew(ctx)
	if err != nil {
		return "", userLoginFail(fmt.Sprintf("the yard's crew is not read: %v.", err))
	}
	settings, err := c.st.Settings(ctx)
	if err != nil {
		return "", userLoginFail(fmt.Sprintf("the yard's settings are not read: %v.", err))
	}
	var mapping map[string]string
	if raw := settings[store.WebJWTUsersSetting]; raw != "" {
		mapping, err = store.ParseWebJWTUsers(raw)
		if err != nil {
			return "", userLoginFail(fmt.Sprintf("%s: %v.", store.WebJWTUsersSetting, err))
		}
	}
	emails := store.MappedEmails(mapping, name)
	ymName := settings[store.YardmasterSetting]
	if ymName == "" && len(crew) == 1 {
		ymName = crew[0].Name
	}
	if ymName == "" {
		ymName = "yardmaster"
	}
	var ym *store.CrewMember
	for i := range crew {
		if crew[i].Name == ymName {
			ym = &crew[i]
		}
	}
	if ym == nil {
		return "", userLoginFail(fmt.Sprintf("the yardmaster talks with %s on their own login, and crew member %s: %v.", name, ymName, store.ErrNotFound))
	}
	login := store.LoginOf(name, groups, routes, crew)
	setup := len(login.Groups) == 0
	var plan *store.UserLoginPlan
	if setup {
		plan, err = store.PlanUserLogin(c.home, name, "claude", groups, routes, depots, users, ymName, ym, exec.LookPath)
		if err != nil {
			return "", userLoginFail(fmt.Sprintf("ask %d: %s.", rec.Ask, err))
		}
	} else {
		if _, _, _, err := store.UserLoginEnv(name, groups); err != nil {
			return "", userLoginFail(fmt.Sprintf("ask %d: %s.", rec.Ask, err))
		}
	}
	// Hold the carry to what the approved card pinned: a re-login card
	// whose groups went after approval must never escalate to first-time
	// setup, and a setup card whose routes, groups or yardmaster moved
	// must fail closed before anything is written.
	nowSeen := store.UserLoginSeen(name, u, ymName, ym, emails, login, plan)
	if len(run.Seen) == 0 || !maps.Equal(nowSeen, run.Seen) {
		return "", userLoginFail(fmt.Sprintf("ask %d: the yard changed since the card was approved, so nothing was written.", rec.Ask))
	}
	// No second sign-in while one stands open: a sign-in opened since
	// the card was approved fails this carry closed, before any write
	// (this carry opens its own sign-in bead below, so it never reads
	// that one as another's).
	beads, err := c.st.ListBeads(ctx, store.BeadFilter{Type: store.TypeQuestion})
	if err != nil {
		return "", userLoginFail(fmt.Sprintf("the yard's beads are not read: %v.", err))
	}
	if bead, ok := store.SigningInBead(beads, name, "claude", c.now()); ok {
		return "", store.SignInStands(name, bead) + ", so no second sign-in was started and nothing was written."
	}
	// made holds what this run itself makes of the setup, so a failure
	// in the span below undoes exactly that and nothing that stood.
	var made userLoginMade
	failMade := func(why string) (string, string) {
		return "", userLoginMadeFail(name, back, why, c.undoUserLogin(ctx, made, rec.Actor))
	}
	if setup {
		if err := os.MkdirAll(filepath.Dir(plan.Folder), 0o700); err != nil {
			return "", userLoginFail(fmt.Sprintf("the login folder of %s was not made: %v.", name, err))
		}
		if err := os.Mkdir(plan.Folder, 0o700); err != nil {
			return "", userLoginFail(fmt.Sprintf("the login folder of %s was not made: %v.", name, err))
		}
		made.folder = plan.Folder
		if err := os.MkdirAll(filepath.Dir(plan.Env), 0o700); err != nil {
			return failMade(fmt.Sprintf("the env file of %s was not made: %v.", name, err))
		}
		created, err := writeLoginFile(plan.Env, []byte(plan.EnvLine()), 0o600)
		if created {
			made.env = plan.Env
		}
		if err != nil {
			return failMade(fmt.Sprintf("the env file of %s was not made: %v.", name, err))
		}
		for _, source := range plan.Mirrors {
			g := plan.UserLoginGroup(source)
			if err := c.st.PutGroup(ctx, g, rec.Actor); err != nil {
				return failMade(fmt.Sprintf("group %s was not made: %v.", g.Name, err))
			}
			made.groups = append(made.groups, g.Name)
		}
		for _, r := range plan.Routes {
			madeRoute, err := c.st.AddRoute(ctx, r, rec.Actor)
			if err != nil {
				return failMade(fmt.Sprintf("a route for %s's beads was not made: %v.", name, err))
			}
			made.routes = append(made.routes, madeRoute.ID)
		}
	}
	// The sign-in's bead is opened before the yardmaster is updated, as
	// the CLI opens it before it serves the conversation.
	bead, err := c.st.CreateBead(ctx, store.SignInBead(name, "claude"), rec.Actor)
	if err != nil {
		if setup {
			return failMade(fmt.Sprintf("the sign-in bead of %s was not opened: %v.", name, err))
		}
		return "", userLoginFail(fmt.Sprintf("the sign-in bead of %s was not opened: %v.", name, err))
	}
	if setup {
		if err := c.serveUserLogin(ctx, plan, rec.Actor); err != nil {
			c.closeSignInBead(ctx, bead.ID, name, rec.Actor)
			return failMade(fmt.Sprintf("the yardmaster's conversation with %s was not served: %v.", name, err))
		}
	}
	spawn := c.login
	if spawn == nil {
		spawn = spawnUserLogin
	}
	pid, err := spawn(c.home, rec.Actor, name, bead.ID)
	if err != nil {
		c.closeSignInBead(ctx, bead.ID, name, rec.Actor)
		if setup {
			return "", userLoginFail(fmt.Sprintf("the login setup for %s stands, and the sign-in was not launched: %v.", name, err))
		}
		return "", userLoginFail(fmt.Sprintf("the sign-in of %s was not launched: %v.", name, err))
	}
	c.log.Info("carrying out a user login: the sign-in runs detached", "user", name, "bead", bead.ID, "pid", pid)
	if setup {
		return fmt.Sprintf("user %s given a login of their own: folder %s, env file %s, groups %s and %s; their sign-in runs detached on bead %s", name, plan.Folder, plan.Env, strings.Join(plan.MirrorNames(), ", "), plural(len(plan.Routes), "twin route"), bead.ID), ""
	}
	return fmt.Sprintf("user %s's sign-in opened again on bead %s; it runs detached", name, bead.ID), ""
}

// serveUserLogin sets the yardmaster's env_for_<user> of plan, and the
// user in its for= where that names some, as the CLI's serveUser does.
func (c *carrier) serveUserLogin(ctx context.Context, p *store.UserLoginPlan, actor string) error {
	sets := map[string]any{store.EnvForPrefix + p.User: p.Env}
	if p.Serving != "" {
		sets[store.CrewForKey] = p.Serving
	}
	for try := 0; ; try++ {
		have, err := c.st.GetCrew(ctx, p.Crew)
		if err != nil {
			return err
		}
		def := *have
		if def.Config, err = store.MergeConfig(p.Crew, have.Config, sets, nil); err != nil {
			return err
		}
		if _, err = c.st.PutCrew(ctx, def, actor); err == nil {
			return nil
		}
		if try == 4 || !errors.Is(err, store.ErrConflict) {
			return err
		}
	}
}

// closeSignInBead closes the bead of a sign-in that never started.
func (c *carrier) closeSignInBead(ctx context.Context, bead, user, actor string) {
	_, _ = c.st.CloseBead(ctx, bead, "the sign-in of "+user+" ended", store.Expect{}, actor)
}

// writeLoginFile writes data to a file that is not there: a login's env
// file is never written over. It reports whether it created the file,
// so a failed write is undone only where this call made the file.
func writeLoginFile(path string, data []byte, mode os.FileMode) (bool, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return false, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return true, err
	}
	if err := f.Close(); err != nil {
		return true, err
	}
	return true, nil
}

// spawnUserLogin starts `yardr user login <name> --bead <bead>` for the
// home, detached, as actor: the sign-in adopts the bead the carrying out
// opened and runs for as long as it waits for the person's code, which
// no carry may wait for. What it prints is discarded; the sign-in mails
// its page and asks its code through the yard.
func spawnUserLogin(home, actor, name, bead string) (int, error) {
	self, err := executable()
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(self, "--home", home, "--actor", actor, "user", "login", name, "--bead", bead)
	cmd.Env = scrubbedGeneralEnv(os.Environ())
	cmd.Dir = home
	if _, err := SpawnDetached(cmd, os.DevNull); err != nil {
		return 0, err
	}
	return cmd.Process.Pid, nil
}
