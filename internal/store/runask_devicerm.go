// The user device rm row of the run-ask list (runList): removing a user's
// device, so that an operator with no terminal can cut off a lost phone of
// another user. A device is a way in that outlives its making, so the
// yardmaster never removes one for the operator (no delegated run: the
// action stands under ruleRemoveDevice, which no site marks Delegate); it
// proposes the removal and the person asked approves it. The user whose
// device it is may approve it too, as the command allows them to remove it
// themselves (ruleRemoveDevice), so the row is a user's own as a
// subscription is (runCommand.whose).
//
// The row is registered here, beside its functions, and not in runList's
// literal: runask.go stands at the file-size ceiling the test gate holds
// (.yardr/test).
package store

import (
	"context"
	"fmt"
)

// deviceRmUse is how a refusal names the user device rm a run ask takes.
const deviceRmUse = "yardr user device rm <user> <device>"

func init() {
	runList = append(runList, runCommand{
		words:       []string{"user", "device", "rm"},
		use:         deviceRmUse,
		description: "Remove a device: its token is refused from now on, and its streams are closed",
		parse:       parseDeviceRm,
		check:       checkDeviceRm,
		card:        (*wtx).deviceRmCard,
		stale:       (*wtx).deviceRmStale,
		run:         runDeviceRm,
		whose:       func(argv []string) string { return argv[3] },
	})
}

// parseDeviceRm reads the words of user device rm <user> <device>: the
// user and one of their devices, by name.
func parseDeviceRm(rest []string) ([]string, error) {
	if len(rest) != 2 {
		return nil, fmt.Errorf("--run takes %s: one user and one device, not %d words", deviceRmUse, len(rest))
	}
	return []string{rest[0], rest[1]}, nil
}

// checkDeviceRm is removing the user's device as the asker: the check
// RemoveDevice refuses with, which a run ask may propose to anyone who
// would be refused it.
func checkDeviceRm(ctx context.Context, w *wtx, argv []string, actor string) error {
	if err := w.may(ctx, actor, ActRemoveDevice, Object{Kind: KindUser, Name: argv[3]}); err != nil {
		// The asker may not remove it, so the ask stands: say whose
		// approval it takes, in the words the removal is refused in.
		return &OperatorOnlyError{msg: err.Error(), ask: Askable}
	}
	return nil
}

// devicesLeft is how many other devices the user has besides the one named.
func devicesLeft(w *wtx, user, name string) int {
	return len(keys(w.Tx, bDevices, user+"\x00")) - 1
}

// deviceRmCard is the card of user device rm <user> <device>: which device,
// when it was added and by whom, that its token is refused from then on and
// what was queued to tell it is dropped, that what it did stands, and how
// many other devices the user has left. It names nothing that moves while
// the device is used (when it last was, and from where), so a device still
// in use does not make its own removal stale. The approval is held to the
// device as it was and to the others the user has (Seen).
func (w *wtx) deviceRmCard(argv []string, asking bool) (AskRun, error) {
	user, name := argv[3], argv[4]
	if _, err := getUser(w.Tx, user); err != nil {
		return AskRun{}, err
	}
	var d deviceRecord
	if ok, err := get(w.Tx, bDevices, join(user, name), &d); err != nil {
		return AskRun{}, err
	} else if !ok {
		return AskRun{}, fmt.Errorf("device %s of %s: %w", name, user, ErrNotFound)
	}
	left := devicesLeft(w, user, name)
	added := fmt.Sprintf("added %s by %s", d.CreatedAt.UTC().Format("2 Jan 2006"), or(d.CreatedBy, "nobody"))
	sentences := []string{
		fmt.Sprintf("removes the device %s of %s (%s): its token is refused from now on, the streams it held are closed, and what was queued to tell it is dropped.", name, user, added),
		"What it did stands: the mail it wrote and the asks it approved are not undone.",
	}
	switch left {
	case 0:
		sentences = append(sentences, fmt.Sprintf("It is the last device %s has: they have none left.", user))
	default:
		sentences = append(sentences, fmt.Sprintf("%s keeps %s.", user, countNoun(left, "other device")))
	}
	return AskRun{
		Argv:   argv,
		Does:   sentences,
		Back:   fmt.Sprintf("a device is paired again with a new token (yardr user device pair %s %s): the old token is not brought back", user, name),
		BackBy: "only the operator",
		Seen: map[string]string{
			"device " + user + "/" + name:          seen(d.CreatedAt, d.CreatedBy, d.TokenSHA256, d.ChannelKey),
			"device " + user + "/" + name + " set": seen(left),
		},
	}, nil
}

// deviceRmStale says why the ask seq for user device rm is approved no
// more: the device is another under its name (removed and paired again), or
// the user's other devices are not the ones the card counted.
func (w *wtx) deviceRmStale(seq uint64, was, now AskRun) error {
	user, name := was.Argv[3], was.Argv[4]
	if key := "device " + user + "/" + name; was.Seen[key] != now.Seen[key] {
		return fmt.Errorf("ask %d was written when the device %s of %s was another: it was removed and paired again since. %s", seq, name, user, nothingRan)
	}
	return staleCard(seq, was, now)
}

// runDeviceRm removes the device as actor through removeDevice, and answers
// what the command prints at a terminal.
func runDeviceRm(ctx context.Context, w *wtx, argv []string, actor string, by *approvedOn) (string, error) {
	d, err := w.removeDevice(ctx, argv[3], argv[4], actor, by)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("device %s of %s removed: its token is refused from now on, and the streams it held are closed", d.Name, d.User), nil
}
