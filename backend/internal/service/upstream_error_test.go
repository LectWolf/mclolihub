package service

import "testing"

func TestIsUpstreamBalanceInsufficientError(t *testing.T) {
	for _, input := range []string{"insufficient_quota", "credit exhausted", "上游账户余额不足"} {
		if !IsUpstreamBalanceInsufficientError(input) {
			t.Fatalf("expected balance classification for %q", input)
		}
	}
	if IsUpstreamBalanceInsufficientError("用户余额不足") {
		t.Fatal("user wallet error must not be classified as upstream account state")
	}
}
