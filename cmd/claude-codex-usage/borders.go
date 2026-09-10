package main

func borderConfig() []string {
	suffix := "#{?#{==:#{@ccu_waiting},reply},#,fg=#ef4444,#{?#{==:#{@ccu_waiting},approval},#,fg=#f59e0b,}}"
	return []string{
		"# Save the existing border appearance once, including theme colours.",
		"if-shell -F '#{!=:#{@ccu_border_saved},1}' {",
		"  set-option -gF @ccu_saved_border_style '#{pane-border-style}'",
		"  set-option -gF @ccu_saved_active_border_style '#{pane-active-border-style}'",
		// Single expansion retrieves the option value; nested pane tokens remain intact.
		"  set-option -gF @ccu_saved_border_format '#{pane-border-format}'",
		"  set-option -gF @ccu_saved_border_status '#{pane-border-status}'",
		"  set-option -g @ccu_border_saved 1",
		"}",
		"set-option -g pane-border-style " + configQuote("#{@ccu_saved_border_style}"+suffix),
		"set-option -g pane-active-border-style " + configQuote("#{@ccu_saved_active_border_style}"+suffix),
		"if-shell -F '#{==:#{pane-border-status},off}' 'set-option -g pane-border-status bottom'",
		"set-option -g pane-border-format " + configQuote("#{E:@ccu_saved_border_format}#{?#{==:#{@ccu_waiting},reply},#[fg=#ef4444] 返答待ち#[default],#{?#{==:#{@ccu_waiting},approval},#[fg=#f59e0b] 承認待ち#[default],}}"),
	}
}
