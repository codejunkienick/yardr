package pack

import (
	"errors"
	"fmt"
	"strings"
)

// Repository is where the packs repository (yardr-dev/yardr-packs) serves
// its packs from, at its default branch: one directory per pack under it,
// with pack.yaml at the pack's root. A delegated pack apply takes a pack
// from here and from nowhere else (docs/users.md): the owner of the
// repository vouches for what its branch serves, which a digest the
// operator's mail names cannot do for any other URL. A commit's URL is not
// held to it on purpose: GitHub serves a fork's commit under its parent's
// path, so such a URL could carry bytes the owner never wrote.
const Repository = "https://raw.githubusercontent.com/yardr-dev/yardr-packs/main/"

// RepositoryPack is the name of the pack of the packs repository that the
// URL from names, with the URL as it is fetched from. origin is where the
// repository serves from (Repository; a test hands a stand-in's): from is
// origin and one pack's name, with a trailing slash or none, and nothing
// else, so no other host, scheme, ref, sub-path, query or user passes,
// however it is written. A name is the word a pack may call itself, which
// holds no slash, dot-dot segment, escape or other character that would
// move the URL.
func RepositoryPack(origin, from string) (name, url string, err error) {
	rest, ok := strings.CutPrefix(from, origin)
	if !ok {
		return "", "", fmt.Errorf("%s is not in the packs repository (%s): a delegated run applies a pack of that repository alone, written as %s<name>/", from, origin, origin)
	}
	name = strings.TrimSuffix(rest, "/")
	if !validName.MatchString(name) {
		return "", "", fmt.Errorf("%s: after %s want one pack's name and a slash (%s<name>/), not %q", from, origin, origin, rest)
	}
	return name, origin + name + "/", nil
}

// LoadRepository reads the pack of the packs repository that from names
// (RepositoryPack) and holds it to digest before it answers anything of
// it: a pack with no pin is refused, and so is one whose digest is another
// or whose pack.yaml calls it by another name than its directory has, since
// the repository's directory is the name its import records. The pack's
// files were read once to find the digest, and its plan and what is
// installed are made from those bytes (Pack.ReadFile), never from a second
// answer of the server.
func LoadRepository(origin, from, digest string) (*Pack, error) {
	name, url, err := RepositoryPack(origin, from)
	if err != nil {
		return nil, err
	}
	if digest == "" {
		return nil, errors.New("a pack of the packs repository is applied pinned: name its digest (--sha256, as yardr pack check prints it)")
	}
	p, err := Load(URLFS(url), url)
	if err != nil {
		return nil, err
	}
	if p.Name != name {
		return nil, fmt.Errorf("%s holds the pack %s, not %s: a pack of the repository is called what its directory is", url, p.Name, name)
	}
	if err := p.CheckDigest(digest); err != nil {
		return nil, err
	}
	return p, nil
}

// ReadFile is the file rel of the pack, a slash path in its directory, as
// the pack's first read of it had it: the bytes Load, the digest and the
// plan were made from. A pack served over HTTP answers every GET afresh, so
// whoever reads a file after the digest was checked reads it here.
func (p *Pack) ReadFile(rel string) ([]byte, error) { return p.read(rel) }
