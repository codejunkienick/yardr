package pack_test

import (
	"testing"
	"testing/fstest"

	"yardr.dev/yardr/internal/pack"
)

// hostedPackFS is packs by directory: scoped-pack declares acme with the
// hosted scopes, plain-pack declares mail without them, and a file beside
// them is no pack.
func hostedPackFS() fstest.MapFS {
	return fstest.MapFS{
		"scoped-pack/pack.yaml": hostedPackFile("scoped-pack", "acme"),
		"scoped-pack/acme.yaml": hostedDeclaration("acme", true),
		"plain-pack/pack.yaml":  hostedPackFile("plain-pack", "mail"),
		"plain-pack/mail.yaml":  hostedDeclaration("mail", false),
		"README.txt":            &fstest.MapFile{Data: []byte("not a pack\n")},
	}
}

func hostedPackFile(name, source string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("name: " + name + "\nkind: source\nsources:\n  " + source + ":\n    declaration: " + source + ".yaml\n")}
}

// hostedDeclaration is a source's declaration whose credential mints for an
// account, with the hosted scopes where scoped.
func hostedDeclaration(name string, scoped bool) *fstest.MapFile {
	scopes := ""
	if scoped {
		scopes = "  mint_scopes: [https://acme.test/auth/read]\n"
	}
	return &fstest.MapFile{Data: []byte("name: " + name + "\nbase: https://acme.test\ncredential:\n  env: ACME_TOKEN\n  mint:\n    - name: ACME_ACCOUNT\n      secret: false\n" + scopes +
		"calls:\n  ping: { kind: read, request: { method: GET, path: /me } }\n")}
}

// The pack a stored declaration without scopes is applied again from is the
// one pack whose declaration of the source names them, by its directory's
// name, which is what yardr pack apply resolves. Where no pack's does, or the
// source is one a pack declares without scopes, there is none to name: applying
// that pack cures nothing. A second pack declaring the source without scopes
// is no rival to the one that declares them, two that declare them are, and a
// pack that cannot be read names nothing and does not fail the lookup.
func TestHostedMintPack(t *testing.T) {
	both := hostedPackFS()
	both["second-pack/pack.yaml"] = hostedPackFile("second-pack", "acme")
	both["second-pack/acme.yaml"] = hostedDeclaration("acme", true)

	unscopedRival := hostedPackFS()
	unscopedRival["rival-pack/pack.yaml"] = hostedPackFile("rival-pack", "acme")
	unscopedRival["rival-pack/acme.yaml"] = hostedDeclaration("acme", false)

	broken := hostedPackFS()
	broken["broken-pack/pack.yaml"] = hostedPackFile("broken-pack", "acme") // names a declaration it has not

	named := hostedPackFS()
	named["dir-pack/pack.yaml"] = hostedPackFile("another-name", "beta")
	named["dir-pack/beta.yaml"] = hostedDeclaration("beta", true)

	for _, c := range []struct {
		name   string
		fsys   fstest.MapFS
		source string
		want   string
	}{
		{"the one pack that declares the scopes", hostedPackFS(), "acme", "scoped-pack"},
		{"a source its pack declares without scopes", hostedPackFS(), "mail", ""},
		{"a source no pack declares", hostedPackFS(), "crm", ""},
		{"two packs that declare the scopes", both, "acme", ""},
		{"a second pack that declares none", unscopedRival, "acme", "scoped-pack"},
		{"a pack that fails to load beside it", broken, "acme", "scoped-pack"},
		{"the directory's name, not the pack's", named, "beta", "dir-pack"},
	} {
		if got := pack.HostedMintPack(c.fsys, c.source); got != c.want {
			t.Errorf("%s: HostedMintPack(%q) = %q, want %q", c.name, c.source, got, c.want)
		}
	}
}
