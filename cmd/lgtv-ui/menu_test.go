package main

import (
	"context"
	"testing"

	"github.com/egoist/mygo"
	"github.com/li-yifei/lgtv-control-menubar/internal/tv"
)

func TestStartupRefreshCanBeInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := remote{host: "tv.local", busy: true, cancelRefresh: cancel}
	items := app.trayMenu(func(fn func()) { fn() }).Items()
	for _, label := range []string{"Power On", "Pair / Re-pair…"} {
		if findItem(t, items, label).Disabled {
			t.Fatalf("%s was disabled during a startup refresh", label)
		}
	}
	findItem(t, items, "Power On").Click(nil, nil)
	if ctx.Err() != context.Canceled || app.afterRefresh == nil {
		t.Fatal("Power On did not cancel the refresh and queue a wake")
	}
}

func TestTrayMenuShowsConnectedControls(t *testing.T) {
	app := remote{
		host:        "tv.local",
		state:       &tv.State{Volume: new(45), CurrentAppID: new("hdmi2")},
		inputs:      []tv.Input{{ID: "HDMI_2", AppID: new("hdmi2"), HasSignal: true}},
		preferences: preferences{shortcuts: map[string]string{"volumeUp": "]"}},
	}
	menu := app.trayMenu(func(func()) {})
	items := menu.Items()
	if items[0].Label != "Volume 45" || !items[0].Disabled {
		t.Fatalf("unexpected status row: %#v", items[0])
	}
	if item := findItem(t, items, "Volume Up"); item.Disabled || item.Accelerator != "]" {
		t.Fatalf("volume control or shortcut missing: %#v", item)
	}
	input := findItem(t, items, "Input")
	if input.Disabled || len(input.Submenu) != 1 || !input.Submenu[0].Checked {
		t.Fatalf("selected input missing: %#v", input)
	}
	findItem(t, findItem(t, items, "Extra").Submenu, "InStart")
	findItem(t, items, "Set Volume…")
	findItem(t, items, "Settings…")
}

func TestTrayMenuOffersWakeInStandby(t *testing.T) {
	app := remote{host: "tv.local", standby: true}
	items := app.trayMenu(func(func()) {}).Items()
	if items[0].Label != "TV in Standby" {
		t.Fatalf("standby status missing: %q", items[0].Label)
	}
	if findItem(t, items, "Power On").Disabled || findItem(t, items, "Refresh").Disabled {
		t.Fatal("resume controls are disabled")
	}
	if !findItem(t, items, "Volume Up").Disabled {
		t.Fatal("volume control is enabled while the TV is in standby")
	}
}

func findItem(t *testing.T, menu []*mygo.MenuItem, label string) *mygo.MenuItem {
	t.Helper()
	for _, item := range menu {
		if item.Label == label {
			return item
		}
	}
	t.Fatalf("menu item %q is absent", label)
	return nil
}
