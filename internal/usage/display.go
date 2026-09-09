package usage

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
)

// Remove CSI/OSC and control strings before untrusted labels reach a terminal.
func Clean(s string) string {
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
		if unicode.IsControl(r) {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
func tmuxText(s string) string { return strings.NewReplacer("#", "＃", "%", "％").Replace(Clean(s)) }
func cellWidth(s string) int {
	n := 0
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == 0x200d {
			continue
		}
		if r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a || r >= 0x2e80 && r <= 0xa4cf || r >= 0xac00 && r <= 0xd7a3 || r >= 0xf900 && r <= 0xfaff || r >= 0xfe10 && r <= 0xfe6f || r >= 0xff01 && r <= 0xff60 || r >= 0xffe0 && r <= 0xffe6 || r >= 0x1f300) {
			n += 2
		} else {
			n++
		}
	}
	return n
}
func shorten(s string, limit int) string {
	if cellWidth(s) <= limit {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if cellWidth(b.String()+string(r))+1 > limit {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + "…"
}
func finite(n *float64) bool { return n != nil && !math.IsNaN(*n) && !math.IsInf(*n, 0) }
func durationLabel(n *float64) string {
	if !finite(n) || *n <= 0 {
		return ""
	}
	v := *n
	if math.Mod(v, 1440) == 0 {
		return fmt.Sprintf("%gd", v/1440)
	}
	if math.Mod(v, 60) == 0 {
		return fmt.Sprintf("%gh", v/60)
	}
	return fmt.Sprintf("%gm", v)
}
func resetLabel(n *float64, now time.Time) string {
	if !finite(n) || *n < 0 || *n > 253402300799 {
		return "↻?"
	}
	reset := time.Unix(int64(*n), 0).In(time.Local)
	s := "↻" + reset.Format("1/2 15:04")
	if !reset.After(now) {
		s += " 更新待ち"
	}
	return s
}
func windowText(w *Window, now time.Time, size int, reset bool) string {
	if w == nil {
		return ""
	}
	gauge, pct := "[不明]", "使用?"
	if finite(w.UsedPercent) {
		used := math.Max(0, math.Min(100, *w.UsedPercent))
		fill := int(math.Round(used / 100 * float64(size)))
		gauge = "[" + strings.Repeat("━", fill) + strings.Repeat("·", size-fill) + "]"
		pct = fmt.Sprintf("使用%.0f%%", used)
	}
	label := durationLabel(w.WindowDurationMins)
	if label != "" {
		label += " "
	}
	s := label + gauge + " " + pct
	if reset {
		s += " " + resetLabel(w.ResetsAt, now)
	}
	return s
}
func providerName(provider string) string {
	if provider == "claude" {
		return "Claude"
	}
	return "Codex"
}
func accountLabel(a AccountUsage) string {
	provider := providerName(a.Provider)
	name := shorten(tmuxText(a.Name), 12)
	if name == "" || name == "default" || strings.EqualFold(name, provider) {
		return provider
	}
	return provider + " " + name
}

type gaugeGroup struct {
	name    string
	windows []*Window
}
type gaugeItem struct {
	variants []string
	color    string
}

func gaugeAccount(a AccountUsage, now time.Time) gaugeItem {
	label := accountLabel(a)
	item := gaugeItem{color: "#166534"}
	var limit, fable *Limit
	for i := range a.Limits {
		l := &a.Limits[i]
		if l.ID == a.Provider {
			limit = l
		}
		if l.ID == "claude_fable" {
			fable = l
		}
	}
	if limit == nil && a.Provider != "claude" {
		for i := range a.Limits {
			if !strings.Contains(strings.ToLower(a.Limits[i].ID), "spark") {
				limit = &a.Limits[i]
				break
			}
		}
	}
	var groups []gaugeGroup
	if a.Provider == "claude" {
		groups = []gaugeGroup{{name: "All models"}, {name: "Fable"}}
		if limit != nil && limit.Secondary != nil {
			groups[0].windows = []*Window{limit.Secondary}
		}
		if fable != nil && fable.Secondary != nil {
			groups[1].windows = []*Window{fable.Secondary}
		}
	} else {
		groups = []gaugeGroup{{}}
		if limit != nil {
			for _, w := range []*Window{limit.Primary, limit.Secondary} {
				if w != nil {
					groups[0].windows = append(groups[0].windows, w)
				}
			}
		}
	}
	var windows []*Window
	for _, g := range groups {
		windows = append(windows, g.windows...)
	}
	if a.Error != "" || len(windows) == 0 {
		item.color = "#a16207"
		item.variants = []string{label + " 利用枠不明", providerName(a.Provider) + " 不明"}
		return item
	}
	max, known := 0.0, false
	for _, w := range windows {
		if finite(w.UsedPercent) {
			known = true
			max = math.Max(max, *w.UsedPercent)
		}
	}
	if max > 80 {
		item.color = "#b91c1c"
	} else if max > 50 || !known {
		item.color = "#a16207"
	}
	for _, v := range []struct {
		size  int
		reset bool
	}{{10, true}, {6, true}, {6, false}, {4, false}} {
		var texts []string
		for _, g := range groups {
			var vals []string
			for _, w := range g.windows {
				vals = append(vals, windowText(w, now, v.size, v.reset))
			}
			text := strings.Join(vals, " / ")
			if len(vals) == 0 {
				text = "未提供"
			}
			if g.name != "" {
				text = g.name + " " + text
			}
			texts = append(texts, text)
		}
		item.variants = append(item.variants, label+" "+strings.Join(texts, " | "))
	}
	// Keep the group name when a narrow screen shows only one Claude quota.
	shortLabel := label
	if a.Provider == "claude" {
		if len(groups[0].windows) > 0 {
			shortLabel += " All models"
		} else {
			shortLabel += " Fable"
		}
	}
	if len(windows) > 1 {
		item.variants = append(item.variants, fmt.Sprintf("%s %s +%d枠", shortLabel, windowText(windows[0], now, 6, true), len(windows)-1))
	}
	item.variants = append(item.variants, shortLabel+" "+windowText(windows[0], now, 4, false), providerName(a.Provider)+" 幅不足")
	return item
}
func RenderGauge(result Result, width int, now time.Time) string {
	if width < 1 {
		width = 1
	}
	if now.IsZero() {
		now = time.Now()
	}
	var items []gaugeItem
	for _, a := range result.Accounts {
		items = append(items, gaugeAccount(a, now))
	}
	if len(items) == 0 {
		items = append(items, gaugeItem{variants: []string{"AI 利用枠不明"}, color: "#a16207"})
	}
	if result.Stale {
		for i := range items[0].variants {
			items[0].variants[i] = "stale " + items[0].variants[i]
		}
	}
	var shown []string
	used := 0
	for i, item := range items {
		extra := ""
		if len(shown) > 0 {
			extra = "  ·  "
		}
		suffix := ""
		if n := len(items) - i - 1; n > 0 {
			suffix = fmt.Sprintf(" +%d", n)
		}
		avail := width - used - cellWidth(extra) - cellWidth(suffix)
		if avail < 24 && len(shown) > 0 {
			break
		}
		text := ""
		for _, v := range item.variants {
			if cellWidth(v) <= avail {
				text = v
				break
			}
		}
		if text == "" {
			break
		}
		shown = append(shown, "#[fg="+item.color+"]"+text+"#[default]")
		used += cellWidth(text) + cellWidth(extra)
	}
	output := strings.Join(shown, "  ·  ")
	if n := len(items) - len(shown); n > 0 {
		output += fmt.Sprintf(" +%d", n)
	}
	return output
}
func RenderUsage(result Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ACCOUNT USAGE\n%s 時点\n\n", Clean(result.ScannedAt))
	for _, a := range result.Accounts {
		fmt.Fprintf(&b, "%s · %s\n", Clean(a.Name), Clean(a.Provider))
		if a.Error != "" {
			fmt.Fprintf(&b, "  %s\n\n", Clean(a.Error))
			continue
		}
		if a.Identity != nil {
			fmt.Fprintf(&b, "  %s · %s\n", Clean(a.Identity.Email), Clean(a.Identity.PlanType))
		}
		for _, l := range a.Limits {
			name := l.Name
			if name == "" {
				name = l.ID
			}
			fmt.Fprintf(&b, "  %s\n", Clean(name))
			count := 0
			for _, w := range []*Window{l.Primary, l.Secondary} {
				if w == nil {
					continue
				}
				count++
				pct := "使用率不明"
				if finite(w.UsedPercent) {
					pct = fmt.Sprintf("使用 %g%% / 残り %g%%", *w.UsedPercent, math.Max(0, 100-*w.UsedPercent))
				}
				reset := "不明"
				if finite(w.ResetsAt) {
					reset = time.Unix(int64(*w.ResetsAt), 0).In(time.Local).Format("2006/01/02 15:04:05")
				}
				fmt.Fprintf(&b, "    %s: %s · リセット %s\n", durationLabel(w.WindowDurationMins), pct, reset)
			}
			if count == 0 {
				b.WriteString("    利用枠の値は提供されていません。\n")
			}
		}
		if len(a.Limits) == 0 {
			b.WriteString("  利用枠は取得できませんでした。\n")
		}
		if a.Summary != nil && finite(a.Summary.LifetimeTokens) {
			fmt.Fprintf(&b, "  累計トークン: %.0f\n", *a.Summary.LifetimeTokens)
		}
		daily := a.DailyUsageBuckets
		if len(daily) > 7 {
			daily = daily[len(daily)-7:]
		}
		for _, day := range daily {
			fmt.Fprintf(&b, "  %s: %.0f tokens\n", Clean(day.StartDate), day.Tokens)
		}
		for _, w := range a.Warnings {
			fmt.Fprintf(&b, "  注意: %s\n", Clean(w))
		}
		b.WriteString("\n")
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(&b, "注意: %s\n", Clean(w))
	}
	return b.String()
}
func Demo() Result {
	now := time.Now()
	window := func(p, m float64, d time.Duration) *Window {
		return &Window{UsedPercent: Number(p), WindowDurationMins: Number(m), ResetsAt: Number(float64(now.Add(d).Unix()))}
	}
	return Result{ScannedAt: now.UTC().Format(time.RFC3339Nano), Warnings: []string{"デモデータです。実際のアカウント使用量ではありません。"}, Accounts: []AccountUsage{
		{Name: "main", Provider: "codex", Identity: &Identity{Type: "chatgpt", Email: "main@example.com", PlanType: "pro"}, Limits: []Limit{{ID: "codex", Primary: window(20, 10080, 7*24*time.Hour)}}},
		{Name: "second", Provider: "codex", Identity: &Identity{Type: "chatgpt", Email: "second@example.com", PlanType: "pro"}, Limits: []Limit{{ID: "codex", Primary: window(75, 10080, 6*24*time.Hour)}}},
		{Name: "claude", Provider: "claude", Identity: &Identity{Type: "claude.ai", Email: "claude@example.com", PlanType: "max"}, Limits: []Limit{{ID: "claude", Name: "All models", Primary: window(24, 300, 2*time.Hour), Secondary: window(71, 10080, 5*time.Hour)}, {ID: "claude_fable", Name: "Fable", Secondary: window(40, 10080, 5*time.Hour)}}},
	}}
}
