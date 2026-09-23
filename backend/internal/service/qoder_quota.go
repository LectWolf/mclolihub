package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/qoderproxy"
	"go.uber.org/zap"
)

const (
	qoderQuotaExtraKey = "qoder_quota"
	// qoderQuotaFreshFor lets a non-forced read reuse the stored snapshot.
	qoderQuotaFreshFor = 5 * time.Minute

	QoderQuotaCredentialDevice = "device"
	QoderQuotaCredentialPAT    = "pat"
)

// QoderQuotaPool is one credit pool of a Qoder account.
type QoderQuotaPool struct {
	// Kind is plan, addon, org or package.
	Kind      string     `json:"kind"`
	Name      string     `json:"name,omitempty"`
	Total     float64    `json:"total"`
	Used      float64    `json:"used"`
	Remaining float64    `json:"remaining"`
	Available bool       `json:"available"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// QoderQuotaSnapshot is the Qoder credits state of one account, stored in
// accounts.extra.qoder_quota for the admin account list.
type QoderQuotaSnapshot struct {
	PlanType string           `json:"plan_type,omitempty"`
	Unit     string           `json:"unit,omitempty"`
	Exceeded bool             `json:"exceeded"`
	ResetsAt *time.Time       `json:"resets_at,omitempty"`
	Pools    []QoderQuotaPool `json:"pools"`
	// Credential is the credential that answered: device or pat.
	Credential string    `json:"credential"`
	CheckedAt  time.Time `json:"checked_at"`
}

// QoderQuota returns the account's Qoder credits. Unless refresh is set, a
// snapshot checked within qoderQuotaFreshFor is served without calling Qoder.
// The query uses the same credential as chat (device login first, personal
// access token as fallback) and retries once with a fresh token on 401/403.
func (s *QoderGatewayService) QoderQuota(ctx context.Context, account *Account, refresh bool) (*QoderQuotaSnapshot, error) {
	if account == nil || !account.IsQoder() {
		return nil, infraerrors.BadRequest("QODER_ACCOUNT_REQUIRED", "not a qoder account")
	}
	if !refresh {
		if cached := StoredQoderQuota(account); cached != nil && time.Since(cached.CheckedAt) < qoderQuotaFreshFor {
			return cached, nil
		}
	}
	doer := s.doerFor(account)
	region := qoderAccountRegion(account)
	var rejected *qoderSession
	for attempt := 0; ; attempt++ {
		session, err := s.resolveSession(ctx, account, doer, rejected)
		if err != nil {
			return nil, qoderQuotaError(err, false)
		}
		quota, err := qoderproxy.FetchQuota(ctx, doer, region, session.identity.JobToken)
		if err != nil {
			if attempt == 0 && qoderproxy.IsAuthError(err) {
				rejected = session
				continue
			}
			return nil, qoderQuotaError(err, !session.device)
		}
		credential := QoderQuotaCredentialPAT
		if session.device {
			credential = QoderQuotaCredentialDevice
		}
		snapshot := newQoderQuotaSnapshot(quota, credential, time.Now())
		s.storeQoderQuota(ctx, account, snapshot)
		return snapshot, nil
	}
}

// StoredQoderQuota decodes the snapshot kept in accounts.extra, if any.
func StoredQoderQuota(account *Account) *QoderQuotaSnapshot {
	if account == nil || account.Extra == nil {
		return nil
	}
	raw, ok := account.Extra[qoderQuotaExtraKey]
	if !ok || raw == nil {
		return nil
	}
	if stored, ok := raw.(*QoderQuotaSnapshot); ok {
		return stored
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var snapshot QoderQuotaSnapshot
	if json.Unmarshal(body, &snapshot) != nil || snapshot.CheckedAt.IsZero() {
		return nil
	}
	return &snapshot
}

func (s *QoderGatewayService) storeQoderQuota(ctx context.Context, account *Account, snapshot *QoderQuotaSnapshot) {
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra[qoderQuotaExtraKey] = snapshot
	if s.accountRepo == nil || account.ID <= 0 {
		return
	}
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{qoderQuotaExtraKey: snapshot}); err != nil {
		logger.FromContext(ctx).Warn("qoder.quota_persist_failed", zap.Int64("account_id", account.ID), zap.Error(err))
	}
}

func newQoderQuotaSnapshot(quota qoderproxy.Quota, credential string, now time.Time) *QoderQuotaSnapshot {
	snapshot := &QoderQuotaSnapshot{
		PlanType:   quota.UserType,
		Exceeded:   quota.Exceeded,
		ResetsAt:   qoderQuotaTime(quota.ExpiresAt),
		Pools:      []QoderQuotaPool{},
		Credential: credential,
		CheckedAt:  now.UTC(),
	}
	add := func(kind, name string, pool *qoderproxy.QuotaPool) {
		if pool == nil {
			return
		}
		if snapshot.Unit == "" {
			snapshot.Unit = pool.Unit
		}
		snapshot.Pools = append(snapshot.Pools, QoderQuotaPool{
			Kind:      kind,
			Name:      name,
			Total:     pool.Total,
			Used:      pool.Used,
			Remaining: pool.Remaining,
			Available: pool.Available,
			ExpiresAt: qoderQuotaTime(pool.ExpiresAt),
		})
	}
	add("plan", "", quota.Plan)
	add("addon", "", quota.AddOn)
	add("org", "", quota.Org)
	for i := range quota.Packages {
		add("package", quota.Packages[i].Name, &quota.Packages[i].QuotaPool)
	}
	return snapshot
}

func qoderQuotaTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	utc := t.UTC()
	return &utc
}

// qoderQuotaError maps a failed quota query onto an admin API error. Upstream
// 401/403 must not surface as-is: the admin client treats those statuses as
// its own session expiring.
func qoderQuotaError(err error, pat bool) error {
	if errors.Is(err, errQoderNoCredential) {
		return infraerrors.BadRequest("QODER_NO_CREDENTIAL", "Qoder account has no personal access token or device login")
	}
	if errors.Is(err, errQoderDeviceLoginExpired) {
		return infraerrors.BadRequest("QODER_DEVICE_LOGIN_EXPIRED", "Qoder device login expired; sign in again")
	}
	message := sanitizeUpstreamErrorMessage(err.Error())
	if pat && qoderproxy.IsAuthError(err) {
		return infraerrors.New(http.StatusBadGateway, "QODER_QUOTA_PAT_REJECTED",
			"Qoder refused the quota query for a personal access token; use device login to see credits")
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return infraerrors.New(http.StatusGatewayTimeout, "QODER_QUOTA_TIMEOUT", "Qoder quota query timed out")
	}
	return infraerrors.New(http.StatusBadGateway, "QODER_QUOTA_FAILED", "Qoder quota query failed: "+message)
}
