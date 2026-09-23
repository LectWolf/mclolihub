//go:build integration

package repository

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

// skipNearDayBoundary 避免在跨日前后运行时，昨日同时段窗口与测试数据的相对位置不稳定。
func (s *UsageLogRepoSuite) skipNearDayBoundary(now, todayStart time.Time) {
	if now.Sub(todayStart) < 5*time.Minute || todayStart.Add(24*time.Hour).Sub(now) < 5*time.Minute {
		s.T().Skip("too close to a day boundary for a stable same-period comparison")
	}
}

type dashboardTestLog struct {
	createdAt             time.Time
	inputTokens           int
	outputTokens          int
	totalCost             float64
	actualCost            float64
	accountStatsCost      *float64
	accountRateMultiplier *float64
	durationMs            int
}

func (s *UsageLogRepoSuite) createDashboardTestLogs(user *service.User, apiKey *service.APIKey, account *service.Account, logs ...dashboardTestLog) {
	for _, l := range logs {
		duration := l.durationMs
		_, err := s.repo.Create(s.ctx, &service.UsageLog{
			UserID:                user.ID,
			APIKeyID:              apiKey.ID,
			AccountID:             account.ID,
			RequestID:             uuid.New().String(),
			Model:                 "claude-3",
			InputTokens:           l.inputTokens,
			OutputTokens:          l.outputTokens,
			TotalCost:             l.totalCost,
			ActualCost:            l.actualCost,
			AccountStatsCost:      l.accountStatsCost,
			AccountRateMultiplier: l.accountRateMultiplier,
			DurationMs:            &duration,
			CreatedAt:             l.createdAt,
		})
		s.Require().NoError(err, "create usage log")
	}
}

func (s *UsageLogRepoSuite) TestDashboardStats_AccountHealthMatchesListFilters() {
	now := time.Now()
	future := now.Add(10 * time.Minute)
	past := now.Add(-10 * time.Minute)

	mustCreateAccount(s.T(), s.client, &service.Account{Name: "h-available"})
	// 已过期的限流不影响可调度；过载与账号列表的 active 筛选口径一致，仍计入可调度。
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "h-expired-limit", RateLimitedAt: &past, RateLimitResetAt: &past})
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "h-overloaded", OverloadUntil: &future})
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "h-rate-limited", RateLimitedAt: &now, RateLimitResetAt: &future})
	pausedLimited := mustCreateAccount(s.T(), s.client, &service.Account{Name: "h-paused-limited", RateLimitedAt: &now, RateLimitResetAt: &future})
	s.Require().NoError(s.client.Account.UpdateOneID(pausedLimited.ID).SetSchedulable(false).Exec(s.ctx))
	// 临时不可调度优先于限流。
	tempLimited := mustCreateAccount(s.T(), s.client, &service.Account{Name: "h-temp", RateLimitedAt: &now, RateLimitResetAt: &future})
	s.Require().NoError(s.client.Account.UpdateOneID(tempLimited.ID).SetTempUnschedulableUntil(future).Exec(s.ctx))
	paused := mustCreateAccount(s.T(), s.client, &service.Account{Name: "h-paused"})
	s.Require().NoError(s.client.Account.UpdateOneID(paused.ID).SetSchedulable(false).Exec(s.ctx))
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "h-error", Status: service.StatusError})
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "h-balance", Status: service.StatusBalanceInsufficient})
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "h-inactive", Status: accountStatusInactive})
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "h-legacy-disabled", Status: service.StatusDisabled})

	stats, err := s.repo.GetDashboardStats(s.ctx)
	s.Require().NoError(err, "GetDashboardStats")
	health := stats.AccountHealth

	accountRepo := newAccountRepositoryWithSQL(s.client, s.tx, nil)
	listTotal := func(status string) int64 {
		_, page, err := accountRepo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 1}, "", "", status, "", 0, "")
		s.Require().NoError(err, "ListWithFilters status=%s", status)
		return page.Total
	}
	s.Require().Equal(listTotal(service.StatusActive), health.Available, "available")
	s.Require().Equal(listTotal("rate_limited"), health.RateLimited, "rate_limited")
	s.Require().Equal(listTotal("temp_unschedulable"), health.TempUnschedulable, "temp_unschedulable")
	s.Require().Equal(listTotal("unschedulable"), health.Unschedulable, "unschedulable")
	s.Require().Equal(listTotal(service.StatusError), health.Error, "error")
	s.Require().Equal(listTotal(service.StatusBalanceInsufficient), health.BalanceInsufficient, "balance_insufficient")
	s.Require().Equal(listTotal(accountStatusInactive), health.Inactive, "inactive")

	s.Require().GreaterOrEqual(health.Available, int64(3))
	s.Require().GreaterOrEqual(health.RateLimited, int64(2))
	s.Require().GreaterOrEqual(health.Other, int64(1), "legacy disabled status falls into other")
	sum := health.Available + health.RateLimited + health.TempUnschedulable + health.Unschedulable +
		health.Error + health.BalanceInsufficient + health.Inactive + health.Other
	s.Require().Equal(stats.TotalAccounts, sum, "health categories must add up to total accounts")
}

func (s *UsageLogRepoSuite) TestDashboardStats_YesterdaySamePeriodFromAggregates() {
	now := time.Now().UTC()
	todayStart := truncateToDayUTC(now)
	s.skipNearDayBoundary(now, todayStart)
	yesterdayStart := todayStart.AddDate(0, 0, -1)
	yesterdayEnd := now.AddDate(0, 0, -1)

	aggRepo := newDashboardAggregationRepositoryWithSQL(s.tx)
	s.Require().NoError(aggRepo.UpdateAggregationWatermark(s.ctx, now), "UpdateAggregationWatermark")
	base, err := s.repo.GetDashboardStats(s.ctx)
	s.Require().NoError(err, "GetDashboardStats base")

	user := mustCreateUser(s.T(), s.client, &service.User{Email: "yday-agg@test.com"})
	apiKey := mustCreateApiKey(s.T(), s.client, &service.APIKey{UserID: user.ID, Key: "sk-yday-agg", Name: "k"})
	account := mustCreateAccount(s.T(), s.client, &service.Account{Name: "acc-yday-agg"})
	statsCost := 0.4
	s.createDashboardTestLogs(user, apiKey, account,
		// 窗口起点附近：落在整点小时桶（小时预聚合部分）。
		dashboardTestLog{createdAt: yesterdayStart.Add(time.Second), inputTokens: 10, outputTokens: 20, totalCost: 1.0, actualCost: 0.8, durationMs: 100},
		// 窗口终点前：落在末尾不足一小时的部分（直接扫 usage_logs），账号成本取 account_stats_cost。
		dashboardTestLog{createdAt: yesterdayEnd.Add(-time.Second), inputTokens: 5, outputTokens: 5, totalCost: 0.5, actualCost: 0.6, accountStatsCost: &statsCost, durationMs: 100},
		// 晚于对比截止时刻：即使与窗口末尾同属一个小时桶也不能计入。
		dashboardTestLog{createdAt: yesterdayEnd.Add(2 * time.Minute), inputTokens: 100, outputTokens: 100, totalCost: 9.0, actualCost: 9.0, durationMs: 100},
		dashboardTestLog{createdAt: todayStart.Add(time.Second), inputTokens: 1, outputTokens: 1, totalCost: 0.1, actualCost: 0.1, durationMs: 300},
		dashboardTestLog{createdAt: now.Add(-time.Minute), inputTokens: 1, outputTokens: 1, totalCost: 0.1, actualCost: 0.1, durationMs: 500},
	)
	s.Require().NoError(aggRepo.AggregateRange(s.ctx, yesterdayStart.Add(-time.Hour), now.Add(2*time.Minute)), "AggregateRange")

	stats, err := s.repo.GetDashboardStats(s.ctx)
	s.Require().NoError(err, "GetDashboardStats")
	s.Require().Equal(base.YesterdayRequests+2, stats.YesterdayRequests, "YesterdayRequests")
	s.Require().Equal(base.YesterdayTokens+40, stats.YesterdayTokens, "YesterdayTokens")
	s.Require().InDelta(base.YesterdayCost+1.5, stats.YesterdayCost, 1e-9, "YesterdayCost")
	s.Require().InDelta(base.YesterdayActualCost+1.4, stats.YesterdayActualCost, 1e-9, "YesterdayActualCost")
	// 1.0（account_stats_cost 为空时回退 total_cost）+ 0.4
	s.Require().InDelta(base.YesterdayAccountCost+1.4, stats.YesterdayAccountCost, 1e-9, "YesterdayAccountCost")

	wantTodayAvg := (base.TodayAverageDurationMs*float64(base.TodayRequests) + 800) / float64(base.TodayRequests+2)
	s.Require().Equal(base.TodayRequests+2, stats.TodayRequests, "TodayRequests")
	s.Require().InDelta(wantTodayAvg, stats.TodayAverageDurationMs, 1e-6, "TodayAverageDurationMs")
}

func (s *UsageLogRepoSuite) TestDashboardStats_YesterdaySamePeriodCappedByWatermark() {
	now := time.Now().UTC()
	todayStart := truncateToDayUTC(now)
	s.skipNearDayBoundary(now, todayStart)
	// 预聚合只推进到 3 分钟前时，今日数据也只覆盖到那里，昨日对比窗口需同步截止。
	watermark := now.Add(-3 * time.Minute)
	if watermark.Before(todayStart) {
		s.T().Skip("watermark would fall on the previous day")
	}

	aggRepo := newDashboardAggregationRepositoryWithSQL(s.tx)
	s.Require().NoError(aggRepo.UpdateAggregationWatermark(s.ctx, watermark), "UpdateAggregationWatermark")
	base, err := s.repo.GetDashboardStats(s.ctx)
	s.Require().NoError(err, "GetDashboardStats base")

	user := mustCreateUser(s.T(), s.client, &service.User{Email: "yday-wm@test.com"})
	apiKey := mustCreateApiKey(s.T(), s.client, &service.APIKey{UserID: user.ID, Key: "sk-yday-wm", Name: "k"})
	account := mustCreateAccount(s.T(), s.client, &service.Account{Name: "acc-yday-wm"})
	s.createDashboardTestLogs(user, apiKey, account,
		dashboardTestLog{createdAt: watermark.AddDate(0, 0, -1).Add(-time.Minute), inputTokens: 1, outputTokens: 1, totalCost: 0.2, actualCost: 0.2, durationMs: 10},
		dashboardTestLog{createdAt: watermark.AddDate(0, 0, -1).Add(time.Minute), inputTokens: 1, outputTokens: 1, totalCost: 0.3, actualCost: 0.3, durationMs: 10},
	)

	stats, err := s.repo.GetDashboardStats(s.ctx)
	s.Require().NoError(err, "GetDashboardStats")
	s.Require().Equal(base.YesterdayRequests+1, stats.YesterdayRequests, "only the log before watermark-24h counts")
	s.Require().InDelta(base.YesterdayActualCost+0.2, stats.YesterdayActualCost, 1e-9)
}

func (s *UsageLogRepoSuite) TestDashboardStatsWithRange_YesterdaySamePeriodAndTodayAverage() {
	now := time.Now().UTC()
	todayStart := truncateToDayUTC(now)
	s.skipNearDayBoundary(now, todayStart)
	yesterdayStart := todayStart.AddDate(0, 0, -1)
	yesterdayEnd := now.AddDate(0, 0, -1)
	rangeStart := todayStart.AddDate(0, 0, -7)

	base, err := s.repo.GetDashboardStatsWithRange(s.ctx, rangeStart, now.Add(time.Second))
	s.Require().NoError(err, "GetDashboardStatsWithRange base")

	user := mustCreateUser(s.T(), s.client, &service.User{Email: "yday-range@test.com"})
	apiKey := mustCreateApiKey(s.T(), s.client, &service.APIKey{UserID: user.ID, Key: "sk-yday-range", Name: "k"})
	account := mustCreateAccount(s.T(), s.client, &service.Account{Name: "acc-yday-range"})
	multiplier := 2.0
	s.createDashboardTestLogs(user, apiKey, account,
		dashboardTestLog{createdAt: yesterdayStart.Add(time.Second), inputTokens: 3, outputTokens: 4, totalCost: 0.5, actualCost: 0.4, accountRateMultiplier: &multiplier, durationMs: 100},
		dashboardTestLog{createdAt: yesterdayEnd.Add(-time.Minute), inputTokens: 1, outputTokens: 2, totalCost: 0.25, actualCost: 0.2, durationMs: 100},
		dashboardTestLog{createdAt: yesterdayEnd.Add(2 * time.Minute), inputTokens: 50, outputTokens: 50, totalCost: 5, actualCost: 5, durationMs: 100},
		dashboardTestLog{createdAt: now.Add(-2 * time.Minute), inputTokens: 1, outputTokens: 1, totalCost: 0.1, actualCost: 0.1, durationMs: 200},
		dashboardTestLog{createdAt: now.Add(-time.Minute), inputTokens: 1, outputTokens: 1, totalCost: 0.1, actualCost: 0.1, durationMs: 400},
	)

	stats, err := s.repo.GetDashboardStatsWithRange(s.ctx, rangeStart, now.Add(time.Second))
	s.Require().NoError(err, "GetDashboardStatsWithRange")
	s.Require().Equal(base.YesterdayRequests+2, stats.YesterdayRequests, "YesterdayRequests")
	s.Require().Equal(base.YesterdayTokens+10, stats.YesterdayTokens, "YesterdayTokens")
	s.Require().InDelta(base.YesterdayCost+0.75, stats.YesterdayCost, 1e-9, "YesterdayCost")
	s.Require().InDelta(base.YesterdayActualCost+0.6, stats.YesterdayActualCost, 1e-9, "YesterdayActualCost")
	// 0.5 × 2（账号倍率）+ 0.25
	s.Require().InDelta(base.YesterdayAccountCost+1.25, stats.YesterdayAccountCost, 1e-9, "YesterdayAccountCost")

	wantTodayAvg := (base.TodayAverageDurationMs*float64(base.TodayRequests) + 600) / float64(base.TodayRequests+2)
	s.Require().InDelta(wantTodayAvg, stats.TodayAverageDurationMs, 1e-6, "TodayAverageDurationMs")
}

func (s *UsageLogRepoSuite) TestGetUsageTrend_IncludesAccountCost() {
	day := truncateToDayUTC(time.Now()).AddDate(0, 0, -3)
	dayEnd := day.Add(24 * time.Hour)
	sumAccountCost := func(granularity string, userID int64) float64 {
		trend, err := s.repo.GetUsageTrendWithFilters(s.ctx, day, dayEnd, granularity, userID, 0, 0, 0, "", nil, nil, nil)
		s.Require().NoError(err, "GetUsageTrendWithFilters %s", granularity)
		total := 0.0
		for _, point := range trend {
			total += point.AccountCost
		}
		return total
	}
	baseHour, baseDay := sumAccountCost("hour", 0), sumAccountCost("day", 0)

	user := mustCreateUser(s.T(), s.client, &service.User{Email: "trend-acct@test.com"})
	apiKey := mustCreateApiKey(s.T(), s.client, &service.APIKey{UserID: user.ID, Key: "sk-trend-acct", Name: "k"})
	account := mustCreateAccount(s.T(), s.client, &service.Account{Name: "acc-trend-acct"})
	multiplier := 1.5
	statsCost := 0.5
	s.createDashboardTestLogs(user, apiKey, account,
		dashboardTestLog{createdAt: day.Add(10*time.Hour + 15*time.Minute), inputTokens: 1, outputTokens: 1, totalCost: 1.0, actualCost: 0.8, accountRateMultiplier: &multiplier, durationMs: 10},
		dashboardTestLog{createdAt: day.Add(10*time.Hour + 45*time.Minute), inputTokens: 1, outputTokens: 1, totalCost: 2.0, actualCost: 2.0, accountStatsCost: &statsCost, durationMs: 10},
	)

	// 带用户过滤时直接扫描 usage_logs。
	s.Require().InDelta(2.0, sumAccountCost("hour", user.ID), 1e-9, "raw trend account_cost")
	userTrend, err := s.repo.GetUserUsageTrendByUserID(s.ctx, user.ID, day, dayEnd, "day")
	s.Require().NoError(err, "GetUserUsageTrendByUserID")
	s.Require().Len(userTrend, 1)
	s.Require().InDelta(2.0, userTrend[0].AccountCost, 1e-9, "user trend account_cost")

	// 无过滤时读取预聚合表（小时 / 天）。
	aggRepo := newDashboardAggregationRepositoryWithSQL(s.tx)
	s.Require().NoError(aggRepo.AggregateRange(s.ctx, day, dayEnd), "AggregateRange")
	s.Require().InDelta(baseHour+2.0, sumAccountCost("hour", 0), 1e-9, "hourly aggregated account_cost")
	s.Require().InDelta(baseDay+2.0, sumAccountCost("day", 0), 1e-9, "daily aggregated account_cost")
}
