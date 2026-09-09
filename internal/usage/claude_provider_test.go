package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
)

type fakeClaudeClient struct {
	status int
	body   string
	req    *http.Request
}

func (f *fakeClaudeClient) Do(r *http.Request) (*http.Response, error) {
	f.req = r
	return &http.Response{StatusCode: f.status, Body: io.NopCloser(strings.NewReader(f.body)), Header: make(http.Header), Request: r}, nil
}

func TestNormalizeClaudeLimitsIncludesInactiveFableScope(t *testing.T) {
	data := map[string]any{"five_hour": map[string]any{"utilization": 10.0, "resets_at": "2025-01-01T00:00:00Z"}, "limits": []any{
		map[string]any{"kind": "session", "scope": map[string]any{"model": map[string]any{"display_name": "Fable"}, "surface": false}, "percent": 20.0, "resets_at": "2025-01-01T00:00:00Z", "group": "session"},
		map[string]any{"kind": "weekly_scoped", "group": "weekly", "scope": map[string]any{"model": map[string]any{"display_name": "Fable"}}, "percent": 30.0, "resets_at": "2025-01-01T00:00:00Z"},
	}}
	limits := normalizeClaudeLimits(data)
	if len(limits) != 2 || limits[1].Primary == nil || limits[1].Secondary == nil {
		t.Fatalf("limits=%+v", limits)
	}
}

func TestReadClaudeUsageSecretAndRedirectHandling(t *testing.T) {
	home := t.TempDir()
	_ = os.WriteFile(home+"/.credentials.json", []byte(`{"claudeAiOauth":{"accessToken":"super-secret"}}`), 0600)
	t.Setenv("CLAUDE_CONFIG_DIR", "ambient")
	t.Setenv("ANTHROPIC_API_KEY", "ambient-secret")
	oldRun, oldClient := runClaudeCommand, newClaudeHTTPClient
	defer func() { runClaudeCommand = oldRun; newClaudeHTTPClient = oldClient }()
	runs := 0
	runClaudeCommand = func(context.Context, string, []string, []string) ([]byte, error) {
		runs++
		return []byte(`{"loggedIn":true,"authMethod":"claude.ai","email":"a@example.com","orgId":"org","subscriptionType":"pro"}`), nil
	}
	client := &fakeClaudeClient{status: http.StatusFound, body: `{"location":"https://evil.example"}`}
	newClaudeHTTPClient = func() claudeHTTPDoer { return client }
	_, err := ReadClaudeUsage(context.Background(), home, os.Args[0])
	if err == nil || strings.Contains(err.Error(), "super-secret") || strings.Contains(err.Error(), "ambient-secret") {
		t.Fatalf("error=%v", err)
	}
	expectedRuns := 1
	if runtime.GOOS == "darwin" {
		expectedRuns = 2
	}
	if runs != expectedRuns {
		t.Fatalf("auth calls=%d", runs)
	}
}

func TestReadClaudeUsageSuccessScopesTokenAndEnvironment(t *testing.T) {
	home := t.TempDir()
	_ = os.WriteFile(home+"/.credentials.json", []byte(`{"claudeAiOauth":{"accessToken":"real-token"}}`), 0600)
	t.Setenv("ANTHROPIC_API_KEY", "ambient-secret")
	t.Setenv("ANTHROPIC_BASE_URL", "https://evil.example")
	oldRun, oldClient := runClaudeCommand, newClaudeHTTPClient
	defer func() { runClaudeCommand = oldRun; newClaudeHTTPClient = oldClient }()
	var gotEnv []string
	runClaudeCommand = func(_ context.Context, exe string, args []string, env []string) ([]byte, error) {
		if exe == "/usr/bin/security" {
			if !strings.Contains(strings.Join(args, " "), claudeService(home)) {
				t.Fatal("wrong keychain service")
			}
			return []byte(`{"claudeAiOauth":{"accessToken":"real-token"}}`), nil
		}
		gotEnv = env
		return []byte(`{"loggedIn":true,"authMethod":"claude.ai","email":"a@example.com","orgId":"org","subscriptionType":"pro"}`), nil
	}
	client := &fakeClaudeClient{status: http.StatusOK, body: `{"five_hour":{"utilization":12.5,"resets_at":"2025-01-01T00:00:00Z"}}`}
	newClaudeHTTPClient = func() claudeHTTPDoer { return client }
	u, err := ReadClaudeUsage(context.Background(), home, os.Args[0])
	if err != nil || u.Identity == nil || u.Identity.Email != "a@example.com" {
		t.Fatalf("usage=%+v err=%v", u, err)
	}
	if client.req.URL.String() != claudeEndpoint || client.req.Header.Get("Authorization") != "Bearer real-token" {
		t.Fatalf("request=%v auth=%q", client.req.URL, client.req.Header.Get("Authorization"))
	}
	b, _ := json.Marshal(u)
	if strings.Contains(string(b), "real-token") {
		t.Fatal("token exposed in result")
	}
	joined := strings.Join(gotEnv, "\n")
	if !strings.Contains(joined, "CLAUDE_CONFIG_DIR="+home) || strings.Contains(joined, "ambient-secret") || strings.Contains(joined, "ANTHROPIC_BASE_URL=") {
		t.Fatal("profile environment did not isolate ambient credentials")
	}
}

func TestReadClaudeUsageDetectsIdentityChange(t *testing.T) {
	home := t.TempDir()
	_ = os.WriteFile(home+"/.credentials.json", []byte(`{"claudeAiOauth":{"accessToken":"token"}}`), 0600)
	oldRun, oldClient := runClaudeCommand, newClaudeHTTPClient
	defer func() { runClaudeCommand = oldRun; newClaudeHTTPClient = oldClient }()
	count := 0
	runClaudeCommand = func(context.Context, string, []string, []string) ([]byte, error) {
		count++
		email := "a@example.com"
		if count > 1 {
			email = "changed@example.com"
		}
		return []byte(`{"loggedIn":true,"authMethod":"claude.ai","email":"` + email + `","orgId":"org"}`), nil
	}
	newClaudeHTTPClient = func() claudeHTTPDoer { return &fakeClaudeClient{status: http.StatusOK, body: `{}`} }
	_, err := ReadClaudeUsage(context.Background(), home, os.Args[0])
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("identity error=%v", err)
	}
}

func TestReadClaudeUsageRejectsOversizedCredential(t *testing.T) {
	home := t.TempDir()
	b := bytes.Repeat([]byte("x"), 65537)
	_ = os.WriteFile(home+"/.credentials.json", b, 0600)
	oldRun := runClaudeCommand
	defer func() { runClaudeCommand = oldRun }()
	runClaudeCommand = func(context.Context, string, []string, []string) ([]byte, error) { return nil, io.ErrUnexpectedEOF }
	_, err := claudeCredential(context.Background(), home)
	if err == nil {
		t.Fatal("expected credential failure")
	}
}

func TestClaudeClientRejectsRedirect(t *testing.T) {
	client, ok := newClaudeHTTPClient().(*http.Client)
	if !ok {
		t.Fatal("unexpected HTTP client")
	}
	request, _ := http.NewRequest("GET", "https://untrusted.example", nil)
	if client.CheckRedirect == nil || client.CheckRedirect(request, nil) != http.ErrUseLastResponse {
		t.Fatal("redirect could forward credential")
	}
}
