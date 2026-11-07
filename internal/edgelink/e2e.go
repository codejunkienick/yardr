package edgelink

// An enveloped call down the link (docs/design/
// 2026-11-05-yardr-device-e2e.md, section 3.2) names the call it is in
// the clear of its frame's header, so the yard can bind the call to
// the records that must open under it before any of them is opened.
// The header is a claim, never a warrant — what proves a call id is
// that a record sealed under it opens — but the claim is registered
// here, in the link's read loop, in the order frames came and before
// the serving goroutine starts, as each live request's cancel is held
// by its id in the same place. A call id already registered to a live
// frame on this link cannot be registered to a second, so two
// concurrent frames cannot come to share one identity; a frame whose
// claim is taken is refused here, unserved, and marks nothing for the
// honest call that may still arrive under its own frame.

// e2eCallHeader is the frame header that names an enveloped call's
// id, as the router copied the device's X-Edge-E2E-Call there. The
// same words are rpc's (internal/rpc/usere2e.go), which opens the
// envelope; a test holds the two equal.
const e2eCallHeader = "X-Edge-E2E-Call"

// registerE2E registers frame f's enveloped call claim against its
// frame id, and says whether the frame may be served: false when its
// call id is already registered to a live frame on this link. A frame
// that names no call registers nothing and is always served.
func (l *Link) registerE2E(f frameIn) bool {
	call := f.Header.Get(e2eCallHeader)
	if call == "" {
		return true
	}
	l.liveMu.Lock()
	defer l.liveMu.Unlock()
	for _, taken := range l.e2eLive {
		if taken == call {
			return false
		}
	}
	if l.e2eLive == nil {
		l.e2eLive = map[string]string{}
	}
	l.e2eLive[f.ID] = call
	return true
}

// registeredE2E is the enveloped call id registered to the frame id,
// "" for a frame that named none: the value serve registered before
// the frame's goroutine started, which request hands the handler in
// place of the frame header's own copy.
func (l *Link) registeredE2E(id string) string {
	l.liveMu.Lock()
	defer l.liveMu.Unlock()
	return l.e2eLive[id]
}
