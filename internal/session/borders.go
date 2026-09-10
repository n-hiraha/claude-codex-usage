package session

import (
	"context"
	"fmt"
	"strings"
)

func borderState(phase string) string {
	switch phase {
	case "done", "input":
		return "reply"
	case "permission":
		return "approval"
	default:
		return ""
	}
}

// RefreshBorders only writes our own per-pane option. Shell panes and unknown
// states have no highlight; pane output is inspected in memory, never stored.
func RefreshBorders(ctx context.Context, socket string) error {
	result := Scan(ctx, socket)
	states := map[string]string{}
	for _, s := range result.Sessions {
		states[s.PaneID] = borderState(s.Phase)
	}
	text, err := run(ctx, "tmux", append(tmuxArgs(socket), "list-panes", "-a", "-F", "#{pane_id}\t#{@ccu_waiting}")...)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	changed := false
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		fields := strings.SplitN(line, "\t", 2)
		id := fields[0]
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		old := ""
		if len(fields) > 1 {
			old = fields[1]
		}
		state := states[id]
		if old == state {
			continue
		}
		if _, err = run(ctx, "tmux", append(tmuxArgs(socket), "set-option", "-p", "-t", id, "@ccu_waiting", state)...); err != nil {
			return fmt.Errorf("pane border update failed")
		}
		changed = true
	}
	if changed {
		_, _ = run(ctx, "tmux", append(tmuxArgs(socket), "refresh-client", "-S")...)
	}
	return nil
}
