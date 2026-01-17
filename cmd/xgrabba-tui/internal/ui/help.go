package ui

import (
	"github.com/rivo/tview"
)

// createHelpPanel creates the help panel.
func (a *App) createHelpPanel() {
	a.helpView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	a.helpView.SetBorder(true).SetTitle(" Help ")

	helpText := `[yellow::b]XGrabba TUI - Terminal User Interface[white]

A comprehensive terminal application for monitoring, managing, and
troubleshooting XGrabba deployments in Kubernetes environments.

[yellow::b]GLOBAL NAVIGATION[white]
[cyan]1[white] or [cyan]F1[white]     Dashboard      - Overview of cluster status
[cyan]2[white] or [cyan]F2[white]     Pods           - Detailed pod information
[cyan]3[white] or [cyan]F3[white]     Logs           - Real-time log streaming
[cyan]4[white] or [cyan]F4[white]     Upgrade        - Version management
[cyan]5[white] or [cyan]F5[white]     Execute        - Run commands in pods
[cyan]6[white] or [cyan]F6[white]     SSH            - Connect to nodes
[cyan]7[white] or [cyan]F7[white]     GitHub         - Actions and releases
[cyan]8[white] or [cyan]F8[white]     Issues         - Browse and edit issues
[cyan]9[white] or [cyan]F9[white]     Crossplane     - Packages and compositions
[cyan]?[white]            Help           - This help screen
[cyan]q[white]            Quit           - Exit the application
[cyan]r[white]            Refresh        - Refresh current data
[cyan]Escape[white]       Dashboard      - Return to dashboard

[yellow::b]DASHBOARD PANEL[white]
The dashboard provides an at-a-glance view of your XGrabba deployment:
- [white::b]Overview[white]: Helm release status, chart version, readiness
- [white::b]Pods[white]: Pod status, ready count, restarts, age
- [white::b]Services[white]: Service types, IPs, and ports
- [white::b]Storage[white]: PVC status and capacity
- [white::b]Events[white]: Recent Kubernetes events
- [white::b]Issues[white]: Detected problems and warnings

[yellow::b]PODS PANEL[white]
Interactive pod management with the following actions:
[cyan]Enter[white]        View logs for selected pod
[cyan]d[white]            Describe pod (detailed info)
[cyan]s[white]            Shell into pod (interactive)
[cyan]l[white]            View logs (same as Enter)
[cyan]h[white]            Health check the pod

[yellow::b]LOGS PANEL[white]
Real-time log streaming with source selection:
[cyan]m[white]            Stream Main App logs
[cyan]u[white]            Stream USB Manager logs
[cyan]a[white]            Stream All pods
[cyan]c[white]            Clear log view
[cyan]p[white]            Scroll to end (auto-scroll)
[cyan]Ctrl+C[white]       Stop log streaming
[cyan]b[white]            Back to dashboard

[yellow::b]UPGRADE PANEL[white]
Manage XGrabba versions via Crossplane:
[cyan]Tab[white]          Navigate between buttons
[cyan]Enter[white]        Activate selected button

Buttons:
- [green]Check for Updates[white] - Query Helm repo for latest version
- [green]Upgrade[white] - Trigger upgrade to latest version
- [red]Force Upgrade[white] - Upgrade even if already at latest

[yellow::b]EXECUTE PANEL[white]
Run commands in pods remotely:
[cyan]Tab[white]          Navigate between pod list, input, output
[cyan]Enter[white]        Execute command (when in input field)

Quick commands are provided for common operations:
- Health check, disk usage, memory, processes, environment

[yellow::b]SSH PANEL[white]
Connect to Kubernetes nodes or custom hosts:
[cyan]Enter[white]        Connect to selected node/host
[cyan]Tab[white]          Navigate between lists and form
[cyan]r[white]            Refresh node list

Add custom hosts using the form on the right.

[yellow::b]GITHUB PANEL[white]
View repository health at a glance:
- Recent Actions workflow runs
- Releases and tags

[yellow::b]ISSUES PANEL[white]
Manage GitHub issues:
[cyan]Enter[white]        Edit selected issue
[cyan]c[white]            Close issue
[cyan]o[white]            Re-open issue
[cyan]f[white]            Cycle filters (open/closed/all)

[yellow::b]CROSSPLANE PANEL[white]
Inspect Crossplane resources:
- Providers and Configurations
- Compositions and XRDs

[yellow::b]ENVIRONMENT VARIABLES[white]
Configure the TUI via environment variables:

[cyan]XGRABBA_NAMESPACE[white]       Kubernetes namespace (default: xgrabba)
[cyan]XGRABBA_RELEASE[white]         Helm release name (default: xgrabba)
[cyan]XGRABBA_KUBE_CONTEXT[white]    kubectl context to use
[cyan]XGRABBA_HELM_REPO[white]       Helm chart repository URL
[cyan]XGRABBA_SSH_USER[white]        SSH username
[cyan]XGRABBA_SSH_KEY[white]         Path to SSH private key
[cyan]XGRABBA_GITHUB_OWNER[white]    GitHub organization/user
[cyan]XGRABBA_GITHUB_REPO[white]     GitHub repository name
[cyan]XGRABBA_GITHUB_TOKEN[white]    GitHub token for issue edits
[cyan]XGRABBA_GITHUB_API_URL[white]  GitHub API URL override
[cyan]XGRABBA_STATUS_REFRESH[white]  Status refresh interval (default: 5s)
[cyan]XGRABBA_LOG_REFRESH[white]     Log refresh interval (default: 1s)

[yellow::b]REQUIREMENTS[white]
- kubectl configured with cluster access
- helm (for version checks)
- ssh (for node connections)

[yellow::b]TIPS[white]
- Use [cyan]r[white] anywhere to refresh data
- The status bar shows issues and last refresh time
- Dashboard auto-refreshes every 5 seconds
- Log colors: [red]red[white]=error, [yellow]yellow[white]=warning, [green]green[white]=info

[dim]Press any navigation key to return to a panel[white]
`

	a.helpView.SetText(helpText)
}
