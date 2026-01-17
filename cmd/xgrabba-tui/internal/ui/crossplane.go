package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/iconidentify/xgrabba/cmd/xgrabba-tui/internal/k8s"
)

func (a *App) createCrossplanePanel() {
	a.crossplaneProvidersView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	a.crossplaneProvidersView.SetBorder(true).SetTitle(" Providers ")

	a.crossplaneConfigsView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	a.crossplaneConfigsView.SetBorder(true).SetTitle(" Configurations ")

	a.crossplaneCompositionsView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	a.crossplaneCompositionsView.SetBorder(true).SetTitle(" Compositions ")

	a.crossplaneXRDView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	a.crossplaneXRDView.SetBorder(true).SetTitle(" XRDs ")

	topRow := tview.NewFlex().
		AddItem(a.crossplaneProvidersView, 0, 1, false).
		AddItem(a.crossplaneConfigsView, 0, 1, false)

	bottomRow := tview.NewFlex().
		AddItem(a.crossplaneCompositionsView, 0, 1, false).
		AddItem(a.crossplaneXRDView, 0, 1, false)

	a.crossplaneView = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(topRow, 0, 1, false).
		AddItem(bottomRow, 0, 1, false)

	a.crossplaneView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
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

func (a *App) refreshCrossplaneStatus() {
	a.app.QueueUpdateDraw(func() {
		a.crossplaneProvidersView.Clear()
		a.crossplaneConfigsView.Clear()
		a.crossplaneCompositionsView.Clear()
		a.crossplaneXRDView.Clear()
		fmt.Fprintln(a.crossplaneProvidersView, "[dim]Loading Crossplane status...[white]")
	})

	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()

	status, err := a.k8sClient.GetCrossplaneStatus(ctx)
	if err != nil {
		a.app.QueueUpdateDraw(func() {
			a.crossplaneProvidersView.Clear()
			a.crossplaneConfigsView.Clear()
			a.crossplaneCompositionsView.Clear()
			a.crossplaneXRDView.Clear()
			fmt.Fprintf(a.crossplaneProvidersView, "[red]Crossplane query failed:[white]\n%v\n", err)
			fmt.Fprintln(a.crossplaneProvidersView, "[dim]Ensure Crossplane CRDs are installed[white]")
		})
		return
	}

	a.setCrossplaneStatus(status)
	a.app.QueueUpdateDraw(func() {
		a.renderCrossplanePackages(a.crossplaneProvidersView, "Providers", status.Providers)
		a.renderCrossplanePackages(a.crossplaneConfigsView, "Configurations", status.Configurations)
		a.renderCrossplaneCompositions(a.crossplaneCompositionsView, status.Compositions)
		a.renderCrossplaneXRDs(a.crossplaneXRDView, status.XRDs)
		if len(status.Issues) > 0 {
			a.updateStatusBar(fmt.Sprintf("[yellow]%d Crossplane issue(s)", len(status.Issues)))
		}
	})
}

func (a *App) renderCrossplanePackages(view *tview.TextView, title string, packages []k8s.CrossplanePackage) {
	view.Clear()
	if len(packages) == 0 {
		fmt.Fprintf(view, "[dim]No %s found[white]\n", title)
		return
	}

	for _, pkg := range packages {
		statusColor := "green"
		if !pkg.Healthy || !pkg.Installed {
			statusColor = "red"
		}
		fmt.Fprintf(view, "[white::b]%s[white]\n", pkg.Name)
		if pkg.Package != "" {
			fmt.Fprintf(view, "  [dim]%s[white]\n", pkg.Package)
		}
		if pkg.Revision != "" {
			fmt.Fprintf(view, "  Revision: %s\n", pkg.Revision)
		}
		fmt.Fprintf(view, "  [%s]Healthy: %v Installed: %v[white]  Age: %s\n", statusColor, pkg.Healthy, pkg.Installed, pkg.Age)
		if pkg.Message != "" && statusColor == "red" {
			fmt.Fprintf(view, "  [red]%s[white]\n", pkg.Message)
		}
		fmt.Fprintln(view)
	}
}

func (a *App) renderCrossplaneCompositions(view *tview.TextView, compositions []k8s.CrossplaneComposition) {
	view.Clear()
	if len(compositions) == 0 {
		fmt.Fprintln(view, "[dim]No compositions found[white]")
		return
	}
	for _, comp := range compositions {
		fmt.Fprintf(view, "[white::b]%s[white]\n", comp.Name)
		if comp.CompositeKind != "" {
			fmt.Fprintf(view, "  Kind: %s\n", comp.CompositeKind)
		}
		fmt.Fprintf(view, "  Age: %s\n\n", comp.Age)
	}
}

func (a *App) renderCrossplaneXRDs(view *tview.TextView, xrds []k8s.CrossplaneXRD) {
	view.Clear()
	if len(xrds) == 0 {
		fmt.Fprintln(view, "[dim]No XRDs found[white]")
		return
	}
	for _, xrd := range xrds {
		fmt.Fprintf(view, "[white::b]%s[white]\n", xrd.Name)
		if xrd.Kind != "" {
			fmt.Fprintf(view, "  Kind: %s\n", xrd.Kind)
		}
		if xrd.ClaimKind != "" {
			fmt.Fprintf(view, "  Claim: %s\n", xrd.ClaimKind)
		}
		fmt.Fprintf(view, "  Age: %s\n\n", xrd.Age)
	}
}
