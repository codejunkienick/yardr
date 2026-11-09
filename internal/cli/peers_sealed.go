package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/yardkey"
	"yardr.dev/yardr/internal/yards"
)

// This file is the sealed wire's part of the peer commands (RFC 9421
// signatures and HPKE-sealed bodies, docs/peers.md): the two keys a yard
// has, and what is said of a link made before them.

// peerKeys is yardr peer key: this yard's two public keys, each pair made
// on first use. The signing key is printed first and alone on its line,
// as scripts have always read it; the encryption key follows, named.
func (a *app) peerKeys(c *cobra.Command, cfg *config.Config) error {
	k, made, err := yardkey.Ensure(cfg.PeerKey())
	if err != nil {
		return err
	}
	_, encPub, encMade, err := yardkey.EnsureEnc(cfg.PeerEncKey())
	if err != nil {
		return err
	}
	return a.out(c.OutOrStdout(), map[string]any{"key": store.Public(k), "enc": encPub, "file": cfg.PeerKey(), "enc_file": cfg.PeerEncKey(), "made": made, "enc_made": encMade}, func(w io.Writer) {
		fmt.Fprintln(w, store.Public(k))
		fmt.Fprintf(w, "encryption: %s\n", encPub)
	})
}

// peerEncPub is this yard's encryption public key, its pair made now if
// it has none: what an invite carries and enrolment presents.
func peerEncPub(cfg *config.Config) (string, error) {
	_, pub, _, err := yardkey.EnsureEnc(cfg.PeerEncKey())
	return pub, err
}

// peerIncoming says how the peer's messages reach this yard, and that they
// do not when they cannot: a peer that is never called must call, and with
// no listener there is nothing for it to call.
func peerIncoming(p store.Peer, listening bool, text string) string {
	switch {
	case listening:
		return fmt.Sprintf("it posts to this yard under %s: %s", yards.Path, text)
	case p.URL != "":
		return "nothing can be posted to this yard, so it gets this peer's messages by asking it: " + text
	}
	return "none. No listener, no incoming: this peer is never called and cannot call: " + text
}
