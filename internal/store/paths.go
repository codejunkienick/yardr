package store

import "path/filepath"

// FlowsDir is where home keeps the stage files of flows that no depot's
// repository holds, a directory for each flow.
func FlowsDir(home string) string { return filepath.Join(home, "flows") }

// TelemetryState is where the yard in home keeps the install id and its
// consent bookkeeping (internal/telemetry).
func TelemetryState(home string) string { return filepath.Join(home, "telemetry.json") }

// ReleaseCheckState is where the yard in home keeps what the release
// check last did: the version it last mailed of and the day it last
// asked (internal/dispatch).
func ReleaseCheckState(home string) string { return filepath.Join(home, "releasecheck.json") }
