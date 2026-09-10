package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	for _, args := range [][]string{{"watch", "--agent", "fake"}, {"usage", "--provider", "fake"}, {"status", "--bell"}, {"usage", "--accounts"}, {"usage", "--bogus"}} {
		if _, e := parse(args); e == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
func TestGoConfig(t *testing.T) {
	o, e := parse([]string{"setup", "tmux", "--split-providers", "--accounts", "/tmp/a b/config.json", "--codex-bin", "/tmp/codex", "--claude-bin", "/tmp/claude"})
	if e != nil {
		t.Fatal(e)
	}
	s, e := config(o, "/tmp/tool with 'quote")
	if e != nil {
		t.Fatal(e)
	}
	for _, part := range []string{"set -g status 3", "status-format[1]", "status-format[2]", "--provider codex", "--provider claude", "--claude-bin", "/tmp/a b/config.json", "display-popup"} {
		if !strings.Contains(s, part) {
			t.Errorf("missing %s", part)
		}
	}
	if strings.Contains(s, "node") || strings.Contains(s, "set -g status-right ") {
		t.Fatal("runtime or existing bar changed")
	}
}

func TestTmuxConfigIntegration(t *testing.T) {
	if os.Getenv("RUN_TMUX_TESTS") == "" {
		t.Skip("set RUN_TMUX_TESTS=1 for isolated tmux")
	}
	socket := fmt.Sprintf("ccu-go-config-%d", os.Getpid())
	runTmux := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		b, e := exec.CommandContext(ctx, "tmux", append([]string{"-L", socket}, args...)...).CombinedOutput()
		if e != nil {
			t.Fatalf("tmux: %v %s", e, b)
		}
		return string(b)
	}
	runTmux("-f", "/dev/null", "new-session", "-d", "-s", "demo", "-x", "223", "-y", "30")
	defer exec.Command("tmux", "-L", socket, "kill-server").Run()
	dir := t.TempDir()
	fake := filepath.Join(dir, "binary with spaces")
	if e := os.WriteFile(fake, []byte("#!/bin/sh\nprintf 'demo usage\\n'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	o, _ := parse([]string{"setup", "tmux", "--split-providers", "--demo", "--accounts", filepath.Join(dir, "accounts with spaces.json")})
	body, e := config(o, fake)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "config")
	if e := os.WriteFile(path, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	runTmux("set", "-g", "status-right", "KEEP CPU CLOCK")
	runTmux("source-file", path)
	if strings.TrimSpace(runTmux("show", "-gv", "status")) != "3" {
		t.Fatal("not 3 rows")
	}
	if strings.TrimSpace(runTmux("show", "-gv", "status-right")) != "KEEP CPU CLOCK" {
		t.Fatal("existing status overwritten")
	}
	for _, row := range []string{"status-format[1]", "status-format[2]"} {
		if !strings.Contains(runTmux("show", "-gv", row), fake) {
			t.Fatal("executable missing")
		}
	}
}

func TestWaitingBordersIntegration(t *testing.T) {
	if os.Getenv("RUN_TMUX_TESTS") == "" {
		t.Skip("set RUN_TMUX_TESTS=1")
	}
	socket := fmt.Sprintf("ccu-border-%d", os.Getpid())
	tm := func(args ...string) string {
		t.Helper()
		b, e := exec.Command("tmux", append([]string{"-L", socket}, args...)...).CombinedOutput()
		if e != nil {
			t.Fatalf("tmux %v: %v %s", args, e, b)
		}
		return strings.TrimSpace(string(b))
	}
	pane := tm("-f", "/dev/null", "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", "test")
	defer exec.Command("tmux", "-L", socket, "kill-server").Run()
	tm("set", "-g", "pane-border-style", "fg=blue,bg=#F4F0EA")
	tm("set", "-g", "pane-active-border-style", "fg=green,bg=#F4F0EA")
	tm("set", "-g", "pane-border-format", "pane #{pane_index}")
	file := filepath.Join(t.TempDir(), "borders.conf")
	if e := os.WriteFile(file, []byte(strings.Join(borderConfig(), "\n")), 0600); e != nil {
		t.Fatal(e)
	}
	tm("source-file", file)
	tm("source-file", file)
	for _, tc := range []struct{ state, color, label string }{{"reply", "#ffffff", "返答待ち"}, {"approval", "#111111", "承認してください"}, {"", "green", "pane 0"}} {
		tm("set", "-p", "-t", pane, "@ccu_waiting", tc.state)
		style := tm("display-message", "-p", "-t", pane, "#{E:pane-active-border-style}")
		if !strings.Contains(style, "fg="+tc.color) || !strings.Contains(style, "bg=#F4F0EA") {
			t.Fatalf("style: %q", style)
		}
		label := tm("display-message", "-p", "-t", pane, "#{E:pane-border-format}")
		if !strings.Contains(label, tc.label) {
			t.Fatalf("label: %q", label)
		}
	}
	if tm("show", "-gv", "@ccu_saved_border_style") != "fg=blue,bg=#F4F0EA" {
		t.Fatal("reload overwrote original")
	}
}
