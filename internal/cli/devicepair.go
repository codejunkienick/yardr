package cli

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"rsc.io/qr"

	"io"
	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/edgelink"
	"yardr.dev/yardr/internal/hooks"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/yardkey"
)

// A device is enrolled by a code, as two yards are paired (docs/users.md,
// "Enrolled by a code"): the operator opens a pairing for one device of one
// user, and the device that calls the yard with the code is answered with
// its token. Nobody types or pastes the token, and no command prints it.

// drawQR draws text as a QR code in the terminal's characters, two modules
// to a line, inside the quiet zone a reader wants around it. With colour it
// is black on white whatever the terminal's own colours are; without, the
// dark modules are the text's colour, which reads on a light ground.
func drawQR(text string, colour bool) (string, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	const quiet = 4
	dark := func(x, y int) bool {
		return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
	}
	var b strings.Builder
	for y := -quiet; y < code.Size+quiet; y += 2 {
		b.WriteString("  ")
		if colour {
			b.WriteString("\x1b[30;47m")
		}
		for x := -quiet; x < code.Size+quiet; x++ {
			switch top, bottom := dark(x, y), dark(x, y+1); {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		if colour {
			b.WriteString("\x1b[0m")
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// linkAddr is the address a device reaches this yard at through the edge:
// the edge's own, when the yard is connected and its link is up. It is the
// device's address where the yard listens nowhere, and "" otherwise.
func linkAddr(cfg *config.Config, settings map[string]string) string {
	s, known := edgelink.ReadState(cfg)
	if !known || !s.Up {
		return ""
	}
	e, connected, err := store.EdgeOf(settings)
	if err != nil || !connected {
		return ""
	}
	return e.URL
}

// devicePaired is what yardr user device pair prints of a pairing it
// opened.
type devicePaired struct {
	Pairing string `json:"pairing"`
	User    string `json:"user"`
	Device  string `json:"device"`
	Code    string `json:"code"`
	Address string `json:"address"`
	// Yard is this yard at the edge, handle/name, where the address is the
	// edge's (linkAddr): the device sends it back to say which of the
	// account's yards it means. A pairing on the listener names none.
	Yard string `json:"yard,omitempty"`
	// ChannelKey is the yard's channel public key, the QR's fourth
	// field: the device pins it and redeems the code inside a Noise
	// handshake to it. ChannelSignature is the yard identity's
	// signature of it, for a device that later learns the key from
	// anywhere but this yard's own screen. Both are absent where the
	// yard's channel key could not be made or read, and the pairing
	// is then the plain one it always was.
	ChannelKey       string    `json:"channel_key,omitempty"`
	ChannelSignature string    `json:"channel_signature,omitempty"`
	Expires          time.Time `json:"expires"`
	// QR is what a QR code of the pairing holds (store.PairingQR).
	QR string `json:"qr"`
}

func (a *app) devicePairCmd() *cobra.Command {
	var addr string
	var draw, end bool
	cmd := &cobra.Command{
		Use: "pair <user> <device>", Short: "Enrol a device by a short code, or a QR code, with no token to copy",
		Long: `Opens a pairing for one device of one user and shows its code. The device
calls this yard with the code and is answered with its token, once: nobody
types or pastes the token, and nothing here prints it. The device is added
when the code is used, and is then as one yardr user device add made.

The code is good for 10 minutes and for one device, and 5 wrong codes end
the pairing. A yard has one such pairing open at a time; --end ends it.

--url is the address the device reaches this yard at, which the device is
given with the code; the default is the listener's own address, which is
right on one machine or one network, or the edge's when the yard's link is
up and it listens nowhere. A yard with neither opens no pairing. Where the
address is the edge's, the device is also given this yard's name at the
edge, so it knows which of the account's yards it pairs with. --qr also
draws a QR code that holds the address and the code, as <address>#<code>,
and the yard's name beside them where the address is the edge's, as
<address>#<code>#<yard>, and nothing else: a phone reads it in place of
each being typed. The QR code also holds this yard's channel public key
as its fourth field, <address>#<code>#<yard>#<channel> (the yard field
empty where no yard is named): the device pins that key, redeems the
code inside a Noise handshake to it, and is answered sealed, with its
own key recorded beside its token. A device given the code alone, typed
or read aloud, pairs plainly as it always has.

The code is all a plain device needs, and whoever has it within those minutes gets
the token: show it, and the QR code, to the device and to nobody else. What
keeps a plain code and its token from whoever is between the device and the
yard is the connection, so give an https address off this machine; a device
that scans the QR code is sealed from that carrier either way.

Only the operator opens a pairing, and never from the pane of an agent, as
for yardr user device add: whoever enrols a device decides who may speak as
a person.`,
		Example: `  yardr user device pair mckean phone --url https://mac.example.net --qr
  yardr user device pair --end`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 && len(args) != 2 {
				return errors.New("give the user and the device's name, or --end alone")
			}
			return nil
		},
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()
			st, err := a.store(ctx)
			if err != nil {
				return err
			}
			if end {
				if len(args) != 0 || addr != "" || draw {
					return errors.New("--end ends the device pairing this yard has open: it takes nothing else")
				}
				p, err := st.DevicePairClose(ctx, a.who())
				if errors.Is(err, store.ErrNotFound) {
					return errors.New("this yard has no device pairing open")
				} else if err != nil {
					return err
				}
				return a.out(c.OutOrStdout(), map[string]any{"pairing": "ended", "user": p.User, "device": p.Device}, func(w io.Writer) {
					fmt.Fprintf(w, "the pairing for the device %s of %s is ended: its code is good for nothing\n", p.Device, p.User)
				})
			}
			if len(args) != 2 {
				return errors.New("give the user and the device's name, or --end to end the pairing that is open")
			}
			cfg, err := a.config()
			if err != nil {
				return err
			}
			all, err := st.Settings(ctx)
			if err != nil {
				return err
			}
			listening, text := receiver(ctx, cfg, st)
			edge := linkAddr(cfg, all)
			if !listening && edge == "" {
				return fmt.Errorf("this yard has no listener, so no device can call it with a code: %s", text)
			}
			if addr == "" {
				if addr, err = listenerURL(cfg); err != nil {
					if edge == "" {
						l, _ := hooks.ReadListener(cfg)
						return fmt.Errorf("the listener's address (%s) is none to give a device: say where the device reaches this yard with --url, such as --url https://<this machine>", l.Addr)
					}
					addr = edge
				}
			} else if addr, err = store.CleanURL(addr); err != nil {
				return fmt.Errorf("%w: the address the device reaches this yard at", err)
			}
			opened, err := st.DevicePairOpen(ctx, args[0], args[1], a.who())
			if err != nil {
				return err
			}
			out := devicePaired{Pairing: opened.ID, User: opened.User, Device: opened.Device, Code: opened.Code,
				Address: addr, Expires: opened.Expires}
			// Where the device reaches this yard through the edge, whether
			// by default or by --url, it is given this yard too, handle/name:
			// the edge holds several yards per account, and the device says
			// which it means with each call.
			if e, connected, err := store.EdgeOf(all); err == nil && connected && addr == e.URL {
				out.Yard = e.HandleName()
			}
			// The yard's channel key, made once beside its identity
			// where it has none: the QR carries its public half as the
			// fourth field, and the device that scans it redeems the
			// code inside a Noise handshake to it. An identity or a
			// channel key that cannot be made costs the pairing its
			// sealed form, never the pairing: it is opened and good as
			// a plain one, and says so.
			if identity, _, ierr := yardkey.Ensure(cfg.PeerKey()); ierr == nil {
				if channel, _, cerr := yardkey.EnsureChannel(cfg.ChannelKey()); cerr == nil {
					out.ChannelKey = yardkey.ChannelPublic(channel)
					out.ChannelSignature = yardkey.SignChannelKey(identity, channel.Public)
				} else {
					fmt.Fprintf(c.ErrOrStderr(), "no channel key could be made (%v): this pairing is a plain one, with no key in its QR code\n", cerr)
				}
			} else {
				fmt.Fprintf(c.ErrOrStderr(), "the yard's key could not be read or made (%v): this pairing is a plain one, with no key in its QR code\n", ierr)
			}
			out.QR = store.PairingQR(addr, opened.Code, out.Yard, out.ChannelKey)
			var picture string
			if draw && !a.json {
				if picture, err = drawQR(out.QR, term.IsTerminal(int(os.Stdout.Fd()))); err != nil {
					// The pairing is open and its code good: only the picture is not there.
					fmt.Fprintf(c.ErrOrStderr(), "no QR code could be drawn of %d characters (%v): give the device the address and the code\n", len(out.QR), err)
				}
			}
			yardr := yardrAt(cfg.Home)
			return a.out(c.OutOrStdout(), out, func(w io.Writer) {
				give := "this yard's address and the code"
				if out.Yard != "" {
					give = "this yard's address, its name at the edge and the code"
				}
				fmt.Fprintf(w, "Pairing is open for %s, until %s, for the device %s of %s. Give the device %s:\n\n"+
					"  address  %s\n",
					short(store.TTL), out.Expires.Local().Format("15:04:05"), out.Device, out.User, give, out.Address)
				if out.Yard != "" {
					fmt.Fprintf(w, "  yard     %s\n", out.Yard)
				}
				fmt.Fprintf(w, "  code     %s\n", out.Code)
				if out.ChannelKey != "" {
					fmt.Fprintf(w, "  channel  %s\n", out.ChannelKey)
				}
				fmt.Fprintf(w, "\n")
				if picture != "" {
					fmt.Fprintf(w, "%s\n", picture)
				}
				if out.ChannelKey != "" {
					fmt.Fprintf(w, "The code is good once: a device that scans the QR code is answered inside a sealed channel, "+
						"and whoever carries the connection reads neither the code nor the device token. "+
						"%d wrong codes end the pairing, and so does: %s user device pair --end\n", store.MaxTries, yardr)
				} else {
					fmt.Fprintf(w, "The code is good once: the device is answered with its token, which is shown nowhere else. "+
						"%d wrong codes end the pairing, and so does: %s user device pair --end\n", store.MaxTries, yardr)
				}
				fmt.Fprintf(w, "It then calls as %s until: %s user device rm %s %s\n", out.User, yardr, out.User, out.Device)
				if !isOperator(c.Context(), st, out.User) {
					fmt.Fprintf(w, "%s is not an operator, and the device reads every bead in the yard, with its notes, and the mail of %s alone.\n", out.User, out.User)
				}
				if u, err := url.Parse(out.Address); err == nil && u.Scheme == "http" && u.Hostname() != "localhost" && !isLoopback(u.Hostname()) {
					if out.ChannelKey != "" {
						fmt.Fprintf(w, "%s is plain http: a code typed there crosses readable, and the token after it; a device that scans the QR code is answered sealed even so. Use it on a network you trust.\n", out.Address)
					} else {
						fmt.Fprintf(w, "%s is plain http: whoever is between the device and this yard reads the code, and the token after it. Use it on a network you trust.\n", out.Address)
					}
				}
			})
		},
	}
	cmd.Flags().StringVar(&addr, "url", "", "where the device reaches this yard, such as https://mac.example.net (default: the listener's address, or the edge's when the link is up)")
	cmd.Flags().BoolVar(&draw, "qr", false, "also draw a QR code that holds the address, the code and the yard's channel key")
	cmd.Flags().BoolVar(&end, "end", false, "end the device pairing this yard has open")
	return cmd
}
