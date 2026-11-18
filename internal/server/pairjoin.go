package server

import (
	"context"
	"net/http"
	"time"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/internal/yards"
)

// pairJoinHTTP is what a card's answer reaches the yard its call reached
// with. The host stretches nothing to answer it, so a call takes a moment;
// the wait for the host's operator is the polls, not one call.
var pairJoinHTTP = &http.Client{Timeout: 30 * time.Second}

// pairJoinSendFor carries the joiner's word on a card pairing's check to
// the host its call reached: yes, asked again until the host's operator
// has answered too, or no, once. It is registered on the store
// (SetPairCallSender) by the process that owns it, which alone holds the
// yard's key and dials.
func pairJoinSendFor(c *config.Config) store.PairCallSender {
	return func(ctx context.Context, url string, t store.Transcript, yes bool) (*store.PairingOutcome, error) {
		key, err := store.Load(c.PeerKey())
		if err != nil {
			return nil, err
		}
		called := &yards.Called{Transcript: t, URL: url}
		caller := &yards.Caller{Key: key, HTTP: pairJoinHTTP}
		if !yes {
			return caller.PairAnswer(ctx, called, false)
		}
		return caller.PairWait(ctx, called, time.Second)
	}
}
