package credential

import "testing"

func TestAccountUsesStableSubscriptionIdentity(t *testing.T) {
	k := Keychain{Username: "alice"}
	if got := k.Account(""); got != "alice" {
		t.Fatalf("legacy account=%q", got)
	}
	if got := k.Account("0123456789abcdef0123456789abcdef"); got != "alice:0123456789abcdef0123456789abcdef" {
		t.Fatalf("subscription account=%q", got)
	}
}
