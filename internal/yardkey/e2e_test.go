package yardkey

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/flynn/noise"
)

// channelPair is a device and yard that have handshaken: their two
// sessions, the device's first and the yard's.
func channelPair(t *testing.T) (device, yard *ChannelSession, deviceKey ChannelKey) {
	t.Helper()
	yardKey, err := GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	deviceKey, err = GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	first, opening, err := StartChannelHandshake(deviceKey, yardKey.Public)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenChannelHandshake(yardKey, first)
	if err != nil {
		t.Fatal(err)
	}
	if opened.DeviceKey != ChannelPublic(deviceKey) {
		t.Fatalf("the handshake proved device %q, want %q", opened.DeviceKey, ChannelPublic(deviceKey))
	}
	tag, err := NewSessionTag()
	if err != nil {
		t.Fatal(err)
	}
	reply, yardSession, err := opened.SealAnswer(tag)
	if err != nil {
		t.Fatal(err)
	}
	deviceSession, err := opening.OpenAnswer(reply)
	if err != nil {
		t.Fatal(err)
	}
	if string(deviceSession.Tag()) != string(tag) || string(yardSession.Tag()) != string(tag) {
		t.Fatal("the two sessions name different tags")
	}
	return deviceSession, yardSession, deviceKey
}

var e2eFirst = &FirstBinding{Path: "/user/e2e"}

func callID(t *testing.T) []byte {
	t.Helper()
	id, err := NewCallID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// A handshake founds a session a whole call crosses in both directions,
// chopped into records where the body needs several, and a message
// reflected into the other direction, carried to another call, or
// carried to another session opens nowhere.
func TestChannelWholeCallNeedsSeveralRecords(t *testing.T) {
	device, yard, _ := channelPair(t)
	call := callID(t)
	plain := bytes.Repeat([]byte("paint the shed; "), MaxRecordPlain/16+3)
	// Well past two records of plaintext.
	for len(plain) < 2*MaxRecordPlain+100 {
		plain = append(plain, plain...)
	}
	run, err := device.SealWhole(call, e2eFirst, plain)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := SplitRecords(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) < 3 {
		t.Fatalf("a body of %d bytes sealed into %d records, want several", len(plain), len(msgs))
	}
	for _, m := range msgs {
		if len(m) > 65535 {
			t.Fatalf("a transport message of %d bytes passes the Noise bound", len(m))
		}
	}
	got, err := yard.OpenWhole(run, call, e2eFirst)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("the yard opened %d bytes, %v", len(got), err)
	}
	back, err := yard.SealWhole(call, e2eFirst, []byte("seen"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := device.OpenWhole(back, call, e2eFirst); err != nil || string(got) != "seen" {
		t.Fatalf("the device opened %q, %v", got, err)
	}

	// A run with no final record is truncated, and one with a record
	// dropped from its middle is refused at the gap: neither answers
	// any of the plaintext as whole. Each variant is sealed fresh,
	// under its own call: the run above has opened, and its records
	// replay nowhere.
	join := func(ms [][]byte) []byte {
		var out []byte
		for _, m := range ms {
			out = FrameRecord(out, m)
		}
		return out
	}
	fresh := func() ([][]byte, []byte) {
		id := callID(t)
		r, err := device.SealWhole(id, e2eFirst, plain)
		if err != nil {
			t.Fatal(err)
		}
		ms, err := SplitRecords(r)
		if err != nil || len(ms) < 3 {
			t.Fatalf("records = %d, %v", len(ms), err)
		}
		return ms, id
	}
	{
		ms, id := fresh()
		if _, err := yard.OpenWhole(join(ms[:len(ms)-1]), id, e2eFirst); !errors.Is(err, ErrTruncated) {
			t.Errorf("a run with no final record opened whole: %v", err)
		}
	}
	{
		ms, id := fresh()
		mid := append([][]byte{ms[0]}, ms[2:]...)
		if _, err := yard.OpenWhole(join(mid), id, e2eFirst); !errors.Is(err, ErrRecordGap) {
			t.Errorf("a run with its middle record dropped opened whole: %v", err)
		}
	}

	one, err := device.Seal(call, 0, e2eFirst, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := device.Open(one, call, e2eFirst); err == nil {
		t.Error("a request record opened in the answer direction")
	}
	if _, _, err := yard.Open(one, callID(t), e2eFirst); err == nil {
		t.Error("a record opened for another call than its own")
	}
	_, otherYard, _ := channelPair(t)
	if _, _, err := otherYard.Open(one, call, e2eFirst); err == nil {
		t.Error("a record opened in another session")
	}
}

// Concurrent messages of one session seal and open in any order: each
// opens with its own copied cipher state, under the number it carries,
// and none waits on another's nonce.
func TestChannelMessagesOpenOutOfOrder(t *testing.T) {
	device, yard, _ := channelPair(t)
	const n = 32
	msgs := make([][]byte, n)
	var wg sync.WaitGroup
	for i := range msgs {
		wg.Go(func() {
			call := append([]byte(nil), byte(i))
			for len(call) < E2ECallSize {
				call = append(call, 0)
			}
			m, err := device.Seal(call, 0, e2eFirst, []byte{byte(i)})
			if err != nil {
				t.Error(err)
				return
			}
			msgs[i] = m
		})
	}
	wg.Wait()
	for i := n - 1; i >= 0; i-- {
		call := append([]byte(nil), byte(i))
		for len(call) < E2ECallSize {
			call = append(call, 0)
		}
		_, plain, err := yard.Open(msgs[i], call, e2eFirst)
		if err != nil || len(plain) != 1 || plain[0] != byte(i) {
			t.Errorf("message %d opened %v, %v", i, plain, err)
		}
	}

	// The last Noise nonce is usable once. Further seals must keep
	// refusing instead of wrapping the allocator back to nonce zero.
	device.next.Store(noise.MaxNonce)
	last, err := device.Seal(callID(t), 0, e2eFirst, []byte("last nonce"))
	if err != nil {
		t.Fatalf("the last nonce = %v", err)
	}
	if got := binary.BigEndian.Uint64(last[e2eHeaderSize-16 : e2eHeaderSize-8]); got != noise.MaxNonce {
		t.Fatalf("the last message used nonce %d, want %d", got, noise.MaxNonce)
	}
	for i := range 2 {
		if _, err := device.Seal(callID(t), 0, e2eFirst, []byte("after exhaustion")); !errors.Is(err, noise.ErrMaxNonce) {
			t.Fatalf("seal after exhaustion %d = %v, want ErrMaxNonce", i, err)
		}
	}
	if got := device.next.Load(); got != noise.MaxNonce+1 {
		t.Fatalf("the exhausted nonce allocator wrapped to %d", got)
	}
}

// A first record whose outer path or stream marker was rewritten
// between the device and the yard fails its opening: the device sealed
// what it sent, and the yard checks what the frame carried.
func TestChannelFirstRecordBindsPathAndMarker(t *testing.T) {
	device, yard, _ := channelPair(t)
	call := callID(t)
	sent := &FirstBinding{Path: "/user/e2e", Marker: "long-lived", Present: true}
	run, err := device.SealWhole(call, sent, []byte("the events stream, please"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := yard.OpenWhole(run, call, sent); err != nil {
		t.Fatalf("an untouched first record = %v", err)
	}
	for name, carried := range map[string]*FirstBinding{
		"path rewritten":   {Path: "/user/call/Head"},
		"marker dropped":   {Path: "/user/e2e"},
		"marker added":     {Path: "/user/e2e", Marker: "long-lived", Present: true},
		"marker rewritten": {Path: "/user/e2e", Marker: "short", Present: true},
	} {
		sentPlain := []byte("a call")
		sentBind := sent
		if name == "marker added" {
			sentBind = &FirstBinding{Path: "/user/e2e"}
		}
		id := callID(t)
		run, err := device.SealWhole(id, sentBind, sentPlain)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := yard.OpenWhole(run, id, carried); err == nil {
			t.Errorf("%s: the first record opened", name)
		}
	}
	// A later record rewritten in its clear header fails too.
	msgs, err := SplitRecords(run)
	if err != nil {
		t.Fatal(err)
	}
	_ = msgs
	plain := bytes.Repeat([]byte("y"), MaxRecordPlain+10)
	id := callID(t)
	run, err = device.SealWhole(id, e2eFirst, plain)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err = SplitRecords(run)
	if err != nil || len(msgs) < 2 {
		t.Fatalf("records = %d, %v", len(msgs), err)
	}
	msgs[1][0] ^= 1
	var tampered []byte
	for _, m := range msgs {
		tampered = FrameRecord(tampered, m)
	}
	if _, err := yard.OpenWhole(tampered, id, e2eFirst); err == nil {
		t.Error("a record with a rewritten tag opened")
	}
	if _, err := SplitRecords(append(run, 1, 2)); err == nil {
		t.Error("a run with bytes left over split")
	}
}

// A streamed call opens record by record as its pieces cross: the
// records before a dropped one stand delivered and the stream is
// refused at the gap, an outer ending with no final record is a
// truncation, and a zero-length beat record — forged, dropped or
// replayed — is read past before any opening and alters no inner
// byte. Two streams of one session seal and open interleaved, each
// under its own call's indexes.
func TestChannelStreamRecordsGapTruncationAndBeats(t *testing.T) {
	device, yard, _ := channelPair(t)
	call, other := callID(t), callID(t)

	seal := func(c *CallSealer, chunk string) []byte {
		t.Helper()
		run, err := c.Seal([]byte(chunk))
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	messages := func(run []byte) [][]byte {
		t.Helper()
		msgs, err := SplitRecords(run)
		if err != nil {
			t.Fatal(err)
		}
		return msgs
	}

	// Interleaved streams, beats between their records: a beat is
	// four zero bytes a reader drops before any opening.
	sealer, otherSealer := device.NewCallSealer(call, e2eFirst), device.NewCallSealer(other, e2eFirst)
	beat := FrameRecord(nil, nil)
	var wire []byte
	wire = append(wire, seal(sealer, "one\n")...)
	wire = append(wire, beat...)
	wire = append(wire, seal(otherSealer, "first\n")...)
	wire = append(wire, seal(sealer, "two\n")...)
	wire = append(wire, beat...)
	wire = append(wire, beat...)
	wire = append(wire, seal(otherSealer, "second\n")...)
	for _, s := range []*CallSealer{sealer, otherSealer} {
		final, err := s.SealFinal()
		if err != nil {
			t.Fatal(err)
		}
		wire = append(wire, final...)
	}
	reader := NewRecordReader(bytes.NewReader(wire))
	openers := map[string]*CallOpener{
		string(call):  yard.NewCallOpener(call, e2eFirst),
		string(other): yard.NewCallOpener(other, e2eFirst),
	}
	got := map[string][]byte{}
	for {
		msg, err := reader.ReadMessage()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		// The message's own call names its opener, as a device knows
		// the call its answer belongs to.
		plain, final, err := openers[string(msg[E2ETagSize:E2ETagSize+E2ECallSize])].Open(msg)
		if err != nil {
			t.Fatal(err)
		}
		if !final {
			key := string(msg[E2ETagSize : E2ETagSize+E2ECallSize])
			got[key] = append(got[key], plain...)
		}
	}
	if string(got[string(call)]) != "one\ntwo\n" || string(got[string(other)]) != "first\nsecond\n" {
		t.Errorf("the streams opened %q and %q", got[string(call)], got[string(other)])
	}
	for _, o := range openers {
		if err := o.Close(); err != nil {
			t.Errorf("a closed stream = %v", err)
		}
	}

	// A record dropped from the middle: what stands before the gap is
	// delivered, the record past it is refused there, and the honest
	// record still opens when it comes. A fresh call: the streams
	// above have closed theirs, and a closed call is never reused.
	gapCall := callID(t)
	sealer = device.NewCallSealer(gapCall, e2eFirst)
	var chunks [][]byte
	for _, chunk := range []string{"a\n", "b\n", "c\n"} {
		chunks = append(chunks, messages(seal(sealer, chunk))...)
	}
	opener := yard.NewCallOpener(gapCall, e2eFirst)
	if plain, _, err := opener.Open(chunks[0]); err != nil || string(plain) != "a\n" {
		t.Fatalf("the first record opened %q, %v", plain, err)
	}
	if _, _, err := opener.Open(chunks[2]); !errors.Is(err, ErrRecordGap) {
		t.Fatalf("the record past the gap = %v, want the gap refused", err)
	}
	if plain, _, err := opener.Open(chunks[1]); err != nil || string(plain) != "b\n" {
		t.Fatalf("the honest record after a refused one opened %q, %v", plain, err)
	}

	// No final record before the outer ending: a truncation, and the
	// part is no whole.
	if err := opener.Close(); !errors.Is(err, ErrTruncated) {
		t.Errorf("an outer ending with no final record = %v", err)
	}
	truncated := NewRecordReader(bytes.NewReader(append(seal(sealer, "x\n"), 0, 0)))
	if _, err := truncated.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	if _, err := truncated.ReadMessage(); !errors.Is(err, ErrTruncated) {
		t.Errorf("a run ending inside a length = %v", err)
	}

	// Data that would itself read as a final record is split by the
	// sealer and delivered as data: sixteen zero bytes open whole,
	// and the final record after them still closes the call.
	finalCall := callID(t)
	sealer = device.NewCallSealer(finalCall, e2eFirst)
	run, err := sealer.Seal(finalPlain(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	final, err := sealer.SealFinal()
	if err != nil {
		t.Fatal(err)
	}
	opener = yard.NewCallOpener(finalCall, e2eFirst)
	var data []byte
	for _, msg := range messages(append(run, final...)) {
		plain, done, err := opener.Open(msg)
		if err != nil {
			t.Fatal(err)
		}
		if done && len(data) != 16 {
			t.Fatalf("the call closed after %d data bytes, want its 16 first", len(data))
		}
		data = append(data, plain...)
	}
	if !bytes.Equal(data, finalPlain(0, 0)) || opener.Close() != nil {
		t.Errorf("final-shaped data opened as %v, close %v", data, opener.Close())
	}
}

// The replay rule of section 3.2: a record replayed whole, a record
// with its sequence rewritten, and a record fallen below the window
// are each refused and mark nothing for the honest message after
// them; a record carried to another concurrent call is refused there
// and still opens on its own, and a closed call's records are refused
// however fresh their sequence looks, neither refusal corrupting the
// other call; a record withheld past the window fails its call whole,
// and the call reissued under a new call id opens.
func TestChannelReplayAndCrossStream(t *testing.T) {
	device, yard, _ := channelPair(t)

	// Replayed whole.
	call := callID(t)
	run, err := device.SealWhole(call, e2eFirst, []byte("paint the shed"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := yard.OpenWhole(run, call, e2eFirst); err != nil {
		t.Fatal(err)
	}
	if _, err := yard.OpenWhole(run, call, e2eFirst); !errors.Is(err, ErrReplay) {
		t.Errorf("a record replayed whole = %v, want the replay refused", err)
	}
	honest := callID(t)
	run, err = device.SealWhole(honest, e2eFirst, []byte("paint the shed"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := yard.OpenWhole(run, honest, e2eFirst); err != nil || string(got) != "paint the shed" {
		t.Errorf("the honest call after a replay opened %q, %v", got, err)
	}

	// Sequence rewritten: refused at its opening, the honest record
	// carrying that sequence still opens after it.
	rwCall := callID(t)
	sealer := device.NewCallSealer(rwCall, e2eFirst)
	recRun, err := sealer.Seal([]byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	recs, err := SplitRecords(recRun)
	if err != nil || len(recs) != 1 {
		t.Fatalf("records = %d, %v", len(recs), err)
	}
	rewritten := append([]byte(nil), recs[0]...)
	binary.BigEndian.PutUint64(rewritten[e2eHeaderSize-16:e2eHeaderSize-8],
		binary.BigEndian.Uint64(rewritten[e2eHeaderSize-16:e2eHeaderSize-8])+1000)
	opener := yard.NewCallOpener(rwCall, e2eFirst)
	if _, _, err := opener.Open(rewritten); err == nil {
		t.Error("a record with its sequence rewritten opened")
	}
	if plain, _, err := opener.Open(recs[0]); err != nil || string(plain) != "first" {
		t.Errorf("the honest record after a rewritten one opened %q, %v", plain, err)
	}

	// Cross-stream: a record of stream A delivered to stream B before
	// A has opened it is refused there and still opens on A; after A
	// closes, its records are refused; B is uncorrupted throughout.
	aCall, bCall := callID(t), callID(t)
	aSeal, bSeal := device.NewCallSealer(aCall, e2eFirst), device.NewCallSealer(bCall, e2eFirst)
	aRun, err := aSeal.Seal([]byte("a1"))
	if err != nil {
		t.Fatal(err)
	}
	bRun, err := bSeal.Seal([]byte("b1"))
	if err != nil {
		t.Fatal(err)
	}
	aRec, bRec := messages0(t, aRun), messages0(t, bRun)
	aOpen, bOpen := yard.NewCallOpener(aCall, e2eFirst), yard.NewCallOpener(bCall, e2eFirst)
	if _, _, err := bOpen.Open(aRec); err == nil {
		t.Error("a record of stream A opened on stream B")
	}
	if plain, _, err := aOpen.Open(aRec); err != nil || string(plain) != "a1" {
		t.Errorf("the record on its own stream opened %q, %v", plain, err)
	}
	aFin, err := aSeal.SealFinal()
	if err != nil {
		t.Fatal(err)
	}
	if finMsgs, err := SplitRecords(aFin); err != nil {
		t.Fatal(err)
	} else {
		for _, m := range finMsgs {
			if _, _, err := aOpen.Open(m); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := aOpen.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := yard.NewCallOpener(aCall, e2eFirst).Open(aRec); !errors.Is(err, ErrReplay) {
		t.Errorf("a record replayed after its stream closed = %v, want the replay refused", err)
	}
	late, err := device.Seal(aCall, 0, e2eFirst, []byte("late"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := yard.NewCallOpener(aCall, e2eFirst).Open(late); !errors.Is(err, ErrReplay) {
		t.Errorf("a fresh record of a closed call = %v, want it refused", err)
	}
	if plain, _, err := bOpen.Open(bRec); err != nil || string(plain) != "b1" {
		t.Errorf("stream B after A's refusals opened %q, %v", plain, err)
	}
	bFin, err := bSeal.SealFinal()
	if err != nil {
		t.Fatal(err)
	}
	if finMsgs, err := SplitRecords(bFin); err != nil {
		t.Fatal(err)
	} else {
		for _, m := range finMsgs {
			if _, _, err := bOpen.Open(m); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := bOpen.Close(); err != nil {
		t.Errorf("stream B closed with %v", err)
	}

	// Withheld past the window, on a fresh session pair so the count
	// is exact: the withheld call fails whole, and the same call under
	// a new call id opens.
	device2, yard2, _ := channelPair(t)
	heldCall := callID(t)
	held, err := device2.SealWhole(heldCall, e2eFirst, []byte("held back"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range ReplayWindow {
		dummy := make([]byte, E2ECallSize)
		binary.BigEndian.PutUint64(dummy[8:], uint64(i)+1)
		msg, err := device2.Seal(dummy, 0, e2eFirst, []byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := yard2.Open(msg, dummy, e2eFirst); err != nil {
			t.Fatalf("a window-filling record %d = %v", i, err)
		}
	}
	if _, err := yard2.OpenWhole(held, heldCall, e2eFirst); !errors.Is(err, ErrReplay) {
		t.Errorf("a record withheld past the window = %v, want the replay refused", err)
	}
	again := callID(t)
	run, err = device2.SealWhole(again, e2eFirst, []byte("held back"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := yard2.OpenWhole(run, again, e2eFirst); err != nil || string(got) != "held back" {
		t.Errorf("the call reissued under a new id opened %q, %v", got, err)
	}
}

func messages0(t *testing.T, run []byte) []byte {
	t.Helper()
	msgs, err := SplitRecords(run)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("records = %d, %v", len(msgs), err)
	}
	return msgs[0]
}
