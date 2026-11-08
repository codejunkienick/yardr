package rpc

import "reflect"

// serveMethods puts in the user server's api what a device is served in
// place of the store's own method, or besides the store's: a call shaped
// for a device (settings, users, crew), or one the store has no part of
// (RegisterChannelKey, which the listener alone serves: listenerOnly).
func (u *UserServer) serveMethods() {
	u.api.methods["Settings"] = method{"Settings", reflect.ValueOf(u.settings)}
	u.api.methods["Bead"] = method{"Bead", reflect.ValueOf(u.bead)}
	u.api.methods["Arrivals"] = method{"Arrivals", reflect.ValueOf(u.arrivals)}
	u.api.methods["UserAsks"] = method{"UserAsks", reflect.ValueOf(u.userAsks)}
	u.api.methods["Users"] = method{"Users", reflect.ValueOf(u.users)}
	u.api.methods["GetUser"] = method{"GetUser", reflect.ValueOf(u.getUser)}
	u.api.methods["Crew"] = method{"Crew", reflect.ValueOf(u.crew)}
	u.api.methods["GetCrew"] = method{"GetCrew", reflect.ValueOf(u.getCrew)}
	u.api.methods["Flows"] = method{"Flows", reflect.ValueOf(u.flows)}
	u.api.methods["Peers"] = method{"Peers", reflect.ValueOf(u.peers)}
	u.api.methods["DeliveryFiles"] = method{"DeliveryFiles", reflect.ValueOf(u.deliveryFiles)}
	u.api.methods["GiveSecret"] = method{"GiveSecret", reflect.ValueOf(u.giveSecret)}
	u.api.methods["RegisterChannelKey"] = method{"RegisterChannelKey", reflect.ValueOf(u.registerChannelKey)}
}
