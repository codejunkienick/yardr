package yardkey

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// A typed pairing code is redeemed through the PAKE (pake.go): the two
// static channel keys cross under the key it derives, the yard's answer
// is sealed the same way, and neither the code nor a key crosses
// readable.
func TestPairingPAKEExchangesTheStaticKeys(t *testing.T) {
	yard, err := GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	device, err := GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	// As typed: lower case, with the dash.
	first, opening, err := StartPairingPAKE(device, "k7qm-2xpd")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 65 {
		t.Fatalf("the PAKE first message is %d bytes, want the 65 of an uncompressed P-256 point", len(first))
	}
	second, responder, err := AnswerPairingPAKE("K7QM2XPD", first)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 65 {
		t.Fatalf("the PAKE second message is %d bytes, want 65", len(second))
	}
	confirm, err := opening.Finish(second)
	if err != nil {
		t.Fatal(err)
	}
	if len(confirm) != 32 {
		t.Fatalf("the device confirmation is %d bytes, want 32", len(confirm))
	}
	yardConfirm, err := responder.Confirm(confirm)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := responder.Confirm(confirm); err == nil || errors.Is(err, ErrPAKEConfirmation) {
		t.Errorf("a second confirmation = %v, want a state error, not a failed confirmation", err)
	}
	if len(yardConfirm) != 32 {
		t.Fatalf("the yard confirmation is %d bytes, want 32", len(yardConfirm))
	}
	if err := opening.Verify(yardConfirm); err != nil {
		t.Fatal(err)
	}
	sealedKey, err := opening.SealKey()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealedKey, device.Public) {
		t.Error("the device channel public key crosses readable in its sealed form")
	}
	deviceKey, err := responder.OpenKey(sealedKey)
	if err != nil {
		t.Fatal(err)
	}
	if deviceKey != ChannelPublic(device) {
		t.Fatalf("the yard opened device key %q, want %q", deviceKey, ChannelPublic(device))
	}
	answer := []byte(`{"token":"yardr-dev-x","channel_key":"` + ChannelPublic(yard) + `"}`)
	sealedAnswer, err := responder.SealAnswer(answer)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealedAnswer, []byte("yardr-dev-x")) || bytes.Contains(sealedAnswer, yard.Public) {
		t.Error("the sealed PAKE answer crosses readable")
	}
	opened, err := opening.OpenAnswer(sealedAnswer)
	if err != nil || !bytes.Equal(opened, answer) {
		t.Fatalf("the device opened %q, %v", opened, err)
	}
}

// A run with the wrong code derives another key: its confirmation is
// refused as the pairing's confirmation (ErrPAKEConfirmation), and no
// device key is opened from it.
func TestPairingPAKEWrongCodeFailsConfirmation(t *testing.T) {
	device, err := GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	first, opening, err := StartPairingPAKE(device, "AAAA-AAAA")
	if err != nil {
		t.Fatal(err)
	}
	second, responder, err := AnswerPairingPAKE("K7QM2XPD", first)
	if err != nil {
		t.Fatal(err)
	}
	confirm, err := opening.Finish(second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := responder.Confirm(confirm); !errors.Is(err, ErrPAKEConfirmation) {
		t.Fatalf("a wrong code's confirmation = %v, want ErrPAKEConfirmation", err)
	}
	sealedKey, err := opening.SealKey()
	if err == nil {
		if _, err := responder.OpenKey(sealedKey); err == nil {
			t.Error("the yard opened a device key in a run whose confirmation failed")
		}
	}
}

// Every message the carrier can rewrite is held by the confirmations
// and the seals: a byte turned in the second message, a confirmation,
// a sealed key or a sealed answer fails that step, and a confirmation
// replayed from a finished run confirms nothing in a new one.
func TestPairingPAKETamperedMessagesFail(t *testing.T) {
	device, err := GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T) (first []byte, opening *PairingPAKEOpening, second []byte, responder *PairingPAKEResponder) {
		t.Helper()
		first, opening, err = StartPairingPAKE(device, "K7QM-2XPD")
		if err != nil {
			t.Fatal(err)
		}
		second, responder, err = AnswerPairingPAKE("K7QM2XPD", first)
		if err != nil {
			t.Fatal(err)
		}
		return first, opening, second, responder
	}
	turn := func(b []byte) []byte {
		out := append([]byte(nil), b...)
		out[len(out)/2] ^= 0xff
		return out
	}

	_, opening, second, _ := run(t)
	if _, err := opening.Finish(turn(second)); err == nil {
		t.Error("a turned second message was finished")
	}

	_, opening, second, responder := run(t)
	confirm, err := opening.Finish(second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := responder.Confirm(turn(confirm)); !errors.Is(err, ErrPAKEConfirmation) {
		t.Errorf("a turned confirmation = %v, want ErrPAKEConfirmation", err)
	}
	yardConfirm, err := responder.Confirm(confirm)
	if err != nil {
		t.Fatal(err)
	}
	if err := opening.Verify(turn(yardConfirm)); !errors.Is(err, ErrPAKEConfirmation) {
		t.Errorf("a turned yard confirmation = %v, want ErrPAKEConfirmation", err)
	}
	if err := opening.Verify(yardConfirm); err != nil {
		t.Fatal(err)
	}
	sealedKey, err := opening.SealKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := responder.OpenKey(turn(sealedKey)); err == nil {
		t.Error("a turned sealed device key opened")
	}
	sealedAnswer, err := responder.SealAnswer([]byte(`{"token":"yardr-dev-x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opening.OpenAnswer(turn(sealedAnswer)); err == nil {
		t.Error("a turned sealed answer opened")
	}

	// The recorded confirmation, replayed into a fresh run for the
	// same code, confirms nothing there: the new run's transcript
	// differs, so the recorded MAC is over another one.
	_, _, second, responder = run(t)
	if _, err := responder.Confirm(confirm); !errors.Is(err, ErrPAKEConfirmation) {
		t.Errorf("a replayed confirmation = %v, want ErrPAKEConfirmation", err)
	}
	_ = second
}

// A message that is no point of the suite is refused as one, never
// handed on to the library: an empty one would have it index past the
// end of the bytes and panic, and an encoding of the point at infinity
// (a lone byte, or a prefix and zeros) it would take as a point. Each
// side refuses them, the yard's first-message check being the one an
// unauthenticated caller reaches.
func TestPairingPAKERefusesWhatIsNoPoint(t *testing.T) {
	device, err := GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	noPoints := map[string][]byte{
		"empty":          {},
		"one byte":       {0x04},
		"infinity":       make([]byte, 65),
		"prefixed zeros": append([]byte{0x04}, make([]byte, 64)...),
		"compressed":     append([]byte{0x02}, bytes.Repeat([]byte{0x11}, 32)...),
		"off the curve":  append([]byte{0x04}, bytes.Repeat([]byte{0x01}, 64)...),
	}
	for name, msg := range noPoints {
		t.Run(name, func(t *testing.T) {
			if _, _, err := AnswerPairingPAKE("K7QM2XPD", msg); err == nil {
				t.Error("the yard answered a first message that is no point")
			}
			_, opening, err := StartPairingPAKE(device, "K7QM2XPD")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := opening.Finish(msg); err == nil {
				t.Error("the device finished on a second message that is no point")
			}
		})
	}
}

// A recorded transcript carries neither the code nor the token as
// cleartext, and its confirmation and sealed answer do not replay into
// a fresh run, even one started with the same code. The candidates
// below demonstrate that cross-run property; they are not an offline
// test of SPAKE2's password security.
func TestPairingPAKETranscriptDoesNotRevealValuesOrReplay(t *testing.T) {
	device, err := GenerateChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	first, opening, err := StartPairingPAKE(device, "K7QM-2XPD")
	if err != nil {
		t.Fatal(err)
	}
	second, responder, err := AnswerPairingPAKE("K7QM2XPD", first)
	if err != nil {
		t.Fatal(err)
	}
	confirm, err := opening.Finish(second)
	if err != nil {
		t.Fatal(err)
	}
	yardConfirm, err := responder.Confirm(confirm)
	if err != nil {
		t.Fatal(err)
	}
	if err := opening.Verify(yardConfirm); err != nil {
		t.Fatal(err)
	}
	sealedAnswer, err := responder.SealAnswer([]byte(`{"token":"yardr-dev-x"}`))
	if err != nil {
		t.Fatal(err)
	}
	transcript := [][]byte{first, second, confirm, yardConfirm, sealedAnswer}
	for _, msg := range transcript {
		if bytes.Contains(msg, []byte("K7QM2XPD")) || bytes.Contains(msg, []byte("yardr-dev-x")) {
			t.Error("a transcript message crosses readable")
		}
	}
	for _, candidate := range []string{"K7QM-2XPE", "AAAA-AAAA", "0000-0000", "K7QM2XPD"} {
		_, guess, err := StartPairingPAKE(device, candidate)
		if err != nil {
			t.Fatal(err)
		}
		guessed, err := guess.Finish(second)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(guessed, confirm) {
			t.Errorf("candidate %s reproduced the recorded confirmation offline", candidate)
		}
		if err := guess.Verify(yardConfirm); !errors.Is(err, ErrPAKEConfirmation) {
			t.Errorf("candidate %s: Verify = %v, want ErrPAKEConfirmation", candidate, err)
		}
		if _, err := guess.OpenAnswer(sealedAnswer); err == nil {
			t.Errorf("candidate %s opened the recorded sealed answer offline", candidate)
		}
	}
	// What is no code, and bytes that are no first message, are refused
	// before any run.
	if _, _, err := StartPairingPAKE(device, "K7QM"); err == nil || !strings.Contains(err.Error(), "no pairing code") {
		t.Errorf("a short code = %v", err)
	}
	if _, _, err := AnswerPairingPAKE("K7QM2XPD", []byte("not a point")); err == nil {
		t.Error("bytes that are no first message were answered")
	}
}
