package service

import (
	"errors"
	"math"
	"sort"
	"strings"
	"time"
)

// IsGroupPolicyDispatchError reports errors caused by the selected group's
// protocol policy rather than by an upstream account.  Such errors must not
// poison account health or trigger failover/403 circuit breakers.
func IsGroupPolicyDispatchError(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "this group does not allow /v1/messages dispatch") ||
		strings.Contains(message, "group does not allow /v1/messages dispatch")
}

const (
	GroupHealthUnknown             = "unknown"
	GroupHealthHealthy             = "healthy"
	GroupHealthUnavailable         = "unavailable"
	GroupHealthBalanceInsufficient = "balance_insufficient"
	AccountRuntimeActive           = "active"
	AccountRuntimeProbing          = "probing"
	AccountRuntimeUnavailable      = "unavailable"
	AccountRuntimeLegacyFailed     = "failed"
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
	ImmediateProbeCooldown         = 2 * time.Minute
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

func NextAccountProbeAt(now time.Time, retryStep int, _ time.Duration) time.Time {
	if retryStep < 0 {
		retryStep = 0
	}
	if retryStep >= len(accountProbeBackoff) {
		retryStep = len(accountProbeBackoff) - 1
	}
	return now.Add(accountProbeBackoff[retryStep])
}

func NextAccountProbeState(now time.Time, retryStep int) (string, *time.Time) {
	if retryStep < 0 {
		retryStep = 0
	}
	if retryStep >= len(accountProbeBackoff) {
		return AccountRuntimeUnavailable, nil
	}
	next := now.Add(accountProbeBackoff[retryStep])
	return AccountRuntimeProbing, &next
}

func CanTriggerImmediateProbe(last *time.Time, now time.Time) bool {
	return last == nil || !now.Before(last.Add(ImmediateProbeCooldown))
}

// CanRunScheduledProbe allows the ten-minute sweep to recover probing and
// unavailable accounts without advancing their dedicated recovery stage.
func CanRunScheduledProbe(state AccountHealthState) bool {
	return state.RuntimeStatus != AccountRuntimeBalance
}

func IsGroupRouteHealthy(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", GroupHealthUnknown, GroupHealthHealthy:
		return true
	default:
		return false
	}
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
