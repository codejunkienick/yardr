// The user login row of the run-ask list (runList): giving a user a
// login of their own, or opening their sign-in again, by card. A user
// who stands already (every teammate user invite made on a hosted yard)
// cannot be reached by user add --login, which refuses a user who
// exists, and user login refused anyone no group carried the login of;
// the row's command does the first-time setup where no login_for group
// stands and only opens the sign-in again where one does. The sign-in
// itself waits up to a quarter of an hour for the person's code and one
// carrying out runs at a time, so the approval is carried out by the
// yard's server (carry.go), which makes the setup, opens the sign-in's
// bead and launches the sign-in detached, never inside the carry.
//
// The row is registered here, beside its functions, and not in runList's
// literal: runask.go stands at the file-size ceiling the test gate holds
// (.yardr/test).
package store

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// userLoginUse is how a refusal names the user login a run ask takes.
const userLoginUse = "yardr user login <name>"

// userLoginDescription is the help line of user login that the card of
// the row keeps (internal/cli holds it to the command's Short).
const userLoginDescription = "Give a user a login of their own, or open their sign-in again"

func init() {
	runList = append(runList, runCommand{
		words:       []string{"user", "login"},
		use:         userLoginUse,
		description: userLoginDescription,
		parse:       parseUserLogin,
		check:       checkUserLogin,
		card:        (*wtx).userLoginCard,
		stale:       (*wtx).userLoginStale,
		weigh:       userLoginWeight,
		run:         runUserLogin,
	})
}

// IsUserLoginCarry says whether argv is a user login carrying out: an
// approved user login the yard's server makes the setup of and launches
// detached.
func IsUserLoginCarry(argv []string) bool {
	return len(argv) == 3 && argv[0] == "user" && argv[1] == "login"
}

// parseUserLogin reads the words of user login <name>.
func parseUserLogin(rest []string) ([]string, error) {
	if len(rest) != 1 {
		return nil, fmt.Errorf("--run takes %s: one user, not %d words", userLoginUse, len(rest))
	}
	return []string{rest[0]}, nil
}

// yardmasterName is the yardmaster crew member's name as the store reads
// it: the yardmaster setting, else the one crew member of a yard from
// before the setting, else the default name.
func (w *wtx) yardmasterName(crew []CrewMember) string {
	if name := w.settingValue(YardmasterSetting); name != "" {
		return name
	}
	if len(crew) == 1 {
		return crew[0].Name
	}
	return "yardmaster"
}

// loginYard is what planning a login reads of the yard, as the
// transaction holds it.
func (w *wtx) loginYard() (groups []Group, routes []Route, depots []Depot, users []User, crew []CrewMember, err error) {
	steps := []func() error{
		func() error {
			return each(w.Tx, bGroups, "", func(_ []byte, g *Group) error { groups = append(groups, *g); return nil })
		},
		func() error {
			return each(w.Tx, bRoutes, "", func(_ []byte, r *Route) error { routes = append(routes, *r); return nil })
		},
		func() error {
			return each(w.Tx, bDepots, "", func(_ []byte, d *Depot) error { depots = append(depots, *d); return nil })
		},
		func() error {
			return each(w.Tx, bUsers, "", func(_ []byte, u *User) error { users = append(users, *u); return nil })
		},
		func() error {
			return each(w.Tx, bCrew, "", func(_ []byte, c *CrewMember) error { crew = append(crew, *c); return nil })
		},
	}
	for _, s := range steps {
		if err := s(); err != nil {
			return nil, nil, nil, nil, nil, err
		}
	}
	return groups, routes, depots, users, crew, nil
}

// MappedEmails are the addresses the web_jwt_users mapping maps to user,
// in order. Exported so the server's user login carrier computes the
// same Seen map as the card.
func MappedEmails(m map[string]string, user string) []string {
	return mappedEmails(m, user)
}

// checkUserLogin is giving a user a login's own check as the asker: the
// setup writes the yardmaster's conversation, which is the operator's.
func checkUserLogin(ctx context.Context, w *wtx, argv []string, actor string) error {
	var crew []CrewMember
	if err := each(w.Tx, bCrew, "", func(_ []byte, c *CrewMember) error { crew = append(crew, *c); return nil }); err != nil {
		return err
	}
	return w.may(ctx, actor, ActChangeCrew, Object{Kind: KindCrew, Name: w.yardmasterName(crew), Ask: Askable})
}

// UserLoginSeen builds the Seen map that pins a user login card's state:
// the user's creation, the yardmaster's config, the web_jwt_users
// mapping, the mode (setup with plan != nil, or re-login with plan ==
// nil), and for setup mode the folder/env absence, mirrored groups and
// twin routes. Shared by the card and the server's carrier so carrying
// fails closed on any drift after approval (including a re-login card
// whose groups were removed before carrying).
func UserLoginSeen(name string, u *User, ymName string, ym *CrewMember, emails []string, login UserLogin, plan *UserLoginPlan) map[string]string {
	if len(emails) == 0 {
		emails = nil
	}
	seenMap := map[string]string{
		"user " + name:          seen(u.CreatedAt, u.CreatedBy),
		"yardmaster " + ymName:  seen(asStored(ym.Config)),
		"web_jwt_users " + name: seen(emails),
	}
	if plan == nil {
		seenMap["user "+name+" login"] = seen("relogin", login.Groups, login.EnvFiles)
		return seenMap
	}
	seenMap["user "+name+" login"] = seen("setup", plan.Folder, plan.Env)
	seenMap["login folder "+plan.Folder] = seen(plan.Folder, "absent")
	seenMap["login env "+plan.Env] = seen(plan.Env, "absent")
	mirrorPrints := make([]string, len(plan.Mirrors))
	for i, g := range plan.Mirrors {
		mirrorPrints[i] = granteeGroupFingerprint(g)
	}
	seenMap["mirrored groups"] = seen(mirrorPrints, plan.MirrorNames())
	twins := make([]string, len(plan.Routes))
	for i, r := range plan.Routes {
		typ := ""
		if r.Type != nil {
			typ = *r.Type
		}
		depot := ""
		if r.Depot != nil {
			depot = *r.Depot
		}
		prio := ""
		if r.BeadPriority != nil {
			prio = fmt.Sprint(*r.BeadPriority)
		}
		twins[i] = strings.Join([]string{r.Stage, typ, r.Group, depot, prio, strings.Join(r.Labels(), ",")}, "|")
	}
	slices.Sort(twins)
	seenMap["twin routes"] = seen(twins)
	return seenMap
}

// userLoginCard is the card of user login <name>: the first-time setup
// where no group carries the user's login (the folder, the env file, the
// mirrored groups, the twin routes, the yardmaster's conversation and
// the sign-in), and the sign-in opened again where one does (its env
// file). Asking refuses a user the yard has not, a setup the shared
// planner refuses, and a login carried on several env files, which is
// no one sign-in. The approval is held to the mode, the user's creation,
// the folder and env file's absence, the routes and groups a setup
// copies, and the yardmaster's standing (Seen).
func (w *wtx) userLoginCard(argv []string, asking bool) (AskRun, error) {
	name := argv[2]
	u, err := getUser(w.Tx, name)
	if err != nil {
		return AskRun{}, err
	}
	groups, routes, depots, users, crew, err := w.loginYard()
	if err != nil {
		return AskRun{}, err
	}
	ymName := w.yardmasterName(crew)
	var ym *CrewMember
	for i := range crew {
		if crew[i].Name == ymName {
			ym = &crew[i]
		}
	}
	if ym == nil {
		return AskRun{}, fmt.Errorf("the yardmaster talks with %s on their own login, and crew member %s: %w", name, ymName, ErrNotFound)
	}
	mapping, _, err := w.webJWTUsers()
	if err != nil {
		return AskRun{}, fmt.Errorf("%s: %w", WebJWTUsersSetting, err)
	}
	emails := mappedEmails(mapping, name)
	back := []string{"user", "rm", name}
	if len(emails) > 0 {
		back = []string{"user", "uninvite", name}
	}
	login := LoginOf(name, groups, routes, crew)
	if len(login.Groups) > 0 {
		_, envFile, groupNames, err := UserLoginEnv(name, groups)
		if err != nil {
			return AskRun{}, err
		}
		does := []string{
			fmt.Sprintf("opens %s's sign-in again, on the login the env file %s names (the groups %s carry it): the page to sign in on is mailed to them and the code it shows is asked of them.", name, envFile, strings.Join(groupNames, ", ")),
			"the sign-in runs detached, as its own command: approving does not wait for it, and it ends within the login's own timeouts.",
			fmt.Sprintf("removing %s with %s takes back the groups, the routes and the crew entries that carry their login; the login folder and the env file stay on disk.", name, runCommandLine(back)),
		}
		return AskRun{
			Argv:   argv,
			Does:   does,
			Back:   runCommandLine(back),
			BackBy: "only the operator",
			Seen:   UserLoginSeen(name, u, ymName, ym, emails, login, nil),
		}, nil
	}
	home := ""
	if w.store != nil {
		home = w.store.home
	}
	plan, err := PlanUserLogin(home, name, "claude", groups, routes, depots, users, ymName, ym, exec.LookPath)
	if err != nil {
		return AskRun{}, err
	}
	serves := fmt.Sprintf("%s serves %s on their login (%s%s)", ymName, name, EnvForPrefix, name)
	if plan.Serving != "" {
		serves += fmt.Sprintf(", and %s joins its for= (%s)", name, plan.Serving)
	}
	does := []string{
		fmt.Sprintf("gives %s a login of their own (Claude): the folder %s (0700) and the env file %s (0600) are made, the second naming the first (%s).", name, plan.Folder, plan.Env, ClaudeFolderEnv),
		fmt.Sprintf("a copy of every herdr group an operator route sends work to is made for them: %s (kind claude, login_for %s, the env file, limit 1).", strings.Join(plan.MirrorNames(), ", "), name),
		fmt.Sprintf("%s for %s's beads to the copies, one for each route of the operator's that sends work to a mirrored group first.", countNoun(len(plan.Routes), "twin route"), name),
		serves + ".",
		"their sign-in is opened: the page to sign in on is mailed to them and the code it shows is asked of them, and it runs detached, as its own command, so approving does not wait for it.",
		fmt.Sprintf("removing %s with %s takes back the groups, the routes and the crew entries that carry their login; the login folder and the env file stay on disk.", name, runCommandLine(back)),
	}
	return AskRun{
		Argv:   argv,
		Does:   does,
		Back:   runCommandLine(back),
		BackBy: "only the operator",
		Seen:   UserLoginSeen(name, u, ymName, ym, emails, login, plan),
	}, nil
}

// userLoginWeight weighs a user login card: a first login brings new
// groups the yard will run on the person's own seat (code, strong) and
// a sign-in that counts as the person in either mode (as-user).
func userLoginWeight(w *wtx, run AskRun) ([]AskWeight, error) {
	name := run.Argv[2]
	groups, routes, _, _, crew, err := w.loginYard()
	if err != nil {
		return nil, err
	}
	login := LoginOf(name, groups, routes, crew)
	asUser := weighs(WeightAsUser, fmt.Sprintf("%s signs in with their own account: the page is mailed to them, the code is asked of them, and the login it makes is theirs", name))
	if len(login.Groups) > 0 {
		return []AskWeight{asUser}, nil
	}
	return []AskWeight{
		weighs(WeightCode, fmt.Sprintf("new groups that run Claude on %s's own login, and routes that send %s's beads to them", name, name)),
		asUser,
	}, nil
}

// userLoginStale says why the ask seq for user login <name> is approved
// no more: the user was removed and added again since, the mode or the
// groups that carry the login changed, the folder or the env file
// stands, the routes or groups a setup copies moved, or the yardmaster
// or the mapping the card named stands otherwise.
func (w *wtx) userLoginStale(seq uint64, was, now AskRun) error {
	name := userLoginOf(was.Argv)
	if key := "user " + name; was.Seen[key] != now.Seen[key] {
		e := lastEvent(w.Tx, "user_added", naming("user", name))
		return fmt.Errorf("ask %d was written when the user %s was another: they were removed and added again since%s. %s", seq, name, since(e), nothingRan)
	}
	if key := "user " + name + " login"; was.Seen[key] != now.Seen[key] {
		return fmt.Errorf("ask %d was written when %s's login stood otherwise (a first login then and a sign-in again now, or the other way, or other groups carry it): it changed since. %s", seq, name, nothingRan)
	}
	for key := range was.Seen {
		if was.Seen[key] == now.Seen[key] {
			continue
		}
		switch {
		case strings.HasPrefix(key, "login folder "), strings.HasPrefix(key, "login env "):
			return fmt.Errorf("ask %d was written while %s stood not: it stands since. %s", seq, strings.TrimPrefix(strings.TrimPrefix(key, "login folder "), "login env "), nothingRan)
		case key == "mirrored groups", key == "twin routes":
			return fmt.Errorf("ask %d was written when the groups or routes a first login of %s copies stood otherwise: they changed since. %s", seq, name, nothingRan)
		case strings.HasPrefix(key, "yardmaster "):
			return fmt.Errorf("ask %d was written when the yardmaster's conversation with %s stood otherwise: it was changed since. %s", seq, name, nothingRan)
		case strings.HasPrefix(key, "web_jwt_users "):
			return fmt.Errorf("ask %d was written when another address mapped to %s in %s: the mapping was changed since. %s", seq, name, WebJWTUsersSetting, nothingRan)
		}
	}
	return staleCard(seq, was, now)
}

// userLoginOf is the user of a parsed user login <name>.
func userLoginOf(argv []string) string {
	if len(argv) > 2 {
		return argv[2]
	}
	return ""
}

// runUserLogin records a user login approval as to be carried out: the
// setup, the sign-in's bead and the detached sign-in are the server's,
// after this write, and nothing of them is written here.
func runUserLogin(ctx context.Context, w *wtx, argv []string, actor string, by *approvedOn) (string, error) {
	if err := checkUserLogin(ctx, w, argv, actor); err != nil {
		return "", err
	}
	return CarryPending, nil
}
