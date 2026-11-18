package store

import bolt "go.etcd.io/bbolt"

// migratePairingAttempts brings schema 37: a card pairing keeps its last
// ended attempt per expected peer (bPairAttempts), which the cooldown is
// refused on. Nothing is carried over: pairings lived in the server's
// memory, so an old store holds no attempt to keep, and the bucket is made
// on open. The stamp is what stops a build from before the cooldown
// running without it.
func migratePairingAttempts(tx *bolt.Tx, from uint64) error {
	if from >= 37 {
		return nil
	}
	return nil
}
