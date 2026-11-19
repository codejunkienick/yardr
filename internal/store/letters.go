package store

import "time"

// Reaction is one party's emoji on a letter: who put it and which of the
// fixed reactions it is.
type Reaction struct {
	From  string `json:"from"`
	Emoji string `json:"emoji"`
}

// Letter is one mail as a conversation and a user's inbox show it.
type Letter struct {
	Seq  uint64    `json:"seq"`
	At   time.Time `json:"at"`
	From string    `json:"from"`
	To   string    `json:"to"`
	Text string    `json:"text"`
	// Read is whether the receiver acknowledged it (yardr inbox --ack <seq>).
	// Mail to a party that is gone was never read as far as the store
	// knows.
	Read bool `json:"read"`
	// Reactions are the parties' emoji on the mail, by who put them; absent
	// on mail without any.
	Reactions []Reaction `json:"reactions,omitempty"`
	// Timer is the id of the timer the yard sent it for, "" for mail its
	// sender wrote: the text then begins "timer:".
	Timer string `json:"timer,omitempty"`
	// Delivery is set on mail to a crew member of another yard, whose reading
	// of it this yard never learns of: MailQueued while it waits here for
	// the peer, MailSent once the peer has it, MailFailed when it was
	// given up.
	Delivery string `json:"delivery,omitempty"`
	// As is the user of this yard the mail speaks as: set on mail from a
	// name of a linked yard that the link trusted as that user when the
	// mail came (Peer.Trust), "" on any other.
	As string `json:"as,omitempty"`
	// Via is the device the mail was written through (WithVia), "" for mail
	// written on the yard's own machine.
	Via string `json:"via,omitempty"`
	// Files are the blobs the mail carries (PutBlob), absent on mail
	// without any: each is read with GET /blobs/<hash>, and is the file of
	// that name in the home's blobs directory.
	Files []BlobRef `json:"files,omitempty"`
	// Answered is the seq of the mail this answers, 0 for mail that
	// answers none: the thread's one link back.
	Answered uint64 `json:"answered,omitempty"`
}

// Mailbox is a user's mail: what they have not acknowledged, and the
// latest of what they have.
type Mailbox struct {
	// Mail is the unread, oldest first, then the read, newest first; at
	// most the limit in all.
	Mail   []Letter `json:"mail"`
	Unread int      `json:"unread"` // all unread, shown or not
	Last   uint64   `json:"last"`   // seq of the newest unread
	// From is who the unread are from, in the order they first wrote.
	From []string `json:"from"`
}
