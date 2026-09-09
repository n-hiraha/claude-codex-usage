package usage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAccountValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	write := func(s string) {
		t.Helper()
		if e := os.WriteFile(path, []byte(s), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(`{"accounts":[{"name":"main","provider":"codex","codexHome":"relative"}]}`)
	if _, e := LoadAccounts(path); e == nil {
		t.Fatal("relative home accepted")
	}
	data, _ := json.Marshal(map[string]any{"accounts": []Account{{Name: "main", Provider: "codex", CodexHome: dir}, {Name: "second", Provider: "codex", CodexHome: dir}}})
	write(string(data))
	if _, e := LoadAccounts(path); e == nil {
		t.Fatal("duplicate home accepted")
	}
	data, _ = json.Marshal(map[string]any{"accounts": []Account{{Name: "main", Provider: "codex", CodexHome: dir}, {Name: "claude", Provider: "claude", ClaudeHome: dir}}})
	write(string(data))
	if a, e := LoadAccounts(path); e != nil || len(a) != 2 {
		t.Fatalf("valid accounts rejected: %v", e)
	}
}
func TestCollectIdentityIsolation(t *testing.T) {
	reader := func(_ context.Context, home, _ string) (AccountUsage, error) {
		if home == "bad" {
			return AccountUsage{}, errors.New("SECRET")
		}
		return AccountUsage{Identity: &Identity{Email: "main@example.com"}, Limits: []Limit{{ID: "codex"}}}, nil
	}
	result := Collect(context.Background(), []Account{{Name: "wrong", Provider: "codex", CodexHome: "ok", ExpectedEmail: "other@example.com"}, {Name: "bad", Provider: "codex", CodexHome: "bad"}, {Name: "good", Provider: "codex", CodexHome: "ok"}}, Readers{Codex: reader})
	if result.Accounts[0].Identity != nil || len(result.Accounts[0].Limits) > 0 || result.Accounts[0].Error == "" {
		t.Fatal("mismatched account leaked")
	}
	if result.Accounts[2].Error != "" {
		t.Fatal("failure affected next profile")
	}
	b, _ := json.Marshal(result)
	if strings.Contains(string(b), "SECRET") {
		t.Fatal("raw error leaked")
	}
}
func TestCachePrivacyAndLock(t *testing.T) {
	dir := t.TempDir()
	accounts := []Account{{Name: "main", Provider: "codex", CodexHome: "/tmp/demo"}}
	started, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	opts := CacheOptions{Directory: dir, Fetch: func(context.Context, []Account) Result {
		close(started)
		<-release
		return Result{ScannedAt: time.Now().Format(time.RFC3339), Accounts: []AccountUsage{
			{Name: "main", Provider: "codex", Identity: &Identity{Email: "private@example.com"}, Summary: &Summary{LifetimeTokens: Number(12345)}, Limits: []Limit{{ID: "codex", Primary: &Window{UsedPercent: Number(71)}}}},
		}}
	}}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, e := Cached(context.Background(), accounts, opts); e != nil {
			t.Error(e)
		}
	}()
	<-started
	loading, e := Cached(context.Background(), accounts, opts)
	if e != nil || !loading.Loading {
		t.Fatalf("concurrent reader: %v %v", loading, e)
	}
	close(release)
	wg.Wait()
	cached, e := Cached(context.Background(), accounts, opts)
	if e != nil || cached.Accounts[0].Identity != nil || cached.Accounts[0].Summary != nil {
		t.Fatalf("cache privacy failed %v", e)
	}
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		b, _ := os.ReadFile(filepath.Join(dir, f.Name()))
		if strings.Contains(string(b), "private@example") || strings.Contains(string(b), "12345") {
			t.Fatal("private fields persisted")
		}
	}
}
func TestGaugeWeeklyClaudeAndReset(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.Local)
	reset := float64(now.Add(6 * time.Hour).Unix())
	result := Result{Accounts: []AccountUsage{{Name: "claude", Provider: "claude", Limits: []Limit{{ID: "claude", Primary: &Window{UsedPercent: Number(24), WindowDurationMins: Number(300)}, Secondary: &Window{UsedPercent: Number(71), WindowDurationMins: Number(10080), ResetsAt: &reset}}, {ID: "claude_fable", Secondary: &Window{UsedPercent: Number(63), WindowDurationMins: Number(10080), ResetsAt: &reset}}}}}}
	out := RenderGauge(result, 223, now)
	for _, s := range []string{"All models", "使用71%", "Fable", "使用63%", "↻9/9 18:00"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %s: %s", s, out)
		}
	}
	if strings.Contains(out, "5h") || strings.Contains(out, "使用24%") {
		t.Fatal("session quota shown")
	}
	result.Accounts[0].Limits = result.Accounts[0].Limits[:1]
	if !strings.Contains(RenderGauge(result, 223, now), "Fable 未提供") {
		t.Fatal("missing quota invented")
	}
}
func TestGaugeWidthsAndSanitization(t *testing.T) {
	r := Demo()
	r.Accounts[0].Name = "\x1b]52;c;SECRET\a#(touch x)\n"
	r.Stale = true
	for width := 20; width <= 230; width++ {
		s := RenderGauge(r, width, time.Now())
		plain := s
		for strings.Contains(plain, "#[") {
			i := strings.Index(plain, "#[")
			j := strings.Index(plain[i:], "]")
			if j < 0 {
				t.Fatal("broken style")
			}
			plain = plain[:i] + plain[i+j+1:]
		}
		if cellWidth(plain) > width {
			t.Fatalf("overflow at %d: %s", width, plain)
		}
		if strings.Count(plain, "[") != strings.Count(plain, "]") {
			t.Fatalf("broken gauge: %s", plain)
		}
		if strings.Contains(s, "#(touch") || strings.Contains(s, "SECRET") || strings.ContainsAny(s, "\x1b\n") {
			t.Fatal("terminal injection")
		}
	}
}
