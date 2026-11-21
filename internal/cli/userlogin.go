package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"yardr.dev/yardr/internal/agents"
	"yardr.dev/yardr/internal/store"
)

// A person with a login of their own is made whole by one command
// (docs/groups.md, "A person with a login of their own"): yardr user add
// <name> --login claude makes, in this order, the user; the folder their
// login lands in and the env file that names it; the groups that run on that
// login; the routes that send their beads to them; the yardmaster's
// conversation with them; and opens the sign-in, whose link goes to them and
// whose code they send back. Each step is a command that stands on its own
// (user add, group add, route add --originator, crew set, harness login
// --group), in the order the docs gave them by hand; a slip in any of them
// bills one person's work to another's login, which is why one command makes
// them. Which groups run on the login and which stages they take are not
// said by the design: every herdr group an operator route serves is copied
// onto the login, and the routes that take them are twinned for the
// person's beads.

// planUserLogin gathers what the shared planner (store.PlanUserLogin)
// reads from the yard as it stands, so the CLI and the user login card
// plan the same setup.
func (a *app) planUserLogin(ctx context.Context, st store.API, user, harness string) (*store.UserLoginPlan, error) {
	cfg, err := a.config()
	if err != nil {
		return nil, err
	}
	groups, err := st.Groups(ctx)
	if err != nil {
		return nil, err
	}
	routes, err := st.Routes(ctx)
	if err != nil {
		return nil, err
	}
	depots, err := st.Depots(ctx)
	if err != nil {
		return nil, err
	}
	users, err := st.Users(ctx)
	if err != nil {
		return nil, err
	}
	crewName := yardmasterOf(ctx, st)
	ym, err := st.GetCrew(ctx, crewName)
	if err != nil {
		return nil, fmt.Errorf("the yardmaster talks with %s on their own login, and %w", user, err)
	}
	return store.PlanUserLogin(cfg.Home, user, harness, groups, routes, depots, users, crewName, ym, lookPath)
}

// userLoginOf is what carries user's own login in the yard, read from its
// groups, routes and crew as the store reads them when it removes the user.
func userLoginOf(ctx context.Context, st store.API, user string) (store.UserLogin, error) {
	groups, err := st.Groups(ctx)
	if err != nil {
		return store.UserLogin{}, err
	}
	routes, err := st.Routes(ctx)
	if err != nil {
		return store.UserLogin{}, err
	}
	crew, err := st.Crew(ctx)
	if err != nil {
		return store.UserLogin{}, err
	}
	return store.LoginOf(user, groups, routes, crew), nil
}

// userLoginMade is what yardr user add --login made, as --json prints it.
type userLoginMade struct {
	User   string      `json:"user"`
	Folder string      `json:"folder,omitempty"`
	Env    string      `json:"env_file,omitempty"`
	Groups []string    `json:"groups,omitempty"`
	Routes []string    `json:"routes,omitempty"`
	Crew   string      `json:"crew,omitempty"`
	SignIn *userSignIn `json:"sign_in,omitempty"`
}

// userSignIn is how far the sign-in got: the link mailed to the person, the
// ask for their code, whether it ended in a login, and whether the login
// answers a call.
type userSignIn struct {
	Mailed   bool   `json:"mailed"`
	Bead     string `json:"bead,omitempty"`
	Ask      uint64 `json:"ask,omitempty"`
	SignedIn bool   `json:"signed_in"`
	// Proved is whether one real call to the harness answered on the login.
	Proved  bool     `json:"proved"`
	Try     tryCheck `json:"-"`
	Account string   `json:"account,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// addUserLogin is yardr user add <name> --login <harness>: the user, then
// what makes the person whole, then their sign-in. It says each step as it
// is made, so that one that fails shows what stands and how to take it back.
func (a *app) addUserLogin(c *cobra.Command, st store.API, user, harness string) error {
	ctx := c.Context()
	p, err := a.planUserLogin(ctx, st, user, harness)
	if err != nil {
		return err
	}
	w := c.OutOrStdout()
	say := func(format string, args ...any) {
		if !a.json {
			fmt.Fprintf(w, format+"\n", args...)
		}
	}
	// The yard holds what was made when a step fails, and takes it back
	// with user rm; the folder stays, as it does there.
	failed := func(step string, err error) error {
		return fmt.Errorf("%s: %w; user %s stands as far as it went, and yardr user rm %s takes back what the yard holds of it (the folder stays on disk)", step, err, user, user)
	}
	if _, err := st.AddUser(ctx, user, a.who()); err != nil {
		return err
	}
	say("user %s added", user)

	made, first, bead, err := a.buildUserLogin(c, st, p, "", say, failed)
	if err != nil {
		return err
	}

	// They read the page and give the code in a device or where a source
	// tells them, and a new user has neither: say so while the sign-in waits,
	// which the operator's other terminal can set right.
	say("  %s has no device yet, and reads the page and gives the code at one: yardr user device pair %s <device> --url <the address the yard is reached at>", user, user)
	// Every copy names the one env file, so the sign-in leads under the
	// first of them.
	in, err := a.signIn(c, st, user, first, bead)
	made.SignIn = in
	if a.json {
		if oerr := a.out(w, made, nil); oerr != nil {
			return oerr
		}
	}
	return err
}

// buildUserLogin makes what giving the user of plan p a login takes,
// after the user stands: the folder and the env file, the mirrored
// groups, the twin routes, the sign-in's bead (bead, or a fresh one where
// it names none) and the yardmaster's conversation, in that order. It
// answers what it made, the first mirrored group and the sign-in's bead.
// failed words a step's failure as the caller has it.
func (a *app) buildUserLogin(c *cobra.Command, st store.API, p *store.UserLoginPlan, bead string, say func(string, ...any), failed func(string, error) error) (*userLoginMade, string, string, error) {
	ctx := c.Context()
	user, harness := p.User, p.Harness
	made := &userLoginMade{User: user}
	if err := os.MkdirAll(filepath.Dir(p.Folder), 0o700); err != nil {
		return nil, "", "", failed("login folder", err)
	}
	if err := os.Mkdir(p.Folder, 0o700); err != nil {
		return nil, "", "", failed("login folder", err)
	}
	if err := os.MkdirAll(filepath.Dir(p.Env), 0o700); err != nil {
		return nil, "", "", failed("env file", err)
	}
	if err := writeNewFile(p.Env, []byte(p.EnvLine()), 0o600); err != nil {
		return nil, "", "", failed("env file", err)
	}
	made.Folder, made.Env = p.Folder, p.Env
	say("  login folder %s (0700), env file %s (0600)", p.Folder, p.Env)

	var first string
	for _, source := range p.Mirrors {
		g := p.UserLoginGroup(source)
		if err := st.PutGroup(ctx, g, a.who()); err != nil {
			return nil, "", "", failed("group "+g.Name, err)
		}
		if first == "" {
			first = g.Name
		}
		made.Groups = append(made.Groups, g.Name)
		say("  group %s (a copy of %s: kind %s, login_for %s, limit %d)", g.Name, source.Name, harness, user, g.Limit)
	}

	for _, r := range p.Routes {
		saved, err := st.AddRoute(ctx, r, a.who())
		if err != nil {
			return nil, "", "", failed("route", err)
		}
		made.Routes = append(made.Routes, saved.ID)
		scope := ""
		if saved.Depot != nil {
			scope = " in " + *saved.Depot
		}
		say("  route %s: %s%s, for %s's beads -> %s", saved.ID, saved.Stage, scope, user, saved.Group)
	}

	// The sign-in's bead is made before the conversation is served, since
	// the daemon starts that session as soon as it is, on a login folder
	// that holds no login yet, and reads this bead open to offer the
	// operator no login card for it while the person signs in
	// (store.SigningIn).
	openedHere := false
	if bead == "" {
		var err error
		bead, err = a.openSignIn(ctx, st, user, harness)
		if err != nil {
			return nil, "", "", failed("the sign-in", err)
		}
		openedHere = true
	}
	if err := a.serveUser(ctx, st, p); err != nil {
		if openedHere {
			a.closeSignIn(ctx, st, bead, user)
		}
		return nil, "", "", failed("the yardmaster's conversation", err)
	}
	made.Crew = p.Crew
	say("  %s serves %s (%s%s)", p.Crew, user, store.EnvForPrefix, user)
	return made, first, bead, nil
}

// serveUser sets the yardmaster's env_for_<user>, and the user in its for=
// where that names some: the line session keeps the yard, and this one is
// the conversation with the person, on their login.
func (a *app) serveUser(ctx context.Context, st store.API, p *store.UserLoginPlan) error {
	sets := map[string]any{store.EnvForPrefix + p.User: p.Env}
	if p.Serving != "" {
		sets[store.CrewForKey] = p.Serving
	}
	// The daemon re-mints a running crew member's revision between the read
	// and the write now and then: read again and apply the same change.
	for try := 0; ; try++ {
		have, err := st.GetCrew(ctx, p.Crew)
		if err != nil {
			return err
		}
		def := *have
		if def.Config, err = store.MergeConfig(p.Crew, have.Config, sets, nil); err != nil {
			return err
		}
		if _, err = st.PutCrew(ctx, def, a.who()); err == nil {
			return nil
		}
		if try == 4 || !errors.Is(err, store.ErrConflict) {
			return err
		}
	}
}

// writeNewFile writes data to a file that is not there: a login's env file is
// never written over.
func writeNewFile(path string, data []byte, mode os.FileMode) error {
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

// signInQuestion is the question of the ask for the person's code, and
// the title of the bead the ask stands on (store.SignInQuestion).
const signInQuestion = store.SignInQuestion

// openSignIn makes the bead user's sign-in stands on, which the code it asks
// for goes on and which stays open for as long as the sign-in does
// (store.SignInMeta). signIn closes it, whatever its end; a caller that
// fails before it gets there closes it itself.
func (a *app) openSignIn(ctx context.Context, st store.API, user, harness string) (string, error) {
	b, err := st.CreateBead(ctx, store.SignInBead(user, harness), a.who())
	if err != nil {
		return "", err
	}
	return b.ID, nil
}

// closeSignIn closes the bead of a sign-in that has ended, in a login or not.
// It is not stopped by the signal that ended the command.
func (a *app) closeSignIn(ctx context.Context, st store.API, bead, user string) {
	end, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_, _ = st.CloseBead(end, bead, "the sign-in of "+user+" ended", store.Expect{}, a.who())
}

// signIn leads user's sign-in under group's login and relays it to them:
// the page to sign in on is mailed to them, and the code it shows is asked
// of them (a login-input secret ask, on bead) and handed to the waiting
// login. It waits for the login to end, within the login's own timeouts, and
// says how it went; a login that did not end in one is an error, after the
// line that says why, with the command that opens it again. A login that
// ended well starts the person's conversation sessions that sat at the login
// screen again, before the bead is closed.
func (a *app) signIn(c *cobra.Command, st store.API, user, group, bead string) (*userSignIn, error) {
	// A closed terminal ends the command as a stop does: the bead must not
	// outlive it, since the daemon reads it open as a sign-in in progress. Not
	// when it runs under nohup: asking for the signal would undo the ignoring.
	stops := []os.Signal{os.Interrupt, syscall.SIGTERM}
	if !signal.Ignored(syscall.SIGHUP) {
		stops = append(stops, syscall.SIGHUP)
	}
	ctx, stop := signal.NotifyContext(c.Context(), stops...)
	defer stop()
	defer a.closeSignIn(ctx, st, bead, user)
	// What is said while the sign-in waits goes to the screen, and in JSON
	// mode to stderr, where the one document of the run is not.
	w := c.OutOrStdout()
	if a.json {
		w = c.ErrOrStderr()
	}
	say := func(format string, args ...any) { fmt.Fprintf(w, "  "+format+"\n", args...) }
	in := &userSignIn{}
	fail := func(err error) (*userSignIn, error) {
		in.Error = err.Error()
		say("%s's sign-in did not finish: %v", user, err)
		say("yardr user login %s opens it again", user)
		return in, errCheckFailed
	}
	h, ok := agents.Lookup("claude")
	if !ok {
		return fail(errors.New("yardr has no harness file for claude"))
	}
	env, file, err := groupLoginEnv(ctx, st, group)
	if err != nil {
		return fail(err)
	}
	path, err := lookPath(store.Executable(h.Kind))
	if err != nil {
		return fail(fmt.Errorf("%s is not on PATH, so there is nothing to log in: %s", store.Executable(h.Kind), agents.InstallHint(h.Kind)))
	}

	needs, needsOut := io.Pipe()
	answersIn, answers := io.Pipe()
	leadCtx, endLead := context.WithCancel(ctx)
	defer endLead()
	led := make(chan error, 1)
	go func() {
		err := leadLogin(leadCtx, h, path, answersIn, needsOut, env, group, st)
		// Nothing reads the answers of a login that has ended: a write still
		// waiting for the leader to take it is let go.
		answersIn.Close()
		needsOut.CloseWithError(err)
		led <- err
	}()
	open, relayed := a.relayNeeds(leadCtx, st, user, bead, needs, answers, in, say)
	if relayed != nil {
		// Whatever stopped the relaying, the login must end: it is stopped,
		// and what it still prints is read to its end so it is not left
		// blocked on a pipe nobody reads.
		endLead()
	}
	_ = answers.Close()
	_, _ = io.Copy(io.Discard, needs)
	err = <-led
	if relayed != nil {
		err = relayed
	}
	// What was asked of them and not answered is taken back, and the bead the
	// asks stand on is closed on the way out: a sign-in that ended leaves
	// nothing waiting.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for _, q := range open {
		_ = st.TakeBackAsk(cleanup, q.bead, q.seq, "the sign-in ended", a.who())
	}
	if err != nil {
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			err = errors.New("stopped before it ended")
		case errors.Is(err, errCheckFailed):
			// The login command ended well and the call did not answer.
			err = fmt.Errorf("the login ended, and one real call to %s did not answer (%s): %s", h.Label, in.Try.State, in.Try.Output)
		}
		return fail(err)
	}
	in.SignedIn = true
	in.Account = loginFolderAccount(env)
	said := user + " is signed in"
	if in.Account != "" {
		said += " as " + in.Account
	}
	if in.Proved {
		said += ", and one call to " + h.Label + " answered"
	}
	say("%s (the login is in %s)", said, file)
	a.restartSeats(cleanup, st, user, h.Kind, say)
	return in, nil
}

// restartSeats starts user's conversation sessions that sit at the harness's
// login screen again: they started on a folder that held no login, and the
// harness reads its login as it starts. While the sign-in is open the
// daemon offers the operator no card for them (store.SigningIn), so nothing
// else would start them again, as the daemon does for a login it leads
// itself (docs/crew.md). The bead is still open: the daemon must not see the
// sign-in ended while a session at the screen is still to be restarted.
func (a *app) restartSeats(ctx context.Context, st store.API, user, kind string, say func(string, ...any)) {
	crew, err := st.Crew(ctx)
	if err != nil {
		say("%s's conversation could not be looked up to start it again: %v", user, err)
		return
	}
	def := defaultKind(ctx, st)
	for _, r := range crew {
		cs := r.Session(user)
		// loginKindOf reads the handle of the member's line session: this is
		// the user's session's.
		r.Handle = cs.Handle
		if !cs.LoginScreen || loginKindOf(def, r) != kind {
			continue
		}
		reason := kind + " was logged in through the yard's asks"
		if err := st.RequestRestartFor(ctx, r.Name, user, reason, "", a.who()); err != nil {
			say("%s's conversation with %s sits at the login screen and could not be started again (%v): yardr crew restart %s --for %s does", user, r.Name, err, r.Name, user)
			continue
		}
		say("%s's conversation with %s starts again on the login", user, r.Name)
	}
}

// askedCode is a code asked of the person: the bead it stands on and its seq.
type askedCode struct {
	bead string
	seq  uint64
}

// relayNeeds reads the needs the leader prints, a JSON line each, and relays
// them: it chooses the harness's first way to log in, mails the page to sign
// in on to the person, asks them for the code the page shows (a login-input
// secret ask, which the waiting login takes), and agrees to the one real call
// that proves the login: it costs a model call on their seat, and a login
// that ended well but answers nothing (a plan without Claude Code, say) is
// better found now than at their first bead. It answers the asks it made, and
// why it stopped early when it did.
func (a *app) relayNeeds(ctx context.Context, st store.API, user, bead string, needs io.Reader, answers io.Writer, in *userSignIn, say func(string, ...any)) ([]askedCode, error) {
	var open []askedCode
	scan := bufio.NewScanner(needs)
	scan.Buffer(nil, loginLineMax)
	for scan.Scan() {
		var line struct {
			loginNeed
			Done   bool     `json:"done"`
			Proved bool     `json:"proved"`
			Try    tryCheck `json:"try"`
		}
		if err := json.Unmarshal(scan.Bytes(), &line); err != nil {
			continue
		}
		switch {
		case line.Need == needChoose && len(line.Options) > 0:
			// Claude's own subscription is the first way the harness lists,
			// and the one a Team or Enterprise seat is signed in with.
			b, _ := json.Marshal(map[string]string{"choose": line.Options[0]})
			_, _ = answers.Write(append(b, '\n'))
		case line.Need == needOpenURL:
			if err := st.Mail(ctx, user, signInMail(user, line.URL), a.who()); err != nil {
				return open, fmt.Errorf("mailing the sign-in page to %s: %w", user, err)
			}
			in.Mailed = true
			say("the page to sign in on is mailed to %s", user)
		case line.Need == needInput && line.Login != "":
			seq, err := st.AddAsk(ctx, bead, store.Ask{
				Question: signInQuestion,
				To:       user,
				Context: []string{"It is the code the page shows once you have signed in to Claude with your own account.",
					"It goes to the sign-in that waits for it, and is kept nowhere."},
				Secret: &store.AskSecret{Login: line.Login, As: user},
			}, a.who())
			if err != nil {
				return open, fmt.Errorf("asking %s for the code: %w", user, err)
			}
			open = append(open, askedCode{bead, seq})
			in.Bead, in.Ask = bead, seq
			say("the code %s pastes back is ask %d on %s; the sign-in waits for it up to %s", user, seq, bead, loginInputTimeout)
		case line.Need == needConfirm:
			_, _ = answers.Write([]byte(`{"confirm":true}` + "\n"))
		case line.Done:
			in.Proved, in.Try = line.Proved, line.Try
		}
	}
	return open, nil
}

// signInMail is the mail that carries the sign-in page to the person.
func signInMail(user, page string) string {
	return fmt.Sprintf("%s, your sign-in to Claude for this yard is waiting.\n"+
		"Open %s and sign in to Claude with your own account. The page then shows a code.\n"+
		"Paste the code into the ask %q, which stands among your questions in footplate and the web view. The sign-in waits for it up to %s.",
		user, page, signInQuestion, loginInputTimeout)
}

// userLoginCmd is yardr user login <name>: a user's first login made
// whole, or the sign-in of one whose group carries their login opened
// again.
func (a *app) userLoginCmd() *cobra.Command {
	var beadFlag string
	cmd := &cobra.Command{
		Use: "login <name>", Short: "Give a user a login of their own, or open their sign-in again", Args: cobra.ExactArgs(1),
		Long: `Gives the user a login of their own where no group carries one yet, as
yardr user add --login does after the user: the login folder and the env
file that names it, a copy of every herdr group an operator route sends
work to, a twin of each such route for their beads, and the yardmaster's
conversation with them on that login. A user who has one (the groups
whose login_for is the user) is only signed in again. Either way the
sign-in is led as user add --login leads it at its end: the harness's own
login runs into the groups' folder, the page to sign in on is mailed to
the user, and the code it shows is asked of them, a secret that goes to
the waiting login and is kept nowhere. It waits for the login to end,
within the login's own timeouts (docs/doctor.md), proves it with one
short call on the user's seat, starts their conversation again on it
where it sits at the login screen, and says how it went. Use it when a
teammate invited by user invite is to build on a seat of their own, when
the sign-in did not finish, or to move a person's seat: the folder it
signs in to is the one the groups' env_file names. With --bead <id> the
sign-in stands on that bead instead of a new one: the user login
carrier opens it and launches this command detached with it, and it
must stand open, for this user, as a claude sign-in.`,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()
			name := args[0]
			st, err := a.store(ctx)
			if err != nil {
				return err
			}
			if _, err := st.GetUser(ctx, name); err != nil {
				return err
			}
			// A carrier-opened sign-in bead is adopted, never doubled: it
			// must stand open, for this user, as a claude sign-in. Once
			// adopted, any failure before signIn takes over closes it so it
			// never stands open with no sign-in running.
			bead := beadFlag
			adopted := false
			handedOff := false
			if bead != "" {
				b, err := st.GetBead(ctx, bead)
				if err != nil {
					return err
				}
				kind, _ := b.Metadata[store.SignInMeta].(string)
				if b.Status != "open" || b.Originator != name || kind != "claude" {
					return fmt.Errorf("bead %s is no open sign-in of %s to claude (it stands %s, for %s, as %q): the sign-in it was opened for is not this one", bead, name, b.Status, b.Originator, kind)
				}
				adopted = true
				defer func() {
					if adopted && !handedOff {
						a.closeSignIn(ctx, st, beadFlag, name)
					}
				}()
			}
			groups, err := st.Groups(ctx)
			if err != nil {
				return err
			}
			first, _, _, err := store.UserLoginEnv(name, groups)
			if err != nil {
				return err
			}
			if first == "" {
				// First time: the same setup user add --login runs, for a
				// user who stands already (an invited teammate, say).
				p, err := a.planUserLogin(ctx, st, name, "claude")
				if err != nil {
					return err
				}
				w := c.OutOrStdout()
				say := func(format string, args ...any) {
					if !a.json {
						fmt.Fprintf(w, format+"\n", args...)
					}
				}
				failed := func(step string, err error) error {
					return fmt.Errorf("%s: %w; user %s stands as far as it went, and yardr user rm %s takes back what the yard holds of it (the folder stays on disk)", step, err, name, name)
				}
				_, first, bead, err = a.buildUserLogin(c, st, p, bead, say, failed)
				if err != nil {
					return err
				}
			} else if bead == "" {
				bead, err = a.openSignIn(ctx, st, name, "claude")
				if err != nil {
					return err
				}
			}
			handedOff = true
			in, err := a.signIn(c, st, name, first, bead)
			if a.json {
				if oerr := a.out(c.OutOrStdout(), in, nil); oerr != nil {
					return oerr
				}
			}
			return err
		},
	}
	cmd.Flags().StringVar(&beadFlag, "bead", "", "stand the sign-in on this bead (the user login carrier's), instead of a new one")
	return cmd
}
