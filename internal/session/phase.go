package session

import (
	"regexp"
	"strings"
)

// These expressions deliberately match UI status lines, rather than arbitrary
// prose in an assistant's answer. A pane can contain old output, so the last
// matching status line wins.
var (
	phaseWorkingRE  = regexp.MustCompile(`(?i)^(?:[*•✽✻✢✳✶✺·]\s*)?(?:working|reviewing approval request)(?:\s|\(|$)`)
	phaseThinkingRE = regexp.MustCompile(`(?i)^(?:[*•✽✻✢✳✶✺·]\s*)?(?:thinking|reasoning|recombobulating)(?:\s|[.…]|$)`)
	phaseToolRE     = regexp.MustCompile(`(?i)^(?:[*•✽✻✢✳✶✺·]\s*)?(?:running|executing)(?:\s|:|$)`)
)

// InferPhase infers the state shown by a Codex or Claude pane. It is intended
// for captures of AI panes only; shell prompts and command processes are not
// part of this heuristic.
func InferPhase(capture string) string {
	text := strings.Map(func(r rune) rune {
		if r >= 0x2800 && r <= 0x28ff {
			return -1
		}
		return r
	}, stripVT(capture))
	// Codex can keep working while a structured question awaits the user.
	// Highlight that request even when the activity footer is still visible.
	if regexp.MustCompile(`(?i)\?\s*\d+\s+questions?`).MatchString(text) && regexp.MustCompile(`(?i)shift[^\n]*to answer`).MatchString(text) {
		return "input"
	}
	phase := "unknown"
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		// A current activity footer takes precedence over the ever-present
		// Codex input placeholder and over an older approval prompt.
		switch {
		case phaseWorkingRE.MatchString(line):
			phase = "thinking"
		case phaseThinkingRE.MatchString(line):
			phase = "thinking"
		case phaseToolRE.MatchString(line) || strings.HasPrefix(strings.ToLower(line), "running command:"):
			phase = "tool"
		case strings.Contains(strings.ToLower(line), "esc to interrupt") && !strings.Contains(strings.ToLower(line), "messages to be submitted") && !strings.Contains(strings.ToLower(line), "press esc"):
			phase = "thinking"
		case isPermissionLine(line):
			phase = "permission"
		case isDoneLine(line):
			// The Codex placeholder and a bare prompt are continuously present
			// beneath an active footer. They mean idle only when no status has
			// already been observed in this capture. Explicit completion text can
			// close an earlier tool status.
			if isIdlePrompt(line) {
				if phase == "unknown" {
					phase = "done"
				}
			} else {
				phase = "done"
			}
		}
	}
	return phase
}

func isPermissionLine(line string) bool {
	l := strings.ToLower(strings.TrimSpace(line))
	// These are labels rendered by the permission UI. Requiring the phrase at
	// the beginning prevents ordinary assistant prose from becoming an alert.
	return strings.HasPrefix(l, "would you like to proceed?") ||
		strings.HasPrefix(l, "would you like to run the following command?") ||
		strings.HasPrefix(l, "allow this command?") ||
		strings.HasPrefix(l, "do you want to run ") ||
		strings.HasPrefix(l, "enter to select ")
}

func isDoneLine(line string) bool {
	l := strings.TrimSpace(line)
	return isIdlePrompt(l) || regexp.MustCompile(`(?i)^(?:[*•✽✻✢✳✶✺]\s*)?(?:done|finished|completed|idle)[.!]?$`).MatchString(l) || regexp.MustCompile(`(?i)^[✽✻✢✳✶✺*]\s+(?:cooked|worked|crunched|baked|brewed) for .*`).MatchString(l)
}

func isIdlePrompt(line string) bool {
	l := strings.TrimSpace(line)
	return l == ">" || l == "❯" || l == "›" ||
		strings.HasPrefix(strings.ToLower(l), "› ask codex to do anything")
}
