package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// createExecPanel creates the remote command execution panel.
func (a *App) createExecPanel() {
	// Pod selector
	podList := tview.NewList().
		ShowSecondaryText(false)
	podList.SetBorder(true).SetTitle(" Select Pod ")

	// Command input
	a.commandInput = tview.NewInputField().
		SetLabel("Command: ").
		SetFieldWidth(0).
		SetFieldBackgroundColor(tcell.ColorDarkBlue)
	a.commandInput.SetBorder(true).SetTitle(" Enter Command ")

	// Output view
	outputView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetChangedFunc(func() {
			a.app.Draw()
		})
	outputView.SetBorder(true).SetTitle(" Output ")

	// Quick commands buttons
	quickCommands := tview.NewFlex()

	addQuickCmd := func(label, cmd string) {
		btn := tview.NewButton(label).SetSelectedFunc(func() {
			a.commandInput.SetText(cmd)
			a.app.SetFocus(a.commandInput)
		})
		btn.SetBackgroundColor(tcell.ColorDarkBlue)
		quickCommands.AddItem(btn, len(label)+4, 0, false)
		quickCommands.AddItem(nil, 1, 0, false)
	}

	addQuickCmd("Health", "wget -q -O- http://localhost:9847/health || curl -sf http://localhost:9847/health")
	addQuickCmd("Disk", "df -h")
	addQuickCmd("Memory", "free -h || cat /proc/meminfo | head -5")
	addQuickCmd("Process", "ps aux")
	addQuickCmd("Env", "env | sort")
	addQuickCmd("Files", "ls -la /data/videos | head -20")

	quickCommands.SetBorder(true).SetTitle(" Quick Commands ")

	// Left panel with pod list and quick commands
	leftPanel := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(podList, 0, 1, true).
		AddItem(quickCommands, 5, 0, false)

	// Right panel with command input and output
	rightPanel := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.commandInput, 3, 0, false).
		AddItem(outputView, 0, 1, false)

	// Main layout
	a.execView = tview.NewFlex().
		AddItem(leftPanel, 30, 0, true).
		AddItem(rightPanel, 0, 1, false)

	// Handle pod selection
	var selectedPodForExec string
	podList.SetSelectedFunc(func(index int, mainText string, secondaryText string, shortcut rune) {
		selectedPodForExec = mainText
		a.app.SetFocus(a.commandInput)
		outputView.Clear()
		fmt.Fprintf(outputView, "[dim]Selected pod: %s[white]\n", mainText)
		fmt.Fprintln(outputView, "[dim]Enter a command and press Enter to execute[white]")
	})

	// Handle command execution
	a.commandInput.SetDoneFunc(func(key tcell.Key) {
		if key != tcell.KeyEnter {
			return
		}

		cmd := a.commandInput.GetText()
		if cmd == "" || selectedPodForExec == "" {
			return
		}

		outputView.Clear()
		fmt.Fprintf(outputView, "[yellow]Executing on %s:[white] %s\n\n", selectedPodForExec, cmd)

		go func() {
			ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
			defer cancel()

			output, err := a.k8sClient.Exec(ctx, selectedPodForExec, []string{"sh", "-c", cmd})

			a.app.QueueUpdateDraw(func() {
				if err != nil {
					fmt.Fprintf(outputView, "[red]Error:[white] %v\n\n", err)
				}
				if output != "" {
					fmt.Fprintln(outputView, output)
				}
				fmt.Fprintln(outputView, "[dim]---[white]")
			})
		}()

		// Clear input for next command
		a.commandInput.SetText("")
	})

	// Handle Tab navigation
	a.execView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			if a.app.GetFocus() == podList {
				a.app.SetFocus(a.commandInput)
			} else if a.app.GetFocus() == a.commandInput {
				a.app.SetFocus(outputView)
			} else {
				a.app.SetFocus(podList)
			}
			return nil
		case tcell.KeyBacktab:
			if a.app.GetFocus() == podList {
				a.app.SetFocus(outputView)
			} else if a.app.GetFocus() == a.commandInput {
				a.app.SetFocus(podList)
			} else {
				a.app.SetFocus(a.commandInput)
			}
			return nil
		}
		return event
	})

	// Populate pod list when panel is shown
	go a.updateExecPodList(podList)
}

// updateExecPodList updates the pod list in the exec panel.
func (a *App) updateExecPodList(podList *tview.List) {
	// Wait for initial status
	for i := 0; i < 10; i++ {
		status := a.getStatus()
		if status != nil && len(status.Pods) > 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	a.app.QueueUpdateDraw(func() {
		podList.Clear()

		status := a.getStatus()
		if status == nil {
			podList.AddItem("No status available", "", 0, nil)
			return
		}

		for _, pod := range status.Pods {
			statusIcon := "[green]R[white]"
			if pod.Status != "Running" {
				statusIcon = "[red]X[white]"
			}

			// Store pod name in main text
			display := fmt.Sprintf("%s %s", statusIcon, pod.Name)
			podList.AddItem(pod.Name, "", 0, nil)

			// Update the display after adding
			idx := podList.GetItemCount() - 1
			podList.SetItemText(idx, display, "")
		}

		if podList.GetItemCount() == 0 {
			podList.AddItem("No pods found", "", 0, nil)
		}
	})
}

// Helper to get actual pod name from display text
func extractPodName(display string) string {
	// Remove status icon prefix
	parts := strings.SplitN(display, " ", 2)
	if len(parts) > 1 {
		return strings.TrimSpace(parts[1])
	}
	return display
}
