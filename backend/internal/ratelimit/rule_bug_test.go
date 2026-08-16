package ratelimit

import "testing"

func TestCreateRule_DuplicateReturnsError(t *testing.T) {
	m := NewRateLimitManager(nil)
	req := &CreateRuleRequest{Key: "ns/api", Capacity: 10, Rate: 1}
	if err := m.CreateRule(req); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := m.CreateRule(req); err == nil {
		t.Fatal("duplicate rule should return error")
	}
}
