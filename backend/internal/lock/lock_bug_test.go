package lock

import "testing"

func TestCloneKeys_DoesNotShareBackingArray(t *testing.T) {
	orig := []string{"ns/a", "ns/b"}
	cloned := CloneKeys(orig)
	cloned[0] = "x"
	if orig[0] != "ns/a" {
		t.Fatalf("orig[0]=%q, want ns/a", orig[0])
	}
}

func TestNextToken_StartsAtOne(t *testing.T) {
	g := NewSimpleTokenGenerator()
	got := g.NextToken()
	if got != 1 {
		t.Fatalf("first token=%d, want 1", got)
	}
}

func TestClientName_NilIsEmpty(t *testing.T) {
	if ClientName(nil) != "" {
		t.Fatal("nil name should be empty")
	}
}
