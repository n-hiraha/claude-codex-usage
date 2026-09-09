package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseProcessesAndAgentDetection(t *testing.T) {
	ps := ParseProcesses(" 12  1  1.5  2048 /usr/bin/zsh -lc 'claude'\n" +
		"20 12  3.0  4096 claude claude --dangerous\n" +
		"21 12  0.0  1024 node node -e console.log('codex')\n")
	if len(ps) != 3 || ps[1].PID != 20 || ps[1].RSS != 4096 {
		t.Fatalf("parse processes: %#v", ps)
	}
	if got := DetectAgent(&ps[1]); got != "claude" {
		t.Fatalf("agent = %q", got)
	}
	// Prompt text and eval source must not turn a shell/node process into an agent.
	if got := DetectAgent(&ps[0]); got != "" {
		t.Fatalf("shell false positive: %q", got)
	}
	if got := DetectAgent(&ps[2]); got != "" {
		t.Fatalf("node eval false positive: %q", got)
	}
}

func TestInferPhaseLatestLinePrecedence(t *testing.T) {
	for _, tc := range []struct{ capture, want string }{
		{"thinking\n\nWould you like to proceed?\n", "permission"},
		{"running command: ls\nDone.\n", "done"},
		{"done\n>\n", "done"},
		{"The prompt says 'Would you like to proceed?' but is ordinary output\n", "unknown"},
	} {
		if got := InferPhase(tc.capture); got != tc.want {
			t.Errorf("InferPhase(%q) = %q, want %q", tc.capture, got, tc.want)
		}
	}
}

func TestTrackerRetainsKnownStateAcrossUnknown(t *testing.T) {
	now := time.Unix(100, 0)
	tracker := &Tracker{}
	base := Session{PaneID: "%1", PID: 7, Agent: "codex", Project: "p", Phase: "thinking"}
	if got := tracker.Update(Result{Sessions: []Session{base}}, now); len(got.Events) != 0 {
		t.Fatal(got.Events)
	}
	base.Phase = "unknown"
	if got := tracker.Update(Result{Sessions: []Session{base}}, now.Add(time.Second)); len(got.Events) != 0 {
		t.Fatal(got.Events)
	}
	base.Phase = "done"
	got := tracker.Update(Result{Sessions: []Session{base}}, now.Add(2*time.Second))
	if len(got.Events) != 1 || got.Events[0].Type != "done" {
		t.Fatalf("events = %#v", got.Events)
	}
	if got.Sessions[0].PhaseAgeSeconds < 0 {
		t.Fatalf("negative age = %d", got.Sessions[0].PhaseAgeSeconds)
	}
}

func TestScanDeduplicatesLinkedPaneIDsWithSyntheticCommands(t *testing.T) {
	d := t.TempDir()
	tmux := filepath.Join(d, "tmux")
	ps := filepath.Join(d, "ps")
	if err := os.WriteFile(tmux, []byte("#!/bin/sh\ncase \"$*\" in\n*list-panes*) printf '%%1\\t100\\t$0\\twork\\t0\\t0\\t/tmp/app\\tclaude\\n%%1\\t100\\t$0\\twork\\t0\\t0\\t/tmp/app\\tclaude\\n' ;;\n*) printf 'thinking\\n' ;;\nesac\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ps, []byte("#!/bin/sh\nprintf '100 1 1.0 2048 claude claude\\n'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	old := os.Getenv("PATH")
	if err := os.Setenv("PATH", d+string(os.PathListSeparator)+old); err != nil {
		t.Fatal(err)
	}
	defer os.Setenv("PATH", old)
	r := Scan(context.Background(), "")
	if len(r.Warnings) != 0 {
		t.Fatalf("warnings = %#v", r.Warnings)
	}
	if len(r.Sessions) != 1 || r.Sessions[0].PaneID != "%1" {
		t.Fatalf("sessions = %#v", r.Sessions)
	}
}

func TestActivePhaseOverridesFooterAndVT(t *testing.T) {
	for _, capture := range []string{"Working\n>\n", "Allow this command?\n\x1b[32mThinking\x1b[0m\n>"} {
		if InferPhase(capture) != "thinking" {
			t.Fatalf("wrong active phase: %q", capture)
		}
	}
	if Clean("\x1b]52;c;SECRET\a#(x)\n") != "#(x)" {
		t.Fatal("OSC/control not removed")
	}
}
