package store

import (
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// ackedBy records that session read note seq of the bead row, n being the
// note as it is stored, or refuses an ack that is not the session's to give.
//
// The session the note was told to gives it. Once that session has ended it
// never will, so the bead's live session may in its place: it reads every
// note the bead holds, whichever session was live when each was written
// (yardr prime, yardr bead show), and a note that asks to be acked, one of
// the yardmaster's that changes the work, would stay unacked however
// plainly the live session had read it. The note then becomes that
// session's, so that bead show says it was read by the session that read it
// and not by one that ended without. A recorded session that is still live
// keeps its tells: they are its own while it lives, and nothing else may
// mark one read or cancel its telling. A note the ended session had read
// already is left as it was: it was read, by that session.
//
// Acking twice is the note again. n is written as ackedBy leaves it, with
// the bead still as the UUID it is stored by.
func ackedBy(tx *bolt.Tx, row *beadRow, seq uint64, n *Note, session string) error {
	stands := false
	if n.Notify != session {
		var told Session
		ok, err := get(tx, bSessions, n.Notify, &told)
		if err != nil {
			return err
		}
		if ok && told.live() {
			return fmt.Errorf("note %d on %s was told to session %s: ack it as that session", seq, row.ID, n.Notify)
		}
		if row.Session == nil || *row.Session != session {
			return fmt.Errorf("note %d on %s was told to session %s, which has ended: only the bead's live session acks it", seq, row.ID, n.Notify)
		}
		stands = true
	}
	if n.Acked {
		return nil
	}
	n.Acked = true
	if stands {
		n.Notify = session
	}
	return put(tx, bNotes, join(row.UUID, seqKey(seq)), n)
}
