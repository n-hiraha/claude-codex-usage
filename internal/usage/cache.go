package usage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

type CacheOptions struct {
	Directory string
	Now       time.Time
	TTL       time.Duration
	Fetch     func(context.Context, []Account) Result
}
type cachedResult struct {
	SavedAt int64  `json:"savedAt"`
	Result  Result `json:"result"`
}

func QuotaOnly(result Result) Result {
	out := Result{ScannedAt: result.ScannedAt, Accounts: []AccountUsage{}}
	for _, a := range result.Accounts {
		r := AccountUsage{Name: a.Name, Provider: a.Provider, Limits: a.Limits}
		if a.Error != "" {
			r.Limits = nil
			r.Error = "usage unavailable"
		}
		out.Accounts = append(out.Accounts, r)
	}
	return out
}
func Cached(ctx context.Context, accounts []Account, opts CacheOptions) (Result, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if opts.TTL == 0 {
		opts.TTL = time.Minute
		for _, a := range accounts {
			if a.Provider == "claude" {
				opts.TTL = 5 * time.Minute
			}
		}
	}
	if opts.Directory == "" {
		base := os.Getenv("XDG_CACHE_HOME")
		if base == "" {
			home, e := os.UserHomeDir()
			if e != nil {
				return Result{}, e
			}
			base = filepath.Join(home, ".cache")
		}
		opts.Directory = filepath.Join(base, "claude-codex-usage")
	}
	if err := os.MkdirAll(opts.Directory, 0700); err != nil {
		return Result{}, err
	}
	encoded, _ := json.Marshal(accounts)
	hash := sha256.Sum256(encoded)
	key := hex.EncodeToString(hash[:])[:24]
	path := filepath.Join(opts.Directory, key+".json")
	lock := filepath.Join(opts.Directory, key+".lock")
	var cached *cachedResult
	if f, e := os.Open(path); e == nil {
		b, e := io.ReadAll(io.LimitReader(f, 1024*1024+1))
		f.Close()
		var c cachedResult
		if e == nil && len(b) <= 1024*1024 && json.Unmarshal(b, &c) == nil && c.SavedAt > 0 && c.Result.Accounts != nil {
			cached = &c
		}
	}
	if cached != nil {
		age := opts.Now.Sub(time.UnixMilli(cached.SavedAt))
		if age >= 0 && age < opts.TTL {
			return cached.Result, nil
		}
	}
	if err := os.Mkdir(lock, 0700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return Result{}, err
		}
		// Maximum 20 profiles with bounded requests. A crashed owner expires after 15 min.
		if info, e := os.Stat(lock); e == nil && opts.Now.Sub(info.ModTime()) > 15*time.Minute {
			_ = os.Remove(lock)
		}
		if cached != nil {
			cached.Result.Stale = true
			return cached.Result, nil
		}
		out := Result{ScannedAt: opts.Now.UTC().Format(time.RFC3339Nano), Loading: true, Accounts: []AccountUsage{}}
		for _, a := range accounts {
			out.Accounts = append(out.Accounts, AccountUsage{Name: a.Name, Provider: a.Provider, Error: "loading"})
		}
		return out, nil
	}
	defer os.Remove(lock)
	if opts.Fetch == nil {
		opts.Fetch = func(ctx context.Context, a []Account) Result { return Collect(ctx, a, Readers{}) }
	}
	result := QuotaOnly(opts.Fetch(ctx, accounts))
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	data, err := json.Marshal(cachedResult{SavedAt: opts.Now.UnixMilli(), Result: result})
	if err != nil {
		return Result{}, err
	}
	// Use a private unique file and rename atomically so other tmux rows see complete JSON.
	f, err := os.CreateTemp(opts.Directory, key+"-*.tmp")
	if err != nil {
		return Result{}, err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return Result{}, err
	}
	if err = f.Close(); err != nil {
		return Result{}, err
	}
	if err = os.Rename(temp, path); err != nil {
		return Result{}, err
	}
	return result, nil
}
