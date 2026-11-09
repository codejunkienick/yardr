package store

import "context"

func (w *wtx) writeCrewSubs(ctx context.Context, name string, r CrewMember, subs []Subscription, kind string, data map[string]any, actor string) error {
	if err := w.packSubscriptionChanges(ctx, name, r.Subscriptions, subs, actor); err != nil {
		return err
	}
	r.Subscriptions = subs
	if err := put(w.Tx, bCrew, name, r); err != nil {
		return err
	}
	data["crew"] = name
	return w.event(r.Journal, kind, actor, data)
}

func (w *wtx) writeGroupSubs(name string, g Group, subs []Subscription, kind string, data map[string]any, actor string) error {
	g.Subscriptions = subs
	if err := put(w.Tx, bGroups, name, g); err != nil {
		return err
	}
	data["group"] = name
	return w.event("", kind, actor, data)
}
