// The user set role row of the run-ask list (runList): changing the role
// of a user the yard has, which decides who approves the yard's commands
// and administers it. The role has no door but the operator's own, typed in
// a terminal, so an operator with none could not give or take it. The row
// shares the words of user set with the tell row (runask_list.go), and
// parseRow tells the two apart by the form: this row, the newer, is tried
// first and answers errNotRow for a command with no role= among its words.
//
// A role change is weighed trust, strong, in both directions: who holds the
// operator role is who may approve everything, so neither giving it nor
// taking it is told after the fact. No delegated run changes a role:
// ActChangeRole stands under ruleOperator and no site marks it Delegate, so
// the yardmaster's call is refused in the words anyone's is.
//
// The row is registered here, beside its functions, and not in runList's
// literal: runask.go stands at the file-size ceiling the test gate holds
// (.yardr/test).
package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// userRoleUse is how a refusal names the user set a run ask takes for a
// role.
const userRoleUse = "yardr user set <user> role=operator|member"

func init() {
	runList = append(runList, runCommand{
		words:       []string{"user", "set"},
		use:         userRoleUse,
		description: userSetDescription,
		parse:       parseUserRole,
		check:       checkUserRole,
		card:        (*wtx).userRoleCard,
		stale:       (*wtx).userRoleStale,
		weigh:       userRoleWeight,
		run:         runUserRole,
	})
}

// userSetDescription is the help line of user set that the card of each of
// its forms keeps (internal/cli holds it to the command's Short).
const userSetDescription = "Set where, how and what a source tells a user, or their role"

// parseUserRole reads the words of user set <user> role=<role>: the user and
// the role, operator or member. A command with no role= among its words is
// another row's, and answers errNotRow; one that mixes role= with any other
// setting or flag is refused whole, since the other settings of a user are
// not the role's and stay typed in a terminal.
func parseUserRole(rest []string) ([]string, error) {
	if !slices.ContainsFunc(rest, func(word string) bool {
		key, _, _ := strings.Cut(word, "=")
		return key == UserRole
	}) {
		return nil, errNotRow
	}
	args, _, err := runWords(userRoleUse, "user set", "the operator's", rest, nil)
	if err != nil {
		return nil, err
	}
	if len(args) != 2 {
		return nil, fmt.Errorf("--run takes %s: one user and one %s=, not %d words. Anything more of user set is typed by the operator, or by the user themselves, in a terminal", userRoleUse, UserRole, len(args))
	}
	key, role, ok := strings.Cut(args[1], "=")
	if strings.Contains(args[0], "=") || !ok || key != UserRole {
		return nil, fmt.Errorf("--run takes %s: the user first, then %s=<role>", userRoleUse, UserRole)
	}
	if role != RoleOperator && role != RoleMember {
		return nil, fmt.Errorf("--run takes %s: %s=%q: a user's role is %s or %s", userRoleUse, UserRole, role, RoleOperator, RoleMember)
	}
	return []string{args[0], UserRole + "=" + role}, nil
}

// userRoleOf is the user and the role of a parsed user set <user> role=<role>.
func userRoleOf(argv []string) (name, role string) {
	_, role, _ = strings.Cut(argv[3], "=")
	return argv[2], role
}

// checkUserRole is changing a user's role as the asker: the check setUser
// refuses with, which a run ask may propose.
func checkUserRole(ctx context.Context, w *wtx, argv []string, actor string) error {
	name, _ := userRoleOf(argv)
	return w.may(ctx, actor, ActChangeRole, Object{Kind: KindUser, Name: name, Ask: Askable})
}

// userRoleCard is the card of user set <user> role=<role>: the role the
// user has and the one proposed, what the role lets whoever speaks as them
// do, and how many devices of theirs speak with it from then on. Asking
// refuses a user the yard has not, a role they have already, and a
// demotion setUser refuses (the operator setting's, or the last operator).
// The approval is held to the user's role and creation, and for a demotion
// to the operators that stand (Seen).
func (w *wtx) userRoleCard(argv []string, asking bool) (AskRun, error) {
	name, role := userRoleOf(argv)
	u, err := getUser(w.Tx, name)
	if err != nil {
		return AskRun{}, err
	}
	if asking && u.RoleOf() == role {
		return AskRun{}, fmt.Errorf("%s is %s already, so the command would change nothing", name, roleWords(role))
	}
	ops, err := operatorsIn(w.Tx)
	if err != nil {
		return AskRun{}, err
	}
	seenMap := map[string]string{"user " + name + " role": seen(u.RoleOf(), u.CreatedAt)}
	var does string
	devices := len(keys(w.Tx, bDevices, name+"\x00"))
	then := ""
	if devices > 0 {
		then = fmt.Sprintf(" Their %s speak with it from then on.", countNoun(devices, "device"))
	}
	if role == RoleOperator {
		does = fmt.Sprintf("makes the user %s an operator (now: %s). An operator approves run asks, setup asks and secrets through a door (a device, the web view) and administers the yard at the shell: whoever speaks as %s does so from then on.%s", name, roleWords(u.RoleOf()), name, then)
	} else {
		if u.IsOperator() {
			if err := keepsOperator(w.Tx, u, "made a member"); err != nil {
				return AskRun{}, err
			}
		}
		remain := slices.DeleteFunc(slices.Clone(ops), func(op string) bool { return op == name })
		does = fmt.Sprintf("makes the user %s a member (now: %s). A member is a party of the yard and no more: they approve no run ask, setup ask or secret and administer nothing from then on.%s", name, roleWords(u.RoleOf()), then)
		if len(remain) > 0 {
			does += fmt.Sprintf(" The operators that remain are %s.", strings.Join(remain, ", "))
		}
		seenMap["operators"] = seen(ops)
	}
	return AskRun{
		Argv:   argv,
		Does:   []string{does},
		Back:   runCommandLine([]string{"user", "set", name, UserRole + "=" + u.RoleOf()}),
		BackBy: "only the operator",
		Seen:   seenMap,
	}, nil
}

// roleWords is a role as a card names it: an operator, a member.
func roleWords(role string) string {
	if role == RoleOperator {
		return "an operator"
	}
	return "a member"
}

// userRoleWeight weighs a role card trust in both directions: the role
// decides who may approve everything, so a change of it is approved on
// sight, whether it widens who holds the yard or narrows it (a stolen device
// that could demote the operators who would object is the narrowing case).
func userRoleWeight(_ *wtx, run AskRun) ([]AskWeight, error) {
	name, role := userRoleOf(run.Argv)
	if role == RoleOperator {
		return []AskWeight{weighs(WeightTrust, fmt.Sprintf("the user %s, and whoever speaks as them, will approve what the yard runs and administer it", name))}, nil
	}
	return []AskWeight{weighs(WeightTrust, fmt.Sprintf("the user %s stops being one who approves what the yard runs, which changes who holds the yard", name))}, nil
}

// userRoleStale says why the ask seq for user set <user> role=<role> is
// approved no more: the user's role was set since the card was written, or
// the user was removed and added again, or the operators that stand are not
// the ones the card named.
func (w *wtx) userRoleStale(seq uint64, was, now AskRun) error {
	name, _ := userRoleOf(was.Argv)
	if key := "user " + name + " role"; was.Seen[key] != now.Seen[key] {
		e := lastEvent(w.Tx, "user_set", naming("user", name))
		return fmt.Errorf("ask %d was written when the role of %s was another: it was set, or the user was added again, since%s. %s", seq, name, since(e), nothingRan)
	}
	if was.Seen["operators"] != now.Seen["operators"] {
		return fmt.Errorf("ask %d was written when the yard had other operators: one was made or unmade since. %s", seq, nothingRan)
	}
	return staleCard(seq, was, now)
}

// runUserRole sets the role as actor through setUser, under the check the
// setting typed at a terminal has, and answers the line yardr user set
// prints for it.
func runUserRole(ctx context.Context, w *wtx, argv []string, actor string, by *approvedOn) (string, error) {
	name, role := userRoleOf(argv)
	u, err := w.setUser(ctx, name, []string{UserRole}, map[string]string{UserRole: role}, actor, by)
	if err != nil {
		return "", err
	}
	return "user " + u.Name + ": " + u.RoleOf(), nil
}
