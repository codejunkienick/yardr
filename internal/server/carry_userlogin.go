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

// userLoginFail words a user login carrying failure: what happened, and
// how the setup is taken back or the card filed again.
func userLoginFail(name, back, why string, made bool) string {
	if made {
		if back == "" {
			back = "yardr user rm " + name
		}
		return why + fmt.Sprintf(" Take back what stands with %s (the login folder stays on disk), then file the card again.", back)
	}
	return why + " Ask again if it is still wanted."
}

func (c *carrier) carryUserLogin(ctx context.Context, rec *store.Carry, run *store.AskRun) (string, string) {
	name := run.Argv[2]
	back := run.Back
	u, err := c.st.GetUser(ctx, name)
	if err != nil {
		return "", userLoginFail(name, back, fmt.Sprintf("user %s does not stand: %v.", name, err), false)
	}
	groups, err := c.st.Groups(ctx)
	if err != nil {
		return "", userLoginFail(name, back, fmt.Sprintf("the yard's groups are not read: %v.", err), false)
	}
	routes, err := c.st.Routes(ctx)
	if err != nil {
		return "", userLoginFail(name, back, fmt.Sprintf("the yard's routes are not read: %v.", err), false)
	}
	depots, err := c.st.Depots(ctx)
	if err != nil {
		return "", userLoginFail(name, back, fmt.Sprintf("the yard's depots are not read: %v.", err), false)
	}
	users, err := c.st.Users(ctx)
	if err != nil {
		return "", userLoginFail(name, back, fmt.Sprintf("the yard's users are not read: %v.", err), false)
	}
	crew, err := c.st.Crew(ctx)
	if err != nil {
		return "", userLoginFail(name, back, fmt.Sprintf("the yard's crew is not read: %v.", err), false)
	}
	settings, err := c.st.Settings(ctx)
	if err != nil {
		return "", userLoginFail(name, back, fmt.Sprintf("the yard's settings are not read: %v.", err), false)
	}
	var mapping map[string]string
	if raw := settings[store.WebJWTUsersSetting]; raw != "" {
		mapping, err = store.ParseWebJWTUsers(raw)
		if err != nil {
			return "", userLoginFail(name, back, fmt.Sprintf("%s: %v.", store.WebJWTUsersSetting, err), false)
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
		return "", userLoginFail(name, back, fmt.Sprintf("the yardmaster talks with %s on their own login, and crew member %s: %v.", name, ymName, store.ErrNotFound), false)
	}
	login := store.LoginOf(name, groups, routes, crew)
	setup := len(login.Groups) == 0
	var plan *store.UserLoginPlan
	if setup {
		plan, err = store.PlanUserLogin(c.home, name, "claude", groups, routes, depots, users, ymName, ym, exec.LookPath)
		if err != nil {
			return "", userLoginFail(name, back, fmt.Sprintf("ask %d: %s.", rec.Ask, err), false)
		}
	} else {
		if _, _, _, err := store.UserLoginEnv(name, groups); err != nil {
			return "", userLoginFail(name, back, fmt.Sprintf("ask %d: %s.", rec.Ask, err), false)
		}
	}
	// Hold the carry to what the approved card pinned: a re-login card
	// whose groups went after approval must never escalate to first-time
	// setup, and a setup card whose routes, groups or yardmaster moved
	// must fail closed before anything is written.
	nowSeen := store.UserLoginSeen(name, u, ymName, ym, emails, login, plan)
	if len(run.Seen) == 0 || !maps.Equal(nowSeen, run.Seen) {
		return "", userLoginFail(name, back, fmt.Sprintf("ask %d: the yard changed since the card was approved, so nothing was written.", rec.Ask), false)
	}
	if setup {
		if err := os.MkdirAll(filepath.Dir(plan.Folder), 0o700); err != nil {
			return "", userLoginFail(name, back, fmt.Sprintf("the login folder of %s was not made: %v.", name, err), false)
		}
		if err := os.Mkdir(plan.Folder, 0o700); err != nil {
			return "", userLoginFail(name, back, fmt.Sprintf("the login folder of %s was not made: %v.", name, err), false)
		}
		if err := os.MkdirAll(filepath.Dir(plan.Env), 0o700); err != nil {
			return "", userLoginFail(name, back, fmt.Sprintf("the env file of %s was not made: %v.", name, err), true)
		}
		if err := writeLoginFile(plan.Env, []byte(plan.EnvLine()), 0o600); err != nil {
			return "", userLoginFail(name, back, fmt.Sprintf("the env file of %s was not made: %v.", name, err), true)
		}
		for _, source := range plan.Mirrors {
			g := plan.UserLoginGroup(source)
			if err := c.st.PutGroup(ctx, g, rec.Actor); err != nil {
				return "", userLoginFail(name, back, fmt.Sprintf("group %s was not made: %v.", g.Name, err), true)
			}
		}
		for _, r := range plan.Routes {
			if _, err := c.st.AddRoute(ctx, r, rec.Actor); err != nil {
				return "", userLoginFail(name, back, fmt.Sprintf("a route for %s's beads was not made: %v.", name, err), true)
			}
		}
	}
	// The sign-in's bead is opened before the yardmaster is updated, as
	// the CLI opens it before it serves the conversation.
	bead, err := c.st.CreateBead(ctx, store.SignInBead(name, "claude"), rec.Actor)
	if err != nil {
		return "", userLoginFail(name, back, fmt.Sprintf("the sign-in bead of %s was not opened: %v.", name, err), setup)
	}
	if setup {
		if err := c.serveUserLogin(ctx, plan, rec.Actor); err != nil {
			c.closeSignInBead(ctx, bead.ID, name, rec.Actor)
			return "", userLoginFail(name, back, fmt.Sprintf("the yardmaster's conversation with %s was not served: %v.", name, err), true)
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
			return "", userLoginFail(name, back, fmt.Sprintf("the login setup for %s stands, and the sign-in was not launched: %v.", name, err), false)
		}
		return "", userLoginFail(name, back, fmt.Sprintf("the sign-in of %s was not launched: %v.", name, err), false)
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
// file is never written over.
func writeLoginFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
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
