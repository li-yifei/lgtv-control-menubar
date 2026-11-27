package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo"
)

func (app *remote) openMenu() {
	if app.menuOpen {
		return
	}
	app.hideEditor()
	app.menuOpen = true
	var selected func()
	menu := app.trayMenu(func(action func()) {
		selected = action
	})
	app.tray.SetMenu(menu)
	volume := menuVolume{
		value: app.currentVolume(), threshold: int(app.preferences.threshold),
		safetyEnabled: app.preferences.safetyEnabled, enabled: !app.busy && app.currentVolume() >= 0,
		commit: func(value int) { selected = func() { app.setVolume(value) } },
	}
	volume.track(app.tray.PopUpMenu)
	app.tray.SetMenu(nil)
	app.menuOpen = false
	if selected != nil {
		selected()
	}
}

func (app *remote) trayMenu(selectAction func(func())) *mygo.Menu {
	connected := app.state != nil
	knownVolume := app.currentVolume() >= 0
	canInterruptRefresh := !app.busy || (app.cancelRefresh != nil && app.afterRefresh == nil)
	label, detail := app.menuStatus()
	action := func(label, shortcut string, enabled bool, run func()) *mygo.MenuItem {
		accelerator := app.preferences.shortcuts[shortcut]
		return &mygo.MenuItem{
			Label: label, Accelerator: accelerator, Disabled: !enabled,
			Click: func(*mygo.MenuItem, *mygo.Window) { selectAction(run) },
		}
	}

	mute := "Mute"
	if connected && app.state.Muted != nil && *app.state.Muted {
		mute = "Unmute"
	}
	pair := "Pair TV…"
	if app.host != "" {
		pair = "Pair / Re-pair…"
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: label, Subtitle: detail, ToolTip: app.message, Disabled: true},
		mygo.Separator(),
		action(volumeMenuLabel, "", !app.busy && knownVolume, app.beginVolume),
		action("Volume Up", "volumeUp", !app.busy && knownVolume, func() { app.stepVolume(true) }),
		action("Volume Down", "volumeDown", !app.busy && connected, func() { app.stepVolume(false) }),
		action(mute, "toggleMute", !app.busy && connected, func() {
			app.tvCommand("Changing mute…", app.controller.ToggleMute)
		}),
		mygo.Separator(),
		action("Power On", "powerOn", canInterruptRefresh && app.host != "", func() { app.power(true) }),
		action("Power Off", "powerOff", !app.busy && connected, func() { app.power(false) }),
		{Label: "Input", Disabled: app.busy || !connected, Submenu: app.inputMenu(selectAction)},
		{Label: "Extra", Disabled: app.busy || !connected, Submenu: []*mygo.MenuItem{
			action("InStart", "", !app.busy && connected, func() { app.serviceMenu("inStart") }),
			action("EZ Adjust", "", !app.busy && connected, func() { app.serviceMenu("ezAdjust") }),
		}},
		mygo.Separator(),
		action(pair, "pair", canInterruptRefresh, app.beginPair),
		action("Refresh", "refresh", !app.busy && app.host != "", app.refresh),
		action("Settings…", "settings", true, app.openSettings),
		mygo.Separator(),
		action("Quit LG TV Control", "", true, mygo.App.Quit),
	})
}

func (app *remote) inputMenu(selectAction func(func())) []*mygo.MenuItem {
	if len(app.inputs) == 0 || app.state == nil {
		return []*mygo.MenuItem{{Label: "No inputs available", Disabled: true}}
	}
	items := make([]*mygo.MenuItem, 0, len(app.inputs))
	for _, input := range app.inputs {
		label := input.ID
		if input.Label != input.ID {
			label = input.Label + " · " + input.ID
		}
		status := "No signal"
		if input.HasSignal {
			status = "Signal detected"
		}
		items = append(items, &mygo.MenuItem{
			Type: mygo.MenuItemRadio, Label: label, Subtitle: status,
			Checked: input.AppID != nil && app.state.CurrentAppID != nil && *input.AppID == *app.state.CurrentAppID,
			Click: func(*mygo.MenuItem, *mygo.Window) {
				selectAction(func() {
					app.tvCommand("Switching input…", func(ctx context.Context) error {
						return app.controller.SwitchInput(ctx, input.ID)
					})
				})
			},
		})
	}
	return items
}

func (app *remote) menuStatus() (string, string) {
	switch {
	case app.busy:
		return app.operation, app.host
	case app.standby:
		return "TV in Standby", "Choose Power On, then Refresh"
	case app.message != "":
		detail := strings.TrimSpace(app.message)
		runes := []rune(detail)
		if len(runes) > 56 {
			detail = string(runes[:55]) + "…"
		}
		if app.messageIsError {
			return "TV Connection", detail
		}
		return "LG TV Control", detail
	case app.state != nil:
		label := "Connected"
		if volume := app.currentVolume(); volume >= 0 {
			label = fmt.Sprintf("Volume %d", volume)
		}
		if app.state.Muted != nil && *app.state.Muted {
			label += " · Muted"
		}
		if app.state.Model != nil {
			return label, *app.state.Model + " · " + app.host
		}
		return label, app.host
	case app.host != "":
		return "LG TV Control", app.host
	default:
		return "LG TV Control", "Pair your TV to get started"
	}
}
