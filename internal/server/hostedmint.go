package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"yardr.dev/yardr/internal/config"
	"yardr.dev/yardr/internal/edgelink"
	"yardr.dev/yardr/internal/pack"
	"yardr.dev/yardr/internal/store"
	"yardr.dev/yardr/packs"
)

// hostedMintFor is the hosted-Google minter the server registers on its
// store (store.SetHostedMint): one signed mint call through the edge for
// account, by the yard's key and the edge setting this yard pinned, with
// the scope check against the scopes the source's declaration names
// (credential.mint_scopes) before the token is held — an ungranted
// source's token is discarded with the answer, as the setup's probe
// discards its. A source that declares no scope is not served and
// reaches no edge; its refusal names the pack to apply again where an
// embedded pack declares the scopes the stored copy was applied without.
// The token goes back to the mint cache alone: it is printed nowhere,
// logged nowhere and kept in no event. The edge is called as the CLI's
// probe calls it (edgelink.MintGoogleThrough), with the system's roots.
func hostedMintFor(st store.API, c *config.Config) store.HostedMint {
	return func(ctx context.Context, addr, source, account string) (string, time.Time, error) {
		needed := declaredMintScopes(ctx, st, source)
		if len(needed) == 0 {
			return "", time.Time{}, edgelink.CheckGoogleScopes(source, addr, needed, nil, pack.HostedMintPack(packs.FS, source))
		}
		mint, err := edgelink.MintGoogleThrough(ctx, st, c.PeerKey(), nil, addr, account)
		if err != nil {
			return "", time.Time{}, err
		}
		if err := edgelink.CheckGoogleScopes(source, addr, needed, mint.GrantedScopes, ""); err != nil {
			return "", time.Time{}, err
		}
		expires, err := time.Parse(time.RFC3339, strings.TrimSpace(mint.ExpiresAt))
		if err != nil {
			return "", time.Time{}, fmt.Errorf("the hosted mint's answer holds no expiry yardr reads (%q): expires_at is a time as RFC 3339 writes one", mint.ExpiresAt)
		}
		return mint.Token, expires, nil
	}
}

// declaredMintScopes are the scopes the declaration of source names for
// the hosted mint, nil where the yard has no such source or its
// declaration names none: the hosted mint serves a source by what its
// declaration declares, and core names no source of its own.
func declaredMintScopes(ctx context.Context, st store.API, source string) []string {
	src, err := st.GetSource(ctx, source)
	if err != nil {
		return nil
	}
	d, err := src.Declaration()
	if err != nil || d.Credential == nil {
		return nil
	}
	return d.Credential.MintScopes
}
