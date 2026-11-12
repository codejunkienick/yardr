// The user device pair row of the run-ask list (runList): opening a
// pairing for one device of one user, so that a user with no terminal
// can pair their next device and an operator away from the machine can
// pair a first one (design yardr-6maq 3.4). Asking opens nothing: the
// card names the user, the device and the address whole, and approval
// opens the pairing inside its own transaction and answers once, to
// the approver alone, with the code, the address, the yard, the expiry,
// the channel key and the QR string (AskPairing on AskApproved). That
// transient is a return value and nothing else: the run's Result keeps
// only the safe sentence, so no note, answer, event or later read holds
// the code. The row is a user's own as a device removal is
// (runCommand.whose): asked of the named user or the operator, and
// either approves it — the user by the self leg of DevicePairOpenSelf,
// anyone else under ActAddDevice as DevicePairOpen checks it, both
// judged inside the approval's transaction (runDevicePair).
//
// The row is registered here, beside its functions, and not in runList's
// literal: runask.go stands at the file-size ceiling the test gate holds
// (.yardr/test).
package store

import (
	"context"
	"fmt"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// devicePairUse is how a refusal names the user device pair a run ask takes.
const devicePairUse = "yardr user device pair <user> <device> [--url <address>]"

func init() {
	runList = append(runList, runCommand{
		words:       []string{"user", "device", "pair"},
		use:         devicePairUse,
		description: "Enrol a device by a short code, or a QR code, with no token to copy",
		parse:       parseDevicePair,
		check:       checkDevicePair,
		card:        (*wtx).devicePairCard,
		stale:       (*wtx).devicePairStale,
		weigh:       devicePairWeight,
		run:         runDevicePair,
		whose:       func(argv []string) string { return argv[3] },
	})
}

// IsDevicePairRun reports whether argv, in the yard's stored form, is
// the device pair row's: the one approval no transcript may carry, which
// MCP refuses without calling ApproveAsk (design yardr-6maq 3.4). It
// reads no record of the yard.
func IsDevicePairRun(argv []string) bool {
	return len(argv) >= 5 && argv[0] == "user" && argv[1] == "device" && argv[2] == "pair"
}

// devicePairOf is the user, the device and the address ("" for the
// yard's own) of a parsed user device pair.
func devicePairOf(argv []string) (user, device, address string) {
	user, device = argv[3], argv[4]
	if len(argv) == 7 {
		address = argv[6]
	}
	return user, device, address
}

// parseDevicePair reads the words of user device pair <user> <device>
// [--url <address>]: the user and the new device by name, and where the
// device reaches the yard at where the command names it. There is no
// --qr on the row: drawing the code is the approving client's work on
// approval, and --end ends a standing pairing at a terminal.
func parseDevicePair(rest []string) ([]string, error) {
	args, values, err := runWords(devicePairUse, "user device pair", "the operator's", rest, map[string]bool{"--url": true})
	if err != nil {
		return nil, err
	}
	if len(args) != 2 {
		return nil, fmt.Errorf("--run takes %s: one user and one device, not %d words", devicePairUse, len(args))
	}
	out := []string{args[0], args[1]}
	if urls := values["--url"]; len(urls) > 0 {
		if len(urls) != 1 {
			return nil, fmt.Errorf("--run takes %s: one --url <address>, not %d", devicePairUse, len(urls))
		}
		address, err := CleanURL(urls[0])
		if err != nil {
			return nil, fmt.Errorf("%w: the address the device reaches this yard at", err)
		}
		out = append(out, "--url", address)
	}
	return out, nil
}

// checkDevicePair is opening the pairing as the asker: the check
// DevicePairOpen refuses with, which a run ask may propose to anyone
// who would be refused it. The user the pairing is for stands in the
// self leg of DevicePairOpenSelf, which takes no ActAddDevice: as the
// one asked they pass, as DevicePairOpenSelf passes them, and the
// approval judges that leg again in its transaction (runDevicePair).
func checkDevicePair(ctx context.Context, w *wtx, argv []string, actor string) error {
	user, _, _ := devicePairOf(argv)
	if actor == user {
		return nil
	}
	if err := w.may(ctx, actor, ActAddDevice, Object{Kind: KindUser, Name: user}); err != nil {
		return &OperatorOnlyError{msg: err.Error(), ask: Askable}
	}
	return nil
}

// PairAddress answers where a device reaches this yard at for a pairing
// the command named no --url for: the listener's own address, or the
// edge's where the yard listens nowhere and its link is up, as the
// terminal command gives it. It is said by the process that owns the
// store (SetPairAddress), which alone knows the yard's listener and
// link; a store no server owns has none, and a card then names no
// address but the command's.
type PairAddress func() (string, error)

// SetPairAddress tells the store where a device reaches this yard at,
// as SetPackReader tells it how that process reads packs.
func (s *Store) SetPairAddress(p PairAddress) { s.pairAddr = p }

// PairChannelKey answers the public channel key for a device pairing.
// The owning process makes it when needed; the store does not write a key
// file from inside the approval transaction.
type PairChannelKey func() string

// SetPairChannelKey tells the store how its owning process makes or reads
// the channel key a device pairing's QR carries.
func (s *Store) SetPairChannelKey(p PairChannelKey) { s.pairChannelKey = p }

// devicePairAddress is where the device of the parsed command reaches
// the yard at: the --url the command names, whole, or the address the
// owning process gives a pairing (PairAddress). A yard with neither
// opens no pairing, as the terminal command refuses.
func (w *wtx) devicePairAddress(argv []string) (string, error) {
	_, _, address := devicePairOf(argv)
	if address != "" {
		return address, nil
	}
	if w.store != nil && w.store.pairAddr != nil {
		return w.store.pairAddr()
	}
	return "", fmt.Errorf("this yard has no listener a device can call with a code, and no edge link up: name where the device reaches this yard with --url <address>")
}

// devicePairSeen is what the card keeps of the user's devices: each by
// name with what the yard holds of it, so a device added, removed or
// paired again since the ask makes the approval stale. A channel key
// pinned in place and a last use are no part of it: the device that
// approves moves both by calling, as a device removal's card holds.
func (w *wtx) devicePairSeen(user string) (string, error) {
	var parts []string
	for _, key := range keys(w.Tx, bDevices, user+"\x00") {
		var d deviceRecord
		if _, err := get(w.Tx, bDevices, key, &d); err != nil {
			return "", err
		}
		parts = append(parts, d.Name+"="+seen(d.CreatedAt, d.CreatedBy, d.TokenSHA256))
	}
	return seen(strings.Join(parts, ",")), nil
}

// devicePairCard is the card of user device pair <user> <device>: the
// user and the device whole, the address the device reaches the yard
// at, every device the user then has by name with when each was last
// used, and in its own sentences what approval opens and for how long,
// that whoever has the code is answered with the token, that nothing
// keeps the code, and how many devices the user will then have. The
// code stands on no card. Asking opens nothing.
func (w *wtx) devicePairCard(argv []string, asking bool) (AskRun, error) {
	user, device, _ := devicePairOf(argv)
	if err := checkDeviceName(device); err != nil {
		return AskRun{}, err
	}
	if _, err := getUser(w.Tx, user); err != nil {
		return AskRun{}, err
	}
	if err := deviceFree(w.Tx, user, device); err != nil {
		return AskRun{}, err
	}
	address, err := w.devicePairAddress(argv)
	if err != nil {
		return AskRun{}, err
	}
	devices, err := w.devicesOf(user)
	if err != nil {
		return AskRun{}, err
	}
	fingerprint, err := w.devicePairSeen(user)
	if err != nil {
		return AskRun{}, err
	}
	var held string
	if len(devices) == 0 {
		held = fmt.Sprintf("%s has no device yet: this one is their first.", user)
	} else {
		named := make([]string, len(devices))
		for i, d := range devices {
			if d.LastUsed == nil {
				named[i] = d.Name + " (never used)"
			} else {
				named[i] = fmt.Sprintf("%s (last used %s)", d.Name, d.LastUsed.UTC().Format("2 Jan 2006 15:04"))
			}
		}
		held = fmt.Sprintf("%s has %s: %s.", user, countNoun(len(devices), "device"), strings.Join(named, ", "))
	}
	sentences := []string{
		fmt.Sprintf("opens a pairing for the device %s of %s, reached at %s.", device, user, address),
		held,
		"Approval opens the pairing for that device alone: its code is good for ten minutes and for one device, and whoever has the code within those minutes is answered with that device's token.",
		"Nothing in the yard keeps the code after it is used or runs out.",
		fmt.Sprintf("The new device then calls as %s until yardr user device rm %s %s, and %s will then have %s.", user, user, device, user, countNoun(len(devices)+1, "device")),
	}
	return AskRun{
		Argv: argv,
		Does: sentences,
		Back: fmt.Sprintf("yardr user device pair --end ends it while it stands; once the code is used, yardr user device rm %s %s removes the device", user, device),
		Seen: map[string]string{
			"devices " + user: fingerprint,
			"pairing address": address,
		},
	}, nil
}

// devicesOf is the user's devices as the card names them, by name.
func (w *wtx) devicesOf(user string) ([]Device, error) {
	var out []Device
	err := each(w.Tx, bDevices, user+"\x00", func(_ []byte, d *deviceRecord) error {
		out = append(out, withPinned(d.Device))
		return nil
	})
	return out, err
}

// devicePairStale says why the ask seq for user device pair is approved
// no more: the user's devices are not the ones the card listed, or the
// address is not the one it named. A device of the name added since is
// refused by the card itself, which names no free device then. A last
// use that moved since is neither: the device that approves moves its
// own by calling, as a device removal's card holds (runask_devicerm.go),
// so where only that wording differs the approval is held to the card
// as it stands and carries on.
func (w *wtx) devicePairStale(seq uint64, was, now AskRun) error {
	user, _, _ := devicePairOf(was.Argv)
	if key := "devices " + user; was.Seen[key] != now.Seen[key] {
		return fmt.Errorf("ask %d was written when %s had other devices: one was added or removed since. %s", seq, user, nothingRan)
	}
	if was.Seen["pairing address"] != now.Seen["pairing address"] {
		return staleCard(seq, was, now)
	}
	return nil
}

// devicePairWeight weighs a pairing card trust: a new device speaks as
// its user from then on, until it is removed.
func devicePairWeight(_ *wtx, run AskRun) ([]AskWeight, error) {
	user, device, _ := devicePairOf(run.Argv)
	return []AskWeight{weighs(WeightTrust, fmt.Sprintf("a new device %s of %s speaks as them: whoever holds its token calls as %s until the device is removed", device, user, user))}, nil
}

// devicePairTarget reports whether ask seq on bead id is a device pair
// ask, for ApproveAsk to open in its own way. A bead or ask that is not
// there, and any other command, is the plain approval's to refuse or
// run.
func (s *Store) devicePairTarget(id string, seq uint64) bool {
	found := false
	_ = s.read(func(tx *bolt.Tx) error {
		row, err := loadBead(tx, id)
		if err != nil {
			return nil
		}
		notes, err := beadNotes(tx, row.UUID)
		if err != nil {
			return nil
		}
		for _, r := range ResolveAsks(notes, now()) {
			if r.Note.Seq == seq && r.AskErr == nil && r.Ask.Run != nil && IsDevicePairRun(r.Ask.Run.Argv) {
				found = true
			}
		}
		return nil
	})
	return found
}

// approveDevicePair is ApproveAsk for a device pair ask: the pairing
// lock is taken first, before any of the store's, and held until the
// approval's write has committed and the pairing is published, so no
// terminal opening, redemption or close slips between them. The code,
// the pairing's id and its expiry are made once, before the write, so
// a batch that runs the transaction again ends the same and publishes
// the same pairing. Only a committed write publishes: the in-memory
// pairing takes the pending one the run left on the transaction, and
// the transient is filled from that same value for the return.
func (s *Store) approveDevicePair(ctx context.Context, id string, seq uint64, actor string, weighed bool, values map[string]string) (*AskApproved, error) {
	s.devPair.mu.Lock()
	defer s.devPair.mu.Unlock()
	channel := s.channelPublic()
	if s.pairChannelKey != nil {
		if public := s.pairChannelKey(); public != "" {
			channel = public
		}
	}
	code := NewCode()
	clean, err := CleanCode(code)
	if err != nil {
		return nil, err
	}
	pairID, expires := newPeerMessageID(), s.devicePairNow().Add(TTL)
	var approved *AskApproved
	var pending *devicePairing
	var shown *AskPairing
	err = s.writeVia(ctx, func(w *wtx) error {
		w.pairID, w.pairCode, w.pairExpires = pairID, clean, expires
		w.pairChannelKey = channel
		out, err := w.approveAsk(ctx, id, seq, actor, weighed, values)
		if err != nil {
			return err
		}
		approved, pending, shown = out, w.pairPending, w.pairShown
		return nil
	})
	if err != nil {
		return nil, err
	}
	if pending != nil {
		s.devPair.cur = pending
	}
	if approved != nil {
		approved.Pairing = shown
	}
	return approved, nil
}

// runDevicePair opens the pairing inside the approval's transaction, in
// the terms of openDevicePairing across its three writes: the busy
// state is judged from the in-memory pairing under the lock the
// approval already holds, an expired one is ended by an event in this
// transaction, the leave is the self leg where the approver is the
// user the pairing is for and ActAddDevice where they are not, the
// device name is checked against the transaction, and the opened event
// is written here without the code. The pairing itself is left pending
// on the transaction for ApproveAsk to publish after commit; its code
// reaches the approver in the transient alone, and Result keeps only
// the safe sentence. A refusal writes nothing and leaves the ask open.
func runDevicePair(ctx context.Context, w *wtx, argv []string, actor string, by *approvedOn) (string, error) {
	user, device, _ := devicePairOf(argv)
	if w.pairID == "" || w.store == nil {
		return "", fmt.Errorf("a pairing approval reached its run with no pairing made for it: nothing ran")
	}
	// A peer does not pair a device on its own word: an approval that
	// arrived through a peer's reach is refused, whoever approves, as
	// one that touches that peer's own link is (approveAsk).
	if reach := reachOf(ctx); reach != "" {
		return "", fmt.Errorf("this approval arrived through the reach of %s, and a peer must not pair a device. Approve it from this yard, in a terminal here or in footplate paired with this yard directly. Nothing ran, and the ask stays open", reach)
	}
	// The yard's one pairing, judged without devicePairing: that ends an
	// expired one through LogEvent, a write of its own, which no write
	// may nest. The lock is the approval's, so the pairing read here is
	// the one publication will replace.
	if cur := w.store.devPair.cur; cur != nil && cur.State == PairOpened {
		if w.store.devicePairNow().Before(cur.Expires) {
			return "", fmt.Errorf("a pairing is already open for %s, try again in a few minutes", cur.User)
		}
		if err := w.event("", "device_pair_refused", "yard", cur.event(map[string]any{"reason": TimedOut})); err != nil {
			return "", err
		}
	}
	if actor != user {
		if err := w.may(ctx, actor, ActAddDevice, Object{Kind: KindUser, Name: user}); err != nil {
			return "", err
		}
	}
	if err := checkDeviceName(device); err != nil {
		return "", err
	}
	if err := deviceFree(w.Tx, user, device); err != nil {
		return "", err
	}
	pending := &devicePairing{DevicePairing: DevicePairing{ID: w.pairID, User: user, Device: device, State: PairOpened,
		Expires: w.pairExpires}, actor: actor, code: w.pairCode, approvalOnly: true}
	if err := w.event("", "device_pair_opened", actor, pending.event(map[string]any{"expires": w.pairExpires})); err != nil {
		return "", err
	}
	address, err := w.devicePairAddress(argv)
	if err != nil {
		return "", err
	}
	channel := w.pairChannelKey
	yard := w.devicePairYard(address)
	shown := displayCode(w.pairCode)
	w.pairPending = pending
	w.pairShown = &AskPairing{User: user, Device: device, Code: shown, Address: address, Yard: yard,
		Expires: w.pairExpires, ChannelKey: channel, QR: PairingQR(address, shown, yard, channel)}
	return fmt.Sprintf("a pairing was opened for the device %s of %s, good until %s: the code was shown to the approver alone and is kept nowhere", device, user, w.pairExpires.Local().Format("15:04:05")), nil
}

// devicePairYard is this yard's name at the edge where the address is
// the edge's, and "" anywhere else: the second field a pairing's QR
// string names (DevicePairingState).
func (w *wtx) devicePairYard(address string) string {
	e, connected, err := EdgeOf(map[string]string{EdgeSetting: w.settingValue(EdgeSetting)})
	if err == nil && connected && address == e.URL {
		return e.HandleName()
	}
	return ""
}
