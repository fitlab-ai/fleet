package model

import (
	"strings"
	"testing"
)

func TestSafeErrorDoesNotExposeCause(t *testing.T) {
	err := NewError("credential", "Subscription is not configured", strings.NewReader("https://secret"))
	if err.Error() != "Subscription is not configured" || err.Category != "credential" {
		t.Fatalf("unexpected safe error: %#v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("safe error exposed its cause")
	}
}

func TestResolveNodeSupportsSourceAndRejectsAmbiguousNames(t *testing.T) {
	nodes := []Node{
		{Name: "Hong Kong", Fleet: Metadata{NodeKey: "a/Hong Kong", SubscriptionName: "a"}},
		{Name: "Hong Kong", Fleet: Metadata{NodeKey: "b/Hong Kong", SubscriptionName: "b"}},
	}
	if _, err := ResolveNode(nodes, "Hong Kong"); err == nil {
		t.Fatal("ambiguous bare name must fail")
	}
	got, err := ResolveNode(nodes, "@b/Hong Kong")
	if err != nil || got.Fleet.SubscriptionName != "b" {
		t.Fatalf("qualified selector failed: %#v, %v", got, err)
	}
}
