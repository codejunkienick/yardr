package store_test

import (
	"context"
	"encoding/binary"
	"path/filepath"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"

	"yardr.dev/yardr/internal/store"
)

// A store of schema 36 keeps no attempt of a card pairing. Opened by this
// build it is stamped 37 and no event is logged; a card pairing that ends
// then cools its name down on the upgraded store, past a restart, and the
// next start changes nothing.
func TestSchema36StoreGainsPairingAttempts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "yard.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddUser(ctx, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, store.OperatorSetting, "jo", "jo"); err != nil {
		t.Fatal(err)
	}
	head, _ := s.Head(ctx)
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	// Age it: a store as a build of schema 36 left it, with no bucket of
	// attempts.
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket([]byte("pairing_attempts")); err != nil {
			return err
		}
		return tx.Bucket([]byte("meta")).Put([]byte("schema"), binary.BigEndian.AppendUint64(nil, 36))
	})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	offer := store.PairOffer{Name: "home", Key: peerKey(t), URL: "http://10.0.0.5:8790", As: "laptop", Card: true}
	for i, pass := range []string{"the upgrade", "the next start"} {
		s, err := store.Open(path)
		if err != nil {
			t.Fatalf("%s: %v", pass, err)
		}
		if after, _ := s.Head(ctx); after != head {
			t.Errorf("%s logged %d events, want none", pass, after-head)
		}
		if i == 0 {
			opened, err := s.PairOpen(ctx, offer, "jo")
			if err != nil {
				t.Fatalf("the opening on the upgraded store: %v", err)
			}
			if err := s.PairClose(ctx, opened.ID, "jo"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.PairOpen(ctx, offer, "jo"); err == nil || !strings.Contains(err.Error(), "cooldown") {
			t.Errorf("%s: a re-open = %v: want the cooldown", pass, err)
		}
		// The opens above logged their own events; the next start is
		// judged from here.
		head, _ = s.Head(ctx)
		if err := s.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if got := schemaOf(t, path); got != 37 {
			t.Fatalf("%s: schema = %d; want 37", pass, got)
		}
	}
}
