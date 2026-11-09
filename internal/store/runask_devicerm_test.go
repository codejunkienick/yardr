package store

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// deviceRmYard is a run yard where ada has two devices, a phone and a
// laptop, and mckean, the operator, has one.
func deviceRmYard(t *testing.T) *askYard {
	t.Helper()
	y, _ := runYard(t)
	for _, d := range [][2]string{{"ada", "phone"}, {"ada", "laptop"}, {"mckean", "tablet"}} {
		if _, err := y.AddDevice(y.ctx, d[0], d[1], "mckean"); err != nil {
			t.Fatal(err)
		}
	}
	return y
}

func deviceRmAsk(command, to string) Ask {
	return Ask{Question: "Remove the lost phone?", To: to, Context: []string{"ada lost it on the train"},
		Run: &AskRun{Argv: []string{command}}}
}

func deviceNames(t *testing.T, y *askYard, user string) []string {
	t.Helper()
	ds, err := y.Devices(y.ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range ds {
		names = append(names, d.Name)
	}
	slices.Sort(names)
	return names
}

// A device rm card is routine, since it narrows: it names the device and
// who made it, what ends with it and what stands, and how many devices the
// user keeps. The operator's approval removes the one device named, as the
// command does, and logs the removal with the ask it came from; the other
// devices of the user, and of everyone else, stand.
func TestRunAskDeviceRm(t *testing.T) {
	t.Parallel()
	y := deviceRmYard(t)
	seq := y.ask(t, deviceRmAsk("yardr user device rm ada phone", "mckean"), "yardmaster")
	got := y.asked(t, seq)
	run := got.Ask.Run
	if want := []string{"user", "device", "rm", "ada", "phone"}; !slices.Equal(run.Argv, want) {
		t.Fatalf("the ask stores %v, want %v", run.Argv, want)
	}
	if run.Description != "Remove a device: its token is refused from now on, and its streams are closed" {
		t.Errorf("description %q", run.Description)
	}
	joined := strings.Join(run.Does, "\n")
	for _, want := range []string{
		"removes the device phone of ada (added ",
		" by mckean): its token is refused from now on, the streams it held are closed, and what was queued to tell it is dropped.",
		"What it did stands: the mail it wrote and the asks it approved are not undone.",
		"ada keeps 1 other device.",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the card does not say %q; it says %q", want, run.Does)
		}
	}
	if run.BackBy != "only the operator" || !strings.Contains(run.Back, "yardr user device pair ada phone") {
		t.Errorf("the card is taken back by %q, %q", run.Back, run.BackBy)
	}
	if len(got.Ask.Weight) != 0 {
		t.Fatalf("the ask weighs %+v, want routine", got.Ask.Weight)
	}
	done, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "device phone of ada removed: its token is refused from now on, and the streams it held are closed"; done.Result != want {
		t.Fatalf("the approval recorded %q, want %q", done.Result, want)
	}
	if got := deviceNames(t, y, "ada"); !slices.Equal(got, []string{"laptop"}) {
		t.Fatalf("ada holds %v, want the laptop alone", got)
	}
	if got := deviceNames(t, y, "mckean"); !slices.Equal(got, []string{"tablet"}) {
		t.Fatalf("the operator holds %v: another user's device was removed", got)
	}
	evs, err := y.EventsOfKind(y.ctx, "device_removed", 2)
	if err != nil || len(evs) != 1 || evs[0].Data["device"] != "phone" || evs[0].Data["user"] != "ada" {
		t.Fatalf("device_removed events = %+v, %v", evs, err)
	}
	y.approved(t, evs[0], seq)

	// The last device a user has says so, and the user whose it is may be
	// the one asked: they could remove it themselves, so they approve it.
	seq = y.ask(t, deviceRmAsk("user device rm ada laptop", "ada"), "yardmaster")
	if joined := strings.Join(y.asked(t, seq).Ask.Run.Does, "\n"); !strings.Contains(joined, "It is the last device ada has: they have none left.") {
		t.Errorf("the card says %q", joined)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "ada", false, nil); err != nil {
		t.Fatalf("ada approving the removal of her own device = %v", err)
	}
	if got := deviceNames(t, y, "ada"); len(got) != 0 {
		t.Fatalf("ada still holds %v", got)
	}
}

// A device rm ask is refused, storing nothing, for what the row refuses: a
// user or a device the yard has not, the wrong number of words, an asker
// who may remove it themselves, one asked who may not approve it, and a
// writer that is no one of the yard.
func TestRunAskDeviceRmRefusals(t *testing.T) {
	t.Parallel()
	y := deviceRmYard(t)
	if _, err := y.AddUser(y.ctx, "eve", "mckean"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, command, to, asker, want string
	}{
		{"no such device", "user device rm ada watch", "mckean", "yardmaster", "device watch of ada: not found"},
		{"no such user", "user device rm ghost phone", "mckean", "yardmaster", "user ghost: not found"},
		{"one word", "user device rm ada", "mckean", "yardmaster", "one user and one device, not 1 words"},
		{"three words", "user device rm ada phone laptop", "mckean", "yardmaster", "one user and one device, not 3 words"},
		{"the operator asks", "user device rm ada phone", "mckean", "mckean", "needs no approval: run it yourself"},
		{"the user asks about their own", "user device rm ada phone", "mckean", "ada", "needs no approval: run it yourself"},
		{"another user is asked", "user device rm ada phone", "eve", "yardmaster", "this command is ada's to approve, or the operator's (mckean) for them: the yard runs it as the one who approves, so eve cannot be asked to"},
		{"nobody", "user device rm ada phone", "mckean", "stranger", "a command for the operator to approve is proposed by a crew member or a user of the yard"},
	} {
		if _, err := y.AddAsk(y.ctx, y.bead, deviceRmAsk(c.command, c.to), c.asker); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s = %v, want %q", c.name, err, c.want)
		}
	}
	// Another user's device is another's to remove: a member who is not
	// ada cannot approve it, and a member who asks for it as theirs is
	// refused as the card is asked of the user, not the asker.
	seq := y.ask(t, deviceRmAsk("user device rm ada phone", "ada"), "yardmaster")
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "eve", false, nil); err == nil {
		t.Fatal("eve approved the removal of ada's device")
	}
	if got := deviceNames(t, y, "ada"); !slices.Equal(got, []string{"laptop", "phone"}) {
		t.Fatalf("ada holds %v after the refusals", got)
	}
}

// An approval is held to the device the card named: one removed and paired
// again under the name is another device, and so is a user's set of others
// that is not the one counted. A device removed since leaves nothing to
// approve. Either way nothing runs. A device that is used between the ask
// and the approval is still the same device: when it was last used is no
// part of the card, so a stolen device in use can be cut off.
func TestRunAskDeviceRmStale(t *testing.T) {
	t.Parallel()
	y := deviceRmYard(t)
	phone, err := y.AddDevice(y.ctx, "ada", "scratch", "mckean")
	if err != nil {
		t.Fatal(err)
	}
	seq := y.ask(t, deviceRmAsk("user device rm ada scratch", "mckean"), "yardmaster")
	if _, err := y.ResolveDevice(y.ctx, phone.Token, "203.0.113.9"); err != nil {
		t.Fatal(err)
	}
	if d, err := y.Devices(y.ctx, "ada"); err != nil || !slices.ContainsFunc(d, func(d Device) bool { return d.Name == "scratch" && d.LastUsed != nil }) {
		t.Fatalf("the device was not used: %+v, %v", d, err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err != nil {
		t.Fatalf("approving the removal of a device used since = %v", err)
	}

	seq = y.ask(t, deviceRmAsk("user device rm ada phone", "mckean"), "yardmaster")
	if _, err := y.AddDevice(y.ctx, "ada", "watch", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil ||
		!strings.Contains(err.Error(), "the card is from before that changed") {
		t.Fatalf("approving with another device added since = %v", err)
	}
	if _, err := y.RemoveDevice(y.ctx, "ada", "watch", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.RemoveDevice(y.ctx, "ada", "phone", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.AddDevice(y.ctx, "ada", "phone", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil ||
		!strings.Contains(err.Error(), "was written when the device phone of ada was another: it was removed and paired again since") {
		t.Fatalf("approving a device made again = %v", err)
	}
	if got := deviceNames(t, y, "ada"); !slices.Equal(got, []string{"laptop", "phone"}) {
		t.Fatalf("the refused approvals left ada %v", got)
	}
	// Removed by hand since: the card names a device that is not there.
	seq = y.ask(t, deviceRmAsk("user device rm ada laptop", "mckean"), "yardmaster")
	if _, err := y.RemoveDevice(y.ctx, "ada", "laptop", "mckean"); err != nil {
		t.Fatal(err)
	}
	if _, err := y.ApproveAsk(y.ctx, y.bead, seq, "mckean", false, nil); err == nil || !strings.Contains(err.Error(), "device laptop of ada: not found") {
		t.Fatalf("approving a device removed since = %v", err)
	}
	if _, err := y.RemoveDevice(y.ctx, "ada", "laptop", "mckean"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removing it again = %v", err)
	}
}
