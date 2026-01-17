package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/iconidentify/xgrabba/cmd/xgrabba-tui/internal/github"
)

func (a *App) createIssuesPanel() {
	a.issuesTable = tview.NewTable().
		SetBorders(false).
		SetSelectable(true, false).
		SetFixed(1, 0)
	a.issuesTable.SetBorder(true).SetTitle(" Issues - Enter to edit, 'c' close, 'o' open, 'f' filter ")

	a.issuesDetails = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	a.issuesDetails.SetBorder(true).SetTitle(" Details ")

	a.issuesView = tview.NewFlex().
		AddItem(a.issuesTable, 0, 2, true).
		AddItem(a.issuesDetails, 0, 1, false)

	a.issuesTable.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		row, _ := a.issuesTable.GetSelection()
		issue := a.issueAtRow(row)

		switch event.Key() {
		case tcell.KeyEnter:
			if issue != nil {
				a.openIssueEditor(*issue)
			}
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case 'e', 'E':
				if issue != nil {
					a.openIssueEditor(*issue)
				}
				return nil
			case 'c', 'C':
				if issue != nil && issue.State != "closed" {
					go a.updateIssueState(issue.Number, "closed")
				}
				return nil
			case 'o', 'O':
				if issue != nil && issue.State != "open" {
					go a.updateIssueState(issue.Number, "open")
				}
				return nil
			case 'f', 'F':
				a.cycleIssueFilter()
				go a.refreshIssues()
				return nil
			}
		}
		return event
	})

	a.issuesTable.SetSelectionChangedFunc(func(row, column int) {
		issue := a.issueAtRow(row)
		if issue != nil {
			a.updateIssueDetails(*issue)
		}
	})

	a.issuesView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
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

func (a *App) refreshIssues() {
	a.app.QueueUpdateDraw(func() {
		a.issuesTable.Clear()
		a.issuesDetails.Clear()
		fmt.Fprintln(a.issuesDetails, "[dim]Loading issues...[white]")
	})

	if !a.githubClient.Enabled() {
		a.app.QueueUpdateDraw(func() {
			a.issuesTable.Clear()
			a.issuesDetails.Clear()
			a.issuesTable.SetCell(0, 0, tview.NewTableCell("GitHub not configured").SetTextColor(tcell.ColorYellow))
			fmt.Fprintln(a.issuesDetails, "[yellow]Set XGRABBA_GITHUB_OWNER and XGRABBA_GITHUB_REPO[white]")
		})
		return
	}

	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()

	issues, err := a.githubClient.ListIssues(ctx, a.issuesState, 40)
	if err != nil {
		a.app.QueueUpdateDraw(func() {
			a.issuesDetails.Clear()
			fmt.Fprintf(a.issuesDetails, "[red]Issue fetch failed:[white] %v\n", err)
		})
		return
	}

	a.issues = issues
	a.app.QueueUpdateDraw(func() {
		a.updateIssuesTable()
		if len(a.issues) > 0 {
			a.updateIssueDetails(a.issues[0])
		} else {
			a.issuesDetails.Clear()
			fmt.Fprintln(a.issuesDetails, "[dim]No issues to display[white]")
		}
	})
}

func (a *App) updateIssuesTable() {
	a.issuesTable.Clear()
	headers := []string{"ID", "Title", "State", "Updated", "Labels", "Assignee"}
	for i, h := range headers {
		cell := tview.NewTableCell(h).
			SetTextColor(tcell.ColorYellow).
			SetSelectable(false)
		a.issuesTable.SetCell(0, i, cell)
	}

	for i, issue := range a.issues {
		row := i + 1
		stateColor := tcell.ColorGreen
		if issue.State != "open" {
			stateColor = tcell.ColorRed
		}
		a.issuesTable.SetCell(row, 0, tview.NewTableCell(fmt.Sprintf("#%d", issue.Number)).SetTextColor(tcell.ColorWhite))
		a.issuesTable.SetCell(row, 1, tview.NewTableCell(issue.Title).SetExpansion(2))
		a.issuesTable.SetCell(row, 2, tview.NewTableCell(issue.State).SetTextColor(stateColor))
		a.issuesTable.SetCell(row, 3, tview.NewTableCell(issue.UpdatedAt.Format("Jan 2 15:04")))
		a.issuesTable.SetCell(row, 4, tview.NewTableCell(strings.Join(issue.Labels, ",")))
		a.issuesTable.SetCell(row, 5, tview.NewTableCell(issue.Assignee))
	}

	if len(a.issues) == 0 {
		a.issuesTable.SetCell(1, 0, tview.NewTableCell("No issues found").SetTextColor(tcell.ColorYellow))
	}
}

func (a *App) updateIssueDetails(issue github.Issue) {
	a.issuesDetails.Clear()
	fmt.Fprintf(a.issuesDetails, "[white::b]%s[white] [dim]#%d[white]\n\n", issue.Title, issue.Number)
	fmt.Fprintf(a.issuesDetails, "[yellow]State:[white] %s\n", issue.State)
	fmt.Fprintf(a.issuesDetails, "[yellow]Updated:[white] %s\n", issue.UpdatedAt.Format("2006-01-02 15:04"))
	if issue.Assignee != "" {
		fmt.Fprintf(a.issuesDetails, "[yellow]Assignee:[white] %s\n", issue.Assignee)
	}
	if len(issue.Labels) > 0 {
		fmt.Fprintf(a.issuesDetails, "[yellow]Labels:[white] %s\n", strings.Join(issue.Labels, ", "))
	}
	if issue.HTMLURL != "" {
		fmt.Fprintf(a.issuesDetails, "[dim]%s[white]\n", issue.HTMLURL)
	}
	fmt.Fprintln(a.issuesDetails, "")
	if issue.Body != "" {
		fmt.Fprintln(a.issuesDetails, issue.Body)
	}
}

func (a *App) issueAtRow(row int) *github.Issue {
	if row <= 0 || row-1 >= len(a.issues) {
		return nil
	}
	return &a.issues[row-1]
}

func (a *App) cycleIssueFilter() {
	switch a.issuesState {
	case "open":
		a.issuesState = "closed"
	case "closed":
		a.issuesState = "all"
	default:
		a.issuesState = "open"
	}
	a.updateStatusBar(fmt.Sprintf("[yellow]Issue filter: %s", a.issuesState))
}

func (a *App) updateIssueState(number int, state string) {
	if a.cfg.GitHubToken == "" {
		a.updateStatusBar("[red]GitHub token required to update issues")
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()

	_, err := a.githubClient.UpdateIssue(ctx, number, github.IssueUpdate{State: state})
	if err != nil {
		a.updateStatusBar(fmt.Sprintf("[red]Issue update failed: %v", err))
		return
	}
	a.updateStatusBar(fmt.Sprintf("[green]Issue #%d marked %s", number, state))
	a.refreshIssues()
}

func (a *App) openIssueEditor(issue github.Issue) {
	if a.cfg.GitHubToken == "" {
		a.updateStatusBar("[red]GitHub token required to edit issues")
		return
	}

	form := tview.NewForm()
	titleInput := tview.NewInputField().SetText(issue.Title)
	labelsInput := tview.NewInputField().SetText(strings.Join(issue.Labels, ", "))
	stateDrop := tview.NewDropDown().SetOptions([]string{"open", "closed"}, nil)
	if issue.State == "closed" {
		stateDrop.SetCurrentOption(1)
	} else {
		stateDrop.SetCurrentOption(0)
	}

	form.AddFormItem(titleInput.SetLabel("Title"))
	form.AddFormItem(stateDrop.SetLabel("State"))
	form.AddFormItem(labelsInput.SetLabel("Labels"))

	bodyArea := tview.NewTextArea()
	bodyArea.SetText(issue.Body, true)
	bodyArea.SetBorder(true)
	bodyArea.SetTitle(" Body ")

	modal := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(form, 6, 0, true).
		AddItem(bodyArea, 0, 1, true)
	modal.SetBorder(true).SetTitle(fmt.Sprintf(" Edit Issue #%d ", issue.Number))

	save := func() {
		_, state := stateDrop.GetCurrentOption()
		labels := parseLabels(labelsInput.GetText())
		update := github.IssueUpdate{
			Title:  titleInput.GetText(),
			Body:   bodyArea.GetText(),
			State:  state,
			Labels: labels,
		}

		go func() {
			ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
			defer cancel()

			_, err := a.githubClient.UpdateIssue(ctx, issue.Number, update)
			if err != nil {
				a.updateStatusBar(fmt.Sprintf("[red]Issue update failed: %v", err))
				return
			}
			a.updateStatusBar(fmt.Sprintf("[green]Issue #%d updated", issue.Number))
			a.app.QueueUpdateDraw(func() {
				a.pages.RemovePage("issue-edit")
			})
			a.refreshIssues()
		}()
	}

	form.AddButton("Save", save)
	form.AddButton("Cancel", func() {
		a.pages.RemovePage("issue-edit")
	})

	modal.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			a.pages.RemovePage("issue-edit")
			return nil
		}
		return event
	})

	a.pages.AddPage("issue-edit", modal, true, true)
	a.app.SetFocus(bodyArea)
}

func parseLabels(input string) []string {
	parts := strings.Split(input, ",")
	var labels []string
	for _, part := range parts {
		label := strings.TrimSpace(part)
		if label == "" {
			continue
		}
		labels = append(labels, label)
	}
	return labels
}
