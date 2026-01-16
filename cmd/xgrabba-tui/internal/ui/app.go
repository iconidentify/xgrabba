// Package ui provides the terminal user interface for XGrabba TUI.
package ui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/iconidentify/xgrabba/cmd/xgrabba-tui/internal/config"
	"github.com/iconidentify/xgrabba/cmd/xgrabba-tui/internal/k8s"
	"github.com/iconidentify/xgrabba/cmd/xgrabba-tui/internal/ssh"
)

// Panel represents a UI panel type.
type Panel int

const (
	PanelDashboard Panel = iota
	PanelPods
	PanelLogs
	PanelUpgrade
	PanelExec
	PanelSSH
	PanelHelp
)

// App is the main TUI application.
type App struct {
	app         *tview.Application
	pages       *tview.Pages
	cfg         *config.Config
	k8sClient   *k8s.Client
	sshClient   *ssh.Client
	status      *k8s.ClusterStatus
	statusMu    sync.RWMutex
	currentPanel Panel
	ctx         context.Context
	cancel      context.CancelFunc

	// UI components
	mainFlex       *tview.Flex
	header         *tview.TextView
	footer         *tview.TextView
	statusBar      *tview.TextView
	dashboardView  *tview.Flex
	podsTable      *tview.Table
	logsView       *tview.TextView
	upgradeView    *tview.Flex
	execView       *tview.Flex
	sshView        *tview.Flex
	helpView       *tview.TextView
	commandInput   *tview.InputField

	// State
	selectedPod    string
	logCancel      context.CancelFunc
	refreshTicker  *time.Ticker
}

// NewApp creates a new TUI application.
func NewApp(cfg *config.Config) (*App, error) {
	ctx, cancel := context.WithCancel(context.Background())

	a := &App{
		app:       tview.NewApplication(),
		pages:     tview.NewPages(),
		cfg:       cfg,
		k8sClient: k8s.NewClient(cfg.Namespace, cfg.ReleaseName, cfg.KubeContext),
		sshClient: ssh.NewClient(cfg.SSHUser, cfg.SSHKeyPath),
		ctx:       ctx,
		cancel:    cancel,
	}

	a.setupUI()
	return a, nil
}

// setupUI initializes all UI components.
func (a *App) setupUI() {
	// Header
	a.header = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	a.header.SetBackgroundColor(tcell.ColorDarkBlue)
	a.updateHeader()

	// Footer with keybindings
	a.footer = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText("[yellow]1[white]:Dashboard [yellow]2[white]:Pods [yellow]3[white]:Logs [yellow]4[white]:Upgrade [yellow]5[white]:Exec [yellow]6[white]:SSH [yellow]?[white]:Help [yellow]q[white]:Quit")
	a.footer.SetBackgroundColor(tcell.ColorDarkBlue)

	// Status bar
	a.statusBar = tview.NewTextView().
		SetDynamicColors(true)
	a.statusBar.SetBackgroundColor(tcell.ColorDarkGreen)

	// Create panels
	a.createDashboardPanel()
	a.createPodsPanel()
	a.createLogsPanel()
	a.createUpgradePanel()
	a.createExecPanel()
	a.createSSHPanel()
	a.createHelpPanel()

	// Add panels to pages
	a.pages.AddPage("dashboard", a.dashboardView, true, true)
	a.pages.AddPage("pods", a.podsTable, true, false)
	a.pages.AddPage("logs", a.logsView, true, false)
	a.pages.AddPage("upgrade", a.upgradeView, true, false)
	a.pages.AddPage("exec", a.execView, true, false)
	a.pages.AddPage("ssh", a.sshView, true, false)
	a.pages.AddPage("help", a.helpView, true, false)

	// Main layout
	a.mainFlex = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header, 3, 0, false).
		AddItem(a.pages, 0, 1, true).
		AddItem(a.statusBar, 1, 0, false).
		AddItem(a.footer, 1, 0, false)

	// Global key bindings
	a.app.SetInputCapture(a.handleGlobalKeys)

	a.app.SetRoot(a.mainFlex, true)
}

// handleGlobalKeys handles global keyboard shortcuts.
func (a *App) handleGlobalKeys(event *tcell.EventKey) *tcell.EventKey {
	// Don't intercept when typing in input fields
	if a.app.GetFocus() == a.commandInput {
		if event.Key() == tcell.KeyEscape {
			a.app.SetFocus(a.pages)
			return nil
		}
		return event
	}

	switch event.Key() {
	case tcell.KeyRune:
		switch event.Rune() {
		case '1':
			a.switchPanel(PanelDashboard)
			return nil
		case '2':
			a.switchPanel(PanelPods)
			return nil
		case '3':
			a.switchPanel(PanelLogs)
			return nil
		case '4':
			a.switchPanel(PanelUpgrade)
			return nil
		case '5':
			a.switchPanel(PanelExec)
			return nil
		case '6':
			a.switchPanel(PanelSSH)
			return nil
		case '?':
			a.switchPanel(PanelHelp)
			return nil
		case 'q', 'Q':
			a.Stop()
			return nil
		case 'r', 'R':
			go a.refreshStatus()
			return nil
		}
	case tcell.KeyF1:
		a.switchPanel(PanelDashboard)
		return nil
	case tcell.KeyF2:
		a.switchPanel(PanelPods)
		return nil
	case tcell.KeyF3:
		a.switchPanel(PanelLogs)
		return nil
	case tcell.KeyF4:
		a.switchPanel(PanelUpgrade)
		return nil
	case tcell.KeyF5:
		a.switchPanel(PanelExec)
		return nil
	case tcell.KeyF6:
		a.switchPanel(PanelSSH)
		return nil
	case tcell.KeyEscape:
		a.switchPanel(PanelDashboard)
		return nil
	}

	return event
}

// switchPanel switches to the specified panel.
func (a *App) switchPanel(panel Panel) {
	a.currentPanel = panel

	// Cancel any active log streaming
	if a.logCancel != nil {
		a.logCancel()
		a.logCancel = nil
	}

	switch panel {
	case PanelDashboard:
		a.pages.SwitchToPage("dashboard")
	case PanelPods:
		a.pages.SwitchToPage("pods")
		a.app.SetFocus(a.podsTable)
	case PanelLogs:
		a.pages.SwitchToPage("logs")
	case PanelUpgrade:
		a.pages.SwitchToPage("upgrade")
	case PanelExec:
		a.pages.SwitchToPage("exec")
	case PanelSSH:
		a.pages.SwitchToPage("ssh")
	case PanelHelp:
		a.pages.SwitchToPage("help")
	}

	a.updateHeader()
}

// updateHeader updates the header with current panel name.
func (a *App) updateHeader() {
	var panelName string
	switch a.currentPanel {
	case PanelDashboard:
		panelName = "Dashboard"
	case PanelPods:
		panelName = "Pods"
	case PanelLogs:
		panelName = "Logs"
	case PanelUpgrade:
		panelName = "Upgrade"
	case PanelExec:
		panelName = "Execute Commands"
	case PanelSSH:
		panelName = "SSH Connections"
	case PanelHelp:
		panelName = "Help"
	}

	a.header.SetText(fmt.Sprintf("\n[white::b]XGrabba TUI[white] - [yellow]%s[white] | Namespace: [green]%s[white] | Release: [green]%s",
		panelName, a.cfg.Namespace, a.cfg.ReleaseName))
}

// updateStatusBar updates the status bar with current status.
func (a *App) updateStatusBar(msg string) {
	a.app.QueueUpdateDraw(func() {
		a.statusBar.SetText(fmt.Sprintf(" %s | Last refresh: %s", msg, time.Now().Format("15:04:05")))
	})
}

// Run starts the TUI application.
func (a *App) Run() error {
	// Start background refresh
	go a.startBackgroundRefresh()

	// Initial status fetch
	go a.refreshStatus()

	return a.app.Run()
}

// Stop stops the TUI application.
func (a *App) Stop() {
	a.cancel()
	if a.refreshTicker != nil {
		a.refreshTicker.Stop()
	}
	if a.logCancel != nil {
		a.logCancel()
	}
	a.app.Stop()
}

// startBackgroundRefresh starts periodic status refresh.
func (a *App) startBackgroundRefresh() {
	a.refreshTicker = time.NewTicker(a.cfg.StatusRefresh)
	defer a.refreshTicker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.refreshTicker.C:
			a.refreshStatus()
		}
	}
}

// refreshStatus fetches current cluster status.
func (a *App) refreshStatus() {
	a.updateStatusBar("Refreshing...")

	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	status, err := a.k8sClient.GetStatus(ctx)
	if err != nil {
		a.updateStatusBar(fmt.Sprintf("[red]Error: %v", err))
		return
	}

	a.statusMu.Lock()
	a.status = status
	a.statusMu.Unlock()

	a.app.QueueUpdateDraw(func() {
		a.updateDashboard()
		a.updatePodsTable()
	})

	issueCount := len(status.Issues)
	if issueCount > 0 {
		a.updateStatusBar(fmt.Sprintf("[yellow]%d issue(s) detected", issueCount))
	} else {
		a.updateStatusBar("[green]All systems operational")
	}
}

// getStatus returns a copy of the current status.
func (a *App) getStatus() *k8s.ClusterStatus {
	a.statusMu.RLock()
	defer a.statusMu.RUnlock()
	return a.status
}
