package service

import (
	"errors"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	GroupHealthUnknown             = "unknown"
	GroupHealthHealthy             = "healthy"
	GroupHealthUnavailable         = "unavailable"
	GroupHealthBalanceInsufficient = "balance_insufficient"
	AccountRuntimeActive           = "active"
	AccountRuntimeFailed           = "failed"
	AccountRuntimeBalance          = "balance_insufficient"
	RouteModeFixed                 = "fixed"
	RouteModeCheapest              = "cheapest"
	RouteModeFastest               = "fastest"
	RouteModeCustom                = "custom"
	RoutePlatformAuto              = "auto"
	RoutePlatformOpenAI            = PlatformOpenAI
	RoutePlatformAnthropic         = PlatformAnthropic
	RoutePlatformGrok              = PlatformGrok
	DefaultGroupProbeInterval      = 10 * time.Minute
	ImmediateProbeCooldown         = 10 * time.Minute
)

var accountProbeBackoff = [...]time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute}

func NormalizeGroupProbeConfig(model string, intervalSeconds int) (string, int, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		model = "gpt-5.6-sol"
	}
	if intervalSeconds == 0 {
		intervalSeconds = int(DefaultGroupProbeInterval / time.Second)
	}
	if intervalSeconds < 30 || intervalSeconds > 3600 {
		return "", 0, errors.New("probe_interval_seconds must be between 30 and 3600")
	}
	return model, intervalSeconds, nil
}

type AccountHealth struct {
	ID                   int64
	RuntimeStatus        string
	Schedulable          bool
	RetryStep            int
	LastImmediateProbeAt *time.Time
}

func NextAccountProbeAt(now time.Time, retryStep int, normalInterval time.Duration) time.Time {
	if retryStep >= 0 && retryStep < len(accountProbeBackoff) {
		return now.Add(accountProbeBackoff[retryStep])
	}
	if normalInterval < 30*time.Second || normalInterval > time.Hour {
		normalInterval = DefaultGroupProbeInterval
	}
	return now.Add(normalInterval)
}

func CanTriggerImmediateProbe(last *time.Time, now time.Time) bool {
	return last == nil || !now.Before(last.Add(ImmediateProbeCooldown))
}

// DeriveGroupHealth keeps an account-level failure from failing a group while another account is healthy.
func DeriveGroupHealth(accounts []AccountHealth) string {
	hasBalance := false
	for _, account := range accounts {
		if account.RuntimeStatus == AccountRuntimeActive && account.Schedulable {
			return GroupHealthHealthy
		}
		if account.RuntimeStatus == AccountRuntimeBalance {
			hasBalance = true
		}
	}
	if hasBalance {
		return GroupHealthBalanceInsufficient
	}
	return GroupHealthUnavailable
}

type GroupRouteCandidate struct {
	GroupID         int64
	RateMultiplier  float64
	Healthy         bool
	ProbeEnabled    bool
	Disabled        bool
	CustomPosition  int
	AdminSortOrder  int
	RealTTFTP50MS   int
	RealTTFTSamples int
	ProbeTTFTMS     int
}

type GroupRouteHealth struct {
	Healthy         bool
	ProbeTTFTMS     int
	RealTTFTP50MS   int
	RealTTFTSamples int
}

func ValidateRouteMode(mode string) error {
	switch mode {
	case RouteModeFixed, RouteModeCheapest, RouteModeFastest, RouteModeCustom:
		return nil
	default:
		return errors.New("invalid API key route mode")
	}
}

func normalizeRoutePlatform(platform string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "" {
		return RoutePlatformAuto
	}
	return platform
}

func ValidateRoutePlatform(platform string) error {
	switch normalizeRoutePlatform(platform) {
	case RoutePlatformAuto, RoutePlatformOpenAI, RoutePlatformAnthropic, RoutePlatformGrok:
		return nil
	default:
		return errors.New("invalid API key route platform")
	}
}

func routePlatformMatches(scope, platform string) bool {
	scope = normalizeRoutePlatform(scope)
	return scope == RoutePlatformAuto || strings.EqualFold(scope, strings.TrimSpace(platform))
}

// RankGroupCandidates applies health, disabled and max-rate gates, then deterministically sorts candidates.
func RankGroupCandidates(mode string, maxRate *float64, candidates []GroupRouteCandidate) ([]GroupRouteCandidate, error) {
	if err := ValidateRouteMode(mode); err != nil {
		return nil, err
	}
	if maxRate != nil && (math.IsNaN(*maxRate) || math.IsInf(*maxRate, 0) || *maxRate < 0) {
		return nil, errors.New("max rate multiplier must be finite and non-negative")
	}
	out := make([]GroupRouteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if maxRate != nil && candidate.RateMultiplier > *maxRate {
			continue
		}
		if mode != RouteModeFixed {
			if candidate.Disabled || (candidate.ProbeEnabled && !candidate.Healthy) {
				continue
			}
		}
		out = append(out, candidate)
	}
	if mode == RouteModeFixed {
		return out, nil
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch mode {
		case RouteModeCheapest:
			if a.RateMultiplier != b.RateMultiplier {
				return a.RateMultiplier < b.RateMultiplier
			}
		case RouteModeCustom:
			if a.CustomPosition != b.CustomPosition {
				return a.CustomPosition < b.CustomPosition
			}
		case RouteModeFastest:
			aReal, bReal := a.RealTTFTSamples > 0, b.RealTTFTSamples > 0
			if aReal != bReal {
				return aReal
			}
			if aReal && a.RealTTFTP50MS != b.RealTTFTP50MS {
				return a.RealTTFTP50MS < b.RealTTFTP50MS
			}
			if !aReal && !bReal {
				aProbe, bProbe := a.ProbeTTFTMS > 0, b.ProbeTTFTMS > 0
				if aProbe != bProbe {
					return aProbe
				}
				if aProbe && a.ProbeTTFTMS != b.ProbeTTFTMS {
					return a.ProbeTTFTMS < b.ProbeTTFTMS
				}
			}
		}
		if a.AdminSortOrder != b.AdminSortOrder {
			return a.AdminSortOrder < b.AdminSortOrder
		}
		return a.GroupID < b.GroupID
	})
	return out, nil
}

// CanFailover reports whether replay is safe and appropriate for a dynamic text request.
func CanFailover(dynamic, textEndpoint, semanticStarted, retryable bool) bool {
	return dynamic && textEndpoint && !semanticStarted && retryable
}
