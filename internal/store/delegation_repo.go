// A delegated pack apply of a pack of the packs repository (design
// yardr-m90n 3.9): the run of delegation_pack.go, whose reading of the pack
// comes from the origin instead of the binary. The store takes neither the
// URL nor the digest on the caller's word: a PackFetcher the owning process
// registers fetches the pack itself, outside the write, holds it to the
// pinned digest and answers a reading of exactly those bytes, which the
// start then holds the run's objects and every later write to. The record
// names the digest that stood before it, so a re-apply pinned to that digest
// takes the run back where the origin still serves those bytes.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// PackFetcher fetches the pack of the packs repository that from names and
// holds it to digest, the pin: the process that owns the store registers
// it (SetPackFetcher) as it registers its reading of the packs it embeds.
// It refuses a URL that is not that repository's, a pack that is not the
// pinned one and any failure of the fetch, in its own words, and answers
// the reading of the bytes it checked: the store never reads a pack of the
// repository from anything else, and never takes it from a caller.
type PackFetcher func(ctx context.Context, from, digest string) (PackReader, error)

// SetPackFetcher tells the store how its owning process fetches a pack of
// the packs repository. A store no server owns has none, and a delegated
// start from the repository is refused naming the server.
func (s *Store) SetPackFetcher(f PackFetcher) { s.packFetcher = f }

// packRepoReader is the reading of the pack of the packs repository a
// delegated start names (start.From), nil for a pack yardr embeds. It is
// fetched before the start's write and never in it: the store has one
// writer, and a slow origin must not stall every other write of the yard.
// So that a call the write would refuse starts no fetch, the delegation and
// the policy are asked first; the write asks them again, and holds the
// start to the reading.
func (s *Store) packRepoReader(ctx context.Context, start DelegatedPackStart, actor string) (PackReader, error) {
	if start.From == "" {
		return nil, nil
	}
	if start.Digest == "" {
		return nil, fmt.Errorf("pack %s: a pack of the packs repository is applied pinned: the run names its digest, as yardr pack check prints it", start.Pack)
	}
	if s.packFetcher == nil {
		return nil, fmt.Errorf("pack %s: no server has registered its fetching of the packs repository on this store, so no pack of it can be applied by a delegated run", start.Pack)
	}
	err := s.read(func(tx *bolt.Tx) error {
		d, err := delegatedIn(ctx, tx, actor)
		if err != nil {
			return err
		}
		if d == nil {
			return fmt.Errorf("pack %s: starting a delegated pack record takes a delegated run (--for <operator> --mail <seq>)", start.Pack)
		}
		return s.may(ctx, tx, actor, ActApplyPack, Object{Kind: KindPack, Name: start.Pack, Delegate: true})
	})
	if err != nil {
		return nil, err
	}
	return s.packFetcher(ctx, start.From, start.Digest)
}

// TextSum is the hash a reading of a pack of the packs repository holds a
// declared source's text by, as the source's target on the record: the
// pack's bytes are not fetched again inside a later write, so the write is
// held to the hash (packSourceWrite).
func TextSum(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// packBackByHand is the taking-back words of a delegated pack run that no
// pinned re-apply takes back: a pack yardr embeds, whose prior digest the
// record does not hold.
const packBackByHand = "no one command takes a pack apply back: an operator reads this record and sets each updated object back from the before-state it holds, or re-applies the pack that stood before where its digest is known"

// packBack is a delegated pack run's prior digest and its taking-back
// words. A pack of the packs repository is taken back by re-applying the
// one that stood before it, pinned to its digest, which the latest finished
// run of it into this depot names (priorRepoPack), with the mappings that
// run had. Where there is none — a first install, or a pack put there by
// anything but a delegated run from the repository — the words say so
// instead of implying a way back.
func (w *wtx) packBack(start DelegatedPackStart, of string) (prior, back string, err error) {
	if start.From == "" {
		return "", packBackByHand, nil
	}
	rec, body, err := w.priorRepoPack(of)
	if err != nil {
		return "", "", err
	}
	if rec == nil {
		return "", "no earlier delegated run of this pack from the repository stands in this depot, so there is no digest to pin a take-back to: an operator sets each updated object back from the before-state this record holds, and removes each object it created by name", nil
	}
	argv := []string{"pack", "apply", body.From, "--sha256", "sha256:" + body.Digest, "--depot", start.Depot}
	for _, flag := range packMappingFlags(rec.Mappings) {
		word, value, _ := strings.Cut(flag, " ")
		argv = append(argv, word, value)
	}
	return body.Digest, runCommandLine(argv) + " (the origin must still serve those bytes: the repository's branch moves, and a digest it no longer serves is refused with nothing changed, then this record's before-state is the way back by hand)", nil
}

// priorRepoPack is the latest delegated run that finished applying a pack
// into the depot of, with its body: the pack that stood after it is the one
// that stood before the run being started, as far as the yard's own records
// know it. A latest run that came from no repository (a pack yardr embeds)
// names no digest to pin, and the answer is none.
func (w *wtx) priorRepoPack(of string) (*Delegation, DelegatedBody, error) {
	c := w.Bucket(bDelegations).Cursor()
	for k, raw := c.Last(); k != nil; k, raw = c.Prev() {
		// Every delegated run of the yard is in this bucket: the state and
		// the words of what it changed are read first, the rest of a record
		// only for the one that is the answer.
		var head struct{ State, Of string }
		if err := json.Unmarshal(raw, &head); err != nil {
			return nil, DelegatedBody{}, fmt.Errorf("%s/%q: %w", bDelegations, k, err)
		}
		if head.State != "done" || head.Of != of {
			continue
		}
		var rec Delegation
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, DelegatedBody{}, fmt.Errorf("%s/%q: %w", bDelegations, k, err)
		}
		body, err := w.delegationBody(&rec)
		if err != nil || body.From == "" {
			return nil, DelegatedBody{}, err
		}
		return &rec, body, nil
	}
	return nil, DelegatedBody{}, nil
}

// delegatedPackMail is a delegated pack apply's telling: at the start,
// that the run has started on the operator's mail; at its end, its state,
// what was written before a failure and the error. The before-state and
// diffs stand in the record alone, as every delegated telling's do. A pack
// of the packs repository names where it came from and the digest that
// stood before it, the one its take-back is pinned to.
func delegatedPackMail(rec Delegation, body DelegatedBody) string {
	command := body.Command
	if rec.Blob != nil {
		command = "(too long for mail: it is in the record)"
	}
	if len(command) > MaxMailBytes/2 {
		command = command[:MaxMailBytes/2] + "…"
	}
	text := fmt.Sprintf("yardmaster for %s ran, on your mail %d (record %d, which yardr delegation show %d reads):\n  %s\n%s: pack %s, digest sha256:%s",
		rec.For, rec.Mail, rec.ID, rec.ID, or(command, "(no command words were sent)"), rec.Of, body.Pack, body.Digest)
	if body.From != "" {
		prior := "none known"
		if body.Prior != "" {
			prior = "sha256:" + body.Prior
		}
		text += "\nfrom " + body.From + "; the digest that stood before: " + prior
	}
	switch rec.State {
	case "started":
		text += "\nstate: started"
	case "done":
		text += "\nstate: done"
	case "failed":
		text += "\nstate: failed: " + body.Error
		if len(body.Written) > 0 {
			text += "\nobjects written before it: " + strings.Join(body.Written, ", ")
		}
		text += "\nthe partial import stays, and applying again finishes it"
	}
	if rec.State != "started" && body.Back != "" {
		text += "\ntaken back by: " + body.Back
	}
	if len(text) > MaxMailBytes {
		text = text[:MaxMailBytes-len("…")] + "…"
	}
	return text
}
