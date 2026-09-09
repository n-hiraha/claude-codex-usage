package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const claudeEndpoint = "https://api.anthropic.com/api/oauth/usage"

var safeUser = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type claudeRunFunc func(context.Context, string, []string, []string) ([]byte, error)

var runClaudeCommand claudeRunFunc = func(ctx context.Context, exe string, args []string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = env
	cmd.WaitDelay = 500 * time.Millisecond
	var b bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &b, n: 65537}
	err := cmd.Run()
	if err != nil || b.Len() > 65536 {
		return nil, providerError("Claude subscription login unavailable")
	}
	return b.Bytes(), nil
}

type claudeHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

var newClaudeHTTPClient = func() claudeHTTPDoer {
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func ReadClaudeUsage(parent context.Context, home, executable string) (AccountUsage, error) {
	result := AccountUsage{Provider: "claude"}
	if home == "" {
		return result, providerError("Claude home is required")
	}
	home = cleanHome(home)
	exe, err := resolveClaudeExecutable(executable)
	if err != nil {
		return result, err
	}
	env := envWithout("CLAUDE_SECURESTORAGE_CONFIG_DIR", "CLAUDE_CONFIG_DIR", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", "ANTHROPIC_BASE_URL")
	defaultHome, _ := os.UserHomeDir()
	defaultHome = cleanHome(filepath.Join(defaultHome, ".claude"))
	if home != defaultHome {
		env = append(env, "CLAUDE_CONFIG_DIR="+home)
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	auth := func() (claudeAuth, error) {
		b, e := runClaudeCommand(ctx, exe, []string{"auth", "status", "--json"}, env)
		if e != nil {
			return claudeAuth{}, providerError("Claude subscription login unavailable")
		}
		var v map[string]any
		if json.Unmarshal(b, &v) != nil {
			return claudeAuth{}, providerError("Claude subscription login unavailable")
		}
		if v["loggedIn"] != true || textValue(v["authMethod"]) != "claude.ai" || textValue(v["email"]) == "" {
			return claudeAuth{}, providerError("Claude subscription login unavailable")
		}
		return claudeAuth{email: textValue(v["email"]), org: textValue(v["orgId"]), plan: textValue(v["subscriptionType"])}, nil
	}
	before, e := auth()
	if e != nil {
		return result, e
	}
	token, e := claudeCredential(ctx, home)
	if e != nil {
		return result, e
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, claudeEndpoint, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("anthropic-beta", "oauth-2025-04-20")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "claude-codex-usage/0.4.0")
	client := newClaudeHTTPClient()
	resp, e := client.Do(request)
	if e != nil {
		return result, providerError("Claude usage request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 429 {
			return result, providerError("Claude usage rate limited; retry later")
		}
		return result, providerError("Claude usage unavailable")
	}
	body, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if e != nil || len(body) > 65536 {
		return result, providerError("Claude usage response too large")
	}
	var data map[string]any
	if json.Unmarshal(body, &data) != nil {
		return result, providerError("Claude usage request failed")
	}
	after, e := auth()
	if e != nil {
		return result, e
	}
	token2, e := claudeCredential(ctx, home)
	if e != nil || before.email != after.email || before.org != after.org || token != token2 {
		return result, providerError("Claude account changed during usage read")
	}
	limits := normalizeClaudeLimits(data)
	warnings := []string{}
	if limits[0].Primary == nil && limits[0].Secondary == nil {
		warnings = []string{"Claude rate limit windows unavailable"}
	}
	result.Identity = &Identity{Type: "claude.ai", Email: before.email, PlanType: before.plan}
	result.Limits = limits
	result.Warnings = warnings
	return result, nil
}

type claudeAuth struct{ email, org, plan string }

func resolveClaudeExecutable(s string) (string, error) {
	if s == "" {
		s = "claude"
	}
	return resolveExecutableNamed(s)
}
func claudeCredential(ctx context.Context, home string) (string, error) { // The macOS keychain is intentionally queried only for the selected profile.
	if runtime.GOOS == "darwin" {
		account := os.Getenv("USER")
		if !safeUser.MatchString(account) {
			if u, e := user.Current(); e == nil {
				account = u.Username
			}
		}
		if safeUser.MatchString(account) {
			if b, e := runClaudeCommand(ctx, "/usr/bin/security", []string{"find-generic-password", "-a", account, "-s", claudeService(home), "-w"}, nil); e == nil {
				if token := parseClaudeCredential(b); token != "" {
					return token, nil
				}
			}
		}
	}
	f, e := os.Open(filepath.Join(home, ".credentials.json"))
	if e == nil {
		defer f.Close()
		b, readErr := io.ReadAll(io.LimitReader(f, 65537))
		if readErr == nil && len(b) <= 65536 {
			if token := parseClaudeCredential(b); token != "" {
				return token, nil
			}
		}
	}
	return "", providerError("Claude credential unavailable")
}

func parseClaudeCredential(b []byte) string {
	var v map[string]any
	if json.Unmarshal(bytes.TrimSpace(b), &v) != nil {
		return ""
	}
	c, _ := v["claudeAiOauth"].(map[string]any)
	return textValue(c["accessToken"])
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if len(p) > w.n {
		p = p[:w.n]
	}
	n, err := w.w.Write(p)
	w.n -= n
	if w.n <= 0 {
		return n, providerError("provider output too large")
	}
	return n, err
}

func normalizeClaudeLimits(data map[string]any) []Limit {
	rows, _ := data["limits"].([]any)
	window := func(v any, mins float64) *Window {
		m, _ := v.(map[string]any)
		if m == nil {
			return nil
		}
		r := time.Time{}
		if s := textValue(m["resets_at"]); s != "" {
			r, _ = time.Parse(time.RFC3339, s)
		}
		var rp *float64
		if !r.IsZero() {
			x := float64(r.Unix())
			rp = &x
		}
		return &Window{UsedPercent: finiteNumber(m["utilization"]), WindowDurationMins: Number(mins), ResetsAt: rp}
	}
	rowWindow := func(row any, mins float64) *Window {
		m, _ := row.(map[string]any)
		if m == nil {
			return nil
		}
		return window(map[string]any{"utilization": m["percent"], "resets_at": m["resets_at"]}, mins)
	}
	find := func(kind string, scope bool, group string) any {
		for _, r := range rows {
			m, _ := r.(map[string]any)
			if textValue(m["kind"]) == kind && ((m["scope"] == nil) == scope) && (group == "" || textValue(m["group"]) == group) {
				return r
			}
		}
		return nil
	}
	first := func(k string, mins float64, kind string) *Window {
		if x := window(data[k], mins); x != nil {
			return x
		}
		return rowWindow(find(kind, true, ""), mins)
	}
	out := []Limit{{ID: "claude", Name: "All models", Primary: first("five_hour", 300, "session"), Secondary: first("seven_day", 10080, "weekly_all")}}
	for _, r := range rows {
		m, _ := r.(map[string]any)
		sc, _ := m["scope"].(map[string]any)
		modelMap, _ := sc["model"].(map[string]any)
		model := strings.ToLower(textValue(modelMap["display_name"]))
		if model == "fable" && !truthy(sc["surface"]) {
			var primary, secondary any
			for _, candidate := range rows {
				cm, _ := candidate.(map[string]any)
				cscope, _ := cm["scope"].(map[string]any)
				cmodel, _ := cscope["model"].(map[string]any)
				if strings.ToLower(textValue(cmodel["display_name"])) != "fable" || truthy(cscope["surface"]) {
					continue
				}
				if textValue(cm["kind"]) == "session" {
					primary = candidate
				}
				if textValue(cm["kind"]) == "weekly_scoped" && textValue(cm["group"]) == "weekly" {
					secondary = candidate
				}
			}
			out = append(out, Limit{ID: "claude_fable", Name: "Fable", Primary: rowWindow(primary, 300), Secondary: rowWindow(secondary, 10080)})
			break
		}
	}
	return out
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	default:
		return v != nil
	}
}
