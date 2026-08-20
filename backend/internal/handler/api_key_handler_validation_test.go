//go:build unit

package handler

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateAPIKeyCreateRequest(t *testing.T) {
	zero, large, negative, nan, inf := 0.0, 1e100, -1.0, math.NaN(), math.Inf(1)
	positiveDays, zeroDays, negativeDays := 1, 0, -1
	require.NoError(t, validateAPIKeyCreateRequest(CreateAPIKeyRequest{}))
	require.NoError(t, validateAPIKeyCreateRequest(CreateAPIKeyRequest{Quota: &zero, RateLimit5h: &large, ExpiresInDays: &positiveDays}))

	for _, req := range []CreateAPIKeyRequest{
		{Quota: &negative},
		{Quota: &nan},
		{RateLimit5h: &inf},
		{RateLimit1d: &negative},
		{RateLimit7d: &negative},
		{ExpiresInDays: &zeroDays},
		{ExpiresInDays: &negativeDays},
	} {
		require.Error(t, validateAPIKeyCreateRequest(req))
	}
}

func TestValidateAPIKeyUpdateRequest(t *testing.T) {
	zero, large, negative, nan, inf := 0.0, 1e100, -1.0, math.NaN(), math.Inf(-1)
	require.NoError(t, validateAPIKeyUpdateRequest(UpdateAPIKeyRequest{Quota: &zero, RateLimit7d: &large}))

	for _, req := range []UpdateAPIKeyRequest{
		{Quota: &negative},
		{RateLimit5h: &nan},
		{RateLimit1d: &inf},
		{RateLimit7d: &negative},
	} {
		require.Error(t, validateAPIKeyUpdateRequest(req))
	}
}

func TestUpdateAPIKeyRequestMaxRateMultiplierTracksNull(t *testing.T) {
	var omitted UpdateAPIKeyRequest
	require.NoError(t, json.Unmarshal([]byte(`{}`), &omitted))
	require.False(t, omitted.MaxRateMultiplier.Set)

	var cleared UpdateAPIKeyRequest
	require.NoError(t, json.Unmarshal([]byte(`{"max_rate_multiplier":null}`), &cleared))
	require.True(t, cleared.MaxRateMultiplier.Set)
	require.Nil(t, cleared.MaxRateMultiplier.Value)

	var configured UpdateAPIKeyRequest
	require.NoError(t, json.Unmarshal([]byte(`{"max_rate_multiplier":1.25}`), &configured))
	require.True(t, configured.MaxRateMultiplier.Set)
	require.Equal(t, 1.25, *configured.MaxRateMultiplier.Value)
}
