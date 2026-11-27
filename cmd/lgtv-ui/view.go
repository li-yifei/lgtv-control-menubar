package main

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type editorMode uint8

const (
	editorNone editorMode = iota
	editorPair
	editorVolume
	editorConfirm
)

type shortcut struct {
	id, label, defaultKey string
}

var shortcuts = []shortcut{
	{"volumeUp", "Volume Up", "]"},
	{"volumeDown", "Volume Down", "["},
	{"toggleMute", "Mute", "m"},
	{"powerOn", "Power On", "o"},
	{"powerOff", "Power Off", "f"},
	{"pair", "Pair / Re-pair", "p"},
	{"refresh", "Refresh", "r"},
	{"settings", "Settings", ","},
}

func (app *remote) editorView(c *ui.Context) {
	c.OnShortcut(0, ui.KeyEscape, app.hideEditor)
	c.OnShortcut(ui.Cmd, ui.KeyQ, mygo.App.Quit)
	ui.Column(c).Width(336).Padding(16).Gap(10).Children(func() {
		switch app.editor {
		case editorPair:
			app.pairView(c)
		case editorVolume:
			app.volumeView(c)
		case editorConfirm:
			app.volumeConfirmView(c)
		}
		if app.busy {
			ui.Text(c, app.operation).MaxLines(3).FontSize(12).TextColor(c.Theme().TextMuted)
		} else if app.message != "" {
			color := c.Theme().TextMuted
			if app.messageIsError {
				color = c.Theme().Danger
			}
			ui.Text(c, app.message).MaxLines(3).FontSize(12).TextColor(color)
		}
	})
}

func (app *remote) beginPair() {
	if app.interruptRefresh(app.beginPair) {
		return
	}
	app.pairHost = app.host
	app.message = ""
	app.showEditor(editorPair)
}

func (app *remote) pairView(c *ui.Context) {
	ui.Text(c, "Pair LG TV").FontSize(17).Bold()
	ui.Text(c, "Enter the TV’s address and accept the pairing request on the TV.").
		MaxLines(3).FontSize(12).TextColor(c.Theme().TextMuted)
	submit := func() {
		host := strings.TrimSpace(app.pairHost)
		if host == "" || app.busy {
			return
		}
		app.tvCommand("Waiting for the TV to accept pairing…", func(ctx context.Context) error {
			return app.controller.Pair(ctx, host, true)
		})
	}
	ui.TextInput(c.Key("pair-host"), &app.pairHost).Label("TV address").
		Placeholder("TV IP address or hostname").Disabled(app.busy).OnSubmit(submit)
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, "Cancel").OnClick(app.hideEditor)
		ui.Spacer(c)
		ui.PrimaryButton(c, "Pair").Disabled(app.busy || strings.TrimSpace(app.pairHost) == "").OnClick(submit)
	})
}

func (app *remote) currentVolume() int {
	if app.state == nil || app.state.Volume == nil {
		return -1
	}
	return *app.state.Volume
}

func (app *remote) beginVolume() {
	app.volume = float64(app.currentVolume())
	app.message = ""
	app.showEditor(editorVolume)
}

func (app *remote) volumeView(c *ui.Context) {
	target := int(math.Round(app.volume))
	ui.Text(c, "Set TV Volume").FontSize(17).Bold()
	ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
		ui.Slider(c.Key("volume"), &app.volume, 0, 100).Step(1).Grow(1).
			Label("TV volume").Disabled(app.busy || app.currentVolume() < 0)
		ui.Text(c, strconv.Itoa(target)).Width(25)
	})
	if app.preferences.safetyEnabled {
		ui.Text(c, fmt.Sprintf("Confirm above %d", int(app.preferences.threshold))).FontSize(12).
			TextColor(c.Theme().TextMuted)
	}
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, "Cancel").OnClick(app.hideEditor)
		ui.Spacer(c)
		ui.PrimaryButton(c, "Set").Disabled(app.busy || target == app.currentVolume()).OnClick(func() {
			app.setVolume(target)
		})
	})
}

func (app *remote) setVolume(target int) {
	app.confirmVolume(target, func(ctx context.Context) error { return app.controller.SetVolume(ctx, target) })
}

func (app *remote) stepVolume(up bool) {
	change := func(ctx context.Context) error { return app.controller.VolumeSteps(ctx, up, 1) }
	if up {
		app.confirmVolume(min(100, app.currentVolume()+1), change)
	} else {
		app.tvCommand("Lowering volume…", change)
	}
}

func (app *remote) confirmVolume(target int, command func(context.Context) error) {
	if app.preferences.safetyEnabled && target > int(app.preferences.threshold) {
		app.pendingVolume, app.pendingTarget = command, target
		app.showEditor(editorConfirm)
		return
	}
	app.hideEditor()
	app.tvCommand("Changing volume…", command)
}

func (app *remote) volumeConfirmView(c *ui.Context) {
	ui.Text(c, "Confirm volume").FontSize(17).Bold()
	ui.Text(c, fmt.Sprintf("Set volume to %d? Your safety threshold is %d.", app.pendingTarget, int(app.preferences.threshold))).
		MaxLines(3).TextColor(c.Theme().Warning)
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, "Cancel").OnClick(app.hideEditor)
		ui.Spacer(c)
		ui.PrimaryButton(c, "Continue").OnClick(func() {
			command := app.pendingVolume
			app.hideEditor()
			app.tvCommand("Changing volume…", command)
		})
	})
}

func (app *remote) serviceMenu(menu string) {
	app.tvCommand("Opening service menu…", func(ctx context.Context) error {
		return app.controller.ServiceMenu(ctx, menu)
	})
}

func validShortcut(value string) bool {
	if value == "space" {
		return true
	}
	if len(value) != 1 {
		return false
	}
	ch := value[0]
	return ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' ||
		strings.ContainsRune("[] ,.-=/;'\\`", rune(ch))
}
