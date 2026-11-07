package yardkey

import (
	"bytes"
	"encoding/binary"
	"errors"
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
		run, err := device.SealWhole(call, sentBind, sentPlain)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := yard.OpenWhole(run, call, carried); err == nil {
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
	run, err = device.SealWhole(call, e2eFirst, plain)
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
	if _, err := yard.OpenWhole(tampered, call, e2eFirst); err == nil {
		t.Error("a record with a rewritten tag opened")
	}
	if _, err := SplitRecords(append(run, 1, 2)); err == nil {
		t.Error("a run with bytes left over split")
	}
}
