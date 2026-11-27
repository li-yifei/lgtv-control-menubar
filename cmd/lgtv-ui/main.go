package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/li-yifei/lgtv-control-menubar/internal/tv"
)

type remote struct {
	ctx        context.Context
	executable string
	controller tv.Controller
	tray       *mygo.Tray
	panel      *mygo.Window
	settings   *mygo.Window
	quitting   bool

	busy           bool
	operation      string
	cancelRefresh  context.CancelFunc
	afterRefresh   func()
	host           string
	state          *tv.State
	inputs         []tv.Input
	volume         float64
	message        string
	messageIsError bool
	menuOpen       bool
	editor         editorMode
	standby        bool
	pairHost       string
	pendingVolume  func(context.Context) error
	pendingTarget  int
	preferences    preferences
	draft          preferences
	settingsStatus string
	keycode        string
	keycodeSet     *bool
	keycodeLoading bool
	cliState       cliInstallation
}

func main() {
	if args, ok := cliArgs(os.Args[1:], filepath.Base(os.Args[0]) == "lgtv"); ok {
		if err := runCLI(args); err != nil {
			log.SetFlags(0)
			log.Fatal("Error: ", err)
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := &remote{
		ctx: ctx, executable: executable, controller: tv.New("", keychain{}),
	}
	mygo.App.SetName("LG TV Control")
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		app.quitting = true
		cancel()
	})
	mygo.App.OnSecondInstance(func(args []string, _ string) {
		if slices.Contains(args, "--show-settings") {
			app.openSettings()
		} else {
			app.openMenu()
		}
	})
	mygo.App.WhenReady(func() {
		app.ready()
		if slices.Contains(os.Args[1:], "--show-settings") {
			app.openSettings()
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func (app *remote) ready() {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID: "remote", Icon: tvIcon(), IconIsTemplate: true, ToolTip: "LG TV Control",
	})
	if err != nil {
		log.Fatal(err)
	}
	app.tray = tray
	app.panel = mygo.NewFlyout(mygo.FlyoutOptions{
		Tray: tray, Placement: mygo.PlacementBottom, Focusable: true, Popover: true,
		FitContent: true, Hidden: true, MinWidth: 336, MaxWidth: 336,
		Width: 336, Height: 165, Content: ui.View(app.editorView),
	})
	app.panel.OnClose(func(event *mygo.CloseEvent) {
		if !app.quitting {
			event.PreventDefault()
			app.hideEditor()
		}
	})
	tray.OnClick(app.openMenu)
	tray.OnRightClick(app.openMenu)
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { tray.Destroy() })

	app.host = app.controller.ConfiguredHost()
	app.preferences = loadPreferences()
	app.cliState = userCLIInstallation(app.executable)
	if app.host != "" {
		app.refresh()
	}
}

func (app *remote) showEditor(mode editorMode) {
	app.editor = mode
	app.panel.Invalidate()
	app.panel.Show()
}

func (app *remote) hideEditor() {
	if app.editor == editorNone {
		return
	}
	app.pendingVolume = nil
	app.editor = editorNone
	app.panel.Hide()
}

func (app *remote) run(operation string, refresh bool, work func(context.Context) func()) {
	if app.busy {
		return
	}
	ctx := app.ctx
	if refresh {
		ctx, app.cancelRefresh = context.WithCancel(ctx)
	}
	app.busy, app.operation = true, operation
	app.invalidate()
	go func() {
		complete := work(ctx)
		app.panel.Update(func() {
			app.busy, app.operation = false, ""
			if app.cancelRefresh != nil {
				app.cancelRefresh()
				app.cancelRefresh = nil
			}
			if queued := app.afterRefresh; queued != nil {
				app.afterRefresh = nil
				queued()
				app.invalidate()
				return
			}
			complete()
			if app.settings != nil {
				app.settings.Invalidate()
			}
		})
	}()
}

func (app *remote) interruptRefresh(next func()) bool {
	if app.cancelRefresh == nil {
		return false
	}
	if app.afterRefresh == nil {
		app.afterRefresh = next
		app.cancelRefresh()
	}
	return true
}

func (app *remote) invalidate() {
	app.panel.Invalidate()
	if app.settings != nil {
		app.settings.Invalidate()
	}
}

func (app *remote) refresh() {
	app.tvCommand("Connecting…", nil)
}

func (app *remote) tvCommand(operation string, command func(context.Context) error) {
	app.run(operation, command == nil, func(ctx context.Context) func() {
		if command != nil {
			if err := command(ctx); err != nil {
				return func() {
					app.message, app.messageIsError = err.Error(), true
					app.standby = errors.Is(err, tv.ErrStandby)
					app.clearTVState()
				}
			}
		}
		state, inputs, err := app.controller.Snapshot(ctx)
		host := app.controller.ConfiguredHost()
		return func() {
			if app.host != host {
				app.clearTVState()
				app.host = host
			}
			if err != nil {
				app.message, app.messageIsError = err.Error(), true
				app.standby = errors.Is(err, tv.ErrStandby)
				if command != nil {
					app.message = "Command completed. Refresh failed: " + app.message
				}
				app.clearTVState()
				return
			}
			if app.editor == editorPair {
				app.hideEditor()
			}
			app.standby = false
			app.message, app.messageIsError = "", false
			app.state = &state
			if state.Volume != nil {
				app.volume = float64(*state.Volume)
			}
			if inputs != nil {
				app.inputs = inputs
			}
		}
	})
}

func (app *remote) power(turnOn bool) {
	operation, message, action := "Powering off…", "Power-off command sent.", app.controller.PowerOff
	if turnOn {
		if app.interruptRefresh(func() { app.power(true) }) {
			return
		}
		operation, message, action = "Sending wake signal…", "Wake signal sent. Refresh once the TV is on.", app.controller.PowerOn
	}
	app.run(operation, false, func(ctx context.Context) func() {
		err := action(ctx)
		return func() {
			if err != nil {
				app.message, app.messageIsError = err.Error(), true
				app.standby = errors.Is(err, tv.ErrStandby)
				return
			}
			app.standby = false
			app.message, app.messageIsError = message, false
			app.clearTVState()
		}
	})
}

func (app *remote) clearTVState() {
	app.state = nil
	app.inputs = nil
}

func tvIcon() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	ink := image.NewUniform(color.NRGBA{A: 255})
	for _, part := range []image.Rectangle{
		image.Rect(3, 6, 29, 9), image.Rect(3, 21, 29, 24),
		image.Rect(3, 9, 6, 21), image.Rect(26, 9, 29, 21),
		image.Rect(11, 26, 21, 28), image.Rect(15, 24, 17, 26),
	} {
		draw.Draw(img, part, ink, image.Point{}, draw.Src)
	}
	var data bytes.Buffer
	png.Encode(&data, img)
	return data.Bytes()
}
