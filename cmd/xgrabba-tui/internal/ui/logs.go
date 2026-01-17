package ui

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// LogSource represents a log source type.
type LogSource int

const (
	LogSourceMainApp LogSource = iota
	LogSourceUSBManager
	LogSourcePod
	LogSourceAll
)

// createLogsPanel creates the logs viewing panel.
func (a *App) createLogsPanel() {
	a.logsView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetChangedFunc(func() {
			a.app.Draw()
		})
	a.logsView.SetBorder(true).SetTitle(" Logs - Press 'm' main, 'u' USB, 'a' all, 'p' pause, 'b' back ")

	// Key bindings
	a.logsView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// Allow navigation keys to pass through to global handler
		switch event.Key() {
		case tcell.KeyEscape, tcell.KeyF1, tcell.KeyF2, tcell.KeyF3, tcell.KeyF4, tcell.KeyF5, tcell.KeyF6:
			return event // Pass through to global handler
		case tcell.KeyRune:
			// Allow navigation number keys and 'q' to pass through
			switch event.Rune() {
			case '1', '2', '3', '4', '5', '6', '7', '8', '9', 'q', 'Q', '?':
				return event // Pass through to global handler
			case 'm', 'M':
				a.streamLogsByLabel("app.kubernetes.io/name=xgrabba")
				return nil
			case 'u', 'U':
				a.streamLogsByLabel("app.kubernetes.io/name=xgrabba-usb-manager")
				return nil
			case 'a', 'A':
				a.streamAllLogs()
				return nil
			case 'c', 'C':
				a.logsView.Clear()
				return nil
			case 'p', 'P':
				// Pause/resume auto-scroll
				a.logsAutoScroll = !a.logsAutoScroll
				if a.logsAutoScroll {
					a.updateStatusBar("[green]Log auto-scroll enabled")
				} else {
					a.updateStatusBar("[yellow]Log auto-scroll paused")
				}
				return nil
			case 'b', 'B':
				a.switchPanel(PanelDashboard)
				return nil
			}
		case tcell.KeyCtrlC:
			if a.logCancel != nil {
				a.logCancel()
				a.logCancel = nil
			}
			return nil
		}
		return event
	})
}

// streamLogs streams logs from a specific pod.
func (a *App) streamLogs(podName string) {
	// Cancel any existing log stream
	if a.logCancel != nil {
		a.logCancel()
	}

	ctx, cancel := context.WithCancel(a.ctx)
	a.logCancel = cancel

	a.app.QueueUpdateDraw(func() {
		a.logsView.Clear()
		a.logsView.SetTitle(fmt.Sprintf(" Logs: %s - Press Ctrl+C to stop ", podName))
	})

	reader, err := a.k8sClient.GetLogs(ctx, podName, true, 100)
	if err != nil {
		a.app.QueueUpdateDraw(func() {
			fmt.Fprintf(a.logsView, "[red]Error starting log stream: %v[white]\n", err)
		})
		return
	}
	defer reader.Close()

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
			line := scanner.Text()
			a.app.QueueUpdateDraw(func() {
				fmt.Fprintln(a.logsView, colorizeLogLine(line))
				if a.logsAutoScroll {
					a.logsView.ScrollToEnd()
				}
			})
		}
	}
}

// streamLogsByLabel streams logs from pods matching a label.
func (a *App) streamLogsByLabel(label string) {
	// Cancel any existing log stream
	if a.logCancel != nil {
		a.logCancel()
	}

	ctx, cancel := context.WithCancel(a.ctx)
	a.logCancel = cancel

	displayName := label
	if strings.Contains(label, "xgrabba-usb") {
		displayName = "USB Manager"
	} else if strings.Contains(label, "xgrabba") {
		displayName = "Main App"
	}

	a.app.QueueUpdateDraw(func() {
		a.logsView.Clear()
		a.logsView.SetTitle(fmt.Sprintf(" Logs: %s - Press Ctrl+C to stop ", displayName))
	})

	reader, err := a.k8sClient.GetLogsByLabel(ctx, label, true, 100)
	if err != nil {
		a.app.QueueUpdateDraw(func() {
			fmt.Fprintf(a.logsView, "[red]Error starting log stream: %v[white]\n", err)
		})
		return
	}
	defer reader.Close()

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
			line := scanner.Text()
			a.app.QueueUpdateDraw(func() {
				fmt.Fprintln(a.logsView, colorizeLogLine(line))
				if a.logsAutoScroll {
					a.logsView.ScrollToEnd()
				}
			})
		}
	}
}

// streamAllLogs streams logs from all pods.
func (a *App) streamAllLogs() {
	// Cancel any existing log stream
	if a.logCancel != nil {
		a.logCancel()
	}

	ctx, cancel := context.WithCancel(a.ctx)
	a.logCancel = cancel

	a.app.QueueUpdateDraw(func() {
		a.logsView.Clear()
		a.logsView.SetTitle(" Logs: All Pods - Press Ctrl+C to stop ")
	})

	// Get all pod names
	status := a.getStatus()
	if status == nil || len(status.Pods) == 0 {
		a.app.QueueUpdateDraw(func() {
			fmt.Fprintln(a.logsView, "[yellow]No pods found[white]")
		})
		return
	}

	// Stream from each pod
	for _, pod := range status.Pods {
		go func(podName string) {
			reader, err := a.k8sClient.GetLogs(ctx, podName, true, 50)
			if err != nil {
				return
			}
			defer reader.Close()

			scanner := bufio.NewScanner(reader)
			for scanner.Scan() {
				select {
				case <-ctx.Done():
					return
				default:
					line := scanner.Text()
					a.app.QueueUpdateDraw(func() {
						timestamp := time.Now().Format("15:04:05")
						fmt.Fprintf(a.logsView, "[dim]%s[white] [cyan]%s[white] %s\n",
							timestamp, podName, colorizeLogLine(line))
						if a.logsAutoScroll {
							a.logsView.ScrollToEnd()
						}
					})
				}
			}
		}(pod.Name)
	}
}

// colorizeLogLine adds color to log lines based on content.
func colorizeLogLine(line string) string {
	lower := strings.ToLower(line)

	// Error patterns
	if strings.Contains(lower, "error") || strings.Contains(lower, "fail") ||
		strings.Contains(lower, "fatal") || strings.Contains(lower, "panic") {
		return "[red]" + line + "[white]"
	}

	// Warning patterns
	if strings.Contains(lower, "warn") || strings.Contains(lower, "warning") {
		return "[yellow]" + line + "[white]"
	}

	// Info patterns
	if strings.Contains(lower, "info") {
		return "[green]" + line + "[white]"
	}

	// Debug patterns
	if strings.Contains(lower, "debug") {
		return "[dim]" + line + "[white]"
	}

	// HTTP success
	if strings.Contains(line, " 200 ") || strings.Contains(line, " 201 ") ||
		strings.Contains(line, " 204 ") {
		return "[green]" + line + "[white]"
	}

	// HTTP errors
	if strings.Contains(line, " 4") || strings.Contains(line, " 5") {
		if strings.Contains(line, " 400 ") || strings.Contains(line, " 401 ") ||
			strings.Contains(line, " 403 ") || strings.Contains(line, " 404 ") ||
			strings.Contains(line, " 500 ") || strings.Contains(line, " 502 ") ||
			strings.Contains(line, " 503 ") {
			return "[yellow]" + line + "[white]"
		}
	}

	return line
}
