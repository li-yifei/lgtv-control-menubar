package main

import (
	"math"
	"strconv"
	"structs"
	"sync"

	"github.com/ebitengine/purego/objc"
)

const volumeMenuLabel = "Set Volume…"
const menuItemAdded = "NSMenuDidAddItemNotification"

type menuRect struct {
	_                   structs.HostLayout
	x, y, width, height float64
}

type menuVolume struct {
	value, threshold int
	safetyEnabled    bool
	enabled          bool
	commit           func(int)

	menu, slider, number objc.ID
	tracking             bool
}

// AppKit menu views receive mouse events in the menu's tracking loop. MyGo
// builds the NSMenu in PopUpMenu, so install the slider when its item is added,
// before AppKit measures the menu.
var activeMenuVolume *menuVolume

func (v *menuVolume) track(popup func()) {
	target := send(objc.ID(menuVolumeTarget()), "new")
	defer send(target, "release")
	center := send(objc.ID(objc.GetClass("NSNotificationCenter")), "defaultCenter")
	notification := nsString(menuItemAdded)
	send(center, "addObserver:selector:name:object:", target, objc.RegisterName("lgMenuItemAdded:"), notification, objc.ID(0))
	defer send(center, "removeObserver:name:object:", target, notification, objc.ID(0))
	activeMenuVolume = v
	defer func() { activeMenuVolume = nil }()
	popup()
}

var menuVolumeTarget = sync.OnceValue(func() objc.Class {
	class, err := objc.RegisterClass("LGTVMenuVolumeTarget", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
		{Cmd: objc.RegisterName("lgMenuItemAdded:"), Fn: func(target objc.ID, _ objc.SEL, notification objc.ID) {
			state := activeMenuVolume
			if state == nil || state.slider != 0 {
				return
			}
			menu := send(notification, "object")
			userInfo := send(notification, "userInfo")
			index := int(send(send(userInfo, "objectForKey:", nsString("NSMenuItemIndex")), "integerValue"))
			item := send(menu, "itemAtIndex:", index)
			if goString(send(item, "title")) == volumeMenuLabel {
				state.attach(menu, item, target)
			}
		}},
		{Cmd: objc.RegisterName("lgVolumeChanged:"), Fn: func(_ objc.ID, _ objc.SEL, slider objc.ID) {
			state := activeMenuVolume
			if state == nil || state.slider != slider {
				return
			}
			value := sliderValue(slider)
			send(state.number, "setStringValue:", nsString(strconv.Itoa(value)))
			if !state.tracking {
				state.finish(value)
			}
		}},
	})
	if err != nil {
		panic(err)
	}
	return class
})

var menuSliderClass = sync.OnceValue(func() objc.Class {
	class, err := objc.RegisterClass("LGTVMenuVolumeSlider", objc.GetClass("NSSlider"), nil, nil, []objc.MethodDef{
		{Cmd: objc.RegisterName("mouseDown:"), Fn: func(slider objc.ID, cmd objc.SEL, event objc.ID) {
			state := activeMenuVolume
			if state == nil || state.slider != slider {
				slider.SendSuper(cmd, event)
				return
			}
			state.tracking = true
			slider.SendSuper(cmd, event)
			state.tracking = false
			state.finish(sliderValue(slider))
		}},
	})
	if err != nil {
		panic(err)
	}
	return class
})

func (v *menuVolume) attach(menu, item, target objc.ID) {
	const sliderWidth = 204
	view := newMenuView(objc.GetClass("NSView"), menuRect{x: 0, y: 0, width: 264, height: 36})
	defer send(view, "release")
	slider := newMenuView(menuSliderClass(), menuRect{x: 14, y: 9, width: sliderWidth, height: 19})
	defer send(slider, "release")
	send(slider, "setMinValue:", 0.0)
	send(slider, "setMaxValue:", 100.0)
	send(slider, "setIntegerValue:", max(0, v.value))
	send(slider, "setEnabled:", v.enabled)
	send(slider, "setContinuous:", true)
	send(slider, "setAccessibilityLabel:", nsString("TV volume"))
	send(slider, "setTarget:", target)
	send(slider, "setAction:", objc.RegisterName("lgVolumeChanged:"))
	send(view, "addSubview:", slider)

	label := "–"
	if v.value >= 0 {
		label = strconv.Itoa(v.value)
	}
	number := send(objc.ID(objc.GetClass("NSTextField")), "labelWithString:", nsString(label))
	send(number, "setAlignment:", 2)
	send(number, "setFrame:", menuRect{x: 224, y: 10, width: 27, height: 17})
	send(view, "addSubview:", number)

	if v.safetyEnabled && v.threshold > 0 && v.threshold < 100 {
		x := 21 + float64(sliderWidth-14)*float64(v.threshold)/100
		mark := newMenuView(objc.GetClass("NSBox"), menuRect{x: x, y: 5, width: 2, height: 5})
		defer send(mark, "release")
		send(mark, "setBoxType:", 4) // NSBoxCustom
		send(mark, "setBorderWidth:", 0.0)
		send(mark, "setFillColor:", send(objc.ID(objc.GetClass("NSColor")), "systemYellowColor"))
		send(view, "addSubview:", mark)
	}

	v.menu, v.slider, v.number = menu, slider, number
	send(item, "setView:", view)
}

func newMenuView(class objc.Class, frame menuRect) objc.ID {
	return send(send(objc.ID(class), "alloc"), "initWithFrame:", frame)
}

func sliderValue(slider objc.ID) int {
	return int(math.Round(objc.Send[float64](slider, objc.RegisterName("doubleValue"))))
}

func (v *menuVolume) finish(value int) {
	if v.enabled && v.value != value {
		v.value = value
		v.commit(value)
		send(v.menu, "cancelTracking")
	}
}
