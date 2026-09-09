package usage

import "context"

type Account struct {
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	CodexHome     string `json:"codexHome,omitempty"`
	ClaudeHome    string `json:"claudeHome,omitempty"`
	ExpectedEmail string `json:"expectedEmail,omitempty"`
}
type Window struct {
	UsedPercent        *float64 `json:"usedPercent"`
	WindowDurationMins *float64 `json:"windowDurationMins"`
	ResetsAt           *float64 `json:"resetsAt"`
}
type Limit struct {
	ID        string  `json:"id"`
	Name      string  `json:"name,omitempty"`
	Primary   *Window `json:"primary"`
	Secondary *Window `json:"secondary"`
}
type Identity struct {
	Type     string `json:"type"`
	Email    string `json:"email"`
	PlanType string `json:"planType"`
}
type Summary struct {
	LifetimeTokens  *float64 `json:"lifetimeTokens"`
	PeakDailyTokens *float64 `json:"peakDailyTokens"`
}
type DailyBucket struct {
	StartDate string  `json:"startDate"`
	Tokens    float64 `json:"tokens"`
}
type AccountUsage struct {
	Name              string        `json:"name"`
	Provider          string        `json:"provider"`
	Identity          *Identity     `json:"identity,omitempty"`
	Limits            []Limit       `json:"limits,omitempty"`
	Summary           *Summary      `json:"summary,omitempty"`
	DailyUsageBuckets []DailyBucket `json:"dailyUsageBuckets,omitempty"`
	Warnings          []string      `json:"warnings,omitempty"`
	Error             string        `json:"error,omitempty"`
}
type Result struct {
	ScannedAt string         `json:"scannedAt"`
	Accounts  []AccountUsage `json:"accounts"`
	Warnings  []string       `json:"warnings,omitempty"`
	Stale     bool           `json:"stale,omitempty"`
	Loading   bool           `json:"loading,omitempty"`
}
type Reader func(context.Context, string, string) (AccountUsage, error)

func Number(n float64) *float64 { return &n }
