// What a run ask carries beside its command for the rows that add a source
// (source add, pack apply): each source's declaration and command files as
// the ask holds them, and what a replacement would change under the
// source's copies. They are the ask's own, kept on AskRun (Carried, Pack)
// because every view renders an ask from its note alone.
package store

// AskFile is one command file a run ask that adds a source carries: the
// program as the declaration names it, its whole text, and the SHA-256 of
// that text. The card renders the text's hash and the approval installs
// exactly these bytes, so what is approved is what the card showed.
type AskFile struct {
	Program string `json:"program"`
	Text    string `json:"text"`
	SHA256  string `json:"sha256"`
}

// AskSource is what adding one source takes, carried on a run ask: the
// source's name as its declaration gives it, the declaration's whole text,
// and each command file the declaration names by a relative program. A
// loose file's bytes are the asker's, read when the ask is written; a
// pack's are the server's, read from its own binary (B2). Either way the
// store parses the text itself and holds the approval to these bytes.
type AskSource struct {
	Name  string    `json:"name"`
	Text  string    `json:"text"`
	Files []AskFile `json:"files,omitempty"`
	// Old, Removed and Inventory record the copy directory as the card
	// found it: each command file's change, each entry neither declaration
	// names that the copy step would remove, and the whole inventory.
	// Source adds use them for replacements and for free names with
	// orphaned copies; pack asks use them for sources they replace or add
	// over orphaned copies.
	Old       []AskFileDiff  `json:"old,omitempty"`
	Removed   []AskRemoved   `json:"removed,omitempty"`
	Inventory []AskInventory `json:"inventory,omitempty"`
}

// AskFileDiff is one command file a pack or source-add ask would change:
// its program, the SHA-256 of the copy that stands and of the carried file, whole,
// and the unified diff old against new. The old bytes are not carried:
// the diff is the showing, and the two hashes are what the approval is
// held to.
type AskFileDiff struct {
	Program   string `json:"program"`
	OldSHA256 string `json:"old_sha256,omitempty"`
	NewSHA256 string `json:"new_sha256,omitempty"`
	Diff      string `json:"diff,omitempty"`
}

// AskRemoved is one entry under a source's copies that neither the
// standing nor carried declaration names: copying removes the whole
// destination, so the card names it with its hash. Text
// stands behind the tap where it is text within the bound.
type AskRemoved struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Mode    string `json:"mode"`
	Size    int64  `json:"size"`
	Symlink bool   `json:"symlink,omitempty"`
	Text    string `json:"text,omitempty"`
}

// AskInventory is one file under a source's copies as an ask found it:
// its path under <home>/sources/<name>/, its SHA-256 and its mode. A
// chmod after the ask changes the mode, and with it the approval.
type AskInventory struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Mode    string `json:"mode"`
	Size    int64  `json:"size"`
	Symlink bool   `json:"symlink,omitempty"`
}
