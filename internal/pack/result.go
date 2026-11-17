package pack

import "yardr.dev/yardr/internal/store"

// Result is what applying a plan did.
type Result struct {
	Pack  string `json:"pack"`
	Depot string `json:"depot"`
	// Created, Changed and Unchanged count the plan's objects by what was
	// done to them.
	Created   int `json:"created"`
	Changed   int `json:"changed"`
	Unchanged int `json:"unchanged"`
	// Pruned are the dropped objects removed, Left those still there: no
	// prune was asked for, the object is held, or its removal failed (then
	// PruneErrors says why).
	Pruned      []string `json:"pruned"`
	Left        []string `json:"left"`
	PruneErrors []string `json:"prune_errors"`
	// Kept are the objects the import left as the yard has them, each as
	// "<kind> <name>: <why>" (Object.keptWords): the yard changed them, the
	// pack's version waits for --take, and none of them counts as written.
	Kept []string `json:"kept"`
	// Restart are the running crew members whose role or subscriptions
	// changed: the import does not restart them.
	Restart []string `json:"restart"`
	// Granted are the crew the import let open threads, each with the
	// sources that are new to it.
	Granted []Grant `json:"granted"`
	// Calls are the calls of declared sources the import granted.
	Calls []CallGrant `json:"calls"`
	// Exposed are the crew the import exposed to peers, each with the
	// peers.
	Exposed []Exposed `json:"exposed"`
	// Mapped are the stage maps the import set on a hook of the yard.
	Mapped []MapOp `json:"mapped"`
	// Declared are the sources the import added or replaced.
	Declared []string `json:"declared"`
	// Folds are what the import folded from each declared source's
	// -declared name into the source itself, one per source that had
	// anything under the old name.
	Folds []Fold `json:"folds"`
	// Recorded says the yard's record of the import was written; false when
	// nothing changed.
	Recorded bool `json:"recorded"`
	// FrameMoved says the import moved its scope's frame choice to this
	// pack (the plan's FrameUse), though it may have written no file: an
	// unchanged re-apply of a displaced frame pack is pack_applied with
	// Wrote false, and this is how the result says so.
	FrameMoved bool `json:"frame_moved,omitempty"`
}

// Grant is a crew member and the sources an import let it open threads in.
type Grant struct {
	CrewMember string   `json:"crew"`
	Sources    []string `json:"sources"`
}

// Exposed is a crew member and the peers an import exposed it to.
type Exposed struct {
	CrewMember string   `json:"crew"`
	Peers      []string `json:"peers"`
}

// CallGrant is a group or a crew member and the calls of one source an
// import granted it.
type CallGrant struct {
	store.Grantee
	Source string   `json:"source"`
	Calls  []string `json:"calls"`
}

// Fold is what one apply folded from a -declared name into the source
// the pack brings: the links, and the records MoveSourceRecords moved.
type Fold struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Links int    `json:"links"`
	store.SourceMove
}

// Wrote reports whether the import changed anything in the yard.
func (r *Result) Wrote() bool {
	return r.Created+r.Changed+len(r.Pruned)+len(r.Granted)+len(r.Calls)+len(r.Exposed) > 0
}
