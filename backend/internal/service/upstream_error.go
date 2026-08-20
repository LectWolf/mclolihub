package service

import "strings"

// IsUpstreamBalanceInsufficientError distinguishes provider credit exhaustion
// from a user's own wallet/quota errors. It is deliberately conservative: only
// explicit provider wording is classified as an account state.
func IsUpstreamBalanceInsufficientError(message string) bool {
	m := strings.ToLower(strings.TrimSpace(message))
	if m == "" {
		return false
	}
	markers := []string{"insufficient_quota", "quota_exceeded", "credit exhausted", "credits exhausted", "insufficient credits", "billing hard limit", "上游账户余额", "upstream account balance", "额度耗尽"}
	for _, marker := range markers {
		if strings.Contains(m, marker) {
			return true
		}
	}
	return false
}
