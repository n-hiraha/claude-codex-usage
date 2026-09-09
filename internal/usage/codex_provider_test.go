package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("CODEX_HELPER") != "1" {
		return
	}
	dec := json.NewDecoder(bufio.NewReader(os.Stdin))
	enc := json.NewEncoder(os.Stdout)
	for {
		var req map[string]any
		if dec.Decode(&req) != nil {
			return
		}
		method, _ := req["method"].(string)
		id, _ := req["id"].(float64)
		if method == "initialized" {
			continue
		}
		if os.Getenv("CODEX_HANG") == "1" {
			time.Sleep(time.Hour)
		}
		if os.Getenv("CODEX_OVERSIZE") == "1" {
			_, _ = os.Stdout.Write([]byte(strings.Repeat("x", codexMaxLine+2)))
			time.Sleep(time.Hour)
		}
		if os.Getenv("CODEX_INTERACTIVE") == "1" {
			_ = enc.Encode(map[string]any{"id": 999, "method": "input/request"})
			var refusal map[string]any
			if dec.Decode(&refusal) != nil {
				os.Exit(3)
			}
			detail, _ := refusal["error"].(map[string]any)
			if refusal["id"] != float64(999) || detail["code"] != float64(-32601) {
				os.Exit(4)
			}
		}
		var result any = map[string]any{}
		switch method {
		case "initialize":
			result = map[string]any{}
		case "account/read":
			email := "a@example.com"
			if os.Getenv("CODEX_CHANGED") == "1" && id > 2 {
				email = "b@example.com"
			}
			result = map[string]any{"account": map[string]any{"type": "chatgpt", "email": email, "planType": "plus"}}
		case "account/rateLimits/read":
			result = map[string]any{"rateLimits": map[string]any{"limitId": "chat", "primary": map[string]any{"usedPercent": 12.5}}}
		case "account/usage/read":
			result = map[string]any{"summary": map[string]any{"lifetimeTokens": 4}, "dailyUsageBuckets": []any{map[string]any{"startDate": "2025-01-02", "tokens": 2}, map[string]any{"startDate": "bad", "tokens": 99}}}
		}
		_ = enc.Encode(map[string]any{"id": id, "result": result})
	}
}

func codexHelper(t *testing.T, extra ...string) string {
	t.Helper()
	exe, _ := os.Executable()
	return exe
}

func TestReadCodexUsageProtocolAndWhitelist(t *testing.T) {
	exe := codexHelper(t)
	t.Setenv("CODEX_HELPER", "1")
	u, err := ReadCodexUsage(context.Background(), t.TempDir(), exe)
	if err != nil {
		t.Fatal(err)
	}
	if u.Identity == nil || u.Identity.Email != "a@example.com" || len(u.Limits) != 1 || len(u.DailyUsageBuckets) != 1 {
		t.Fatalf("unexpected usage: %+v", u)
	}
}

func TestReadCodexUsageIdentityChangeAndTimeout(t *testing.T) {
	exe := codexHelper(t)
	t.Setenv("CODEX_HELPER", "1")
	t.Setenv("CODEX_CHANGED", "1")
	_, err := ReadCodexUsage(context.Background(), t.TempDir(), exe)
	if err == nil || !strings.Contains(err.Error(), "Account changed") {
		t.Fatalf("identity error = %v", err)
	}
	t.Setenv("CODEX_HANG", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = ReadCodexUsage(ctx, t.TempDir(), exe)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("timeout failure = %v", err)
	}
}

func TestReadCodexUsageDoesNotLeakProtocolData(t *testing.T) {
	exe := codexHelper(t)
	t.Setenv("CODEX_HELPER", "1")
	t.Setenv("CODEX_CHANGED", "1")
	_, err := ReadCodexUsage(context.Background(), filepath.Join(t.TempDir(), "secret-home"), exe)
	if strings.Contains(err.Error(), "example.com") || strings.Contains(err.Error(), "secret-home") {
		t.Fatalf("error leaked data: %v", err)
	}
}

func TestReadCodexUsageRejectsOversizeAndInteractive(t *testing.T) {
	exe := codexHelper(t)
	t.Setenv("CODEX_HELPER", "1")
	t.Setenv("CODEX_OVERSIZE", "1")
	_, err := ReadCodexUsage(context.Background(), t.TempDir(), exe)
	if err == nil {
		t.Fatal("expected oversized output failure")
	}
	t.Setenv("CODEX_OVERSIZE", "")
	t.Setenv("CODEX_INTERACTIVE", "1")
	_, err = ReadCodexUsage(context.Background(), t.TempDir(), exe)
	if err != nil {
		t.Fatalf("interactive error = %v", err)
	}
}
