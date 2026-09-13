package codebuddy

import "time"

const usageDayLayout = "2006-01-02"

// CreditsUsage is the running tally of credits an account has spent through the
// gateway. CodeBuddy bills per request against the catalog multiplier rather
// than per token, so requests are counted next to the credits they consumed.
//
// The tally is an estimate: it only sees traffic that went through this gateway,
// and FetchCredits remains the authoritative balance.
type CreditsUsage struct {
	// Day is the local calendar day the Requests/Credits/Unpriced counters cover.
	Day      string  `json:"day"`
	Requests int64   `json:"requests"`
	Credits  float64 `json:"credits"`
	// Unpriced counts requests whose model was absent from the catalog, making
	// Credits a lower bound for the day rather than an exact figure.
	Unpriced      int64   `json:"unpriced,omitempty"`
	TotalRequests int64   `json:"total_requests"`
	TotalCredits  float64 `json:"total_credits"`
	UpdatedAt     int64   `json:"updated_at"`
}

// Charge folds one upstream call into the tally, rolling the daily counters over
// at local midnight. A model the catalog does not price still counts as a
// request so the daily figure cannot silently understate traffic.
func (u CreditsUsage) Charge(credits float64, priced bool, now time.Time) CreditsUsage {
	day := now.Format(usageDayLayout)
	if u.Day != day {
		u.Day = day
		u.Requests = 0
		u.Credits = 0
		u.Unpriced = 0
	}
	if credits < 0 || !priced {
		credits = 0
	}
	u.Requests++
	u.TotalRequests++
	u.Credits = round2(u.Credits + credits)
	u.TotalCredits = round2(u.TotalCredits + credits)
	if !priced {
		u.Unpriced++
	}
	u.UpdatedAt = now.Unix()
	return u
}
