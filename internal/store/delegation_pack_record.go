// The import record of a delegated pack apply (design yardr-m90n 3.4): the
// one write of the run that is not a plan object, and the one a later
// command reads as the yard's own memory of the pack. A later apply by name
// starts from its source, mappings, flow and answers, and a page is dressed
// from its frame, so a caller that reaches RecordPack over the socket under
// a started record must not choose any of them. The start therefore keeps
// the record the store's own reading of the pack says the run leaves
// (Delegation.Import), and the write is held to it field by field.
package store

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// heldImport is the record a delegated pack start holds the run's own
// record of its import to: the reading's (PackReading.Import, empty where
// the reader carries none), with the origin the run reads the pack from. A
// pack of the packs repository is read from the URL the start fetched it
// at, and a pack yardr embeds from the build the reading names, as a later
// apply by name reads it again; a delegated run takes no credential. The
// reading's own Source and Auth are not taken, for the origin is the
// start's alone.
func heldImport(start *DelegatedPackStart, reading *PackReading) *PackImport {
	var held PackImport
	if reading.Import != nil {
		held = *reading.Import
	}
	held.Pack, held.Depot = start.Pack, start.Depot
	held.Source, held.Auth = or(start.From, reading.Label), ""
	// What the write holds apart or sets itself, in nobody's reading: the
	// objects are the plan's (recordedObject), the time and actor the
	// write's own, and the run ask an import is approved on is a carrying
	// out's, which no delegated run is.
	held.Objects, held.At, held.By = nil, time.Time{}, ""
	held.ApprovedBead, held.ApprovedAsk = "", 0
	return &held
}

// packImportHeld refuses an import's record imp that is not the one the
// delegated pack record rec holds it to (Delegation.Import), naming the
// first field that differs. Every field of the record is compared but
// the objects, which recordedObject holds, and the time and actor, which
// are the write's own, so a field added to PackImport later is held by
// default and a legitimate apply that sets it fails its test until the
// reading says it too. imp is held as RecordPack normalises it, so a
// field the caller leaves out and one it leaves empty are the same. The
// refusal names what the record holds, never the caller's value.
func packImportHeld(rec *Delegation, imp PackImport) error {
	var held PackImport
	if rec.Import != nil {
		held = *rec.Import
	}
	held, imp = normalPack(held), normalPack(imp)
	held.Objects, imp.Objects = nil, nil
	held.At, held.By, imp.At, imp.By = time.Time{}, "", time.Time{}, ""
	hv, iv := reflect.ValueOf(held), reflect.ValueOf(imp)
	for i := range hv.NumField() {
		field := hv.Type().Field(i)
		if !field.IsExported() || reflect.DeepEqual(hv.Field(i).Interface(), iv.Field(i).Interface()) {
			continue
		}
		if field.Name == "Source" || field.Name == "Auth" {
			return fmt.Errorf("pack %s: the delegated pack record %d reads the pack from %s, and its import records no other source and no credential", imp.Pack, rec.ID, or(held.Source, "the binary that runs the yard"))
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			name = field.Name
		}
		return fmt.Errorf("pack %s: the delegated pack record %d records the import's %s as %s, not as this import has it", imp.Pack, rec.ID, name, heldWords(hv.Field(i).Interface()))
	}
	return nil
}

// heldWords is a held field's value as a refusal says it: a string as it
// stands, a flag as true or false, a mapping or answers as JSON, and none
// for what is empty, cut short.
func heldWords(v any) string {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Bool {
		return fmt.Sprint(v)
	}
	if rv.IsZero() || (rv.Kind() == reflect.Map && rv.Len() == 0) {
		return "none"
	}
	return clip(valueWords(v), 200)
}
