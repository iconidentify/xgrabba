package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// createDashboardPanel creates the main dashboard panel.
func (a *App) createDashboardPanel() {
	// Overview box
	overviewBox := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	overviewBox.SetBorder(true).SetTitle(" Overview ")

	// Pods summary box
	podsBox := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	podsBox.SetBorder(true).SetTitle(" Pods ")

	// Services box
	servicesBox := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	servicesBox.SetBorder(true).SetTitle(" Services ")

	// Storage box
	storageBox := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	storageBox.SetBorder(true).SetTitle(" Storage ")

	// Events box
	eventsBox := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	eventsBox.SetBorder(true).SetTitle(" Recent Events ")

	// Issues box
	issuesBox := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	issuesBox.SetBorder(true).SetTitle(" Issues ")
	issuesBox.SetBackgroundColor(tcell.ColorDefault)

	// Layout - top row with overview and pods
	topRow := tview.NewFlex().
		AddItem(overviewBox, 0, 1, false).
		AddItem(podsBox, 0, 1, false)

	// Middle row with services and storage
	middleRow := tview.NewFlex().
		AddItem(servicesBox, 0, 1, false).
		AddItem(storageBox, 0, 1, false)

	// Bottom row with events and issues
	bottomRow := tview.NewFlex().
		AddItem(eventsBox, 0, 2, false).
		AddItem(issuesBox, 0, 1, false)

	// Main dashboard flex
	a.dashboardView = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(topRow, 0, 1, false).
		AddItem(middleRow, 0, 1, false).
		AddItem(bottomRow, 0, 1, false)

	// Store references for updates
	a.dashboardView.SetTitle("Dashboard")
}

// updateDashboard updates the dashboard with current status.
func (a *App) updateDashboard() {
	status := a.getStatus()
	if status == nil {
		return
	}

	// Get dashboard flex items
	if a.dashboardView.GetItemCount() < 3 {
		return
	}

	topRow := a.dashboardView.GetItem(0).(*tview.Flex)
	middleRow := a.dashboardView.GetItem(1).(*tview.Flex)
	bottomRow := a.dashboardView.GetItem(2).(*tview.Flex)

	overviewBox := topRow.GetItem(0).(*tview.TextView)
	podsBox := topRow.GetItem(1).(*tview.TextView)
	servicesBox := middleRow.GetItem(0).(*tview.TextView)
	storageBox := middleRow.GetItem(1).(*tview.TextView)
	eventsBox := bottomRow.GetItem(0).(*tview.TextView)
	issuesBox := bottomRow.GetItem(1).(*tview.TextView)

	// Update overview
	var overviewText strings.Builder
	overviewText.WriteString(fmt.Sprintf("[white::b]Namespace:[white] %s\n", a.cfg.Namespace))
	overviewText.WriteString(fmt.Sprintf("[white::b]Release:[white] %s\n\n", a.cfg.ReleaseName))

	if status.Release != nil {
		overviewText.WriteString(fmt.Sprintf("[white::b]Chart:[white] %s\n", status.Release.ChartName))
		overviewText.WriteString(fmt.Sprintf("[white::b]Version:[white] %s\n", status.Release.ChartVersion))
		if status.Release.Ready {
			overviewText.WriteString("[green]Ready: Yes[white]\n")
		} else {
			overviewText.WriteString("[red]Ready: No[white]\n")
			if status.Release.Message != "" {
				overviewText.WriteString(fmt.Sprintf("[red]%s[white]\n", status.Release.Message))
			}
		}
		if status.Release.Synced {
			overviewText.WriteString("[green]Synced: Yes[white]\n")
		} else {
			overviewText.WriteString("[yellow]Synced: No[white]\n")
		}
	} else {
		overviewText.WriteString("[yellow]No Crossplane release found[white]\n")
	}
	overviewBox.SetText(overviewText.String())

	// Update pods
	var podsText strings.Builder
	if len(status.Pods) == 0 {
		podsText.WriteString("[yellow]No pods found[white]")
	} else {
		for _, pod := range status.Pods {
			statusColor := "green"
			if pod.Status != "Running" {
				statusColor = "red"
			}
			restartColor := "white"
			if pod.Restarts > 0 {
				restartColor = "yellow"
			}
			if pod.Restarts > 5 {
				restartColor = "red"
			}
			podsText.WriteString(fmt.Sprintf("[%s]%s[white]\n", statusColor, pod.Name))
			podsText.WriteString(fmt.Sprintf("  Status: [%s]%s[white] Ready: %s\n", statusColor, pod.Status, pod.Ready))
			podsText.WriteString(fmt.Sprintf("  Restarts: [%s]%d[white] Age: %s\n", restartColor, pod.Restarts, pod.Age))
			podsText.WriteString(fmt.Sprintf("  Node: %s\n\n", pod.Node))
		}
	}
	podsBox.SetText(podsText.String())

	// Update services
	var servicesText strings.Builder
	if len(status.Services) == 0 {
		servicesText.WriteString("[yellow]No services found[white]")
	} else {
		for _, svc := range status.Services {
			servicesText.WriteString(fmt.Sprintf("[white::b]%s[white]\n", svc.Name))
			servicesText.WriteString(fmt.Sprintf("  Type: %s\n", svc.Type))
			servicesText.WriteString(fmt.Sprintf("  ClusterIP: %s\n", svc.ClusterIP))
			if svc.ExternalIP != "<none>" {
				servicesText.WriteString(fmt.Sprintf("  External: [green]%s[white]\n", svc.ExternalIP))
			}
			servicesText.WriteString(fmt.Sprintf("  Ports: %s\n\n", svc.Ports))
		}
	}
	servicesBox.SetText(servicesText.String())

	// Update storage
	var storageText strings.Builder
	if len(status.PVCs) == 0 {
		storageText.WriteString("[yellow]No PVCs found[white]")
	} else {
		for _, pvc := range status.PVCs {
			statusColor := "green"
			if pvc.Status != "Bound" {
				statusColor = "red"
			}
			storageText.WriteString(fmt.Sprintf("[white::b]%s[white]\n", pvc.Name))
			storageText.WriteString(fmt.Sprintf("  Status: [%s]%s[white]\n", statusColor, pvc.Status))
			storageText.WriteString(fmt.Sprintf("  Capacity: %s\n", pvc.Capacity))
			storageText.WriteString(fmt.Sprintf("  Class: %s\n", pvc.StorageClass))
			storageText.WriteString(fmt.Sprintf("  Access: %s\n\n", pvc.AccessModes))
		}
	}

	// DaemonSets
	if len(status.DaemonSets) > 0 {
		storageText.WriteString("[white::b]DaemonSets:[white]\n")
		for _, ds := range status.DaemonSets {
			statusColor := "green"
			if ds.Ready < ds.Desired {
				statusColor = "yellow"
			}
			storageText.WriteString(fmt.Sprintf("  [%s]%s[white]: %d/%d ready\n", statusColor, ds.Name, ds.Ready, ds.Desired))
		}
	}
	storageBox.SetText(storageText.String())

	// Update events
	var eventsText strings.Builder
	if len(status.Events) == 0 {
		eventsText.WriteString("[dim]No recent events[white]")
	} else {
		for _, event := range status.Events {
			typeColor := "white"
			if event.Type == "Warning" {
				typeColor = "yellow"
			}
			eventsText.WriteString(fmt.Sprintf("[%s]%s[white] [dim]%s[white]\n", typeColor, event.Reason, event.Age))
			eventsText.WriteString(fmt.Sprintf("  %s\n", truncateString(event.Message, 60)))
			eventsText.WriteString(fmt.Sprintf("  [dim]%s[white]\n\n", event.Object))
		}
	}
	eventsBox.SetText(eventsText.String())

	// Update issues
	var issuesText strings.Builder
	if len(status.Issues) == 0 {
		issuesText.WriteString("[green]No issues detected[white]\n\n")
		issuesText.WriteString("[dim]All systems operational[white]")
	} else {
		for _, issue := range status.Issues {
			if strings.Contains(issue, "restart") || strings.Contains(issue, "Warning") {
				issuesText.WriteString(fmt.Sprintf("[yellow]! %s[white]\n", issue))
			} else {
				issuesText.WriteString(fmt.Sprintf("[red]! %s[white]\n", issue))
			}
		}
	}
	issuesBox.SetText(issuesText.String())
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
