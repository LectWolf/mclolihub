package service

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	groupHealthScanInterval = 10 * time.Second
	groupHealthProbeTimeout = 90 * time.Second
	groupHealthLockTTL      = 2 * time.Minute
	groupHealthScanLimit    = 100
)

// GroupHealthService owns active probes, account recovery and rolling health
// aggregation. Probe requests reuse AccountTestService adapters but never write
// user usage logs.
type GroupHealthService struct {
	repo        GroupHealthRepository
	accountRepo AccountRepository
	testService *AccountTestService
	lockCache   LeaderLockCache
	db          *sql.DB
	instanceID  string

	ctx         context.Context
	cancel      context.CancelFunc
	startOnce   sync.Once
	stopOnce    sync.Once
	lifecycleMu sync.Mutex
	stopped     bool
	wg          sync.WaitGroup
}

func NewGroupHealthService(repo GroupHealthRepository, accountRepo AccountRepository, testService *AccountTestService, lockCache LeaderLockCache, db *sql.DB) *GroupHealthService {
	ctx, cancel := context.WithCancel(context.Background())
	return &GroupHealthService{
		repo: repo, accountRepo: accountRepo, testService: testService,
		lockCache: lockCache, db: db, instanceID: uuid.NewString(), ctx: ctx, cancel: cancel,
	}
}

func (s *GroupHealthService) Start() {
	if s == nil || s.repo == nil || s.accountRepo == nil || s.testService == nil {
		return
	}
	s.startOnce.Do(func() {
		s.lifecycleMu.Lock()
		if s.stopped {
			s.lifecycleMu.Unlock()
			return
		}
		s.wg.Add(1)
		s.lifecycleMu.Unlock()
		go s.loop()
	})
}

func (s *GroupHealthService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		s.lifecycleMu.Lock()
		s.stopped = true
		s.lifecycleMu.Unlock()
		s.cancel()
		s.wg.Wait()
	})
}

func (s *GroupHealthService) loop() {
	defer s.wg.Done()
	s.runCycle()
	ticker := time.NewTicker(groupHealthScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.runCycle()
		}
	}
}

func (s *GroupHealthService) runCycle() {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
	defer cancel()
	release, acquired := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, "group-health:scan", s.instanceID, groupHealthLockTTL)
	if !acquired {
		return
	}
	defer release()
	now := time.Now()
	groups, err := s.repo.ListDueProbeGroups(ctx, now, groupHealthScanLimit)
	if err != nil {
		slog.Warn("group_health: list due groups failed", "error", err)
		return
	}
	for _, target := range groups {
		if ctx.Err() != nil {
			return
		}
		if err := s.probeGroup(ctx, target, now); err != nil {
			slog.Warn("group_health: group probe failed", "group_id", target.GroupID, "error", err)
		}
	}
	retries, err := s.repo.ListDueAccountProbes(ctx, now, groupHealthScanLimit*2)
	if err != nil {
		slog.Warn("group_health: list due account probes failed", "error", err)
		return
	}
	for _, target := range retries {
		if ctx.Err() != nil {
			return
		}
		if err := s.probeRecoveryAccount(ctx, target, now); err != nil {
			slog.Warn("group_health: account recovery probe failed", "account_id", target.AccountID, "error", err)
		}
	}
	if err := s.repo.UpdateRollingMetrics(ctx, now); err != nil {
		slog.Warn("group_health: rolling metric aggregation failed", "error", err)
	}
}

func (s *GroupHealthService) probeGroup(ctx context.Context, target GroupProbeTarget, now time.Time) error {
	release, acquired := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, fmt.Sprintf("group-health:group:%d", target.GroupID), s.instanceID, groupHealthLockTTL)
	if !acquired {
		return nil
	}
	defer release()
	accounts, err := s.accountRepo.ListByGroup(ctx, target.GroupID)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(accounts))
	for i := range accounts {
		ids = append(ids, accounts[i].ID)
	}
	states, err := s.repo.LoadAccountHealth(ctx, ids)
	if err != nil {
		return err
	}
	succeeded := false
	failures := 0
	var successTTFT time.Duration
	for i := range accounts {
		account := accounts[i]
		if !account.Schedulable {
			continue
		}
		if state, ok := states[account.ID]; ok && state.RuntimeStatus == AccountRuntimeBalance {
			continue
		}
		result, run := s.runAccountProbe(ctx, target.GroupID, account.ID, target.Model)
		if !run {
			continue
		}
		if result.Success {
			if err := s.markProbeSuccess(ctx, target.GroupID, account.ID, result, now); err != nil {
				return err
			}
			succeeded = true
			successTTFT = result.TTFT
			break
		}
		failures++
		if err := s.markProbeFailure(ctx, target.GroupID, account.ID, target.Interval, 0, result, now); err != nil {
			return err
		}
	}
	if err := s.repo.RefreshDerivedGroupHealth(ctx, target.GroupID, now); err != nil {
		return err
	}
	snapshot, err := s.repo.Load(ctx, target.GroupID)
	if err != nil {
		return err
	}
	if snapshot == nil {
		snapshot = &GroupHealthSnapshot{GroupID: target.GroupID, Status: GroupHealthUnknown}
	}
	snapshot.LastProbeAt = &now
	snapshot.FailureCount = failures
	next := now.Add(target.Interval)
	snapshot.NextProbeAt = &next
	if succeeded {
		snapshot.Status = GroupHealthHealthy
		snapshot.Reason = "probe_success"
		snapshot.LastSuccessAt = &now
		snapshot.ProbeTTFTMS = int(successTTFT / time.Millisecond)
	}
	return s.repo.Save(ctx, snapshot)
}

func (s *GroupHealthService) probeRecoveryAccount(ctx context.Context, target AccountProbeTarget, now time.Time) error {
	result, run := s.runAccountProbe(ctx, target.GroupID, target.AccountID, target.Model)
	if !run {
		return nil
	}
	if result.Success {
		return s.markProbeSuccess(ctx, target.GroupID, target.AccountID, result, now)
	}
	return s.markProbeFailure(ctx, target.GroupID, target.AccountID, target.Interval, target.RetryStep+1, result, now)
}

func (s *GroupHealthService) runAccountProbe(parent context.Context, groupID, accountID int64, model string) (*AccountProbeResult, bool) {
	release, acquired := tryAcquireSingletonLeaderLock(parent, s.lockCache, s.db, fmt.Sprintf("group-health:account:%d", accountID), s.instanceID, groupHealthLockTTL)
	if !acquired {
		return nil, false
	}
	defer release()
	ctx, cancel := context.WithTimeout(parent, groupHealthProbeTimeout)
	defer cancel()
	result, err := s.testService.RunProbeBackground(ctx, accountID, model)
	if err != nil {
		return &AccountProbeResult{Success: false, ErrorMessage: err.Error()}, true
	}
	return result, true
}

func (s *GroupHealthService) markProbeSuccess(ctx context.Context, groupID, accountID int64, result *AccountProbeResult, now time.Time) error {
	states, err := s.repo.LoadAccountHealth(ctx, []int64{accountID})
	if err != nil {
		return err
	}
	if current, ok := states[accountID]; ok && current.RuntimeStatus == AccountRuntimeBalance {
		return nil
	}
	state := states[accountID]
	state.AccountID = accountID
	state.ProbeGroupID = &groupID
	state.RuntimeStatus = AccountRuntimeActive
	state.Reason = ""
	state.RetryStep = 0
	state.NextProbeAt = nil
	state.LastProbeAt = &now
	state.LastSuccessAt = &now
	if err := s.repo.SaveAccountHealth(ctx, state); err != nil {
		return err
	}
	_ = s.repo.RecordEvent(ctx, GroupHealthEventInput{GroupID: groupID, AccountID: accountID, Kind: "probe", IsProbe: true, Success: true, TTFT: result.TTFT, Total: result.Total, ObservedAt: now})
	if err := s.repo.RefreshDerivedGroupHealth(ctx, groupID, now); err != nil {
		return err
	}
	snapshot, err := s.repo.Load(ctx, groupID)
	if err != nil {
		return err
	}
	if snapshot == nil {
		snapshot = &GroupHealthSnapshot{GroupID: groupID}
	}
	snapshot.Status = GroupHealthHealthy
	snapshot.Reason = "probe_success"
	snapshot.LastProbeAt = &now
	snapshot.LastSuccessAt = &now
	snapshot.ProbeTTFTMS = int(result.TTFT / time.Millisecond)
	return s.repo.Save(ctx, snapshot)
}

func (s *GroupHealthService) markProbeFailure(ctx context.Context, groupID, accountID int64, interval time.Duration, retryStep int, result *AccountProbeResult, now time.Time) error {
	if IsUpstreamBalanceInsufficientError(result.ErrorMessage) {
		if err := s.repo.MarkAccountBalanceInsufficient(ctx, accountID, groupID, result.ErrorMessage, now); err != nil {
			return err
		}
		_ = s.repo.RecordEvent(ctx, GroupHealthEventInput{GroupID: groupID, AccountID: accountID, Kind: "probe", IsProbe: true, Success: false, ErrorCategory: "balance_insufficient", ErrorMessage: result.ErrorMessage, TTFT: result.TTFT, Total: result.Total, ObservedAt: now})
		return s.repo.RefreshDerivedGroupHealth(ctx, groupID, now)
	}
	states, err := s.repo.LoadAccountHealth(ctx, []int64{accountID})
	if err != nil {
		return err
	}
	state := states[accountID]
	if state.RuntimeStatus == AccountRuntimeBalance {
		return nil
	}
	next := NextAccountProbeAt(now, retryStep, interval)
	state.AccountID = accountID
	state.ProbeGroupID = &groupID
	state.RuntimeStatus = AccountRuntimeFailed
	state.Reason = result.ErrorMessage
	state.RetryStep = retryStep
	state.NextProbeAt = &next
	state.LastProbeAt = &now
	state.LastFailureAt = &now
	if err := s.repo.SaveAccountHealth(ctx, state); err != nil {
		return err
	}
	_ = s.repo.RecordEvent(ctx, GroupHealthEventInput{GroupID: groupID, AccountID: accountID, Kind: "probe", IsProbe: true, Success: false, ErrorCategory: "upstream_failure", ErrorMessage: result.ErrorMessage, TTFT: result.TTFT, Total: result.Total, ObservedAt: now})
	return nil
}

// ReportRuntimeFailure records a real request failure and asynchronously runs
// at most one immediate verification per account in each ten-minute window.
func (s *GroupHealthService) ReportRuntimeFailure(groupID, accountID int64, failoverErr *UpstreamFailoverError, semanticStarted bool) {
	if s == nil || s.repo == nil || accountID <= 0 || groupID <= 0 || failoverErr == nil {
		return
	}
	now := time.Now()
	message := strings.TrimSpace(failoverErr.ClientMessage + " " + string(failoverErr.ResponseBody))
	category := "upstream_failure"
	if IsUpstreamBalanceInsufficientError(message) {
		category = "balance_insufficient"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.repo.RecordEvent(ctx, GroupHealthEventInput{GroupID: groupID, AccountID: accountID, Kind: "request", IsProbe: false, Success: false, SemanticStarted: semanticStarted, ErrorCategory: category, ErrorMessage: message, ObservedAt: now})
	if category == "balance_insufficient" {
		if err := s.repo.MarkAccountBalanceInsufficient(ctx, accountID, groupID, message, now); err == nil {
			_ = s.repo.RefreshDerivedGroupHealth(ctx, groupID, now)
		}
		return
	}
	groupTarget, err := s.findGroupProbeTarget(ctx, groupID)
	if err != nil || !groupTarget.ProbeEnabled {
		return
	}
	claimed, err := s.repo.ClaimImmediateProbe(ctx, accountID, groupID, now, ImmediateProbeCooldown)
	if err != nil {
		slog.Warn("group_health: immediate probe claim failed", "account_id", accountID, "error", err)
		return
	}
	states, err := s.repo.LoadAccountHealth(ctx, []int64{accountID})
	if err == nil {
		state := states[accountID]
		if state.RuntimeStatus != AccountRuntimeBalance && state.RuntimeStatus != AccountRuntimeFailed {
			next := now.Add(DefaultGroupProbeInterval)
			state.AccountID = accountID
			state.ProbeGroupID = &groupID
			state.RuntimeStatus = AccountRuntimeFailed
			state.Reason = message
			state.NextProbeAt = &next
			state.LastFailureAt = &now
			_ = s.repo.SaveAccountHealth(ctx, state)
		}
	}
	_ = s.repo.RefreshDerivedGroupHealth(ctx, groupID, now)
	if !claimed {
		return
	}
	if !s.submitProbeTask(func() {
		probeCtx, probeCancel := context.WithTimeout(s.ctx, groupHealthProbeTimeout)
		defer probeCancel()
		result, run := s.runAccountProbe(probeCtx, groupID, accountID, groupTarget.Model)
		if !run {
			return
		}
		probeNow := time.Now()
		if result.Success {
			_ = s.markProbeSuccess(probeCtx, groupID, accountID, result, probeNow)
			return
		}
		_ = s.markProbeFailure(probeCtx, groupID, accountID, groupTarget.Interval, 0, result, probeNow)
		_ = s.repo.RefreshDerivedGroupHealth(probeCtx, groupID, probeNow)
	}) {
		return
	}
}

// submitProbeTask serializes WaitGroup.Add with Stop's transition to stopped.
// This prevents Add/Wait races when a runtime failure arrives during shutdown.
func (s *GroupHealthService) submitProbeTask(task func()) bool {
	if s == nil || task == nil {
		return false
	}
	s.lifecycleMu.Lock()
	if s.stopped || s.ctx.Err() != nil {
		s.lifecycleMu.Unlock()
		return false
	}
	s.wg.Add(1)
	s.lifecycleMu.Unlock()
	go func() {
		defer s.wg.Done()
		task()
	}()
	return true
}

func (s *GroupHealthService) findGroupProbeTarget(ctx context.Context, groupID int64) (GroupProbeTarget, error) {
	return s.repo.GetProbeGroup(ctx, groupID)
}

func (s *GroupHealthService) RestoreBalance(ctx context.Context, accountID int64) error {
	now := time.Now()
	if err := s.repo.RestoreAccountBalance(ctx, accountID, now); err != nil {
		return err
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return err
	}
	for _, groupID := range account.GroupIDs {
		if err := s.repo.RefreshDerivedGroupHealth(ctx, groupID, now); err != nil {
			return err
		}
		_ = s.repo.RecordEvent(ctx, GroupHealthEventInput{
			GroupID: groupID, AccountID: accountID, Kind: "admin_recovery", Success: true,
			ErrorCategory: "balance_restored", ErrorMessage: "admin_balance_restored", ObservedAt: now,
		})
		snapshot, err := s.repo.Load(ctx, groupID)
		if err != nil {
			return err
		}
		if snapshot != nil && snapshot.Status == GroupHealthHealthy {
			snapshot.Reason = "admin_balance_restored"
			snapshot.LastSuccessAt = &now
			if err := s.repo.Save(ctx, snapshot); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *GroupHealthService) LoadMetrics(ctx context.Context, groupIDs []int64) (map[int64]GroupHealthSnapshot, error) {
	return s.repo.LoadMetrics(ctx, groupIDs)
}

func (s *GroupHealthService) LoadTrend(ctx context.Context, groupIDs []int64, start, end time.Time) (map[int64][]GroupHealthTrendBucket, error) {
	return s.repo.LoadTrend(ctx, groupIDs, start, end)
}
