package service

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Quality checks use the same selectors and admission gates as gateway requests.
// The checker supplies only its due-account set and per-run failure exclusions.
type qualityAccountScheduler interface {
	Select(context.Context, int64, string, map[int64]struct{}) (*qualityScheduledAccount, error)
}

type qualityScheduledAccount struct {
	Account *Account
	Model   string
	Context context.Context
	Release func()
}

type qualityScheduledRequestContextKey struct{}

type gatewayQualityAccountScheduler struct {
	gateway     *GatewayService
	openai      *OpenAIGatewayService
	concurrency *ConcurrencyService
}

func (s *gatewayQualityAccountScheduler) selectOnce(ctx context.Context, groupID int64, model string, excluded map[int64]struct{}) (_ *qualityScheduledAccount, err error) {
	if s == nil || s.gateway == nil || s.openai == nil {
		return nil, ErrQualityRunUnavailable
	}
	group, resolvedID, err := s.gateway.resolveGatewayGroup(ctx, &groupID)
	if err != nil {
		return nil, err
	}
	if group == nil || resolvedID == nil || !group.IsActive() {
		return nil, ErrGroupNotFound
	}
	ctx = s.gateway.withGroupContext(ctx, group)
	platform := group.Platform
	model = qualityRequestedModel(&Account{Platform: platform}, model)
	publicModel := model
	if platform == PlatformComposite {
		decision, ok, routeErr := s.gateway.resolveCompositeRouteDecision(ctx, group, model, CompositeRouteEndpointChatCompletions)
		if routeErr != nil {
			return nil, routeErr
		}
		if !ok {
			return nil, ErrNoAvailableAccounts
		}
		ctx = WithCompositeRouteDecision(ctx, decision)
		platform, model = decision.TargetPlatform, decision.UpstreamModel
	}
	policyModel := model
	mapping, restricted := s.gateway.ResolveChannelMappingAndRestrict(ctx, resolvedID, model)
	if restricted {
		return nil, ErrNoAvailableAccounts
	}
	if mapping.MappedModel != "" {
		model = mapping.MappedModel
	}
	var selection *AccountSelectionResult
	compatible := (&Account{Platform: platform}).IsOpenAICompatible()
	if compatible {
		policyModel = model
		ctx, _ = s.openai.WithOpenAIRequestPricingContext(ctx, resolvedID)
		selection, _, err = s.openai.SelectAccountWithSchedulerForCapability(ctx, resolvedID, "", "", model, excluded, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, true, platform)
	} else {
		selection, err = s.gateway.SelectAccountWithLoadAwareness(ctx, resolvedID, "", publicModel, excluded, "", 0)
	}
	if err != nil {
		return nil, err
	}
	if selection == nil || selection.Account == nil {
		return nil, ErrNoAvailableAccounts
	}
	defer func() {
		if err != nil && ctx.Err() == nil {
			err = &qualityAdmissionError{accountID: selection.Account.ID, err: err}
		}
	}()
	release, err := s.acquire(ctx, selection)
	if err != nil {
		return nil, err
	}
	admitted := false
	defer func() {
		if !admitted {
			release()
		}
	}()
	ctx = ContextWithSelectionProfitGate(ctx, selection)
	account := selection.Account
	var vetoed bool
	if compatible {
		account, vetoed, _ = s.openai.ProfitControlVetoLatest(ctx, account)
	} else {
		account, vetoed, _ = s.gateway.GatewayProfitControlVetoLatest(ctx, account)
	}
	if vetoed || account == nil {
		return nil, ErrNoAvailableAccounts
	}
	ctx = context.WithValue(ctx, qualityPolicyModelContextKey{}, policyModel)
	// Re-read after any queue wait. No stale snapshot may authorize an upstream call.
	account, err = s.gateway.accountRepo.GetByID(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	if account == nil || !account.IsSchedulableForModelWithContext(ctx, policyModel) || !accountAllowsQualityModel(account, policyModel) {
		return nil, ErrNoAvailableAccounts
	}
	if vetoed, _ := openAIProfitControlVetoReason(ctx, account); vetoed {
		return nil, ErrNoAvailableAccounts
	}
	if compatible {
		if !isOpenAICompatibleAccountEligibleForRequest(ctx, account, NormalizeOpenAICompatiblePlatform(platform), model, false, OpenAIEndpointCapabilityChatCompletions) || !parentHealthyForShadow(account, s.openai.parentAccountLookup(ctx)) || s.openai.isOpenAIAccountRequestRuntimeBlocked(account, model) {
			return nil, ErrNoAvailableAccounts
		}
	} else if !parentHealthyForShadow(account, s.openai.parentAccountLookup(ctx)) {
		return nil, ErrNoAvailableAccounts
	}
	if s.gateway.rateLimitService != nil {
		usable, precheckErr := s.gateway.rateLimitService.PreCheckUsage(ctx, account, policyModel)
		if precheckErr != nil {
			return nil, precheckErr
		}
		if !usable {
			return nil, ErrNoAvailableAccounts
		}
	}
	ctx = context.WithValue(ctx, qualityScheduledRequestContextKey{}, true)
	admitted = true
	return &qualityScheduledAccount{Account: account, Model: model, Context: ctx, Release: release}, nil
}

func (s *gatewayQualityAccountScheduler) acquire(ctx context.Context, selection *AccountSelectionResult) (func(), error) {
	if selection.Acquired {
		if selection.ReleaseFunc == nil {
			return func() {}, nil
		}
		var once sync.Once
		return func() { once.Do(selection.ReleaseFunc) }, nil
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	plan := selection.WaitPlan
	if plan == nil || s.concurrency == nil {
		return nil, ErrNoAvailableAccounts
	}
	canWait, err := s.concurrency.IncrementAccountWaitCount(ctx, selection.Account.ID, plan.MaxWaiting)
	if err != nil {
		return nil, err
	}
	if !canWait {
		return nil, ErrNoAvailableAccounts
	}
	defer s.concurrency.DecrementAccountWaitCount(context.WithoutCancel(ctx), selection.Account.ID)
	waitCtx, cancel := context.WithTimeout(ctx, plan.Timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := s.concurrency.AcquireAccountSlot(waitCtx, selection.Account.ID, plan.MaxConcurrency)
		if err != nil {
			return nil, err
		}
		if result != nil && result.Acquired {
			var once sync.Once
			return func() { once.Do(result.ReleaseFunc) }, nil
		}
		select {
		case <-waitCtx.Done():
			return nil, waitCtx.Err()
		case <-ticker.C:
		}
	}
}

func releaseQualityAccount(selected *qualityScheduledAccount) {
	if selected != nil && selected.Release != nil {
		selected.Release()
	}
}

func (c *GroupQualityChecker) selectQualityAccount(ctx context.Context, groupID int64, candidates []qualityCandidate, excluded map[int64]struct{}) (qualityCandidate, *qualityScheduledAccount, error) {
	if c.scheduler == nil {
		return qualityCandidate{}, nil, ErrQualityRunUnavailable
	}
	if len(candidates) == 0 {
		return qualityCandidate{}, nil, ErrQualityRunNoAccounts
	}
	available := make(map[int64]qualityCandidate, len(candidates))
	for _, candidate := range candidates {
		available[candidate.id] = candidate
	}
	modelRejected := false
	// Each rejection grows excluded; the real scheduler owns ordering and capacity.
	for ctx.Err() == nil {
		selected, err := c.scheduler.Select(ctx, groupID, candidates[0].modelID, excluded)
		if err != nil {
			if errors.Is(err, ErrNoAvailableAccounts) {
				if modelRejected {
					return qualityCandidate{}, nil, ErrQualityRunModelNotAllowed
				}
				return qualityCandidate{}, nil, ErrQualityRunNoAccounts
			}
			return qualityCandidate{}, nil, err
		}
		if selected == nil || selected.Account == nil {
			releaseQualityAccount(selected)
			return qualityCandidate{}, nil, ErrQualityRunNoAccounts
		}
		candidate, due := available[selected.Account.ID]
		_, repeated := excluded[selected.Account.ID]
		if repeated {
			releaseQualityAccount(selected)
			return qualityCandidate{}, nil, ErrQualityRunNoAccounts
		}
		if due && accountAllowsQualityModel(selected.Account, qualityPolicyModel(selected.Context, selected.Account, selected.Model)) {
			if selected.Context == nil {
				selected.Context = ctx
			}
			return candidate, selected, nil
		}
		if due && accountAcceptsQualityPrompt(selected.Account) {
			modelRejected = true
		}
		excluded[selected.Account.ID] = struct{}{}
		releaseQualityAccount(selected)
	}
	return qualityCandidate{}, nil, ctx.Err()
}

type qualityAdmissionError struct {
	accountID int64
	err       error
}

func (e *qualityAdmissionError) Error() string { return "Quality account admission failed" }
func (e *qualityAdmissionError) Unwrap() error { return e.err }

func (s *gatewayQualityAccountScheduler) Select(ctx context.Context, groupID int64, model string, excluded map[int64]struct{}) (*qualityScheduledAccount, error) {
	if excluded == nil {
		excluded = make(map[int64]struct{})
	}
	for ctx.Err() == nil {
		selected, err := s.selectOnce(ctx, groupID, model, excluded)
		var rejected *qualityAdmissionError
		if !errors.As(err, &rejected) {
			return selected, err
		}
		if _, repeated := excluded[rejected.accountID]; repeated {
			return nil, ErrNoAvailableAccounts
		}
		excluded[rejected.accountID] = struct{}{}
	}
	return nil, ctx.Err()
}

func (s *gatewayQualityAccountScheduler) Report(account *Account, model string, result qualityProbeResult) {
	if account != nil && account.IsOpenAICompatible() {
		s.openai.ReportOpenAIAccountScheduleResult(account, model, result.Status == "success", nil)
		if result.Status == "failed" {
			s.openai.RecordOpenAIAccountSwitch()
		}
	}
}
