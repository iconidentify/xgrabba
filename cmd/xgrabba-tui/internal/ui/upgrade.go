package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// createUpgradePanel creates the upgrade management panel.
func (a *App) createUpgradePanel() {
	// Current version info
	currentBox := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	currentBox.SetBorder(true).SetTitle(" Current Version ")

	// Available version info
	availableBox := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	availableBox.SetBorder(true).SetTitle(" Available Version ")

	// Upgrade progress
	progressBox := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetChangedFunc(func() {
			a.app.Draw()
		})
	progressBox.SetBorder(true).SetTitle(" Upgrade Progress ")

	// Buttons
	buttonFlex := tview.NewFlex()

	a.upgradeCheckButton = tview.NewButton("Check for Updates").SetSelectedFunc(func() {
		go a.checkForUpdates(availableBox)
	})
	a.upgradeCheckButton.SetBackgroundColor(tcell.ColorDarkCyan)

	a.upgradeButton = tview.NewButton("Upgrade").SetSelectedFunc(func() {
		go a.performUpgrade(progressBox)
	})
	a.upgradeButton.SetBackgroundColor(tcell.ColorDarkGreen)

	a.upgradeForceButton = tview.NewButton("Force Upgrade").SetSelectedFunc(func() {
		go a.performUpgrade(progressBox)
	})
	a.upgradeForceButton.SetBackgroundColor(tcell.ColorDarkRed)

	buttonFlex.
		AddItem(nil, 0, 1, false).
		AddItem(a.upgradeCheckButton, 20, 0, true).
		AddItem(nil, 2, 0, false).
		AddItem(a.upgradeButton, 12, 0, false).
		AddItem(nil, 2, 0, false).
		AddItem(a.upgradeForceButton, 16, 0, false).
		AddItem(nil, 0, 1, false)

	// Top row - current and available versions
	topRow := tview.NewFlex().
		AddItem(currentBox, 0, 1, false).
		AddItem(availableBox, 0, 1, false)

	// Main layout
	a.upgradeView = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(topRow, 12, 0, false).
		AddItem(buttonFlex, 3, 0, true).
		AddItem(progressBox, 0, 1, false)

	// Navigation between buttons - handle at button level
	navigateButtons := func(direction int) {
		currentFocus := a.app.GetFocus()
		if currentFocus == a.upgradeCheckButton {
			if direction > 0 {
				a.app.SetFocus(a.upgradeButton)
			} else {
				a.app.SetFocus(a.upgradeForceButton)
			}
		} else if currentFocus == a.upgradeButton {
			if direction > 0 {
				a.app.SetFocus(a.upgradeForceButton)
			} else {
				a.app.SetFocus(a.upgradeCheckButton)
			}
		} else if currentFocus == a.upgradeForceButton {
			if direction > 0 {
				a.app.SetFocus(a.upgradeCheckButton)
			} else {
				a.app.SetFocus(a.upgradeButton)
			}
		}
	}

	// Set input capture on each button for arrow key navigation
	a.upgradeCheckButton.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyRight:
			a.app.SetFocus(a.upgradeButton)
			return nil
		case tcell.KeyLeft:
			a.app.SetFocus(a.upgradeForceButton)
			return nil
		case tcell.KeyTab:
			navigateButtons(1)
			return nil
		}
		return event
	})

	a.upgradeButton.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyRight:
			a.app.SetFocus(a.upgradeForceButton)
			return nil
		case tcell.KeyLeft:
			a.app.SetFocus(a.upgradeCheckButton)
			return nil
		case tcell.KeyTab:
			navigateButtons(1)
			return nil
		}
		return event
	})

	a.upgradeForceButton.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyRight:
			a.app.SetFocus(a.upgradeCheckButton)
			return nil
		case tcell.KeyLeft:
			a.app.SetFocus(a.upgradeButton)
			return nil
		case tcell.KeyTab:
			navigateButtons(1)
			return nil
		}
		return event
	})

	// Global navigation keys should pass through
	a.upgradeView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// Allow global navigation keys to pass through
		switch event.Key() {
		case tcell.KeyEscape, tcell.KeyF1, tcell.KeyF2, tcell.KeyF3, tcell.KeyF4, tcell.KeyF5, tcell.KeyF6:
			return event
		case tcell.KeyRune:
			switch event.Rune() {
			case '1', '2', '3', '4', '5', '6', 'q', 'Q', '?':
				return event
			}
		}
		return event
	})

	// Initial data load
	go a.updateCurrentVersion(currentBox)
}

// updateCurrentVersion updates the current version display.
func (a *App) updateCurrentVersion(view *tview.TextView) {
	a.app.QueueUpdateDraw(func() {
		view.Clear()
		fmt.Fprintln(view, "[dim]Loading current version...[white]")
	})

	status := a.getStatus()

	a.app.QueueUpdateDraw(func() {
		view.Clear()

		if status == nil {
			fmt.Fprintln(view, "[yellow]Unable to fetch status[white]")
			return
		}

		if status.Release != nil {
			fmt.Fprintf(view, "[white::b]Release:[white] %s\n", status.Release.Name)
			fmt.Fprintf(view, "[white::b]Chart:[white] %s\n", status.Release.ChartName)
			fmt.Fprintf(view, "[white::b]Version:[white] [cyan]%s[white]\n", status.Release.ChartVersion)
			if status.Release.AppVersion != "" {
				fmt.Fprintf(view, "[white::b]App Version:[white] %s\n", status.Release.AppVersion)
			}
			fmt.Fprintln(view)

			if status.Release.Ready {
				fmt.Fprintln(view, "[green]Status: Ready[white]")
			} else {
				fmt.Fprintln(view, "[red]Status: Not Ready[white]")
				if status.Release.Message != "" {
					fmt.Fprintf(view, "[red]%s[white]\n", status.Release.Message)
				}
			}
		} else {
			fmt.Fprintln(view, "[yellow]No Crossplane release found[white]")
			fmt.Fprintln(view, "[dim]Using direct Helm or kubectl?[white]")
		}

		fmt.Fprintln(view)
		fmt.Fprintln(view, "[white::b]Deployed Images:[white]")
		for _, pod := range status.Pods {
			for _, c := range pod.Containers {
				fmt.Fprintf(view, "  %s:\n", c.Name)
				fmt.Fprintf(view, "    [dim]%s[white]\n", c.Image)
			}
		}
	})
}

// checkForUpdates checks for available updates.
func (a *App) checkForUpdates(view *tview.TextView) {
	a.app.QueueUpdateDraw(func() {
		view.Clear()
		fmt.Fprintln(view, "[dim]Checking for updates...[white]")
	})

	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	latestVersion, err := a.k8sClient.GetLatestChartVersion(ctx, a.cfg.HelmRepo)

	a.app.QueueUpdateDraw(func() {
		view.Clear()

		if err != nil {
			fmt.Fprintf(view, "[red]Error checking updates:[white]\n%v\n", err)
			return
		}

		fmt.Fprintf(view, "[white::b]Latest Chart Version:[white]\n")
		fmt.Fprintf(view, "[green::b]%s[white]\n\n", latestVersion)

		// Compare with current
		status := a.getStatus()
		if status != nil && status.Release != nil {
			currentVersion := status.Release.ChartVersion
			if currentVersion == latestVersion {
				fmt.Fprintln(view, "[green]You are running the latest version![white]")
			} else if currentVersion == "" {
				fmt.Fprintln(view, "[yellow]Current version unknown[white]")
			} else {
				fmt.Fprintf(view, "[yellow]Update available![white]\n")
				fmt.Fprintf(view, "Current: %s\n", currentVersion)
				fmt.Fprintf(view, "Latest:  %s\n", latestVersion)
			}
		}

		fmt.Fprintln(view)
		fmt.Fprintf(view, "[dim]Repository: %s[white]\n", a.cfg.HelmRepo)
	})
}

// performUpgrade performs the actual upgrade.
func (a *App) performUpgrade(view *tview.TextView) {
	a.app.QueueUpdateDraw(func() {
		view.Clear()
		fmt.Fprintln(view, "[yellow::b]Starting upgrade...[white]")
		fmt.Fprintln(view)
	})

	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
	defer cancel()

	// Log progress
	logProgress := func(msg string) {
		a.app.QueueUpdateDraw(func() {
			timestamp := time.Now().Format("15:04:05")
			fmt.Fprintf(view, "[dim]%s[white] %s\n", timestamp, msg)
		})
	}

	logProgress("Triggering Crossplane release upgrade...")

	// Trigger upgrade
	if err := a.k8sClient.TriggerUpgrade(ctx); err != nil {
		logProgress(fmt.Sprintf("[red]Failed to trigger upgrade: %v[white]", err))
		return
	}

	logProgress("[green]Upgrade triggered[white]")
	logProgress("Waiting for Crossplane to reconcile...")

	// Wait a moment for Crossplane to pick up the change
	time.Sleep(5 * time.Second)

	// Monitor release status
	logProgress("Monitoring Helm release status...")

	for i := 0; i < 60; i++ {
		select {
		case <-ctx.Done():
			logProgress("[red]Upgrade timed out[white]")
			return
		default:
		}

		release, err := a.k8sClient.GetHelmRelease(ctx)
		if err != nil {
			logProgress(fmt.Sprintf("[yellow]Waiting for release... (%v)[white]", err))
			time.Sleep(2 * time.Second)
			continue
		}

		statusStr := fmt.Sprintf("Release: version=%s ready=%v synced=%v",
			release.ChartVersion, release.Ready, release.Synced)
		logProgress(statusStr)

		if release.Ready && release.Synced {
			logProgress("[green]Release is ready![white]")
			break
		}

		time.Sleep(2 * time.Second)
	}

	// Wait for deployment rollout
	logProgress("Waiting for deployment rollout...")
	if err := a.k8sClient.WaitForRollout(ctx, a.cfg.ReleaseName, 2*time.Minute); err != nil {
		logProgress(fmt.Sprintf("[yellow]Deployment rollout: %v[white]", err))
	} else {
		logProgress("[green]Deployment rollout complete[white]")
	}

	// Check for USB Manager DaemonSet
	status := a.getStatus()
	if status != nil {
		for _, ds := range status.DaemonSets {
			logProgress(fmt.Sprintf("Waiting for DaemonSet %s rollout...", ds.Name))
			if err := a.k8sClient.WaitForDaemonSetRollout(ctx, ds.Name, 2*time.Minute); err != nil {
				logProgress(fmt.Sprintf("[yellow]DaemonSet rollout: %v[white]", err))
			} else {
				logProgress(fmt.Sprintf("[green]DaemonSet %s rollout complete[white]", ds.Name))
			}
		}
	}

	// Refresh status
	logProgress("Refreshing status...")
	a.refreshStatus()

	// Health checks
	logProgress("Running health checks...")
	time.Sleep(5 * time.Second) // Give pods time to start

	pods, _ := a.k8sClient.GetPodNames(ctx, "app.kubernetes.io/name=xgrabba")
	for _, podName := range pods {
		health, _ := a.k8sClient.CheckHealth(ctx, podName, 9847, "health")
		if health != nil && health.Healthy {
			logProgress(fmt.Sprintf("[green]%s health: OK[white]", podName))
		} else {
			logProgress(fmt.Sprintf("[yellow]%s health: checking...[white]", podName))
		}
	}

	logProgress("")
	logProgress("[green::b]=== Upgrade Complete ===[white]")

	// Update current version display
	if a.upgradeView.GetItemCount() > 0 {
		topRow := a.upgradeView.GetItem(0).(*tview.Flex)
		if topRow.GetItemCount() > 0 {
			currentBox := topRow.GetItem(0).(*tview.TextView)
			go a.updateCurrentVersion(currentBox)
		}
	}
}
