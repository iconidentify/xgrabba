package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (a *App) createGitHubPanel() {
	a.githubInfoView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	a.githubInfoView.SetBorder(true).SetTitle(" Repository ")

	a.githubActionsView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	a.githubActionsView.SetBorder(true).SetTitle(" Actions ")

	a.githubReleasesView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	a.githubReleasesView.SetBorder(true).SetTitle(" Releases ")

	topRow := tview.NewFlex().
		AddItem(a.githubInfoView, 0, 1, false)

	bottomRow := tview.NewFlex().
		AddItem(a.githubActionsView, 0, 1, false).
		AddItem(a.githubReleasesView, 0, 1, false)

	a.githubView = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(topRow, 8, 0, false).
		AddItem(bottomRow, 0, 1, false)

	a.githubView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape, tcell.KeyF1, tcell.KeyF2, tcell.KeyF3, tcell.KeyF4, tcell.KeyF5, tcell.KeyF6, tcell.KeyF7, tcell.KeyF8, tcell.KeyF9:
			return event
		case tcell.KeyRune:
			switch event.Rune() {
			case '1', '2', '3', '4', '5', '6', '7', '8', '9', 'q', 'Q', '?', 'r', 'R':
				return event
			}
		}
		return event
	})
}

func (a *App) refreshGitHubOverview() {
	a.app.QueueUpdateDraw(func() {
		a.githubInfoView.Clear()
		a.githubActionsView.Clear()
		a.githubReleasesView.Clear()
		fmt.Fprintln(a.githubInfoView, "[dim]Loading repository info...[white]")
		fmt.Fprintln(a.githubActionsView, "[dim]Loading workflow runs...[white]")
		fmt.Fprintln(a.githubReleasesView, "[dim]Loading releases...[white]")
	})

	if !a.githubClient.Enabled() {
		a.app.QueueUpdateDraw(func() {
			a.githubInfoView.Clear()
			fmt.Fprintln(a.githubInfoView, "[yellow]GitHub repository not configured[white]")
			fmt.Fprintln(a.githubInfoView, "[dim]Set XGRABBA_GITHUB_OWNER and XGRABBA_GITHUB_REPO[white]")
		})
		return
	}

	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()

	repo, repoErr := a.githubClient.GetRepo(ctx)
	runs, runsErr := a.githubClient.ListWorkflowRuns(ctx, 10)
	releases, releasesErr := a.githubClient.ListReleases(ctx, 8)

	a.app.QueueUpdateDraw(func() {
		a.githubInfoView.Clear()
		if repoErr != nil {
			fmt.Fprintf(a.githubInfoView, "[red]Repo error:[white] %v\n", repoErr)
		} else if repo != nil {
			fmt.Fprintf(a.githubInfoView, "[white::b]%s[white]\n", repo.FullName)
			if repo.Description != "" {
				fmt.Fprintf(a.githubInfoView, "%s\n\n", repo.Description)
			}
			fmt.Fprintf(a.githubInfoView, "[yellow]Default:[white] %s  [yellow]Issues:[white] %d  [yellow]Stars:[white] %d  [yellow]Forks:[white] %d\n",
				repo.DefaultBranch, repo.OpenIssues, repo.Stars, repo.Forks)
			fmt.Fprintf(a.githubInfoView, "[yellow]Updated:[white] %s\n", repo.UpdatedAt.Format("2006-01-02 15:04"))
			fmt.Fprintf(a.githubInfoView, "[dim]%s[white]\n", repo.HTMLURL)
		}

		if a.cfg.GitHubToken == "" {
			fmt.Fprintln(a.githubInfoView, "\n[yellow]Note:[white] set XGRABBA_GITHUB_TOKEN for issue edits/private data.")
		}
	})

	a.app.QueueUpdateDraw(func() {
		a.githubActionsView.Clear()
		if runsErr != nil {
			fmt.Fprintf(a.githubActionsView, "[red]Actions error:[white] %v\n", runsErr)
			return
		}
		if len(runs) == 0 {
			fmt.Fprintln(a.githubActionsView, "[dim]No workflow runs found[white]")
			return
		}
		for _, run := range runs {
			statusColor := "[yellow]"
			if run.Conclusion == "success" {
				statusColor = "[green]"
			} else if run.Conclusion == "failure" || run.Conclusion == "cancelled" {
				statusColor = "[red]"
			}
			fmt.Fprintf(a.githubActionsView, "%s%s[white] [dim]%s[white]\n", statusColor, run.Name, run.Event)
			fmt.Fprintf(a.githubActionsView, "  %s%s[white] [%s] %s\n",
				statusColor, strings.ToUpper(run.Status), run.Branch, run.Actor)
			fmt.Fprintf(a.githubActionsView, "  [dim]Updated %s[white]\n\n", run.UpdatedAt.Format("Jan 2 15:04"))
		}
	})

	a.app.QueueUpdateDraw(func() {
		a.githubReleasesView.Clear()
		if releasesErr != nil {
			fmt.Fprintf(a.githubReleasesView, "[red]Releases error:[white] %v\n", releasesErr)
			return
		}
		if len(releases) == 0 {
			fmt.Fprintln(a.githubReleasesView, "[dim]No releases found[white]")
			return
		}
		for _, rel := range releases {
			status := ""
			if rel.Draft {
				status = "draft"
			} else if rel.Prerelease {
				status = "pre-release"
			}
			line := fmt.Sprintf("%s (%s)", rel.Name, rel.TagName)
			if rel.Name == "" {
				line = rel.TagName
			}
			if status != "" {
				line += " [" + status + "]"
			}
			fmt.Fprintf(a.githubReleasesView, "[white::b]%s[white]\n", line)
			if !rel.PublishedAt.IsZero() {
				fmt.Fprintf(a.githubReleasesView, "  [dim]%s[white]\n", rel.PublishedAt.Format("Jan 2 15:04"))
			}
			fmt.Fprintf(a.githubReleasesView, "  [dim]%s[white]\n\n", rel.HTMLURL)
		}
	})
}
