package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Options struct {
	Socket, Client, Agent                string
	Attention, Compact, Demo, Bell, JSON bool
	Interval                             time.Duration
}
type Process struct {
	PID, PPID  int
	CPU, RSS   float64
	Comm, Args string
}
type Pane struct{ PaneID, PanePID, SessionID, SessionName, WindowIndex, PaneIndex, CWD, CurrentCommand string }
type Session struct {
	ID              string  `json:"id,omitempty"`
	PaneID          string  `json:"paneId"`
	PID             int     `json:"pid"`
	Agent           string  `json:"agent"`
	Project         string  `json:"project"`
	CWD             string  `json:"cwd"`
	SessionID       string  `json:"sessionId"`
	SessionName     string  `json:"sessionName"`
	WindowIndex     int     `json:"windowIndex"`
	PaneIndex       int     `json:"paneIndex"`
	Phase           string  `json:"phase"`
	CPU             float64 `json:"cpu"`
	MemoryMB        float64 `json:"memoryMb"`
	PhaseSource     string  `json:"phaseSource,omitempty"`
	PhaseSince      string  `json:"phaseSince,omitempty"`
	PhaseAgeSeconds int     `json:"phaseAgeSeconds,omitempty"`
}
type Result struct {
	Sessions  []Session `json:"sessions"`
	Warnings  []string  `json:"warnings"`
	ScannedAt string    `json:"scannedAt"`
	Events    []Event   `json:"events,omitempty"`
}
type Event struct {
	Type, PaneID, Agent, Project string
	At                           time.Time
}

var processRE = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s+([\d.]+)\s+(\d+)\s+(\S+)(?:\s+(.*?))?\s*$`)

func ParseProcesses(out string) []Process {
	var r []Process
	for _, l := range strings.Split(out, "\n") {
		m := processRE.FindStringSubmatch(l)
		if len(m) == 0 {
			continue
		}
		pid, _ := strconv.Atoi(m[1])
		ppid, _ := strconv.Atoi(m[2])
		cpu, _ := strconv.ParseFloat(m[3], 64)
		rss, _ := strconv.ParseFloat(m[4], 64)
		r = append(r, Process{pid, ppid, cpu, rss, m[5], m[6]})
	}
	return r
}

func DetectAgent(p *Process) string {
	if p == nil {
		return ""
	}
	comm := strings.ToLower(filepath.Base(p.Comm))
	args := strings.TrimSpace(p.Args)
	if strings.Contains(p.Comm, "/") && agentName(comm) == "" && comm != "node" {
		fields := strings.Fields(args)
		if len(fields) > 0 {
			comm = strings.ToLower(filepath.Base(fields[0]))
		}
	}
	if comm == "node" {
		comm = nodeScript(args)
	}
	if x := agentName(comm); x != "" {
		return x
	}
	return agentName(comm)
}
func agentName(v string) string {
	v = strings.ToLower(v)
	v = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(v, ".mjs"), ".js"), ".cjs")
	v = strings.TrimSuffix(v, ".bin")
	b := filepath.Base(v)
	switch b {
	case "claude", "claude-code", "claude_cli":
		return "claude"
	case "codex", "codex-cli":
		return "codex"
	case "gemini", "gemini-cli":
		return "gemini"
	}
	if strings.HasSuffix(v, "/@anthropic-ai/claude-code/cli") || strings.HasSuffix(v, "/claude/cli") || strings.HasSuffix(v, "/claude-code/cli") {
		return "claude"
	}
	if strings.HasSuffix(v, "/@openai/codex/bin/codex") {
		return "codex"
	}
	if strings.HasSuffix(v, "/@google/gemini-cli/dist/index") || strings.HasSuffix(v, "/gemini/dist/index") || strings.HasSuffix(v, "/gemini-cli/dist/index") {
		return "gemini"
	}
	return ""
}
func nodeScript(args string) string {
	a := strings.Fields(args)
	for i := 1; i < len(a); i++ {
		if strings.HasPrefix(a[i], "--eval") || strings.HasPrefix(a[i], "--print") || strings.HasPrefix(a[i], "-e") || strings.HasPrefix(a[i], "-p") {
			return ""
		}
		if a[i] == "-r" || a[i] == "--require" || a[i] == "--loader" || a[i] == "--experimental-loader" || a[i] == "--import" {
			i++
			continue
		}
		if !strings.HasPrefix(a[i], "-") {
			return a[i]
		}
	}
	return ""
}

func ParsePanes(out string) []Pane {
	var r []Pane
	for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if l == "" {
			continue
		}
		v := strings.Split(l, "\t")
		for len(v) < 8 {
			v = append(v, "")
		}
		r = append(r, Pane{v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7]})
	}
	return r
}
func stripVT(s string) string {
	var out strings.Builder
	state := 0
	for _, r := range s {
		switch state {
		case 1:
			switch r {
			case '[':
				state = 2
			case ']', 'P', 'X', '^', '_':
				state = 3
			default:
				state = 0
			}
			continue
		case 2:
			if r >= 0x40 && r <= 0x7e {
				state = 0
			}
			continue
		case 3:
			if r == 7 || r == 0x9c {
				state = 0
			} else if r == 27 {
				state = 4
			}
			continue
		case 4:
			if r == '\\' {
				state = 0
			} else {
				state = 3
			}
			continue
		}
		switch r {
		case 27:
			state = 1
			continue
		case 0x9b:
			state = 2
			continue
		case 0x9d, 0x90, 0x98, 0x9e, 0x9f:
			state = 3
			continue
		}
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
func InferPhase(capture string) string {
	lines := strings.Split(stripVT(capture), "\n")
	var x []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			x = append(x, l)
		}
	}
	if len(x) > 12 {
		x = x[len(x)-12:]
	}
	for i := len(x) - 1; i >= 0; i-- {
		l := x[i]
		if regexp.MustCompile(`(?i)^\s*(thinking|reasoning|working)(\s*\([^\n]*\))?[.… ]*$`).MatchString(l) {
			return "thinking"
		}
		if regexp.MustCompile(`(?i)(^|\s)([⏺●▶▸]|[-*])\s*(tool|running|executing)\b|\b(running|executing)\s+(command|tool)\b`).MatchString(l) {
			return "tool"
		}
		if regexp.MustCompile(`(?i)^\s*(approval:\s*)?(would you like to proceed\?|(?:do you want|would you like) to (run|execute|allow)\b.*\?|allow (this|the)\b[^\n]*\?|approve (this|the)\b[^\n]*\?)`).MatchString(l) {
			return "permission"
		}
		if regexp.MustCompile(`(?i)^\s*(done|finished|completed|idle)\s*[.!]?\s*$`).MatchString(l) {
			return "done"
		}
	}
	for _, line := range x {
		if regexp.MustCompile(`^\s*[❯›>]\s*$`).MatchString(line) {
			return "done"
		}
	}
	return "unknown"
}

func tmuxArgs(socket string) []string {
	if socket != "" {
		return []string{"-L", socket}
	}
	return nil
}

type boundedOutput struct {
	b         bytes.Buffer
	remaining int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.remaining {
		p = p[:b.remaining]
		n, _ := b.b.Write(p)
		b.remaining -= n
		return n, errors.New("command output exceeded 1 MiB")
	}
	n, e := b.b.Write(p)
	b.remaining -= n
	return n, e
}
func run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 500 * time.Millisecond
	b := &boundedOutput{remaining: 1024 * 1024}
	cmd.Stdout = b
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s command failed", name)
	}
	return b.b.String(), nil
}
func Scan(ctx context.Context, socket string) Result {
	r := Result{ScannedAt: time.Now().Format(time.RFC3339Nano), Sessions: []Session{}, Warnings: []string{}}
	ta := tmuxArgs(socket)
	po, e := run(ctx, "tmux", append(ta, "list-panes", "-a", "-F", "#{pane_id}\t#{pane_pid}\t#{session_id}\t#{session_name}\t#{window_index}\t#{pane_index}\t#{pane_current_path}\t#{pane_current_command}")...)
	if e != nil {
		r.Warnings = []string{"Unable to list tmux panes: " + e.Error()}
		return r
	}
	ps, e := run(ctx, "ps", "-ww", "-axo", "pid=,ppid=,pcpu=,rss=,comm=,args=")
	if e != nil {
		r.Warnings = append(r.Warnings, "Unable to inspect processes: "+e.Error())
	}
	procs := ParseProcesses(ps)
	panes := ParsePanes(po)
	if len(panes) == 0 {
		return r
	}
	seen := map[string]bool{}
	var mu sync.Mutex
	jobs := make(chan Pane)
	out := make(chan *Session)
	var wg sync.WaitGroup
	n := 4
	if len(panes) < n {
		n = len(panes)
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				var best *Process
				depth := map[int]int{}
				q := []int{}
				root, _ := strconv.Atoi(p.PanePID)
				q = append(q, root)
				by := map[int][]Process{}
				for _, z := range procs {
					by[z.PPID] = append(by[z.PPID], z)
				}
				visited := map[int]bool{}
				for len(q) > 0 {
					id := q[0]
					q = q[1:]
					if visited[id] {
						continue
					}
					visited[id] = true
					if id == root {
						for _, z := range procs {
							if z.PID == id && DetectAgent(&z) != "" {
								zz := z
								best = &zz
								break
							}
						}
					}
					for _, z := range by[id] {
						depth[z.PID] = depth[id] + 1
						q = append(q, z.PID)
						if DetectAgent(&z) != "" && (best == nil || depth[z.PID] < depth[best.PID] || depth[z.PID] == depth[best.PID] && z.PID < best.PID) {
							zz := z
							best = &zz
						}
					}
				}
				if best == nil {
					continue
				}
				cap, e := run(ctx, "tmux", append(ta, "capture-pane", "-p", "-t", p.PaneID, "-S", "-30")...)
				if e != nil {
					mu.Lock()
					r.Warnings = append(r.Warnings, "Unable to capture pane "+p.PaneID+": "+e.Error())
					mu.Unlock()
				}
				wi, _ := strconv.Atoi(p.WindowIndex)
				pi, _ := strconv.Atoi(p.PaneIndex)
				pid := best.PID
				s := &Session{ID: p.PaneID, PaneID: p.PaneID, PID: pid, Agent: DetectAgent(best), Project: filepath.Base(p.CWD), CWD: p.CWD, SessionID: p.SessionID, SessionName: p.SessionName, WindowIndex: wi, PaneIndex: pi, Phase: InferPhase(cap), CPU: best.CPU, MemoryMB: best.RSS / 1024, PhaseSource: "pane heuristic"}
				if s.Project == "." || s.Project == "" {
					s.Project = p.SessionName
				}
				out <- s
			}
		}()
	}
	go func() {
		for _, p := range panes {
			if !seen[p.PaneID] {
				seen[p.PaneID] = true
				jobs <- p
			}
		}
		close(jobs)
		wg.Wait()
		close(out)
	}()
	for s := range out {
		r.Sessions = append(r.Sessions, *s)
	}
	sort.SliceStable(r.Sessions, func(i, j int) bool {
		return rank(r.Sessions[i].Phase) < rank(r.Sessions[j].Phase) || rank(r.Sessions[i].Phase) == rank(r.Sessions[j].Phase) && (r.Sessions[i].SessionName < r.Sessions[j].SessionName || r.Sessions[i].SessionName == r.Sessions[j].SessionName && (r.Sessions[i].WindowIndex < r.Sessions[j].WindowIndex || r.Sessions[i].WindowIndex == r.Sessions[j].WindowIndex && r.Sessions[i].PaneIndex < r.Sessions[j].PaneIndex))
	})
	return r
}
func rank(p string) int {
	switch p {
	case "permission":
		return 0
	case "thinking":
		return 1
	case "tool":
		return 2
	case "done":
		return 3
	}
	return 4
}

func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, stripVT(s))
}
func TmuxText(s string) string { return strings.NewReplacer("#", "＃", "%", "％").Replace(Clean(s)) }
func Filter(s []Session, agent string, attention bool) []Session {
	r := []Session{}
	for _, x := range s {
		if (agent == "" || x.Agent == agent) && (!attention || x.Phase == "permission" || x.Phase == "done") {
			r = append(r, x)
		}
	}
	return r
}
func Statusline(s []Session, compact bool) string {
	if len(s) == 0 {
		return "AI · 0 sessions"
	}
	counts := []string{}
	for _, a := range []struct{ k, n string }{{"claude", "Claude"}, {"codex", "Codex"}, {"gemini", "Gemini"}} {
		n := 0
		for _, x := range s {
			if x.Agent == a.k {
				n++
			}
		}
		if n > 0 {
			counts = append(counts, fmt.Sprintf("%s %d", a.n, n))
		}
	}
	if compact {
		return "AI " + strings.Join(counts, " · ")
	}
	pills := ""
	for i, x := range s {
		if i >= 5 {
			break
		}
		icon := map[string]string{"permission": "!", "thinking": "~", "tool": ">", "done": "+", "unknown": "?"}[x.Phase]
		if icon == "" {
			icon = "?"
		}
		if i > 0 {
			pills += "  "
		}
		pills += fmt.Sprintf("#[fg=%s]%s %s %s#[default]", map[string]string{"permission": "yellow", "thinking": "cyan", "tool": "blue", "done": "green", "unknown": "white"}[x.Phase], icon, TmuxText(x.PaneID), shortProject(x.Project))
	}
	return "AI " + strings.Join(counts, " · ") + " │ " + pills
}
func shortProject(s string) string {
	r := []rune(TmuxText(s))
	if len(r) > 24 {
		r = r[:24]
	}
	return string(r)
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func phaseLabel(s string) string {
	if v, ok := map[string]string{"permission": "承認待ち", "thinking": "応答中", "tool": "ツール実行", "done": "入力待ち", "unknown": "不明"}[s]; ok {
		return v
	}
	return "不明"
}
func ageLabel(s string, n int) string {
	if s == "" {
		return ""
	}
	if n < 60 {
		return fmt.Sprintf(" (%ds)", n)
	}
	if n < 3600 {
		return fmt.Sprintf(" (%dm %ds)", n/60, n%60)
	}
	return fmt.Sprintf(" (%dh %dm)", n/3600, n%3600/60)
}
func RenderStatus(r Result, attention bool, agent string) string {
	ss := Filter(r.Sessions, agent, attention)
	count := fmt.Sprintf("%d", len(r.Sessions))
	if attention || agent != "" {
		count = fmt.Sprintf("%d/%d", len(ss), len(r.Sessions))
	}
	lines := []string{"CLAUDE CODEX USAGE", fmt.Sprintf("%s sessions · %s · 状態は画面からの推定", count, time.Now().Format("15:04:05")), ""}
	for i, s := range ss {
		icon := map[string]string{"permission": "!", "thinking": "~", "tool": ">", "done": "+", "unknown": "?"}[s.Phase]
		lines = append(lines, fmt.Sprintf("%d. %s  %s  %s %s  %s", i+1, Clean(s.PaneID), Clean(s.Agent), icon, phaseLabel(s.Phase)+ageLabel(s.PhaseSince, s.PhaseAgeSeconds), Clean(s.Project)), fmt.Sprintf("    %s:%d.%d  CPU %.1f%%  MEM %.0f MB", Clean(s.SessionName), s.WindowIndex, s.PaneIndex, s.CPU, s.MemoryMB), "    "+Clean(s.CWD))
	}
	if len(ss) == 0 {
		if attention || agent != "" {
			lines = append(lines, "条件に一致するセッションはありません。")
		} else {
			lines = append(lines, "tmux内にAIセッションが見つかりません。")
		}
	}
	for _, w := range r.Warnings {
		lines = append(lines, "\n注意: "+Clean(w))
	}
	return strings.Join(lines, "\n")
}

type Tracker struct{ states map[string]track }
type track struct {
	phase, last string
	since       time.Time
}

func (t *Tracker) Update(r Result, now time.Time) Result {
	if t.states == nil {
		t.states = map[string]track{}
	}
	seen := map[string]bool{}
	r.Events = nil
	for i := range r.Sessions {
		s := &r.Sessions[i]
		k := fmt.Sprintf("%s:%d:%s", s.PaneID, s.PID, s.Agent)
		seen[k] = true
		p := t.states[k]
		if p.last != "" && s.Phase != p.last && (s.Phase == "permission" || (s.Phase == "done" && (p.last == "thinking" || p.last == "tool"))) {
			r.Events = append(r.Events, Event{Type: map[bool]string{true: "permission", false: "done"}[s.Phase == "permission"], PaneID: s.PaneID, Agent: s.Agent, Project: s.Project, At: now})
		}
		if p.phase != s.Phase {
			p.since = now
		}
		p.phase = s.Phase
		if s.Phase == "permission" || s.Phase == "thinking" || s.Phase == "tool" || s.Phase == "done" {
			p.last = s.Phase
		}
		t.states[k] = p
		s.PhaseSince = p.since.Format(time.RFC3339Nano)
		s.PhaseAgeSeconds = int(now.Sub(p.since).Seconds())
		if s.PhaseAgeSeconds < 0 {
			s.PhaseAgeSeconds = 0
		}
	}
	if len(r.Warnings) == 0 {
		for k := range t.states {
			if !seen[k] {
				delete(t.states, k)
			}
		}
	}
	return r
}

func Demo() Result {
	return Result{ScannedAt: time.Now().Format(time.RFC3339Nano), Warnings: []string{"デモデータです。実際のセッションではありません。"}, Sessions: []Session{{ID: "%1", PaneID: "%1", PID: 1000, Agent: "claude", Phase: "permission", Project: "api-server", CWD: "/demo/api-server", CPU: .2, MemoryMB: 240}, {ID: "%2", PaneID: "%2", PID: 1001, Agent: "codex", Phase: "thinking", Project: "web-app", CWD: "/demo/web-app", CPU: 12.8, MemoryMB: 180}, {ID: "%3", PaneID: "%3", PID: 1002, Agent: "gemini", Phase: "done", Project: "docs", CWD: "/demo/docs", CPU: 0, MemoryMB: 135}}}
}
func ShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func Jump(ctx context.Context, pane string, o Options) error {
	if !regexp.MustCompile(`^%\d+$`).MatchString(pane) {
		return errors.New("移動先は %3 のようなペインIDで指定してください。")
	}
	args := append([]string{"switch-client"}, func() []string {
		if o.Client != "" {
			return []string{"-c", o.Client}
		}
		return nil
	}()...)
	args = append(args, "-t", pane)
	_, e := run(ctx, "tmux", append(tmuxArgs(o.Socket), args...)...)
	return e
}

func outputResult(r Result, command string, o Options) error {
	ss := Filter(r.Sessions, o.Agent, o.Attention)
	if o.JSON {
		b, _ := json.MarshalIndent(struct {
			Sessions  []Session `json:"sessions"`
			Warnings  []string  `json:"warnings"`
			ScannedAt string    `json:"scannedAt"`
		}{ss, r.Warnings, r.ScannedAt}, "", "  ")
		_, e := fmt.Fprintln(os.Stdout, string(b))
		return e
	}
	if command == "statusline" {
		if len(r.Sessions) == 0 && len(r.Warnings) > 0 && !o.Demo {
			fmt.Fprintln(os.Stdout, "AI · unavailable")
			return nil
		}
		_, e := fmt.Fprintln(os.Stdout, Statusline(ss, o.Compact))
		return e
	}
	_, e := fmt.Fprintln(os.Stdout, RenderStatus(r, o.Attention, o.Agent))
	return e
}
func isTTY() bool {
	in, e := os.Stdin.Stat()
	if e != nil || in.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	out, e := os.Stdout.Stat()
	return e == nil && out.Mode()&os.ModeCharDevice != 0
}
func (t *Tracker) Reset() { t.states = nil }
