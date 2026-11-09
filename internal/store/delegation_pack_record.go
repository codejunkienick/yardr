// The import record of a delegated pack apply (design yardr-m90n 3.4): the
// one write of the run that is not a plan object, and the one a later
// command reads as the yard's own memory of the pack. A later apply by name
// starts from its source, mappings, flow and answers, a page is dressed
// from its frame, and a later plan takes a file's hash for the file the
// import installed and a group's binding or a map's lines for the pack's
// own, so a caller that reaches RecordPack over the socket under a started
// record must not choose any of them. The start therefore keeps the record
// the store's own reading of the pack says the run leaves
// (Delegation.Import), and the write is held to it field by field, each
// object of it too, none of them left out. The one field the command sets
// that the start can check at once, the source it read the pack from, is
// held there too (startSourceHeld), so that a command and a server that are
// two builds refuse before anything writes and not at this write.
package store

import (
	"fmt"
	"reflect"
	"slices"
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
	held.Source, held.Auth = heldSource(start, reading), ""
	// What the write sets itself, in nobody's reading: the time and actor
	// are the write's own, and the run ask an import is approved on is a
	// carrying out's, which no delegated run is. The objects stay the
	// reading's: packObjectHeld holds each recorded one to them.
	held.At, held.By = time.Time{}, ""
	held.ApprovedBead, held.ApprovedAsk = "", 0
	return &held
}

// heldSource is the origin a delegated pack start holds the run's record of
// its import to: the URL a pack of the packs repository was fetched at,
// else the reading's label, which names the build that embeds the pack. The
// record (heldImport) and the start's own check (startSourceHeld) both take
// it from here, so what the start checks is what the write will hold.
func heldSource(start *DelegatedPackStart, reading *PackReading) string {
	return or(start.From, reading.Label)
}

// startSourceHeld refuses a delegated pack start whose command read the pack
// from a source other than heldSource, before anything writes. The command
// records that source as the import's (pack.Plan.ImportRecord), and the
// write of the record refuses any other (packImportHeld), but only after
// every object was written: the run would read failed over a partial
// import, and applying again would fail the same way until the server ran
// the command's build. A pack yardr embeds is read under a label that names
// the build, so a source that differs from the reading's in the build alone
// is a command and a server of two builds, as in the seconds between an
// install and the server's restart; the refusal names both labels whole,
// each with its build. Any other source is refused as what it is. A start
// that names no source, from a client older than the field, is held as
// before, by the write alone.
func startSourceHeld(start *DelegatedPackStart, reading *PackReading) error {
	held := heldSource(start, reading)
	if start.Source == "" || start.Source == held {
		return nil
	}
	if head := LabelWithoutBuild(reading.Label, reading.Build); start.From == "" && strings.HasPrefix(start.Source, head+" ") {
		return fmt.Errorf("pack %s: the command and the server are two builds: the command read it from %q and the server reads it from %q, and a run across two builds writes the import and then cannot record it: apply again once both are one build: nothing was written", start.Pack, start.Source, held)
	}
	return fmt.Errorf("pack %s: the command read it from %q, and the run is held to %q: nothing was written", start.Pack, start.Source, held)
}

// packImportHeld refuses an import's record imp that is not the one the
// delegated pack record rec holds it to (Delegation.Import), naming the
// first field that differs. Every field of the record is compared but
// the objects, which packObjectHeld holds, and the time and actor, which
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
	name, value, differs := differingField(held, imp)
	switch {
	case !differs:
		return nil
	case name == "source" || name == "auth":
		return fmt.Errorf("pack %s: the delegated pack record %d reads the pack from %s, and its import records no other source and no credential", imp.Pack, rec.ID, or(held.Source, "the binary that runs the yard"))
	}
	return fmt.Errorf("pack %s: the delegated pack record %d records the import's %s as %s, not as this import has it", imp.Pack, rec.ID, name, heldWords(value))
}

// packObjectHeld refuses an object po of the import record of pack that is
// not the one the delegated pack record rec holds it to: the object of the
// reading's record (Delegation.Import) that stands under the same Key,
// compared field by field as packImportHeld compares the rest of the
// record. recordedObject has said the record names po; this holds what a
// later plan reads back of it as the pack's own: a file's or a declared
// source's hash (the next plan takes a match for text no one edited), a
// group's binding, a map's table and its says and makes lines. It holds as
// closely what recordedObject only matches, now that the reading says it:
// the path a file was installed at, a route's group, a pointer's flow, a
// subscription's scope, the crew member a map acts as. A field added to
// PackObject later is held by default, and one the reading carries nothing
// for is held to empty. An object the reading carries none of is refused in
// recordedObject's words. The refusal of a field names what the record
// holds, never the caller's value.
func packObjectHeld(rec *Delegation, pack string, po PackObject) error {
	var held []PackObject
	if rec.Import != nil {
		held = rec.Import.Objects
	}
	key, po := po.Key(), normalObject(po)
	var near *PackObject
	for _, h := range held {
		if h.Key() != key {
			continue
		}
		h = normalObject(h)
		if reflect.DeepEqual(h, po) {
			return nil
		}
		if near == nil {
			near = &h
		}
	}
	if near == nil {
		return fmt.Errorf("pack %s: object %s stands on no delegated pack record %d", pack, objectWords(po), rec.ID)
	}
	name, value, _ := differingField(*near, po)
	return fmt.Errorf("pack %s: the delegated pack record %d records the import's %s with its %s as %s, not as this import has it", pack, rec.ID, objectWords(po), name, heldWords(value))
}

// packObjectsLeftOut refuses an import's record imp that leaves out an object
// the delegated pack record rec holds it to, naming the first. Each object
// imp has is held to the reading's (packObjectHeld), but the objects are a
// memory too, and a file the record does not name is one the next plan finds
// no hash for and takes for a file no one edited, so that leaving it out
// lets the import overwrite a hand edit as a forged hash does. A delegated
// run prunes nothing and is refused no stage map or source (pack.Apply's
// ApplyOptions), so the record it leaves has every object its reading's has.
func packObjectsLeftOut(rec *Delegation, imp PackImport) error {
	if rec.Import == nil {
		return nil
	}
	got := make([]PackObject, len(imp.Objects))
	for i, po := range imp.Objects {
		got[i] = normalObject(po)
	}
	for _, h := range rec.Import.Objects {
		if h = normalObject(h); !slices.ContainsFunc(got, func(po PackObject) bool { return reflect.DeepEqual(po, h) }) {
			return fmt.Errorf("pack %s: the delegated pack record %d records the import with %s, which this import's record leaves out", imp.Pack, rec.ID, objectWords(h))
		}
	}
	return nil
}

// normalObject is an import record's object as two are compared: an empty
// map or list is none, as omitempty keeps it in the store and a caller over
// the socket sends it, and so is the filter of a subscription, a say or a
// make that has no key. What is stored is never changed by it.
func normalObject(po PackObject) PackObject {
	po.Map = nonEmpty(po.Map)
	if po.Subscription != nil {
		s := *po.Subscription
		s.Where = nonEmpty(s.Where)
		po.Subscription = &s
	}
	po.Says = slices.Clone(po.Says)
	for i := range po.Says {
		po.Says[i].Where = nonEmpty(po.Says[i].Where)
	}
	po.Makes = slices.Clone(po.Makes)
	for i := range po.Makes {
		po.Makes[i].Where = nonEmpty(po.Makes[i].Where)
	}
	if len(po.Says) == 0 {
		po.Says = nil
	}
	if len(po.Makes) == 0 {
		po.Makes = nil
	}
	return po
}

// nonEmpty is m, or none where it has no key.
func nonEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

// objectWords is an import record's object as a refusal names it: its kind
// and what finds it again (Key).
func objectWords(po PackObject) string {
	return strings.TrimSpace(strings.ReplaceAll(po.Key(), "\x00", " "))
}

// differingField is the first exported field of held, in the order the type
// declares them, that got has otherwise, by its name in JSON, with the value
// held has for it; differs is false where none does. Both are of one struct
// type.
func differingField(held, got any) (name string, value any, differs bool) {
	hv, gv := reflect.ValueOf(held), reflect.ValueOf(got)
	for i := range hv.NumField() {
		field := hv.Type().Field(i)
		if !field.IsExported() || reflect.DeepEqual(hv.Field(i).Interface(), gv.Field(i).Interface()) {
			continue
		}
		name, _, _ = strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			name = field.Name
		}
		return name, hv.Field(i).Interface(), true
	}
	return "", nil, false
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
