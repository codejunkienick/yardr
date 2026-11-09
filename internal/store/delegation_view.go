package store

// DelegationView is a delegated record as an operator reads it in JSON, from
// yardr delegation show --json and from the record's page: the record whole,
// its body merged in from the blob, and under plan the body's objects with
// their diffs and before-states. The merged Delegation alone would print
// them by name only: its Objects and the body's share the JSON name
// "objects", and the shallower wins (DelegatedBody). The names stay under
// objects, so nothing that reads them changes. The text and the HTML read the
// body's objects in Go and need no view.
//
// It is an output and nothing else: no record is stored or served as one, and
// a MarshalJSON on Delegation would have fired on every put and rpc encode of
// a merged record too.
type DelegationView struct {
	Delegation
	// Plan is a delegated pack apply's objects whole, as the record's body
	// holds them and Store.DelegationBlob serves them: what the import does
	// to each, the diff of an updated one, the complete before-state and
	// whether one stood. Absent on a record that names no pack.
	Plan []DelegatedPackObject `json:"plan,omitempty"`
}

// ViewDelegation is rec as an operator reads it in JSON. rec has its body
// merged in where it stands in its blob (rec.DelegatedBody = *body), as the
// readers of Store.Delegation do for a record that has one; the view copies
// the record and changes nothing in it.
func ViewDelegation(rec *Delegation) *DelegationView {
	return &DelegationView{Delegation: *rec, Plan: rec.DelegatedBody.Objects}
}
