package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
	"github.com/li-yifei/lgtv-control-menubar/internal/tv"
)

const bundleID = "io.github.li-yifei.lgtv-control"

type keychain struct{}

var keychainService = mygo.Secrets.Service("com.lgtv-control")

func (keychain) Get(account string) (string, error) {
	value, err := keychainService.Get(account)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		err = tv.ErrSecretNotFound
	}
	return string(value), err
}

func (keychain) Set(account, value string) error {
	return keychainService.Set(account, []byte(value))
}

func (keychain) Delete(account string) error {
	return keychainService.Delete(account)
}

type preferences struct {
	safetyEnabled bool
	threshold     float64
	shortcuts     map[string]string
}

var defaults = sync.OnceValue(func() objc.ID {
	class := objc.ID(objc.GetClass("NSUserDefaults"))
	bundle := send(objc.ID(objc.GetClass("NSBundle")), "mainBundle")
	if goString(send(bundle, "bundleIdentifier")) == bundleID {
		return send(class, "standardUserDefaults")
	}
	return send(send(class, "alloc"), "initWithSuiteName:", nsString(bundleID))
})

func send(receiver objc.ID, selector string, args ...any) objc.ID {
	return receiver.Send(objc.RegisterName(selector), args...)
}

func nsString(value string) objc.ID {
	return send(objc.ID(objc.GetClass("NSString")), "stringWithUTF8String:", value)
}

func goString(value objc.ID) string {
	if value == 0 {
		return ""
	}
	return stringUTF8()(value, objc.RegisterName("UTF8String"))
}

var stringUTF8 = sync.OnceValue(func() func(objc.ID, objc.SEL) string {
	lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		panic(err)
	}
	var utf8 func(objc.ID, objc.SEL) string
	purego.RegisterLibFunc(&utf8, lib, "objc_msgSend")
	return utf8
})

func withPreferences(fn func(objc.ID)) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := send(objc.ID(objc.GetClass("NSAutoreleasePool")), "new")
	defer send(pool, "drain")
	fn(defaults())
}

func loadPreferences() (result preferences) {
	result = preferences{safetyEnabled: true, threshold: 20, shortcuts: make(map[string]string)}
	withPreferences(func(prefs objc.ID) {
		safety := nsString("safeVolumeReminderEnabled")
		if send(prefs, "objectForKey:", safety) != 0 {
			result.safetyEnabled = byte(send(prefs, "boolForKey:", safety)) != 0
		}
		threshold := nsString("safeVolumeThreshold")
		if send(prefs, "objectForKey:", threshold) != 0 {
			result.threshold = float64(max(0, min(100, int(send(prefs, "integerForKey:", threshold)))))
		}
		for _, action := range shortcuts {
			key := action.defaultKey
			if value := send(prefs, "stringForKey:", nsString("shortcut."+action.id)); value != 0 {
				key = goString(value)
				if key == " " {
					key = "space"
				}
			}
			result.shortcuts[action.id] = key
		}
	})
	return result
}

func savePreferences(value preferences) {
	withPreferences(func(prefs objc.ID) {
		send(prefs, "setBool:forKey:", value.safetyEnabled, nsString("safeVolumeReminderEnabled"))
		send(prefs, "setInteger:forKey:", int(value.threshold), nsString("safeVolumeThreshold"))
		for _, action := range shortcuts {
			key := strings.ToLower(strings.TrimSpace(value.shortcuts[action.id]))
			if key == "space" {
				key = " "
			}
			send(prefs, "setObject:forKey:", nsString(key), nsString("shortcut."+action.id))
		}
		send(prefs, "synchronize")
	})
}

func userCLIPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin", "lgtv"), nil
}

type cliInstallation uint8

const (
	cliMissing cliInstallation = iota
	cliCurrent
	cliNeedsUpdate
)

func userCLIInstallation(executable string) cliInstallation {
	path, err := userCLIPath()
	if err != nil {
		return cliMissing
	}
	return cliInstallationAt(path, executable)
}

func cliInstallationAt(path, executable string) cliInstallation {
	if _, err := os.Lstat(path); err != nil {
		return cliMissing
	}
	target, targetErr := filepath.EvalSymlinks(path)
	current, currentErr := filepath.EvalSymlinks(executable)
	if targetErr == nil && currentErr == nil && target == current {
		return cliCurrent
	}
	return cliNeedsUpdate
}

func installUserCLI(executable string) error {
	path, err := userCLIPath()
	if err != nil {
		return err
	}
	return linkUserCLI(path, executable)
}

func linkUserCLI(path, executable string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(dir, ".lgtv-cli-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	link := filepath.Join(temp, "lgtv")
	if err := os.Symlink(executable, link); err != nil {
		return err
	}
	return os.Rename(link, path)
}

func uninstallUserCLI() error {
	path, err := userCLIPath()
	if err != nil {
		return err
	}
	return os.Remove(path)
}
