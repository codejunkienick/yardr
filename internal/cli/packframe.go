package cli

import (
	"fmt"

	"yardr.dev/yardr/internal/pack"
)

// appliedLine is what an import that wrote no file says of itself: nothing
// changed, unless the import moved its scope's frame choice to this pack
// (an unchanged re-apply of a displaced frame pack), which the record
// wrote and the plan named in its FrameUse line.
func appliedLine(plan *pack.Plan, res *pack.Result) string {
	if res.FrameMoved && plan.FrameUse != "" {
		return fmt.Sprintf("pack %s is already applied to %s: no file changed; %s", res.Pack, inDepot(res.Depot), plan.FrameUse)
	}
	return fmt.Sprintf("pack %s is already applied to %s: nothing changed", res.Pack, inDepot(res.Depot))
}
