package main

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (app *remote) openSettings() {
	app.hideEditor()
	if app.settings != nil {
		app.settings.Show()
		return
	}
	app.draft = app.preferences
	app.draft.shortcuts = maps.Clone(app.preferences.shortcuts)
	app.settingsStatus, app.keycode = "", ""
	app.keycodeSet = nil
	app.cliState = userCLIInstallation(app.executable)
	app.settings = mygo.NewWindow(mygo.WindowOptions{
		Title: "LG TV Control Settings", Width: 500, Height: 590, MinWidth: 460, MinHeight: 420,
		DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		Content: ui.View(app.settingsView),
	})
	app.settings.OnClosed(func() {
		app.settings = nil
		app.keycode = ""
	})
	app.loadKeycodeState()
}

func (app *remote) loadKeycodeState() {
	window := app.settings
	app.keycodeLoading = true
	go func() {
		set, err := app.controller.KeycodeSet()
		app.panel.Update(func() {
			if app.settings != window {
				return
			}
			app.keycodeLoading = false
			if err == nil {
				app.keycodeSet = &set
			}
			if err != nil && app.settingsStatus == "" {
				app.settingsStatus = err.Error()
			}
			window.Invalidate()
		})
	}()
}

func (app *remote) settingsView(c *ui.Context) {
	c.OnShortcut(ui.Cmd, ui.KeyW, app.settings.Close)
	c.OnShortcut(0, ui.KeyEscape, app.settings.Close)
	ui.Column(c).Fill().Padding(20).Gap(12).Children(func() {
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(14).Children(func() {
				app.safetySettings(c)
				ui.Divider(c)
				app.shortcutSettings(c)
				ui.Divider(c)
				app.keycodeSettings(c)
				ui.Divider(c)
				app.cliSettings(c)
			})
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, app.settingsStatus).Grow(1).MaxLines(3).FontSize(12).TextColor(c.Theme().TextMuted)
			ui.Button(c, "Close").OnClick(app.settings.Close)
			ui.PrimaryButton(c, "Save Settings").OnClick(app.commitSettings)
		})
	})
}

func (app *remote) safetySettings(c *ui.Context) {
	ui.Text(c, "Volume safety").FontSize(15).Bold()
	ui.Checkbox(c, &app.draft.safetyEnabled, "Confirm volume above the threshold")
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Slider(c, &app.draft.threshold, 0, 100).Step(1).Grow(1).Label("Safety threshold").
			Disabled(!app.draft.safetyEnabled)
		ui.Text(c, strconv.Itoa(int(app.draft.threshold))).Width(25)
	})
}

func (app *remote) shortcutSettings(c *ui.Context) {
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Keyboard shortcuts").FontSize(15).Bold()
		ui.Spacer(c)
		ui.Button(c, "Reset").OnClick(func() {
			for _, action := range shortcuts {
				app.draft.shortcuts[action.id] = action.defaultKey
			}
		})
	})
	for i := 0; i < len(shortcuts); i += 2 {
		ui.Row(c.Key("shortcuts-" + strconv.Itoa(i))).Gap(12).Children(func() {
			for _, action := range shortcuts[i : i+2] {
				value := app.draft.shortcuts[action.id]
				ui.Row(c.Key(action.id)).Grow(1).Gap(4).AlignItems(ui.Center).Children(func() {
					ui.Text(c, action.label).Grow(1).FontSize(12)
					ui.TextInput(c.Key("key"), &value).Width(58).Label(action.label + " shortcut").
						OnChange(func() { app.draft.shortcuts[action.id] = value })
				})
			}
		})
	}
}

func (app *remote) commitSettings() {
	used := make(map[string]string, len(shortcuts))
	for _, action := range shortcuts {
		value := strings.ToLower(strings.TrimSpace(app.draft.shortcuts[action.id]))
		if value == "" {
			continue
		}
		if !validShortcut(value) {
			app.settingsStatus = fmt.Sprintf("Use a letter, digit, punctuation key or “space” for %s.", action.label)
			return
		}
		if prior := used[value]; prior != "" {
			app.settingsStatus = fmt.Sprintf("%s and %s use the same shortcut.", prior, action.label)
			return
		}
		used[value] = action.label
		app.draft.shortcuts[action.id] = value
	}
	savePreferences(app.draft)
	app.preferences = app.draft
	app.preferences.shortcuts = maps.Clone(app.draft.shortcuts)
	app.settingsStatus = "Settings saved."
}

func (app *remote) keycodeSettings(c *ui.Context) {
	ui.Text(c, "IP control keycode").FontSize(15).Bold()
	label := "Used for automatic service-menu PIN entry."
	if app.keycodeSet != nil && *app.keycodeSet {
		label = "Keycode saved in Keychain."
	}
	ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted)
	ui.Row(c).Gap(8).Children(func() {
		ui.TextInput(c, &app.keycode).Password().Grow(1).Label("IP control keycode").Placeholder("8 characters")
		ui.Button(c, "Save Keycode").Disabled(app.busy || app.keycodeLoading || app.keycode == "").OnClick(func() {
			code := app.keycode
			app.settingsAction("Saving keycode…", "Keycode saved.", func() error {
				return app.controller.SaveKeycode(code)
			}, func() {
				app.keycodeSet = new(true)
				app.keycode = ""
			})
		})
		ui.Button(c, "Clear").Disabled(app.busy || app.keycodeLoading || (app.keycodeSet != nil && !*app.keycodeSet)).OnClick(func() {
			app.settingsAction("Removing keycode…", "Keycode removed.", app.controller.ClearKeycode, func() {
				app.keycodeSet = new(false)
				app.keycode = ""
			})
		})
	})
}

func (app *remote) cliSettings(c *ui.Context) {
	ui.Text(c, "Command line tool").FontSize(15).Bold()
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, "~/.local/bin/lgtv").Grow(1).FontSize(12).TextColor(c.Theme().TextMuted)
		if app.cliState == cliCurrent {
			ui.Button(c, "Uninstall CLI").Disabled(app.busy).OnClick(func() {
				app.settingsAction("Uninstalling CLI…", "CLI removed.", uninstallUserCLI, func() {
					app.cliState = cliMissing
				})
			})
		} else {
			title := "Install CLI"
			if app.cliState == cliNeedsUpdate {
				title = "Update CLI"
			}
			ui.Button(c, title).Disabled(app.busy).OnClick(func() {
				app.settingsAction("Installing CLI…", "CLI installed.", func() error {
					return installUserCLI(app.executable)
				}, func() {
					app.cliState = cliCurrent
				})
			})
		}
	})
}

func (app *remote) settingsAction(operation, success string, work func() error, update func()) {
	app.settingsStatus = operation
	app.run(operation, false, func(context.Context) func() {
		err := work()
		return func() {
			if err != nil {
				app.settingsStatus = err.Error()
				return
			}
			update()
			app.settingsStatus = success
		}
	})
}
