package pack

import (
	"io/fs"
	"slices"
)

// HostedMintPack is the pack a yard applies again to give a source the scopes
// the hosted mint needs of it (credential.mint_scopes): the name of the one
// pack in fsys, as its directory is named, which is the word `yardr pack
// apply` resolves, whose declaration of source names those scopes. A stored
// declaration is the copy its apply made, so one applied before its pack
// declared its scopes holds none, and applying the pack again is what writes
// them. It answers "" where no pack's declaration of source names scopes
// (google-mail's does not, as mail mints locally, so applying its pack again
// would cure nothing and only send the operator round once more) and where two
// packs do, which of them to apply being no one's to guess. A pack that fails
// to load names nothing: the refusal this serves must not fail on the lookup
// of its cure.
func HostedMintPack(fsys fs.FS, source string) string {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return ""
	}
	var declaring []string
	for _, e := range entries {
		if _, err := fs.Stat(fsys, e.Name()+"/"+File); err != nil {
			continue
		}
		sub, err := fs.Sub(fsys, e.Name())
		if err != nil {
			continue
		}
		p, err := Load(sub, e.Name())
		if err != nil {
			continue
		}
		if slices.ContainsFunc(p.Declared, func(d Declared) bool {
			return d.Name == source && d.Declaration != nil && d.Declaration.Credential != nil && len(d.Declaration.Credential.MintScopes) > 0
		}) {
			declaring = append(declaring, e.Name())
		}
	}
	if len(declaring) != 1 {
		return ""
	}
	return declaring[0]
}
