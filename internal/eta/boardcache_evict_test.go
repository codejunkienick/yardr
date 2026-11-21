package eta

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Past the cap of kept boards the stalest head and minute are dropped,
// however many distinct viewers computed: the cache holds 32, a dropped
// key asked again is computed whole, and a kept key is still reused.
func TestBoardCacheEvictsStalestPastCap(t *testing.T) {
	t.Parallel()
	var c boardCache[string]
	ctx := context.Background()
	base := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	computed := map[int]int{}
	clone := func(s string) string { return s }
	key := func(i int) boardKey {
		return boardKey{viewer: fmt.Sprintf("viewer-%d", i), head: uint64(100 + i), minute: base.Add(time.Duration(i) * time.Minute)}
	}
	for i := 0; i < 40; i++ {
		compute := func() (string, error) {
			computed[i]++
			return fmt.Sprintf("board %d", i), nil
		}
		if _, err := c.do(ctx, key(i), compute, clone); err != nil {
			t.Fatal(err)
		}
	}
	c.mu.Lock()
	kept, missing := len(c.kept), 0
	for i := 0; i < 40; i++ {
		_, ok := c.kept[key(i)]
		if i < 8 && ok {
			t.Errorf("key %d is kept, want the eight stalest dropped", i)
		}
		if i >= 8 && !ok {
			missing++
		}
	}
	c.mu.Unlock()
	if kept != 32 || missing != 0 {
		t.Fatalf("the cache keeps %d boards with %d of keys 8..39 missing, want 32 kept whole", kept, missing)
	}
	// A kept key is reused without computing; the stalest dropped key
	// asked again is computed whole (and, its head and minute still the
	// stalest, is dropped again for the keeping).
	if _, err := c.do(ctx, key(39), func() (string, error) {
		computed[39]++
		return "recomputed", nil
	}, clone); err != nil {
		t.Fatal(err)
	}
	if computed[39] != 1 {
		t.Errorf("key 39 computed %d times, want 1 (reused)", computed[39])
	}
	got, err := c.do(ctx, key(0), func() (string, error) {
		computed[0]++
		return "board 0", nil
	}, clone)
	if err != nil {
		t.Fatal(err)
	}
	if computed[0] != 2 || got != "board 0" {
		t.Errorf("key 0 asked again computed %d times with %q, want 2 and the whole board", computed[0], got)
	}
}
