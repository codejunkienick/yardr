package yardkey

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync/atomic"

	"github.com/flynn/noise"
)

// A device and its yard speak over one channel session once the device
// has paired (docs/design/2026-11-05-yardr-device-e2e.md, section 3.2,
// candidate 2B): the device opens a Noise_IK handshake to the yard's
// pinned channel key, the yard mints a random sixteen-byte tag that
// names the session in the clear of every later message, and each call
// the device makes is a whole inner call — method, path, headers and
// body — sealed inside one outer call on the fixed path /user/e2e. The
// channel key, proved by the handshake's static, authenticates every
// message of the session; no device token crosses on such a call.
//
// Transport messages do not ride the handshake's counters. The yard
// serves up to 64 of a link's calls at once, in goroutines of their
// own, in no sealing order, so each message carries its number with
// it, as Noise section 11.4 prescribes: to seal, the sender takes the
// session's next sequence, copies its sending key into a fresh
// CipherState at that nonce and seals once; to open, the receiver does
// the same with its receiving key and the number the message carries.
// After the handshake the two keys are never mutated, only copied, so
// no call waits on another. The clear header of a message is the
// session tag, the call id the device minted for the call, the
// sequence and the record index of the call in its direction; those
// bytes, a direction byte, and — on a call's first record in a
// direction — the outer path and the outer stream marker the sender
// sent, are the AEAD's associated data, so none of them can be
// rewritten or swapped between sessions, calls or directions without
// the opening failing.

const (
	// E2ETagSize is the session tag's size: sixteen random bytes the
	// handshake mints, named in the clear of every transport message.
	E2ETagSize = 16
	// E2ECallSize is a call id's size: sixteen random bytes the device
	// mints once for each call it opens, never reused in a session.
	E2ECallSize = 16
	// e2eHeaderSize is a transport message's clear header: the tag, the
	// call id, the sequence and the record index.
	e2eHeaderSize = E2ETagSize + E2ECallSize + 8 + 8
	// MaxRecordPlain is the most plaintext one transport message
	// carries: the Noise bound of 65,535 bytes, its clear header and
	// its AEAD tag counted, as the design has it.
	MaxRecordPlain = noise.MaxMsgLen - e2eHeaderSize - 16
)

// The directions a message travels, as the associated data names them.
const (
	dirRequest byte = 0 // device to yard
	dirAnswer  byte = 1 // yard to device
)

// FirstBinding is what a call's first record in a direction binds
// beside the clear header: the fixed outer path both ends know without
// it crossing, and the outer stream marker's presence and value as the
// sender sent them (the device) or as the link frame carried them (the
// yard). A first record whose outer path or marker was rewritten
// between the device and the yard fails its opening, and its call with
// it, rather than silently reclassifying the call.
type FirstBinding struct {
	Path    string
	Marker  string
	Present bool
}

// ChannelSession is one device-to-yard channel after its handshake:
// the two transport keys, never mutated, and the sender's sequence
// allocator. The zero value is no session; one is made by a handshake
// (StartChannelHandshake/OpenChannelHandshake) alone.
type ChannelSession struct {
	tag     []byte
	sendKey [32]byte
	recvKey [32]byte
	sendDir byte
	recvDir byte
	next    atomic.Uint64
}

// Tag is the session's tag, as its messages carry it.
func (s *ChannelSession) Tag() []byte { return append([]byte(nil), s.tag...) }

// ChannelOpening is a channel handshake a device has begun: its first
// message sent, its answer still to open.
type ChannelOpening struct {
	hs *noise.HandshakeState
}

// StartChannelHandshake begins a channel handshake to the yard channel
// public key a device pinned at pairing, with the device's own channel
// key as its static: the device's half of opening a session, and the
// stand-in device of the tests'.
func StartChannelHandshake(device ChannelKey, yardPublic []byte) ([]byte, *ChannelOpening, error) {
	if len(yardPublic) != 32 {
		return nil, nil, fmt.Errorf("a yard channel public key is 32 bytes, not %d", len(yardPublic))
	}
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   channelSuite,
		Random:        rand.Reader,
		Pattern:       noise.HandshakeIK,
		Initiator:     true,
		StaticKeypair: device,
		PeerStatic:    yardPublic,
	})
	if err != nil {
		return nil, nil, err
	}
	first, _, _, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, nil, err
	}
	return first, &ChannelOpening{hs: hs}, nil
}

// OpenAnswer opens the yard's handshake answer, whose payload is the
// session tag the yard minted, and answers the session it founds: this
// device sends under the handshake's first transport key and opens
// under its second.
func (o *ChannelOpening) OpenAnswer(reply []byte) (*ChannelSession, error) {
	payload, send, recv, err := o.hs.ReadMessage(nil, reply)
	if err != nil {
		return nil, fmt.Errorf("a channel handshake answer that does not open: %w", err)
	}
	if len(payload) != E2ETagSize {
		return nil, fmt.Errorf("a channel handshake answer names no session tag")
	}
	return &ChannelSession{
		tag:     append([]byte(nil), payload...),
		sendKey: send.UnsafeKey(), recvKey: recv.UnsafeKey(),
		sendDir: dirRequest, recvDir: dirAnswer,
	}, nil
}

// ChannelHandshake is a channel handshake's first message as the yard
// opened it: the device key it proved, and the state its answer is
// sealed with.
type ChannelHandshake struct {
	hs        *noise.HandshakeState
	DeviceKey string
}

// OpenChannelHandshake opens the first message of a channel handshake
// to the yard's channel key. A message made to another yard's key, or
// no handshake at all, is refused here, before any device is looked
// for.
func OpenChannelHandshake(yard ChannelKey, first []byte) (*ChannelHandshake, error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   channelSuite,
		Random:        rand.Reader,
		Pattern:       noise.HandshakeIK,
		Initiator:     false,
		StaticKeypair: yard,
	})
	if err != nil {
		return nil, err
	}
	if _, _, _, err := hs.ReadMessage(nil, first); err != nil {
		return nil, fmt.Errorf("a channel handshake that does not open: %w", err)
	}
	device := hs.PeerStatic()
	if len(device) != 32 {
		return nil, fmt.Errorf("a channel handshake with no device key")
	}
	return &ChannelHandshake{hs: hs, DeviceKey: enc.EncodeToString(device)}, nil
}

// SealAnswer seals the session tag as the handshake's second message
// and answers the session it founds: the yard opens under the
// handshake's first transport key and sends under its second.
func (h *ChannelHandshake) SealAnswer(tag []byte) ([]byte, *ChannelSession, error) {
	if len(tag) != E2ETagSize {
		return nil, nil, fmt.Errorf("a session tag is %d bytes, not %d", E2ETagSize, len(tag))
	}
	reply, send, recv, err := h.hs.WriteMessage(nil, tag)
	if err != nil {
		return nil, nil, err
	}
	// WriteMessage returns the transport states in the handshake's own
	// order — the initiator's first — whoever wrote the message: the
	// yard sends under the second and opens under the first.
	return reply, &ChannelSession{
		tag:     append([]byte(nil), tag...),
		sendKey: recv.UnsafeKey(), recvKey: send.UnsafeKey(),
		sendDir: dirAnswer, recvDir: dirRequest,
	}, nil
}

// NewSessionTag mints a session tag: sixteen random bytes.
func NewSessionTag() ([]byte, error) {
	tag := make([]byte, E2ETagSize)
	_, err := rand.Read(tag)
	return tag, err
}

// NewCallID mints a call id: sixteen random bytes, once for each call.
func NewCallID() ([]byte, error) {
	id := make([]byte, E2ECallSize)
	_, err := rand.Read(id)
	return id, err
}

// associated builds the AEAD's associated data for one message: the
// tag, the direction, the call id, the sequence and the index, and, for
// a call's first record (first != nil), the outer path and the stream
// marker's presence and value, each length-named so no two writings
// agree by accident.
func associated(tag []byte, dir byte, call []byte, seq, index uint64, first *FirstBinding) []byte {
	ad := make([]byte, 0, e2eHeaderSize+64)
	ad = append(ad, tag...)
	ad = append(ad, dir)
	ad = append(ad, call...)
	ad = binary.BigEndian.AppendUint64(ad, seq)
	ad = binary.BigEndian.AppendUint64(ad, index)
	if first == nil {
		return append(ad, 0)
	}
	ad = append(ad, 1)
	ad = binary.BigEndian.AppendUint16(ad, uint16(len(first.Path)))
	ad = append(ad, first.Path...)
	if first.Present {
		ad = append(ad, 1)
	} else {
		ad = append(ad, 0)
	}
	ad = binary.BigEndian.AppendUint16(ad, uint16(len(first.Marker)))
	ad = append(ad, first.Marker...)
	return ad
}

// Seal seals one transport message of the call call at record index,
// bound to first when it is the call's first record in this direction
// (first != nil exactly at index 0): its sequence is the session's
// next, taken once and never again, and its plaintext is sealed with a
// fresh copy of the sending key at that nonce, so any number of
// messages seal at once.
func (s *ChannelSession) Seal(call []byte, index uint64, first *FirstBinding, plaintext []byte) ([]byte, error) {
	if len(call) != E2ECallSize {
		return nil, fmt.Errorf("a call id is %d bytes, not %d", E2ECallSize, len(call))
	}
	if len(plaintext) > MaxRecordPlain {
		return nil, fmt.Errorf("a record holds at most %d bytes of plaintext, not %d", MaxRecordPlain, len(plaintext))
	}
	if (index == 0) != (first != nil) {
		return nil, fmt.Errorf("record 0 alone carries the first binding")
	}
	seq, err := s.nextSequence()
	if err != nil {
		return nil, err
	}
	cs := noise.UnsafeNewCipherState(channelSuite, s.sendKey, seq)
	ciphertext, err := cs.Encrypt(nil, associated(s.tag, s.sendDir, call, seq, index, first), plaintext)
	if err != nil {
		return nil, err
	}
	msg := make([]byte, 0, e2eHeaderSize+len(ciphertext))
	msg = append(msg, s.tag...)
	msg = append(msg, call...)
	msg = binary.BigEndian.AppendUint64(msg, seq)
	msg = binary.BigEndian.AppendUint64(msg, index)
	return append(msg, ciphertext...), nil
}

// nextSequence takes the session's next nonce once, or refuses after the
// reserved exhaustion value. Keeping MaxUint64 as the exhausted sentinel
// prevents the allocator from wrapping back to a nonce already used under
// this key.
func (s *ChannelSession) nextSequence() (uint64, error) {
	for {
		seq := s.next.Load()
		if seq > noise.MaxNonce || seq == ^uint64(0) {
			return 0, noise.ErrMaxNonce
		}
		if s.next.CompareAndSwap(seq, seq+1) {
			return seq, nil
		}
	}
}

// Open opens one transport message that names the call want (sixteen
// bytes; nil takes the message's own word, for a caller that has no
// expectation yet) and answers its record index and plaintext. The
// clear header is checked against the session, the call and the first
// binding before a copied cipher state is spent on it, so a message
// carried to the wrong session or call is refused unopened; a header
// rewritten in transit, or a first record whose outer path or marker
// was rewritten, fails the opening itself.
func (s *ChannelSession) Open(msg, want []byte, first *FirstBinding) (index uint64, plaintext []byte, err error) {
	if len(msg) < e2eHeaderSize+16 {
		return 0, nil, fmt.Errorf("a transport message is %d bytes, too short to open", len(msg))
	}
	tag, call := msg[:E2ETagSize], msg[E2ETagSize:e2eHeaderSize-16]
	if string(tag) != string(s.tag) {
		return 0, nil, fmt.Errorf("a transport message of no session this one knows")
	}
	if want != nil && string(call) != string(want) {
		return 0, nil, fmt.Errorf("a transport message of another call")
	}
	seq := binary.BigEndian.Uint64(msg[e2eHeaderSize-16 : e2eHeaderSize-8])
	index = binary.BigEndian.Uint64(msg[e2eHeaderSize-8 : e2eHeaderSize])
	if seq > noise.MaxNonce {
		return 0, nil, noise.ErrMaxNonce
	}
	if (index == 0) != (first != nil) {
		return 0, nil, fmt.Errorf("record 0 alone carries the first binding")
	}
	cs := noise.UnsafeNewCipherState(channelSuite, s.recvKey, seq)
	plaintext, err = cs.Decrypt(nil, associated(tag, s.recvDir, call, seq, index, first), msg[e2eHeaderSize:])
	if err != nil {
		return 0, nil, fmt.Errorf("a transport message that does not open: %w", err)
	}
	return index, plaintext, nil
}

// FrameRecord appends one transport message to a record run as it
// travels: a four-byte big-endian length, then the message. A run of
// records is the body of a whole enveloped call or answer.
func FrameRecord(run, msg []byte) []byte {
	run = binary.BigEndian.AppendUint32(run, uint32(len(msg)))
	return append(run, msg...)
}

// SplitRecords splits a record run into its transport messages. A
// length that runs past the run, or bytes left over, are no run; a
// zero-length record is the router's beat, no message, and is dropped
// here, before any opening, as a device drops it.
func SplitRecords(run []byte) ([][]byte, error) {
	var out [][]byte
	for len(run) > 0 {
		if len(run) < 4 {
			return nil, fmt.Errorf("a record run ends inside a length")
		}
		n := int(binary.BigEndian.Uint32(run))
		run = run[4:]
		if n > len(run) {
			return nil, fmt.Errorf("a record claims %d bytes of a run with %d left", n, len(run))
		}
		if n > 0 {
			out = append(out, run[:n])
		}
		run = run[n:]
	}
	return out, nil
}

// SealWhole seals a whole call or answer's joined plaintext as a record
// run: the plaintext chopped into records of at most MaxRecordPlain,
// in order, the first bound to first. An empty plaintext still travels
// as one empty record, so the run of a call always holds its first.
func (s *ChannelSession) SealWhole(call []byte, first *FirstBinding, plaintext []byte) ([]byte, error) {
	var run []byte
	var index uint64
	for {
		n := min(len(plaintext), MaxRecordPlain)
		bind := (*FirstBinding)(nil)
		if index == 0 {
			bind = first
		}
		msg, err := s.Seal(call, index, bind, plaintext[:n])
		if err != nil {
			return nil, err
		}
		run = FrameRecord(run, msg)
		plaintext = plaintext[n:]
		index++
		if len(plaintext) == 0 {
			return run, nil
		}
	}
}

// OpenWhole opens a whole call or answer's record run and joins its
// records in the order they stand: each record's index must be the
// next its call expects here, as the records of one outer body stand
// in the order they were sealed. The first is bound to first, and the
// run must hold at least that one.
func (s *ChannelSession) OpenWhole(run, call []byte, first *FirstBinding) ([]byte, error) {
	msgs, err := SplitRecords(run)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("an enveloped call with no first record")
	}
	var plain []byte
	for i, msg := range msgs {
		bind := (*FirstBinding)(nil)
		if i == 0 {
			bind = first
		}
		index, piece, err := s.Open(msg, call, bind)
		if err != nil {
			return nil, err
		}
		if index != uint64(i) {
			return nil, fmt.Errorf("record %d of a call stands at index %d", i, index)
		}
		plain = append(plain, piece...)
	}
	return plain, nil
}
