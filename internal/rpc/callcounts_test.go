package rpc

// The served-calls counts, held once for the tests that pin them
// (TestCallsAreTheDocs in api_test.go and TestUserServesTheCallsAndNoOther
// in user_test.go): adding a call to Calls, OperatorCalls or listenerOnly
// updates these literals, here, not each test. They stay written down,
// never derived from the tables' lengths, so an accidental addition fails
// the pin.
const (
	wantCalls         = 41
	wantOperatorCalls = 14
	wantListenerOnly  = 1
)
