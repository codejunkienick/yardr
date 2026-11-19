package cli

import (
	"context"
	"fmt"
	"io"

	"yardr.dev/yardr/internal/store"
)

// userBox is a user's inbox as yardr inbox prints it: its two parts, the
// mail (a person or a crew member talking) and the notifications (the yard
// reporting). A part that was not asked for is absent. The mail is at the
// top level, where it was when the inbox had one part.
type userBox struct {
	*store.Mailbox
	Notifications *store.NotificationPage `json:"notifications,omitempty"`
}

// userInbox is yardr inbox run as a user: of each part asked for, the
// unread and the latest read, under the part's name and unread count. With
// ack what is through the seq is marked read, in the parts shown only:
// each part has a cursor of its own.
func (a *app) userInbox(w io.Writer, ctx context.Context, st store.API, name string, limit int, ack bool, through uint64, mail, notes bool) error {
	var box userBox
	var err error
	if mail {
		if box.Mailbox, err = st.UserInbox(ctx, name, limit, a.who()); err != nil {
			return err
		}
	}
	if notes {
		if box.Notifications, err = st.UserNotifications(ctx, name, limit, a.who()); err != nil {
			return err
		}
	}
	if ack && through == 0 {
		// Bare --ack: refuse, naming the newest seq the parts shown
		// hold and the command to ack through it.
		var newest uint64
		if mail {
			newest = max(newest, box.Last)
		}
		if notes {
			newest = max(newest, box.Notifications.Last)
		}
		scope := "yardr inbox"
		if mail && !notes {
			scope += " --mail"
		}
		if notes && !mail {
			scope += " --notifications"
		}
		return ackRefused(scope, newest)
	}
	// Both are read before either is acknowledged: an ack is logged, and
	// what is shown is the inbox as it was found.
	rest, acked, newer := map[string]int{}, map[string]int{}, map[string]int{}
	if mail {
		// The unread come first, oldest first.
		shown := 0
		for _, l := range box.Mail {
			if !l.Read {
				shown++
			}
		}
		rest["mail"] = box.Unread - shown
		if ack && box.Unread > 0 {
			// Past the newest unread the seq cannot go: a future
			// seq must not pre-acknowledge mail not yet written.
			if t := min(through, box.Last); t > 0 {
				if err := st.AckUserInbox(ctx, name, t, a.who()); err != nil {
					return err
				}
			}
			after, err := st.UserInbox(ctx, name, 1, a.who())
			if err != nil {
				return err
			}
			newer["mail"] = after.Unread
			acked["mail"] = box.Unread - newer["mail"]
		}
	}
	if notes {
		// Oldest first, so the unread are last and the newest of them ends
		// the page unless the limit cut it.
		ns := box.Notifications.Notifications
		shown := 0
		for _, n := range ns {
			if !n.Read {
				shown++
			}
		}
		rest["notifications"] = box.Notifications.Unread - shown
		if ack && box.Notifications.Unread > 0 {
			if t := min(through, box.Notifications.Last); t > 0 {
				if err := st.AckUserNotifications(ctx, name, t, a.who()); err != nil {
					return err
				}
			}
			after, err := st.UserNotifications(ctx, name, 1, a.who())
			if err != nil {
				return err
			}
			newer["notifications"] = after.Unread
			acked["notifications"] = box.Notifications.Unread - newer["notifications"]
		}
	}
	return a.out(w, box, func(w io.Writer) {
		if mail {
			fmt.Fprintln(w, partHead("Mail", len(box.Mail), box.Unread))
			for _, l := range box.Mail {
				state := ""
				if l.Read {
					state = " (read)"
				}
				from := l.From
				if l.As != "" {
					from += " (as " + l.As + ")"
				}
				from = store.ViaMark(from, l.Via)
				// The sender and the text are writers' words, shown without
				// the mark, and the files stand under them on a marked line
				// of their own, so no mail's words forge its files line.
				fmt.Fprintf(w, "%6d %s  mail from %s%s: %s\n", l.Seq, l.At.Local().Format(mailTime), store.Letters(store.ReMark(from, l.Answered)), state, store.Letters(l.Text))
				if line := store.NoteFilesLine(a.mailHome(), l.Files); line != "" {
					fmt.Fprintf(w, "    %s\n", line)
				}
				printReactions(w, l)
			}
			partFoot(w, rest["mail"], acked["mail"], ack, newer["mail"])
		}
		if mail && notes {
			fmt.Fprintln(w)
		}
		if notes {
			p := box.Notifications
			fmt.Fprintln(w, partHead("Notifications", len(p.Notifications), p.Unread))
			for _, n := range unreadFirst(p.Notifications) {
				state := ""
				if n.Read {
					state = "(read) "
				}
				fmt.Fprintf(w, "%6d %s  %s%s\n", n.Seq, n.At.Local().Format(mailTime), state, n.Text)
			}
			partFoot(w, rest["notifications"], acked["notifications"], ack, newer["notifications"])
		}
	})
}

// unreadFirst is a page of notifications in the order the mail above it
// is listed in: the unread, oldest first, then the read, the latest first.
// The store gives a page oldest first throughout, which put the unread
// under the read in one part and over them in the other.
func unreadFirst(page []store.Notification) []store.Notification {
	rows := make([]store.Notification, 0, len(page))
	for _, n := range page {
		if !n.Read {
			rows = append(rows, n)
		}
	}
	for i := len(page) - 1; i >= 0; i-- {
		if page[i].Read {
			rows = append(rows, page[i])
		}
	}
	return rows
}

// partHead is the line over a part of a user's inbox: its name and how
// much of it is unread.
func partHead(part string, shown, unread int) string {
	switch {
	case shown == 0:
		return part + ": none"
	case unread == 0:
		return part + ": nothing unread; the latest read:"
	}
	return fmt.Sprintf("%s: %d unread", part, unread)
}

// partFoot says, under a part, what of its unread was not shown, what an
// ack marked read, and what newer than the ack's seq stays unread.
func partFoot(w io.Writer, rest, ackedCount int, ack bool, newer int) {
	if rest > 0 {
		fmt.Fprintf(w, "… %d more unread\n", rest)
	}
	if ack {
		fmt.Fprintf(w, "acknowledged %d\n", ackedCount)
		if newer > 0 {
			fmt.Fprint(w, newerUnreadLine(newer))
		}
	}
}

// notifications is "1 notification" or "N notifications".
func notifications(n int) string {
	if n == 1 {
		return "1 notification"
	}
	return fmt.Sprintf("%d notifications", n)
}
