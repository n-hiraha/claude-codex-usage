package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

func Run(parent context.Context, command string, o Options) error {
	if o.Interval <= 0 {
		o.Interval = 2 * time.Second
	}
	read := func(ctx context.Context) Result {
		if o.Demo {
			return Demo()
		}
		return Scan(ctx, o.Socket)
	}
	if command != "watch" || !isTTY() || o.JSON {
		return outputResult(read(parent), command, o)
	}
	state, err := terminalRaw()
	if err != nil {
		return outputResult(read(parent), command, o)
	}
	ctx, cancel := context.WithCancel(parent)
	keys := make(chan byte, 16)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer close(keys)
		var b [1]byte
		for ctx.Err() == nil {
			n, e := syscall.Read(int(os.Stdin.Fd()), b[:])
			if e == syscall.EINTR {
				continue
			}
			if e != nil {
				return
			}
			if n == 0 {
				continue
			}
			select {
			case keys <- b[0]:
			case <-ctx.Done():
				return
			}
		}
	}()
	fmt.Fprint(os.Stdout, "\x1b[?1049h\x1b[?25l")
	defer func() { cancel(); <-readerDone; terminalRestore(state); fmt.Fprint(os.Stdout, "\x1b[?25h\x1b[?1049l") }()
	tr := &Tracker{}
	attention := o.Attention
	var latest *Event
	for ctx.Err() == nil {
		r := tr.Update(read(ctx), time.Now())
		if ctx.Err() != nil {
			return nil
		}
		visible := Filter(r.Sessions, o.Agent, attention)
		fmt.Fprint(os.Stdout, "\x1b[H\x1b[2J")
		if err := outputResult(r, "status", Options{Agent: o.Agent, Attention: attention}); err != nil {
			return err
		}
		for _, event := range r.Events {
			if o.Agent == "" || o.Agent == event.Agent {
				e := event
				latest = &e
				if o.Bell && !o.Demo {
					fmt.Fprint(os.Stdout, "\a")
				}
			}
		}
		if latest != nil {
			fmt.Fprintf(os.Stdout, "\n最新の変化: %s %s → %s (%s)\n", Clean(latest.PaneID), Clean(latest.Project), phaseLabel(latest.Type), latest.At.Format("15:04:05"))
		}
		mode := "承認待ち・入力待ちへ"
		if attention {
			mode = "全状態へ"
		}
		navigation := "1〜9 で移動"
		if o.Demo {
			navigation = "デモ表示（移動・ベル無効）"
		}
		fmt.Fprintf(os.Stdout, "\nq 終了 · a %s · %s\n", mode, navigation)
		timer := time.NewTimer(o.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case k, ok := <-keys:
			timer.Stop()
			if !ok || k == 'q' || k == 3 || k == 4 {
				return nil
			}
			if k == 'a' {
				attention = !attention
			}
			if k >= '1' && k <= '9' && !o.Demo {
				index := int(k - '1')
				if index < len(visible) {
					if e := Jump(ctx, visible[index].PaneID, o); e == nil {
						return nil
					}
					fmt.Fprintln(os.Stdout, "移動できませんでした。対象ペインを確認してください。")
				}
			}
		case <-timer.C:
		}
	}
	return nil
}
func terminalRaw() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "stty", "-g")
	cmd.Stdin = os.Stdin
	state, err := cmd.Output()
	if err != nil {
		return "", err
	}
	// Timed reads let the input goroutine exit on cancellation before restoring tty.
	cmd = exec.CommandContext(ctx, "stty", "-icanon", "-echo", "min", "0", "time", "1")
	cmd.Stdin = os.Stdin
	if err = cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(state)), nil
}
func terminalRestore(state string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "stty", state)
	cmd.Stdin = os.Stdin
	_ = cmd.Run()
}
