package service

import (
	"context"
	"sync"
	"time"
)

// ProbeAccountFunc is implemented by the platform gateway adapter. It must send
// a low-token probe and return TTFT/total latency without writing user usage logs.
type ProbeAccountFunc func(context.Context, int64, int64, string) (ttft, total time.Duration, err error)

type GroupHealthSnapshot struct {
	GroupID             int64
	Status              string
	Reason              string
	LastProbeAt         *time.Time
	LastSuccessAt       *time.Time
	NextProbeAt         *time.Time
	FailureCount        int
	ProbeTTFTMS         int
	ProbeAvailability6h float64
	ProbeTTFTAvgMS      int
	ProbeTTFTP95MS      int
	ProbeSamples        int
	RealTTFTP50MS       int
	RealTTFTAvgMS       int
	RealTTFTP95MS       int
	RealTTFTSamples     int
	RealAvailability6h  float64
	RealTotalAvgMS      int
	CacheRateOverall    float64
	CacheRate6h         float64
}

type GroupHealthStore interface {
	Load(context.Context, int64) (*GroupHealthSnapshot, error)
	Save(context.Context, *GroupHealthSnapshot) error
	RecordEvent(context.Context, GroupHealthEventInput) error
}

type GroupHealthEventInput struct {
	GroupID, AccountID                int64
	Kind, ErrorCategory, ErrorMessage string
	Success, IsProbe, SemanticStarted bool
	TTFT, Total                       time.Duration
	ObservedAt                        time.Time
}

type GroupProbeTarget struct {
	GroupID      int64
	Platform     string
	Model        string
	Interval     time.Duration
	ProbeEnabled bool
}

type AccountProbeTarget struct {
	AccountID int64
	GroupID   int64
	Platform  string
	Model     string
	Interval  time.Duration
	RetryStep int
}

type AccountHealthState struct {
	AccountID            int64
	ProbeGroupID         *int64
	RuntimeStatus        string
	Reason               string
	RetryStep            int
	NextProbeAt          *time.Time
	LastProbeAt          *time.Time
	LastSuccessAt        *time.Time
	LastFailureAt        *time.Time
	LastImmediateProbeAt *time.Time
}

type GroupHealthMetrics struct {
	GroupHealthSnapshot
	ProbeEnabled bool
}

type GroupHealthTrendBucket struct {
	StartedAt    time.Time `json:"started_at"`
	ProbeSuccess int       `json:"probe_success"`
	ProbeFailure int       `json:"probe_failure"`
	RealSuccess  int       `json:"real_success"`
	RealFailure  int       `json:"real_failure"`
	ProbeTTFTMS  int       `json:"probe_ttft_ms"`
	RealTTFTMS   int       `json:"real_ttft_ms"`
}

// GroupHealthRepository is the durable port used by the background runner and
// the user/admin health views. The smaller GroupHealthStore remains useful for
// state-machine unit tests.
type GroupHealthRepository interface {
	GroupHealthStore
	GetProbeGroup(context.Context, int64) (GroupProbeTarget, error)
	ListDueProbeGroups(context.Context, time.Time, int) ([]GroupProbeTarget, error)
	ListDueAccountProbes(context.Context, time.Time, int) ([]AccountProbeTarget, error)
	LoadAccountHealth(context.Context, []int64) (map[int64]AccountHealthState, error)
	SaveAccountHealth(context.Context, AccountHealthState) error
	MarkAccountBalanceInsufficient(context.Context, int64, int64, string, time.Time) error
	RestoreAccountBalance(context.Context, int64, time.Time) error
	ClaimImmediateProbe(context.Context, int64, int64, time.Time, time.Duration) (bool, error)
	RefreshDerivedGroupHealth(context.Context, int64, time.Time) error
	UpdateRollingMetrics(context.Context, time.Time) error
	LoadMetrics(context.Context, []int64) (map[int64]GroupHealthSnapshot, error)
	LoadTrend(context.Context, []int64, time.Time, time.Time) (map[int64][]GroupHealthTrendBucket, error)
}

type GroupHealthRuntime struct {
	store            GroupHealthStore
	mu               sync.Mutex
	probeLocks       map[int64]struct{}
	accountImmediate map[int64]time.Time
}

func NewGroupHealthRuntime(store GroupHealthStore) *GroupHealthRuntime {
	return &GroupHealthRuntime{store: store, probeLocks: make(map[int64]struct{}), accountImmediate: make(map[int64]time.Time)}
}

// ProbeRound executes accounts serially and stops at the first success. Failed
// accounts remain eligible for their own backoff tasks; the caller can persist
// RetryStep using NextAccountProbeAt.
func (r *GroupHealthRuntime) ProbeRound(ctx context.Context, groupID int64, model string, accounts []AccountHealth, probe ProbeAccountFunc, now time.Time) (*GroupHealthSnapshot, error) {
	if r == nil || r.store == nil || probe == nil {
		return nil, context.Canceled
	}
	r.mu.Lock()
	if _, ok := r.probeLocks[groupID]; ok {
		r.mu.Unlock()
		return r.store.Load(ctx, groupID)
	}
	r.probeLocks[groupID] = struct{}{}
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.probeLocks, groupID); r.mu.Unlock() }()

	snapshot, err := r.store.Load(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		snapshot = &GroupHealthSnapshot{GroupID: groupID, Status: GroupHealthUnknown}
	}
	snapshot.LastProbeAt = &now
	snapshot.FailureCount = 0
	success := false
	for _, account := range accounts {
		if account.RuntimeStatus == AccountRuntimeBalance || !account.Schedulable {
			continue
		}
		ttft, total, probeErr := probe(ctx, groupID, account.ID, model)
		event := GroupHealthEventInput{GroupID: groupID, AccountID: account.ID, Kind: "probe", IsProbe: true, TTFT: ttft, Total: total, ObservedAt: now, Success: probeErr == nil}
		if probeErr != nil {
			event.ErrorMessage = probeErr.Error()
			snapshot.FailureCount++
			_ = r.store.RecordEvent(ctx, event)
			continue
		}
		success = true
		snapshot.LastSuccessAt = &now
		snapshot.ProbeTTFTMS = int(ttft / time.Millisecond)
		_ = r.store.RecordEvent(ctx, event)
		break
	}
	if success {
		snapshot.Status = GroupHealthHealthy
		snapshot.Reason = "probe_success"
	} else {
		snapshot.Status = GroupHealthUnavailable
		snapshot.Reason = "all_accounts_failed"
	}
	next := now.Add(DefaultGroupProbeInterval)
	snapshot.NextProbeAt = &next
	if err := r.store.Save(ctx, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// TriggerImmediateProbe schedules at most one asynchronous verification per
// account in the ten-minute window. It never blocks the caller.
func (r *GroupHealthRuntime) TriggerImmediateProbe(ctx context.Context, groupID, accountID int64, model string, probe ProbeAccountFunc, now time.Time) bool {
	if r == nil || probe == nil {
		return false
	}
	r.mu.Lock()
	if last, ok := r.accountImmediate[accountID]; ok && now.Before(last.Add(ImmediateProbeCooldown)) {
		r.mu.Unlock()
		return false
	}
	r.accountImmediate[accountID] = now
	r.mu.Unlock()
	go func() { _, _, _ = probe(ctx, groupID, accountID, model) }()
	return true
}
