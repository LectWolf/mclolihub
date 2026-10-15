package service

import (
	"context"
	"database/sql"
	"sort"
)

type qualitySchedulerFunc func(context.Context, int64, string, map[int64]struct{}) (*qualityScheduledAccount, error)

func (f qualitySchedulerFunc) Select(ctx context.Context, groupID int64, model string, excluded map[int64]struct{}) (*qualityScheduledAccount, error) {
	return f(ctx, groupID, model, excluded)
}

func (r *qualityRunAccounts) qualityFixtureAccounts() []*Account {
	if r.account == nil {
		return nil
	}
	account := *r.account
	if account.ID == 0 {
		account.ID = 11
	}
	return []*Account{&account}
}

// Persistence tests explicitly inject a scheduler; production has no fallback selector.
func newQualityTestChecker(db *sql.DB, accounts AccountRepository, tests *AccountTestService, lockCache LeaderLockCache, owner string) *GroupQualityChecker {
	checker := newGroupQualityChecker(db, accounts, tests, lockCache, owner)
	checker.scheduler = qualitySchedulerFunc(func(ctx context.Context, groupID int64, model string, excluded map[int64]struct{}) (*qualityScheduledAccount, error) {
		source, ok := accounts.(interface{ qualityFixtureAccounts() []*Account })
		if !ok {
			return nil, ErrNoAvailableAccounts
		}
		items := source.qualityFixtureAccounts()
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		for _, account := range items {
			if _, skip := excluded[account.ID]; skip {
				continue
			}
			return &qualityScheduledAccount{Account: account, Model: model, Context: ctx, Release: func() {}}, nil
		}
		return nil, ErrNoAvailableAccounts
	})
	return checker
}
