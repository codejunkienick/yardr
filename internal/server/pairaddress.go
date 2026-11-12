package server

import (
	"context"
	"fmt"
	"net"
	"net/netip"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/edgelink"
	"yardr.dev/yardr/internal/hooks"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/yardkey"
)

// pairAddressFor answers where a device reaches this yard at for a
// device pair card that named no --url: the listener's own address, or
// the edge's where the yard listens nowhere and its link is up, as the
// terminal command gives it (internal/cli/devicepair.go). It is
// registered on the store (SetPairAddress) by the process that owns it,
// which alone knows the yard's listener and link.
func pairAddressFor(c *config.Config, st *store.Store) func() (string, error) {
	return func() (string, error) {
		if addr, err := listenerAddress(c); err == nil {
			return addr, nil
		}
		if edge := edgeAddress(c, st); edge != "" {
			return edge, nil
		}
		return "", fmt.Errorf("this yard has no listener a device can call with a code, and no edge link up: name where the device reaches this yard with --url <address>")
	}
}

// pairChannelKeyFor makes the channel key when a run-ask approval first
// needs a sealed QR, as the direct pairing command does. It is called
// before the approval's store write; a key that cannot be made leaves the
// pairing's QR plain, as the direct command's fallback does.
func pairChannelKeyFor(c *config.Config) func() string {
	return func() string {
		channel, _, err := yardkey.EnsureChannel(c.ChannelKey())
		if err != nil {
			return ""
		}
		return yardkey.ChannelPublic(channel)
	}
}

// listenerAddress is the listener's address as a URL, where it is on
// one address; an address on every address of the machine is none.
func listenerAddress(c *config.Config) (string, error) {
	l, known := hooks.ReadListener(c)
	if !known || !l.Listening() {
		return "", fmt.Errorf("the yard's listener is not listening")
	}
	host, port, err := net.SplitHostPort(l.Addr)
	if err != nil {
		return "", fmt.Errorf("the listener's address %q is none to give a device", l.Addr)
	}
	if ip, err := netip.ParseAddr(host); host == "" || err == nil && ip.IsUnspecified() {
		return "", fmt.Errorf("the listener is on every address of this machine (%s), which tells a device nothing", l.Addr)
	}
	return "http://" + net.JoinHostPort(host, port), nil
}

// edgeAddress is the edge's address where the yard's link to it is up,
// and "" anywhere else.
func edgeAddress(c *config.Config, st *store.Store) string {
	s, known := edgelink.ReadState(c)
	if !known || !s.Up {
		return ""
	}
	all, err := st.Settings(context.Background())
	if err != nil {
		return ""
	}
	e, connected, err := store.EdgeOf(all)
	if err != nil || !connected {
		return ""
	}
	return e.URL
}
