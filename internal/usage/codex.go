package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const codexMaxLine = 256 * 1024

func ReadCodexUsage(parent context.Context, home, executable string) (AccountUsage, error) {
	result := AccountUsage{Provider: "codex"}
	if home == "" {
		return result, providerError("CODEX_HOME is required")
	}
	exe, err := resolveCodexExecutable(executable)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "app-server", "--listen", "stdio://")
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Env = append(envWithout("CODEX_HOME"), "CODEX_HOME="+home)
	cmd.Stderr = io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		return result, providerError("Unable to start Codex app-server")
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		return result, providerError("Codex app-server has no stdio transport")
	}
	if err := cmd.Start(); err != nil {
		_ = in.Close()
		return result, providerError("Unable to start Codex app-server")
	}
	stopClosing := context.AfterFunc(ctx, func() { _ = out.Close(); _ = in.Close() })
	defer stopClosing()
	defer func() {
		_ = in.Close()
		_ = out.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()
	proto := &codexProtocol{in: in, scan: bufio.NewReaderSize(out, codexMaxLine+1), total: 0}
	if _, err = proto.request(ctx, 1, "initialize", map[string]any{"clientInfo": map[string]any{"name": "claude-codex-usage", "title": "Claude Codex Usage", "version": "0.4.0"}, "capabilities": nil}); err != nil {
		return result, providerError("Codex initialization failed")
	}
	if err = proto.notify(ctx, "initialized"); err != nil {
		return result, providerError("Codex app-server communication failed")
	}
	first, err := proto.request(ctx, 2, "account/read", map[string]any{"refreshToken": false})
	if err != nil {
		return result, providerError("Codex account read failed")
	}
	before := codexIdentity(first)
	warnings := []string{}
	if before.Type == "chatgpt" && before.Email == "" {
		warnings = append(warnings, "Account email unavailable; identity changes cannot be fully verified")
	}
	limits := []Limit{}
	var summary *Summary
	var daily []DailyBucket
	if before.Type == "" && before.Email == "" && before.PlanType == "" {
		warnings = append(warnings, "Codex account unavailable")
	} else {
		if v, e := proto.request(ctx, 3, "account/rateLimits/read", map[string]any{}); e == nil {
			limits = codexLimits(v)
		} else {
			warnings = append(warnings, "Rate limits unavailable")
		}
		if v, e := proto.request(ctx, 4, "account/usage/read", map[string]any{}); e == nil {
			summary, daily = codexUsage(v)
		} else {
			warnings = append(warnings, "Token usage unavailable")
		}
	}
	last, err := proto.request(ctx, 5, "account/read", map[string]any{"refreshToken": false})
	if err != nil {
		return result, providerError("Codex account recheck failed")
	}
	if before != codexIdentity(last) {
		return result, providerError("Account changed while reading Codex usage")
	}
	if proto.malformed {
		warnings = append(warnings, "Ignored malformed app-server message")
	}
	result.Identity, result.Limits, result.Summary, result.DailyUsageBuckets, result.Warnings = &before, limits, summary, daily, warnings
	return result, nil
}

func resolveCodexExecutable(s string) (string, error) {
	if s == "" {
		s = "codex"
	}
	return resolveExecutableNamed(s)
}
func resolveExecutableNamed(s string) (string, error) {
	p, e := exec.LookPath(s)
	if e != nil {
		return "", providerError("provider executable unavailable")
	}
	return p, nil
}

type codexProtocol struct {
	in        io.Writer
	scan      *bufio.Reader
	total     int
	malformed bool
}

func (p *codexProtocol) notify(ctx context.Context, method string) error {
	return p.write(ctx, map[string]any{"method": method, "params": map[string]any{}})
}
func (p *codexProtocol) write(ctx context.Context, v any) error {
	b, _ := json.Marshal(v)
	b = append(b, '\n')
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	_, err := p.in.Write(b)
	return err
}
func (p *codexProtocol) request(ctx context.Context, id int, method string, params any) (map[string]any, error) {
	if err := p.write(ctx, map[string]any{"method": method, "id": id, "params": params}); err != nil {
		return nil, err
	}
	for {
		part, err := p.scan.ReadSlice('\n')
		line := string(part)
		if err != nil {
			return nil, err
		}
		p.total += len(line)
		if p.total > maxProviderOutput || len(line) > codexMaxLine {
			return nil, providerError("Codex response too large")
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var msg map[string]any
		if json.Unmarshal([]byte(line), &msg) != nil {
			p.malformed = true
			continue
		}
		if _, ok := msg["method"].(string); ok && msg["id"] != nil {
			if err := p.write(ctx, map[string]any{"id": msg["id"], "error": map[string]any{"code": -32601, "message": "Method not supported"}}); err != nil {
				return nil, err
			}
			continue
		}
		if float64(id) == numberID(msg["id"]) {
			if msg["error"] != nil {
				return nil, providerError("Codex request failed")
			}
			if r, ok := msg["result"].(map[string]any); ok {
				return r, nil
			}
			return map[string]any{}, nil
		}
	}
}
func numberID(v any) float64 { n, _ := v.(float64); return n }
func codexIdentity(v map[string]any) Identity {
	a, _ := v["account"].(map[string]any)
	return Identity{Type: textValue(a["type"]), Email: textValue(a["email"]), PlanType: textValue(a["planType"])}
}
func codexBucket(v any) *Window {
	m, _ := v.(map[string]any)
	if m == nil {
		return nil
	}
	return &Window{UsedPercent: finiteNumber(m["usedPercent"]), WindowDurationMins: finiteNumber(m["windowDurationMins"]), ResetsAt: finiteNumber(m["resetsAt"])}
}
func codexLimits(v map[string]any) []Limit {
	var out []Limit
	src, ok := v["rateLimitsByLimitId"].(map[string]any)
	if !ok {
		if x, yes := v["rateLimits"].(map[string]any); yes {
			src = map[string]any{"default": x}
		}
	}
	for id, raw := range src {
		m, _ := raw.(map[string]any)
		if m == nil {
			continue
		}
		got := textValue(m["limitId"])
		if got == "" {
			got = id
		}
		out = append(out, Limit{ID: got, Name: textValue(m["limitName"]), Primary: codexBucket(m["primary"]), Secondary: codexBucket(m["secondary"])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func codexUsage(v map[string]any) (*Summary, []DailyBucket) {
	var s *Summary
	if m, ok := v["summary"].(map[string]any); ok {
		s = &Summary{LifetimeTokens: finiteNumber(m["lifetimeTokens"]), PeakDailyTokens: finiteNumber(m["peakDailyTokens"])}
	}
	var d []DailyBucket
	if a, ok := v["dailyUsageBuckets"].([]any); ok {
		for _, x := range a {
			m, _ := x.(map[string]any)
			date := textValue(m["startDate"])
			if len(date) == 10 && date[4] == '-' && date[7] == '-' && validDate(date) && finiteNumber(m["tokens"]) != nil {
				d = append(d, DailyBucket{StartDate: date, Tokens: *finiteNumber(m["tokens"])})
			}
		}
	}
	sort.Slice(d, func(i, j int) bool { return d[i].StartDate < d[j].StartDate })
	return s, d
}

func validDate(s string) bool { _, err := time.Parse("2006-01-02", s); return err == nil }
