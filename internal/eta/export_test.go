package eta

import (
	"maps"

	"yardr.dev/yardr/internal/store"
)

// ForgetBoards drops the boards the two board caches keep for the yard st.
// The caches live as long as the test binary, so a board a test leaves
// behind is still there for the tests after it and for the next run of the
// same test under -count. It takes one of the cap's slots, and evict then
// drops a board that a later test counts on, so that test's next read lists
// beads again. A test that asks the boards of a yard of its own calls
// ForgetBoards at cleanup. It leaves a computation in flight alone, since
// that computation's caller removes it and a test's calls have all returned
// by cleanup.
func ForgetBoards(st store.API) {
	arrivalBoards.mu.Lock()
	maps.DeleteFunc(arrivalBoards.kept, func(k boardKey, _ *ArrivalBoard) bool { return k.st == st })
	arrivalBoards.mu.Unlock()
	flowBoards.mu.Lock()
	maps.DeleteFunc(flowBoards.kept, func(k boardKey, _ *store.FlowsBoard) bool { return k.st == st })
	flowBoards.mu.Unlock()
}
