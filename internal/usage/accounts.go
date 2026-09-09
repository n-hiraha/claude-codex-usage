package usage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

func LoadAccounts(path string) ([]Account, error) {
	if path == "" {
		home := os.Getenv("CODEX_HOME")
		if home == "" {
			h, e := os.UserHomeDir()
			if e != nil {
				return nil, e
			}
			home = filepath.Join(h, ".codex")
		}
		home, e := filepath.Abs(home)
		if e != nil {
			return nil, e
		}
		return []Account{{Name: "default", Provider: "codex", CodexHome: home}}, nil
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, errors.New("アカウント設定を読み込めません")
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil || len(data) > 65536 {
		return nil, errors.New("アカウント設定は64KB以下で指定してください")
	}
	var cfg struct {
		Accounts []Account `json:"accounts"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return nil, errors.New("アカウント設定をJSONとして読み込めません")
	}
	if len(cfg.Accounts) < 1 || len(cfg.Accounts) > 20 {
		return nil, errors.New("accountsには1〜20件の設定が必要です")
	}
	names, homes := map[string]bool{}, map[string]bool{}
	for i := range cfg.Accounts {
		a := &cfg.Accounts[i]
		if strings.TrimSpace(a.Name) == "" || utf8.RuneCountInString(a.Name) > 80 || names[a.Name] {
			return nil, errors.New("アカウント名は重複しない1〜80文字で指定してください")
		}
		var home *string
		switch a.Provider {
		case "codex":
			home = &a.CodexHome
			a.ClaudeHome = ""
		case "claude":
			home = &a.ClaudeHome
			a.CodexHome = ""
		default:
			return nil, errors.New("providerにはcodexまたはclaudeを指定してください")
		}
		if !filepath.IsAbs(*home) {
			return nil, errors.New("アカウントのホームは絶対パスで指定してください")
		}
		if a.ExpectedEmail != "" && strings.TrimSpace(a.ExpectedEmail) == "" {
			return nil, errors.New("expectedEmailには空でない文字列を指定してください")
		}
		*home = filepath.Clean(*home)
		if real, e := filepath.EvalSymlinks(*home); e == nil {
			*home = real
		}
		key := a.Provider + ":" + *home
		if homes[key] {
			return nil, errors.New("同じプロバイダーのホームが重複しています")
		}
		homes[key] = true
		names[a.Name] = true
	}
	return cfg.Accounts, nil
}

type Readers struct {
	Codex, Claude                     Reader
	CodexExecutable, ClaudeExecutable string
}

func Collect(ctx context.Context, accounts []Account, readers Readers) Result {
	if readers.Codex == nil {
		readers.Codex = ReadCodexUsage
	}
	if readers.Claude == nil {
		readers.Claude = ReadClaudeUsage
	}
	result := Result{ScannedAt: time.Now().UTC().Format(time.RFC3339Nano), Accounts: []AccountUsage{}}
	identities := map[string]bool{}
	// Serialize credential refreshes; do not merge separate account quotas.
	for _, a := range accounts {
		reader, home, exe := readers.Codex, a.CodexHome, readers.CodexExecutable
		if a.Provider == "claude" {
			reader, home, exe = readers.Claude, a.ClaudeHome, readers.ClaudeExecutable
		}
		data, err := reader(ctx, home, exe)
		if err != nil {
			data = AccountUsage{Error: "取得できません。CLI・指定ホームのログイン状態・ネットワークを確認してください。"}
		} else if a.ExpectedEmail != "" && (data.Identity == nil || !strings.EqualFold(a.ExpectedEmail, data.Identity.Email)) {
			data = AccountUsage{Error: "ログイン中のメールアドレスがexpectedEmailと一致しません。usageを表示しません。"}
		}
		data.Name = a.Name
		data.Provider = a.Provider
		if data.Identity != nil && data.Identity.Email != "" {
			key := a.Provider + ":" + strings.ToLower(data.Identity.Email)
			if identities[key] && len(result.Warnings) == 0 {
				result.Warnings = append(result.Warnings, "同じプロバイダー・メールアドレスのログインが複数あります。値は合算しません。")
			}
			identities[key] = true
		}
		result.Accounts = append(result.Accounts, data)
	}
	return result
}
