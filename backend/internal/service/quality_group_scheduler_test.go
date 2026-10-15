//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPelicanQualitySchedulingCooldownBlocksRequest(t *testing.T) {
	for _, state := range []string{"rate-limit", "overload", "temporary-cooldown"} {
		t.Run(state, func(t *testing.T) {
			account := pelicanChatAccount()
			account.Status = StatusActive
			account.Schedulable = true
			until := time.Now().Add(time.Hour)
			switch state {
			case "rate-limit":
				account.RateLimitResetAt = &until
			case "overload":
				account.OverloadUntil = &until
			case "temporary-cooldown":
				account.TempUnschedulableUntil = &until
			}
			svc, upstream := pelicanMockService(account, pelicanMockResponse("<html>must not request</html>", "model", true))
			result := svc.runPelicanQualityCheck(context.Background(), account.ID, "model")
			require.Empty(t, upstream.requests, "quality checks must honor existing scheduling cooldowns")
			require.NotEqual(t, "success", result.Status)
		})
	}
}

func TestPelicanQualityBillingNotActiveDoesNotRetrySameAccount(t *testing.T) {
	account := pelicanChatAccount()
	responses := make([]*http.Response, 6)
	for i := range responses {
		responses[i] = newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"billing_not_active","message":"Your account is not active, please check your billing details on our website."}}`)
	}
	svc, upstream := pelicanMockService(account, responses...)
	result := svc.runPelicanQualityCheck(context.Background(), account.ID, "model")
	require.Equal(t, "failed", result.Status)
	require.Len(t, upstream.requests, 1, "billing failures must leave this account after the first attempt")
}
