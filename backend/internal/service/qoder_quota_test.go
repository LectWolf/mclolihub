package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func qoderDeviceAccount() *Account {
	return &Account{ID: 11, Platform: PlatformQoder, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"qoder_region":  "cn",
		"access_token":  "dt-live",
		"refresh_token": "drt-live",
		"expires_at":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"user_id":       "user-11",
	}}
}

func TestQoderQuotaUsesDeviceLoginAndStoresSnapshot(t *testing.T) {
	upstream := &qoderFakeUpstream{quota: func(req *http.Request, _ int32) *http.Response {
		require.Equal(t, "openapi.qoder.com.cn", req.URL.Host, "the account edition picks the API host")
		require.Equal(t, "Bearer dt-live", req.Header.Get("Authorization"))
		return qoderJSON(http.StatusOK, `{"userType":"pro","isQuotaExceeded":false,"expiresAt":1790812800000,
			"userQuota":{"total":2000,"used":500,"remaining":1500,"unit":"credits"},
			"addOnQuota":{"total":300,"used":300,"remaining":0,"unit":"credits"}}`)
	}}
	svc := newQoderTestService(t, upstream)
	account := qoderDeviceAccount()

	snapshot, err := svc.QoderQuota(context.Background(), account, true)
	require.NoError(t, err)
	require.Equal(t, "pro", snapshot.PlanType)
	require.Equal(t, "credits", snapshot.Unit)
	require.Equal(t, QoderQuotaCredentialDevice, snapshot.Credential)
	require.NotNil(t, snapshot.ResetsAt)
	require.True(t, snapshot.ResetsAt.Equal(time.UnixMilli(1790812800000)))
	require.Len(t, snapshot.Pools, 2)
	require.Equal(t, "plan", snapshot.Pools[0].Kind)
	require.Equal(t, 1500.0, snapshot.Pools[0].Remaining)
	require.Equal(t, "addon", snapshot.Pools[1].Kind)
	require.Zero(t, upstream.exchanges.Load(), "device login does not exchange the PAT")

	stored := StoredQoderQuota(account)
	require.NotNil(t, stored, "the snapshot is kept on the account for the list view")
	require.Equal(t, snapshot.CheckedAt, stored.CheckedAt)
}

func TestQoderQuotaServesFreshSnapshotUnlessRefreshed(t *testing.T) {
	upstream := &qoderFakeUpstream{quota: func(*http.Request, int32) *http.Response {
		return qoderJSON(http.StatusOK, `{"userQuota":{"total":100,"used":10}}`)
	}}
	svc := newQoderTestService(t, upstream)
	account := qoderPATAccount()
	// Shape read back from the database: plain JSON maps, not the struct.
	account.Extra = map[string]any{qoderQuotaExtraKey: map[string]any{
		"plan_type":  "free",
		"pools":      []any{map[string]any{"kind": "plan", "total": 50, "used": 5, "remaining": 45, "available": true}},
		"credential": "pat",
		"checked_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano),
	}}

	cached, err := svc.QoderQuota(context.Background(), account, false)
	require.NoError(t, err)
	require.Equal(t, "free", cached.PlanType)
	require.Zero(t, upstream.quotas.Load(), "a fresh snapshot needs no upstream call")

	refreshed, err := svc.QoderQuota(context.Background(), account, true)
	require.NoError(t, err)
	require.EqualValues(t, 1, upstream.quotas.Load())
	require.Equal(t, 90.0, refreshed.Pools[0].Remaining)
	require.Equal(t, QoderQuotaCredentialPAT, refreshed.Credential)
}

func TestQoderQuotaPATRejectedAfterOneRetry(t *testing.T) {
	upstream := &qoderFakeUpstream{quota: func(*http.Request, int32) *http.Response {
		return qoderJSON(http.StatusUnauthorized, `{"message":"unsupported token"}`)
	}}
	svc := newQoderTestService(t, upstream)

	_, err := svc.QoderQuota(context.Background(), qoderPATAccount(), true)
	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, infraerrors.Code(err), "upstream 401 must not reach the admin client as 401")
	require.Equal(t, "QODER_QUOTA_PAT_REJECTED", infraerrors.Reason(err))
	require.EqualValues(t, 2, upstream.quotas.Load(), "retried once with a fresh job token")
	require.EqualValues(t, 2, upstream.exchanges.Load())
}

func TestQoderQuotaRequiresQoderAccountWithCredential(t *testing.T) {
	svc := NewQoderGatewayService(nil, nil, nil, nil, nil)

	_, err := svc.QoderQuota(context.Background(), &Account{ID: 1, Platform: PlatformOpenAI}, true)
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))

	_, err = svc.QoderQuota(context.Background(), &Account{ID: 2, Platform: PlatformQoder, Credentials: map[string]any{}}, true)
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Equal(t, "QODER_NO_CREDENTIAL", infraerrors.Reason(err))
}
