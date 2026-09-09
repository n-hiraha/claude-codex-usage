package usage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const maxProviderOutput = 1 << 20

func providerError(msg string) error { return errors.New(msg) }

func finiteNumber(v any) *float64 {
	n, ok := v.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return nil
	}
	return &n
}

func textValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func cleanHome(s string) string {
	p, err := filepath.Abs(filepath.Clean(s))
	if err != nil {
		return s
	}
	return p
}

func envWithout(keys ...string) []string {
	drop := make(map[string]bool, len(keys))
	for _, k := range keys {
		drop[k] = true
	}
	out := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if i := strings.IndexByte(e, '='); i >= 0 && !drop[e[:i]] {
			out = append(out, e)
		}
	}
	return out
}

func claudeService(home string) string {
	home = cleanHome(home)
	userHome, _ := os.UserHomeDir()
	defaultHome := cleanHome(filepath.Join(userHome, ".claude"))
	suffix := ""
	if home != defaultHome {
		sum := sha256.Sum256([]byte(home))
		suffix = "-" + hex.EncodeToString(sum[:])[:8]
	}
	return "Claude Code-credentials" + suffix
}
