package rpc

import (
	"context"
	"encoding/json"
	"slices"

	"yardr.dev/yardr/internal/store"
)

// The secrets a device may give are told from the body of the call that
// carries them: which ask a GiveSecret names, and whether an ApproveSetup types
// the account. The listener reads the body to judge such a call before it takes
// it down the link from the edge (UserServer.call, inTheClear).

// ownSecret reports whether body, the arguments of GiveSecret from the
// device c of a user who is not the operator, names a secret ask for that
// user's own address in a source (store.AskSecret.User), for their own
// credential in one (store.AskSecret.As) or for the input of their own
// login (As beside a Login: the code their sign-in page shows). Those
// givings are the user's own to make, on the socket (the store's mayTell)
// and so here: each writes a file of theirs or hands their code to their
// own waiting login, and sets no credential of the yard. For every other ask, and
// for arguments that name none, the call stays what it is to anyone but
// the operator: not there. Who may give is still the store's to decide,
// with the user of the device's token as the actor (asUser).
func (u *UserServer) ownSecret(ctx context.Context, c *Caller, body []byte) bool {
	if c.User == "" {
		return false
	}
	sec := u.namedSecret(ctx, body)
	return sec != nil && (sec.User == c.User || sec.As == c.User)
}

// loginCode reports whether body is a GiveSecret call from an operator's
// device that names the daemon's ask for a harness login's input and carries
// only that ask's values. It is the one secret taken down the link from the
// edge from an operator's device on a yard that is not hosted (inTheClear),
// because it is used once, within minutes, and the login it is handed to keeps
// it in memory and nowhere else. A user's own login code (As) is not it: that
// is a member's, taken from a sealed device alone (memberLoginCode).
func (u *UserServer) loginCode(ctx context.Context, body []byte, user string) bool {
	return u.loginInput(ctx, body, user, store.AskSecret.OperatorLoginInput)
}

// memberLoginCode reports whether body is a GiveSecret call from the device of
// the member user that gives the code their own sign-in page shows (the ask
// askedLogin stores for a Login with an As), and the call came sealed: the
// channel session is on the context, which serveInner alone puts there, so
// nothing a client sends says it (e2eSessionKey). It is the one secret a
// member's device gives down the link from the edge, hosted or not, and the
// body carries only that ask's values, as the operator's does (loginInput).
//
// Sealed is a device paired with the QR, and one sealed in place
// (store.PinnedInPlace) as well. The operator's code crosses plain today, so a
// channel the edge may have held at its first call is no worse than that bar.
// A member's is sealed-only because a new exception is not given to plain
// calls, which yardr-dz5e will refuse for a secret altogether.
//
// The gate is of the ask's form and not of a harness kind: a harness added
// later whose login asks for an input in this form inherits it. What makes
// that safe holds for every kind: the code is used once, within ten minutes
// (loginInputTimeout in cli/harnesslogin.go), kept in the yard's memory and
// nowhere else, and handed only to the login of the user the ask is for
// (store.LoginWait, handLogin).
func (u *UserServer) memberLoginCode(ctx context.Context, body []byte, user string) bool {
	if _, sealed := ctx.Value(e2eSessionKey{}).(*e2eSession); !sealed {
		return false
	}
	return u.loginInput(ctx, body, user, func(sec store.AskSecret) bool { return ownLoginInput(sec, user) })
}

// ownLoginInput reports whether sec is the ask for the input of the login led
// for user: the operator's form of it (OperatorLoginInput, the whole of the
// struct compared) with As the user, which is the form askedLogin stores for a
// member's. The operator's form is the one compared, with As put aside, so the
// two cannot drift and a field AskSecret gains later is no part of this one
// until the operator's says it is. INPUT stays the store's own.
func ownLoginInput(sec store.AskSecret, user string) bool {
	as := sec.As
	sec.As = ""
	return user != "" && as == user && sec.OperatorLoginInput()
}

// loginInput reports whether body is a GiveSecret call from the device of
// user that names an ask for a harness login's input in the form is takes
// (the operator's, or a member's own), and carries only that ask's values.
// Other argument shapes must be refused before the edge reads them, even if a
// client later rejects them.
func (u *UserServer) loginInput(ctx context.Context, body []byte, user string, is func(store.AskSecret) bool) bool {
	if _, err := named("GiveSecret", body); err != nil {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return false
	}
	decode := func(name string, value any) bool {
		raw, ok := fields[name]
		return !ok || json.Unmarshal(raw, value) == nil
	}
	var id string
	var ask uint64
	var values map[string]string
	var actor string
	var weighed bool
	if !decode("bead", &id) || !decode("ask", &ask) || !decode("values", &values) ||
		!decode("actor", &actor) || !decode("weighed", &weighed) {
		return false
	}
	if id == "" || ask == 0 || !weighed || actor != "" && actor != user {
		return false
	}
	sec := u.namedSecret(ctx, body)
	if sec == nil || !is(*sec) || len(values) != len(sec.Env) {
		return false
	}
	for _, name := range sec.Env {
		if _, ok := values[name]; !ok {
			return false
		}
	}
	return true
}

// crossesConnected reports whether the operator's call name, with the
// arguments body from the device of user, is one that a yard connected to the
// edge and not hosted takes down the link: a GiveSecret for the code of a
// harness login (loginCode), and an ApproveSetup without typed account values
// (typesAccount returns false), which settles by a candidate taken or the
// account kept.
// Hosted yards take both whole and do not ask (call).
func (u *UserServer) crossesConnected(ctx context.Context, name string, body []byte, user string) bool {
	switch name {
	case "GiveSecret":
		return u.loginCode(ctx, body, user)
	case "ApproveSetup":
		return !typesAccount(body)
	}
	return false
}

// typesAccount reports whether body, the arguments of an ApproveSetup, carries
// values: the account a person typed, which is a secret on its way to the file
// a source's credential is read from. It is read as the call reads it (named),
// so a body the call would refuse, or values that are no object of strings, are
// taken to carry some: the refusal is the safe answer to what cannot be told.
func typesAccount(body []byte) bool {
	args, err := named("ApproveSetup", body)
	if err != nil {
		return true
	}
	var values map[string]string
	if json.Unmarshal(args[slices.Index(OperatorCalls["ApproveSetup"], "values")], &values) != nil {
		return true
	}
	return len(values) > 0
}

// ownPairApproval reports whether body, the arguments of ApproveAsk
// from the device c of a user who is not the operator, names a device
// pair ask for that user's own device (design yardr-6maq 3.4). That
// approval is the user's own to make, as a giving of their own address
// is: the pairing it opens is for their device, and its code is
// answered to the approver alone. For every other ask, and for
// arguments that name none, the call stays what it is to anyone but
// the operator: not there. Who may approve is still the store's to
// decide, with the user of the device's token as the actor (asUser).
func (u *UserServer) ownPairApproval(ctx context.Context, c *Caller, body []byte) bool {
	if c.User == "" {
		return false
	}
	ask := u.namedAsk(ctx, body)
	return ask != nil && ask.To == c.User && ask.Run != nil &&
		store.IsDevicePairRun(ask.Run.Argv) && ask.Run.Argv[3] == c.User
}

// namedAsk is the ask that the body, the arguments of an approval, names:
// nil for arguments that name no ask of a bead. Only the bead and the ask
// are read from the arguments, and nothing of what is found is said back.
func (u *UserServer) namedAsk(ctx context.Context, body []byte) *store.Ask {
	var named map[string]json.RawMessage
	if json.Unmarshal(body, &named) != nil {
		return nil
	}
	var id string
	if json.Unmarshal(named["bead"], &id) != nil || id == "" {
		return nil
	}
	var ask uint64
	if json.Unmarshal(named["ask"], &ask) != nil {
		return nil
	}
	notes, err := u.st.Notes(ctx, id)
	if err != nil {
		return nil
	}
	for _, n := range notes {
		if n.Seq != ask || n.Kind != store.NoteAsk {
			continue
		}
		parsed, err := store.ParseAsk(n)
		if err != nil {
			return nil
		}
		return &parsed
	}
	return nil
}

// namedSecret is the secret that the ask body, the arguments of GiveSecret,
// names asks for: nil for arguments that name no ask of a bead, and for an ask
// that asks for none. Only the bead and the ask are read from the arguments,
// and nothing of what is found is said back; who may give is the store's to
// decide.
func (u *UserServer) namedSecret(ctx context.Context, body []byte) *store.AskSecret {
	var named map[string]json.RawMessage
	if json.Unmarshal(body, &named) != nil {
		return nil
	}
	var id string
	if json.Unmarshal(named["bead"], &id) != nil || id == "" {
		return nil
	}
	var ask uint64
	if json.Unmarshal(named["ask"], &ask) != nil {
		return nil
	}
	notes, err := u.st.Notes(ctx, id)
	if err != nil {
		return nil
	}
	for _, n := range notes {
		if n.Seq != ask || n.Kind != store.NoteAsk {
			continue
		}
		ask, err := store.ParseAsk(n)
		if err != nil {
			return nil
		}
		return ask.Secret
	}
	return nil
}
