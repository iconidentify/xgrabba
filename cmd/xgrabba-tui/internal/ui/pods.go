package ui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// createPodsPanel creates the pods panel with detailed pod information.
func (a *App) createPodsPanel() {
	a.podsTable = tview.NewTable().
		SetBorders(false).
		SetSelectable(true, false).
		SetFixed(1, 0)
	a.podsTable.SetBorder(true).SetTitle(" Pods - Press Enter to view logs, 'd' for describe, 's' for shell ")

	// Set header style
	a.podsTable.SetSelectedStyle(tcell.StyleDefault.
		Foreground(tcell.ColorWhite).
		Background(tcell.ColorDarkCyan))

	// Header row
	headers := []string{"NAME", "STATUS", "READY", "RESTARTS", "AGE", "NODE", "IP"}
	for i, h := range headers {
		cell := tview.NewTableCell(h).
			SetTextColor(tcell.ColorYellow).
			SetSelectable(false).
			SetExpansion(1)
		if i == 0 {
			cell.SetExpansion(2)
		}
		a.podsTable.SetCell(0, i, cell)
	}

	// Key bindings for pods table
	a.podsTable.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		row, _ := a.podsTable.GetSelection()
		if row == 0 {
			return event
		}

		podCell := a.podsTable.GetCell(row, 0)
		if podCell == nil {
			return event
		}
		podName := podCell.Text

		switch event.Key() {
		case tcell.KeyEnter:
			// View logs
			a.viewPodLogs(podName)
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case 'd', 'D':
				// Describe pod
				a.describePod(podName)
				return nil
			case 's', 'S':
				// Shell into pod
				a.shellIntoPod(podName)
				return nil
			case 'l', 'L':
				// View logs
				a.viewPodLogs(podName)
				return nil
			case 'h', 'H':
				// Health check
				a.checkPodHealth(podName)
				return nil
			}
		}
		return event
	})
}

// updatePodsTable updates the pods table with current data.
func (a *App) updatePodsTable() {
	status := a.getStatus()
	if status == nil {
		return
	}

	// Clear existing rows (except header)
	for row := a.podsTable.GetRowCount() - 1; row > 0; row-- {
		a.podsTable.RemoveRow(row)
	}

	// Add pod rows
	for i, pod := range status.Pods {
		row := i + 1

		// Pod name
		nameCell := tview.NewTableCell(pod.Name).
			SetExpansion(2).
			SetTextColor(tcell.ColorWhite)
		a.podsTable.SetCell(row, 0, nameCell)

		// Status with color
		statusColor := tcell.ColorGreen
		if pod.Status != "Running" && pod.Status != "Succeeded" {
			statusColor = tcell.ColorRed
		}
		statusCell := tview.NewTableCell(pod.Status).
			SetExpansion(1).
			SetTextColor(statusColor)
		a.podsTable.SetCell(row, 1, statusCell)

		// Ready
		readyCell := tview.NewTableCell(pod.Ready).
			SetExpansion(1).
			SetTextColor(tcell.ColorWhite)
		a.podsTable.SetCell(row, 2, readyCell)

		// Restarts with color
		restartColor := tcell.ColorWhite
		if pod.Restarts > 0 {
			restartColor = tcell.ColorYellow
		}
		if pod.Restarts > 5 {
			restartColor = tcell.ColorRed
		}
		restartCell := tview.NewTableCell(fmt.Sprintf("%d", pod.Restarts)).
			SetExpansion(1).
			SetTextColor(restartColor)
		a.podsTable.SetCell(row, 3, restartCell)

		// Age
		ageCell := tview.NewTableCell(pod.Age).
			SetExpansion(1).
			SetTextColor(tcell.ColorWhite)
		a.podsTable.SetCell(row, 4, ageCell)

		// Node
		nodeCell := tview.NewTableCell(pod.Node).
			SetExpansion(1).
			SetTextColor(tcell.ColorWhite)
		a.podsTable.SetCell(row, 5, nodeCell)

		// IP
		ipCell := tview.NewTableCell(pod.IP).
			SetExpansion(1).
			SetTextColor(tcell.ColorWhite)
		a.podsTable.SetCell(row, 6, ipCell)
	}

	if len(status.Pods) == 0 {
		cell := tview.NewTableCell("No pods found").
			SetTextColor(tcell.ColorYellow)
		a.podsTable.SetCell(1, 0, cell)
	}
}

// viewPodLogs switches to logs view for a specific pod.
func (a *App) viewPodLogs(podName string) {
	a.selectedPod = podName
	a.switchPanel(PanelLogs)
	go a.streamLogs(podName)
}

// describePod shows pod description in a modal.
func (a *App) describePod(podName string) {
	modal := tview.NewModal().
		SetText(fmt.Sprintf("Fetching details for %s...", podName)).
		AddButtons([]string{"Close"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			a.pages.RemovePage("pod-describe")
		})

	a.pages.AddPage("pod-describe", modal, true, true)

	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		defer cancel()

		out, err := a.k8sClient.DescribePod(ctx, podName)
		if err != nil {
			out = fmt.Sprintf("Error fetching details: %v\n\n%s", err, out)
		}

		a.app.QueueUpdateDraw(func() {
			descView := tview.NewTextView().
				SetDynamicColors(true).
				SetScrollable(true)
			descView.SetBorder(true).SetTitle(fmt.Sprintf(" Pod: %s ", podName))
			descView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
				if event.Key() == tcell.KeyEscape || event.Rune() == 'q' {
					a.pages.RemovePage("pod-describe")
					return nil
				}
				return event
			})

			// Build detailed view
			status := a.getStatus()
			var text string
			for _, pod := range status.Pods {
				if pod.Name == podName {
					text = fmt.Sprintf("[yellow::b]Pod:[white] %s\n", pod.Name)
					text += fmt.Sprintf("[yellow::b]Status:[white] %s\n", pod.Status)
					text += fmt.Sprintf("[yellow::b]Ready:[white] %s\n", pod.Ready)
					text += fmt.Sprintf("[yellow::b]Restarts:[white] %d\n", pod.Restarts)
					text += fmt.Sprintf("[yellow::b]Node:[white] %s\n", pod.Node)
					text += fmt.Sprintf("[yellow::b]IP:[white] %s\n", pod.IP)
					text += fmt.Sprintf("[yellow::b]Age:[white] %s\n\n", pod.Age)

					text += "[yellow::b]Containers:[white]\n"
					for _, c := range pod.Containers {
						readyStatus := "[red]Not Ready"
						if c.Ready {
							readyStatus = "[green]Ready"
						}
						text += fmt.Sprintf("  [white::b]%s[white]\n", c.Name)
						text += fmt.Sprintf("    Image: %s\n", c.Image)
						text += fmt.Sprintf("    State: %s\n", c.State)
						text += fmt.Sprintf("    Status: %s[white]\n", readyStatus)
						text += fmt.Sprintf("    Restarts: %d\n\n", c.RestartCount)
					}
					break
				}
			}

			if text == "" {
				text = out
			}

			if out != "" {
				text += "\n[yellow::b]kubectl describe output[white]\n"
				text += out
			}

			text += "\n[dim]Press 'q' or Escape to close[white]"
			descView.SetText(text)

			a.pages.RemovePage("pod-describe")
			a.pages.AddPage("pod-describe", descView, true, true)
			a.app.SetFocus(descView)
		})
	}()
}

// shellIntoPod opens an interactive shell in the pod.
func (a *App) shellIntoPod(podName string) {
	a.app.Suspend(func() {
		ctx := context.Background()
		cmd := a.k8sClient.ExecInteractive(ctx, podName, []string{"sh"})
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		// Run interactive session
		_ = cmd.Run()
	})
}

// checkPodHealth performs health check on the pod.
func (a *App) checkPodHealth(podName string) {
	modal := tview.NewModal().
		SetText(fmt.Sprintf("Checking health for %s...", podName)).
		AddButtons([]string{"Close"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			a.pages.RemovePage("pod-health")
		})

	a.pages.AddPage("pod-health", modal, true, true)

	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
		defer cancel()

		healthStatus, _ := a.k8sClient.CheckHealth(ctx, podName, 9847, "health")
		readyStatus, _ := a.k8sClient.CheckHealth(ctx, podName, 9847, "ready")

		a.app.QueueUpdateDraw(func() {
			var text string
			text = fmt.Sprintf("[yellow::b]Health Check Results for %s[white]\n\n", podName)

			if healthStatus != nil {
				if healthStatus.Healthy {
					text += fmt.Sprintf("[green]/health: OK[white]\n  %s\n\n", healthStatus.Message)
				} else {
					text += fmt.Sprintf("[red]/health: FAILED[white]\n  %s\n\n", healthStatus.Message)
				}
			}

			if readyStatus != nil {
				if readyStatus.Healthy {
					text += fmt.Sprintf("[green]/ready: OK[white]\n  %s\n", readyStatus.Message)
				} else {
					text += fmt.Sprintf("[red]/ready: FAILED[white]\n  %s\n", readyStatus.Message)
				}
			}

			modal.SetText(text)
		})
	}()
}
