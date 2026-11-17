package store

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// deviceOf is the device name of user as the list reads it.
func deviceOf(t *testing.T, s *Store, user, name string) Device {
	t.Helper()
	list, err := s.Devices(context.Background(), user)
	if err != nil {
		t.Fatalf("Devices = %v", err)
	}
	for _, d := range list {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("%s has no device %s: %+v", user, name, list)
	return Device{}
}

// A device's last use is written when it is first used, when it calls from
// another address, and otherwise at most once a minute: a window that asks
// every few seconds is not a write of the store each time. The address is
// text from outside and is kept bounded. A call that carries the device's
// token (ResolveDevice) and one sealed on its channel, which carries none
// (StampDeviceUse), are kept by the same rules, so each runs the same
// sequence; and a device nothing called for 30 days is unused, and yardr
// doctor notes it, until a call of either kind is stamped.
func TestDeviceLastUse(t *testing.T) {
	// Sequential: the package's fake clock.
	at := time.Date(2026, 10, 12, 19, 0, 0, 0, time.UTC)
	clock = func() time.Time { return at }
	t.Cleanup(func() { clock = time.Now })
	s, err := Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	ctx := context.Background()
	if err := s.SetSetting(ctx, OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	for _, via := range []struct {
		name, device, channelKey string
		call                     func(t *testing.T, d *NewDevice, from string)
	}{
		{"a token", "phone", "", func(t *testing.T, d *NewDevice, from string) {
			t.Helper()
			if got, err := s.ResolveDevice(ctx, d.Token, from); err != nil || got == nil {
				t.Fatalf("ResolveDevice = %+v, %v", got, err)
			}
		}},
		{"a sealed call", "tablet", enc.EncodeToString(make([]byte, 32)), func(t *testing.T, d *NewDevice, from string) {
			t.Helper()
			if err := s.StampDeviceUse(ctx, d.User, d.Name, d.ChannelKey, from); err != nil {
				t.Fatalf("StampDeviceUse = %v", err)
			}
		}},
	} {
		t.Run(via.name, func(t *testing.T) {
			d, err := s.AddDevice(ctx, "jo", via.device, "jo")
			if err != nil {
				t.Fatal(err)
			}
			if via.channelKey != "" {
				if _, err := s.PinChannelKeyInPlace(ctx, d.Token, via.channelKey); err != nil {
					t.Fatal(err)
				}
				d.ChannelKey = via.channelKey
			}
			use := func(after time.Duration, from string) (time.Time, string) {
				t.Helper()
				at = at.Add(after)
				via.call(t, d, from)
				got := deviceOf(t, s, "jo", via.device)
				if got.LastUsed == nil {
					t.Fatalf("a device that was called has no last use: %+v", got)
				}
				return *got.LastUsed, got.LastFrom
			}
			first, from := use(time.Minute, "100.64.0.7")
			if !first.Equal(at) || from != "100.64.0.7" {
				t.Fatalf("the first use = %s from %s", first, from)
			}
			if got, _ := use(59*time.Second, "100.64.0.7"); !got.Equal(first) {
				t.Fatalf("a use within the minute was written: %s", got)
			}
			if got, _ := use(time.Second, "100.64.0.7"); !got.Equal(at) {
				t.Fatalf("a use after the minute was not written: %s, want %s", got, at)
			}
			if got, from := use(time.Second, "100.64.0.9"); !got.Equal(at) || from != "100.64.0.9" {
				t.Fatalf("a use from another address was not written: %s from %s", got, from)
			}
			if _, from := use(time.Second, " "+strings.Repeat("a", 200)+"\xff "); from != strings.Repeat("a", 64) {
				t.Fatalf("an address from outside is kept as %q", from)
			}

			at = at.Add(DeviceUnusedAfter)
			if !deviceOf(t, s, "jo", via.device).Unused(at) {
				t.Fatal("a device nothing called for 30 days is not unused")
			}
			use(0, "100.64.0.7")
			if deviceOf(t, s, "jo", via.device).Unused(at) {
				t.Fatal("a device that was called just now is unused")
			}
		})
	}
	t.Run("missing device", testDeviceStampOfAMissingDevice)
}

// A device's call must answer while the yard's one writer is held, as a
// daemon pass holds it writing back to back: the list read is a snapshot
// and answers at once, and resolving the device's token answers from its
// read too, leaving the last-use stamp — a write — to a later call
// (yardr-wpgc: resolves timed out at the resolver's 5 s behind slow
// passes). No test drove a resolve against a held write before, so this
// is its own test rather than a case of TestDeviceLastUse above. The stamp
// of a call sealed on a device's channel (StampDeviceUse) waits out the held
// write the same way, and leaves no error behind.
func TestDeviceResolveWhileWriteHeld(t *testing.T) {
	t.Parallel()
	s, err := Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	ctx := context.Background()
	if err := s.SetSetting(ctx, OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	d, err := s.AddDevice(ctx, "jo", "phone", "jo")
	if err != nil {
		t.Fatal(err)
	}
	// Added before the write is held: adding is a write too.
	tablet, err := s.AddDevice(ctx, "jo", "tablet", "jo")
	if err != nil {
		t.Fatal(err)
	}
	tabletKey := enc.EncodeToString(append(make([]byte, 31), 1))
	if _, err := s.PinChannelKeyInPlace(ctx, tablet.Token, tabletKey); err != nil {
		t.Fatal(err)
	}
	tablet.ChannelKey = tabletKey
	held := make(chan struct{})
	release := make(chan struct{})
	// Once each: a batched write may run its function again, and a failed
	// assertion below must still let the held write go, through cleanup.
	var heldOnce, releaseOnce sync.Once
	releaseWrite := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseWrite)
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- s.write(func(w *wtx) error {
			heldOnce.Do(func() { close(held) })
			<-release
			return nil
		})
	}()
	<-held

	// The bead's bound: a device's call answers within 2 s while a pass
	// phase is held. The write stays held past it, until released below.
	within := func(name string, call func() error) {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- call() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s while a write is held: %v", name, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not answer in 2 s while a write is held", name)
		}
	}
	var got *Device
	within("ResolveDevice", func() error {
		var err error
		got, err = s.ResolveDevice(ctx, d.Token, "100.64.0.7")
		return err
	})
	if got == nil || got.User != "jo" || got.Name != "phone" {
		t.Fatalf("ResolveDevice while a write is held = %+v", got)
	}
	if got.LastUsed != nil {
		t.Fatalf("the last use was stamped while a write is held: %+v", got)
	}
	within("StampDeviceUse", func() error {
		return s.StampDeviceUse(ctx, "jo", "tablet", tablet.ChannelKey, "edge")
	})
	if got := deviceOf(t, s, "jo", "tablet"); got.LastUsed != nil {
		t.Fatalf("the last use of a sealed call was stamped while a write is held: %+v", got)
	}
	within("ListBeads", func() error {
		_, err := s.ListBeads(ctx, BeadFilter{})
		return err
	})

	releaseWrite()
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	// The stamp the held write deferred is made by the next call.
	got, err = s.ResolveDevice(ctx, d.Token, "100.64.0.7")
	if err != nil || got == nil || got.LastUsed == nil || got.LastFrom != "100.64.0.7" {
		t.Fatalf("ResolveDevice after the write = %+v, %v", got, err)
	}
	if err := s.StampDeviceUse(ctx, "jo", "tablet", tablet.ChannelKey, "edge"); err != nil {
		t.Fatal(err)
	}
	if got := deviceOf(t, s, "jo", "tablet"); got.LastUsed == nil || got.LastFrom != "edge" {
		t.Fatalf("StampDeviceUse after the write = %+v", got)
	}
}

// A stamp of a missing device makes nothing, and a call admitted on an old
// channel does not mark a replacement that reuses its name. This is a step in
// TestDeviceLastUse's stamp coverage: ResolveDevice has no missing-device
// case because it finds a device by its token.
func testDeviceStampOfAMissingDevice(t *testing.T) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	ctx := context.Background()
	if err := s.SetSetting(ctx, OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	d, err := s.AddDevice(ctx, "jo", "phone", "jo")
	if err != nil {
		t.Fatal(err)
	}
	key := enc.EncodeToString(make([]byte, 32))
	if _, err := s.PinChannelKeyInPlace(ctx, d.Token, key); err != nil {
		t.Fatal(err)
	}
	if err := s.StampDeviceUse(ctx, "jo", "tablet", key, "edge"); err != nil {
		t.Fatalf("StampDeviceUse of a device that was never there = %v", err)
	}
	if _, err := s.RemoveDevice(ctx, "jo", "phone", "jo"); err != nil {
		t.Fatal(err)
	}
	// The stamp after the removal: it finds no device. And the write of a
	// stamp whose device went between its look and its write, which reads the
	// record again.
	if err := s.StampDeviceUse(ctx, "jo", "phone", key, "edge"); err != nil {
		t.Fatalf("StampDeviceUse of a removed device = %v", err)
	}
	if err := s.write(func(w *wtx) error { return stampDeviceIn(w.Tx, join("jo", "phone"), key, "edge", now()) }); err != nil {
		t.Fatalf("the write of a stamp of a removed device = %v", err)
	}
	if list, err := s.Devices(ctx, "jo"); err != nil || len(list) != 0 {
		t.Fatalf("the devices after a stamp of one removed and one never there = %+v, %v, want none", list, err)
	}
	if got, err := s.DeviceByChannelKey(ctx, key); err != nil || got != nil {
		t.Fatalf("the removed device's channel key names %+v, %v", got, err)
	}
	if got, err := s.ResolveDevice(ctx, d.Token, "edge"); err != nil || got != nil {
		t.Fatalf("the removed device's token resolves: %+v, %v", got, err)
	}

	// Reusing the name does not turn the old session's admitted call into a
	// use of the replacement. Check both the store method's initial read and
	// the transaction-time read for a replacement after the look.
	replacement, err := s.AddDevice(ctx, "jo", "phone", "jo")
	if err != nil {
		t.Fatal(err)
	}
	replacementBytes := make([]byte, 32)
	replacementBytes[0] = 1
	replacementKey := enc.EncodeToString(replacementBytes)
	if _, err := s.PinChannelKeyInPlace(ctx, replacement.Token, replacementKey); err != nil {
		t.Fatal(err)
	}
	if err := s.StampDeviceUse(ctx, "jo", "phone", key, "edge"); err != nil {
		t.Fatalf("StampDeviceUse of a replacement = %v", err)
	}
	if err := s.write(func(w *wtx) error {
		return stampDeviceIn(w.Tx, join("jo", "phone"), key, "edge", now())
	}); err != nil {
		t.Fatalf("the write of a stamp of a replacement = %v", err)
	}
	if got := deviceOf(t, s, "jo", "phone"); got.LastUsed != nil || got.ChannelKey != replacementKey {
		t.Fatalf("the replacement after a stamp from the old session = %+v", got)
	}
	if got, err := s.DeviceByChannelKey(ctx, key); err != nil || got != nil {
		t.Fatalf("the old channel key names %+v, %v after replacement", got, err)
	}
}

// A device record kept with a pinned key before the store wrote how
// it was pinned is read as pinned at a pairing: pairings were the
// only pin then, and the device list keeps its label.
func TestDevicePinnedReadsPairedByDefault(t *testing.T) {
	t.Parallel()
	s, err := Open(filepath.Join(t.TempDir(), "y.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	ctx := context.Background()
	if err := s.SetSetting(ctx, OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	d, err := s.AddDevice(ctx, "jo", "phone", "jo")
	if err != nil {
		t.Fatal(err)
	}
	key := enc.EncodeToString(make([]byte, 32))
	err = s.write(func(w *wtx) error {
		return put(w.Tx, bDevices, join("jo", "phone"), deviceRecord{
			Device:      Device{User: "jo", Name: "phone", CreatedAt: d.CreatedAt, CreatedBy: "jo", ChannelKey: key},
			TokenSHA256: tokenHash(d.Token),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Devices(ctx, "jo")
	if err != nil || len(list) != 1 || list[0].ChannelPinned != PinnedPaired || list[0].Sealed() != "sealed (paired)" {
		t.Fatalf("an old pinned record lists as %+v, %v, want paired", list, err)
	}
	if got, err := s.DeviceByChannelKey(ctx, key); err != nil || got == nil || got.ChannelPinned != PinnedPaired {
		t.Fatalf("DeviceByChannelKey = %+v, %v, want paired", got, err)
	}
	// The key it sends is the record's own: only a record that says in place
	// is answered its key again (PinChannelKeyInPlace), and this one does not.
	if got, err := s.PinChannelKeyInPlace(ctx, d.Token, enc.EncodeToString(make([]byte, 32))); got != nil || err == nil || !strings.Contains(err.Error(), "at its pairing") {
		t.Fatalf("pinning in place over an old pinned record = %+v, %v, want refused", got, err)
	}
}
