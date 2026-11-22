package rpc

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Several devices call in one minute, one down the edge's link and one
// over the socket: on the first call past the boundary the meter logs one
// line with the finished minute's calls per method and device, and the
// socket's call is absent from it. Idle minutes log nothing, and only the
// current minute's bucket is kept.
func TestDeviceCallsCountedByMethodAndDevice(t *testing.T) {
	y := newSecretYard(t)
	now := time.Date(2026, 11, 21, 12, 0, 30, 0, time.UTC)
	y.u.api.meter.now = func() time.Time { return now }
	// tables is the minutes= value of each minute line the meter logged.
	// The checks read it, never the whole line: the line begins with the
	// log's own clock, which is the real one and not the meter's, so a
	// check for a minute (12:02) over the line would hold or fail by the
	// time the suite runs.
	tables := func() []string {
		var out []string
		for line := range strings.Lines(y.log.String()) {
			if !strings.Contains(line, "device calls served") {
				continue
			}
			_, table, ok := strings.Cut(line, "minutes=")
			if !ok {
				t.Fatalf("a minute line has no minutes= value:\n%s", line)
			}
			out = append(out, table)
		}
		return out
	}

	call(t, y.h, y.jo, "Bead", `{"bead":"yd-crm"}`)
	call(t, y.h, y.jo, "Bead", `{"bead":"yd-crm"}`)
	call(t, y.h, y.sam, "UserAsks", `{}`)
	edgeCall(t, y.h, y.jo, "UserAsks", `{}`)
	// The socket's call is served and not counted: the table holds
	// jo's two Bead calls and no third.
	sock := handler(y.st, y.u.log, StreamWriteTimeout)
	req := httptest.NewRequest(http.MethodPost, "/call/Bead", strings.NewReader(`["yd-crm"]`))
	w := httptest.NewRecorder()
	sock.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("socket Bead: status %d: %s", w.Code, w.Body)
	}
	if got := tables(); len(got) != 0 {
		t.Fatalf("minute lines within the minute: %q", got)
	}

	now = now.Add(61 * time.Second)
	call(t, y.h, y.sam, "Bead", `{"bead":"yd-crm"}`)
	got := tables()
	if len(got) != 1 {
		t.Fatalf("minute lines past the boundary: %d, want 1:\n%s", len(got), y.log.String())
	}
	for _, want := range []string{"12:00", "Bead phone=2", "UserAsks phone via edge=1", "UserAsks tablet=1"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("the minutes= value lacks %q:\n%s", want, got[0])
		}
	}
	y.u.api.meter.mu.Lock()
	n := len(y.u.api.meter.buckets)
	y.u.api.meter.mu.Unlock()
	if n != 1 {
		t.Errorf("buckets kept after the flush: %d, want the current minute's alone", n)
	}

	// Four idle minutes pass: the next call logs the one minute that
	// held a call, and nothing for the idle ones.
	now = now.Add(5 * time.Minute)
	call(t, y.h, y.jo, "Bead", `{"bead":"yd-crm"}`)
	got = tables()
	if len(got) != 2 {
		t.Fatalf("minute lines after idle minutes: %d, want 2:\n%s", len(got), y.log.String())
	}
	if !strings.Contains(got[1], "12:01 Bead tablet=1") {
		t.Errorf("the second minutes= value names no 12:01 Bead tablet=1:\n%s", got[1])
	}
	for _, idle := range []string{"12:02", "12:03", "12:04", "12:05"} {
		if strings.Contains(got[1], idle) {
			t.Errorf("the second minutes= value names idle %s:\n%s", idle, got[1])
		}
	}
}

// Counting costs a bucket lookup under one mutex: beside a served call,
// which resolved the device's token through the store and invoked the
// method, it is noise. The note of the change carries the numbers.
func BenchmarkCallMeterCount(b *testing.B) {
	m := newCallMeter(func() *slog.Logger { return nil })
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.count("UserAsks", "phone", i%2 == 0)
	}
}

func BenchmarkDeviceCallServed(b *testing.B) {
	y := newSecretYard(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		call(b, y.h, y.jo, "Bead", `{"bead":"yd-crm"}`)
	}
}
