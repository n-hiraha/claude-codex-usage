package session

import "testing"

func TestInferPhaseCurrentCodexAndClaudeScreens(t *testing.T) {
	for _, tc := range []struct {
		name, capture, want string
	}{
		{"codex working", "› fix it\n* Working (1m 12s • esc to interrupt)\n› Ask Codex to do anything", "thinking"},
		{"codex approval review is active", "* Reviewing approval request (2m 00s • esc to interrupt)\n› Ask Codex to do anything", "thinking"},
		{"codex tool", "* Running gh issue view 42 --json body\n› Ask Codex to do anything", "tool"},
		{"claude thinking", "* Recombobulating… (still thinking with xhigh effort)", "thinking"},
		{"claude idle", "Cooked for 1m 23s\n❯", "done"},
		{"codex idle placeholder", "› Ask Codex to do anything", "done"},
		{"queued question still needs an answer", "Queued follow-up inputs ? 1 question shift + ↑ to answer\n* Working (12s • esc to interrupt)", "input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := InferPhase(tc.capture); got != tc.want {
				t.Fatalf("InferPhase() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInferPhasePermissionOrderingAndFalsePositives(t *testing.T) {
	for _, tc := range []struct {
		name, capture, want string
	}{
		{"latest explicit modal", "* Running npm test\nWould you like to run the following command?\n1. Yes\n2. No", "permission"},
		{"latest activity beats stale modal", "Would you like to proceed?\n* Thinking…", "thinking"},
		{"interactive permission", "Enter to select an option", "permission"},
		{"narrative question", "The prompt says 'Would you like to proceed?' but is ordinary output", "unknown"},
		{"approval review is not permission", "* Reviewing approval request (2m 00s • esc to interrupt)", "thinking"},
		{"queued question is reply not approval", "Queued follow-up inputs ? 1 question shift + ↑ to answer", "input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := InferPhase(tc.capture); got != tc.want {
				t.Fatalf("InferPhase() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInferPhaseStripsVTFromStatus(t *testing.T) {
	capture := "\x1b[32m* Working (4s • esc to interrupt)\x1b[0m\n"
	if got := InferPhase(capture); got != "thinking" {
		t.Fatalf("InferPhase() = %q, want thinking", got)
	}
}

func TestActualClaudeSpinnerAndCompletion(t *testing.T) {
	for _, tc := range []struct{ s, want string }{
		{"✽ Recombobulating… (17m 5s · ↓ 32.9k tokens)\n❯ signup text", "thinking"},
		{"✻ Cooked for 1m 23s · done 13:19\n❯\u00a0", "done"},
		{"• Working (2m 02s • esc to interrupt)\n›⠁Ask Codex to do anything⡀", "thinking"},
	} {
		if got := InferPhase(tc.s); got != tc.want {
			t.Fatalf("%s: %s", tc.s, got)
		}
	}
}
