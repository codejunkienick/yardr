package edgelink

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"slices"
	"strings"

	"yardr.dev/yardr/internal/store"
)

// MintGoogleThrough makes one signed mint call for account at the edge
// addr, as the hosted setup's probe and the server's in-process mint both
// make it: the yard's key loaded from peerKey, and the edge's key the one
// this yard pinned where its edge setting names addr, else the one a
// connect would fetch. The answer's token is the caller's to discard,
// check or hold; nothing here checks its scopes (CheckGoogleScopes).
func MintGoogleThrough(ctx context.Context, st store.API, peerKey string, hc *http.Client, addr, account string) (*GoogleMint, error) {
	yard, err := store.Load(peerKey)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("this yard has no key, so it can sign nothing: connect it first (yardr edge connect <url> makes the pair)")
		}
		return nil, err
	}
	settings, err := st.Settings(ctx)
	if err != nil {
		return nil, err
	}
	client := &Client{URL: addr, HTTP: hc}
	// The key this yard pinned, where its edge setting names this edge;
	// else the key a connect would fetch. A pin is never replaced by a
	// later fetch.
	edgeKey := ""
	if e, connected, perr := store.EdgeOf(settings); perr == nil && connected && e.URL == addr {
		edgeKey = e.Key
	}
	if edgeKey == "" {
		if edgeKey, err = client.Key(ctx); err != nil {
			return nil, err
		}
	}
	return client.MintGoogle(ctx, edgeKey, yard, account)
}

// CheckGoogleScopes refuses a mint answer whose granted scopes miss what
// the source's declaration names as needed (credential.mint_scopes),
// naming the source, the missing scope and where to reconnect: the scope
// check every hosted mint makes before its token is held, whose words
// the setup's probe and the in-process mint share. Needed is matched
// exactly, in declaration order; a source that declares no scope is not
// served, since treating that as no missing scopes would hand it a
// token unchecked.
func CheckGoogleScopes(source, addr string, needed, granted []string) error {
	if len(needed) == 0 {
		return fmt.Errorf("source %s declares no mint_scopes, so the hosted mint does not serve it", source)
	}
	var missing []string
	for _, need := range needed {
		if !slices.Contains(granted, need) {
			missing = append(missing, need)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s needs %s, missing from the Google connection's grant: reconnect at %s/google", source, strings.Join(missing, ", "), addr)
	}
	return nil
}
