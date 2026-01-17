package ui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/iconidentify/xgrabba/cmd/xgrabba-tui/internal/ssh"
)

// createSSHPanel creates the SSH connections panel.
func (a *App) createSSHPanel() {
	// Node list
	nodeList := tview.NewList().
		ShowSecondaryText(true)
	nodeList.SetBorder(true).SetTitle(" Kubernetes Nodes ")

	// Custom hosts list
	customHostList := tview.NewList().
		ShowSecondaryText(true)
	customHostList.SetBorder(true).SetTitle(" Custom Hosts ")

	// SSH info panel
	infoPanel := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	infoPanel.SetBorder(true).SetTitle(" SSH Configuration ")

	// Add custom host form
	hostForm := tview.NewForm().
		AddInputField("Host", "", 30, nil, nil).
		AddInputField("Address", "", 30, nil, nil).
		AddInputField("Port", "22", 10, nil, nil).
		AddInputField("User", a.cfg.SSHUser, 20, nil, nil)
	hostForm.SetBorder(true).SetTitle(" Add Custom Host ")

	var customHosts []ssh.Host

	hostForm.AddButton("Add", func() {
		name := hostForm.GetFormItemByLabel("Host").(*tview.InputField).GetText()
		addr := hostForm.GetFormItemByLabel("Address").(*tview.InputField).GetText()
		portStr := hostForm.GetFormItemByLabel("Port").(*tview.InputField).GetText()
		user := hostForm.GetFormItemByLabel("User").(*tview.InputField).GetText()

		if name == "" || addr == "" {
			return
		}

		port := 22
		fmt.Sscanf(portStr, "%d", &port)

		host := ssh.Host{
			Name:    name,
			Address: addr,
			Port:    port,
			User:    user,
		}
		customHosts = append(customHosts, host)

		customHostList.AddItem(host.Name, fmt.Sprintf("%s@%s:%d", host.User, host.Address, host.Port), 0, nil)

		// Clear form
		hostForm.GetFormItemByLabel("Host").(*tview.InputField).SetText("")
		hostForm.GetFormItemByLabel("Address").(*tview.InputField).SetText("")
		hostForm.GetFormItemByLabel("Port").(*tview.InputField).SetText("22")
	})

	// Left panel with node list and custom hosts
	leftPanel := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nodeList, 0, 1, true).
		AddItem(customHostList, 8, 0, false)

	// Right panel with info and form
	rightPanel := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(infoPanel, 0, 1, false).
		AddItem(hostForm, 12, 0, false)

	// Main layout
	a.sshView = tview.NewFlex().
		AddItem(leftPanel, 40, 0, true).
		AddItem(rightPanel, 0, 1, false)

	// Update SSH info
	updateInfo := func() {
		infoPanel.Clear()
		fmt.Fprintf(infoPanel, "[white::b]SSH Configuration[white]\n\n")
		fmt.Fprintf(infoPanel, "[yellow]User:[white] %s\n", a.cfg.SSHUser)
		fmt.Fprintf(infoPanel, "[yellow]Key Path:[white] %s\n\n", a.cfg.SSHKeyPath)
		fmt.Fprintln(infoPanel, "[dim]Press Enter on a node to connect[white]")
		fmt.Fprintln(infoPanel, "[dim]Use Tab to navigate between panels[white]")
		fmt.Fprintln(infoPanel, "[dim]Press 'r' to refresh node list[white]")
		fmt.Fprintln(infoPanel, "")
		fmt.Fprintln(infoPanel, "[yellow::b]Environment Variables:[white]")
		fmt.Fprintln(infoPanel, "  XGRABBA_SSH_USER - SSH username")
		fmt.Fprintln(infoPanel, "  XGRABBA_SSH_KEY  - Path to SSH key")
	}
	updateInfo()

	// Handle node selection for SSH
	nodeList.SetSelectedFunc(func(index int, mainText string, secondaryText string, shortcut rune) {
		// Get the host from the stored data
		go func() {
			ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
			defer cancel()

			nodes, err := ssh.GetKubernetesNodes(ctx, a.cfg.KubeContext)
			if err != nil || index >= len(nodes) {
				return
			}

			host := nodes[index]
			a.connectSSH(&host)
		}()
	})

	// Handle custom host selection
	customHostList.SetSelectedFunc(func(index int, mainText string, secondaryText string, shortcut rune) {
		if index >= len(customHosts) {
			return
		}
		host := customHosts[index]
		a.connectSSH(&host)
	})

	// Tab navigation
	a.sshView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			if a.app.GetFocus() == nodeList {
				a.app.SetFocus(customHostList)
			} else if a.app.GetFocus() == customHostList {
				a.app.SetFocus(hostForm)
			} else {
				a.app.SetFocus(nodeList)
			}
			return nil
		case tcell.KeyRune:
			if event.Rune() == 'r' || event.Rune() == 'R' {
				go a.refreshNodeList(nodeList)
				return nil
			}
		}
		return event
	})

	// Initial node list load
	go a.refreshNodeList(nodeList)
}

// refreshNodeList refreshes the Kubernetes node list.
func (a *App) refreshNodeList(nodeList *tview.List) {
	a.app.QueueUpdateDraw(func() {
		nodeList.Clear()
		nodeList.AddItem("Loading...", "", 0, nil)
	})

	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()

	nodes, err := ssh.GetKubernetesNodes(ctx, a.cfg.KubeContext)

	a.app.QueueUpdateDraw(func() {
		nodeList.Clear()

		if err != nil {
			nodeList.AddItem("Error loading nodes", err.Error(), 0, nil)
			return
		}

		if len(nodes) == 0 {
			nodeList.AddItem("No nodes found", "", 0, nil)
			return
		}

		for _, node := range nodes {
			nodeList.AddItem(
				node.Name,
				fmt.Sprintf("%s (port %d)", node.Address, node.Port),
				0,
				nil,
			)
		}
	})
}

// connectSSH connects to a host via SSH.
func (a *App) connectSSH(host *ssh.Host) {
	a.app.Suspend(func() {
		ctx := context.Background()
		cmd := a.sshClient.Connect(ctx, host)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		fmt.Printf("\n--- Connecting to %s@%s:%d ---\n", host.User, host.Address, host.Port)
		fmt.Println("Type 'exit' to return to TUI")
		fmt.Println()

		// Run interactive SSH session
		if err := cmd.Run(); err != nil {
			fmt.Printf("\nSSH session ended: %v\n", err)
		}

		fmt.Println("\n--- Returning to TUI ---")
		time.Sleep(500 * time.Millisecond)
	})
}
