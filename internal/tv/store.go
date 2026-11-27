package tv

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

const clientKeyAccount = "clientKey"
const keycodeAccount = "ipControlKeycode"

var ErrSecretNotFound = errors.New("secret not found")

type Secrets interface {
	Get(account string) (string, error)
	Set(account, value string) error
	Delete(account string) error
}

type store struct {
	path    string
	secrets Secrets
}

type credentialChange struct {
	account string
	value   string
	remove  bool
}

type config struct {
	Host     string   `json:"host"`
	Model    string   `json:"model"`
	MAC      string   `json:"mac"`
	WakeMACs []string `json:"wakeMACs"`
}

func ConfigPath(path string) string {
	if path != "" {
		return path
	}
	if path = os.Getenv("LG_TV_CONFIG"); path != "" {
		return path
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "lgtv-pairing.json")
}

func (s store) load() (config, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return config{}, fmt.Errorf("missing pairing config: %s", s.path)
	}
	if err != nil {
		return config{}, err
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, fmt.Errorf("read pairing config: %w", err)
	}
	cfg.Host = normalizeHost(cfg.Host)
	if cfg.Host == "" {
		return config{}, errors.New("missing TV host")
	}
	return cfg, nil
}

func (s store) readRaw() (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]json.RawMessage), nil
	}
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("read pairing config: %w", err)
	}
	if raw == nil {
		return nil, errors.New("pairing config must be an object")
	}
	return raw, nil
}

func readString(raw map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(raw[key], &value)
	return value
}

func setField(raw map[string]json.RawMessage, key string, value any) {
	raw[key], _ = json.Marshal(value)
}

func (s store) change(update func(map[string]json.RawMessage) (bool, error), credential *credentialChange) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.path+".lock", os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	raw, err := s.readRaw()
	if err != nil {
		return err
	}
	changed, err := update(raw)
	if err != nil {
		return err
	}
	if !changed {
		if credential != nil && credential.remove {
			return s.secrets.Delete(credential.account)
		}
		return nil
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	staged, err := stageConfig(dir, append(data, '\n'))
	if err != nil {
		return err
	}
	defer os.Remove(staged)
	if credential == nil {
		return os.Rename(staged, s.path)
	}
	previous, err := s.secrets.Get(credential.account)
	existed := err == nil
	if !existed && !errors.Is(err, ErrSecretNotFound) {
		// Re-pairing must still be able to replace credentials that macOS won't read.
		// Commit the staged config first, so rollback doesn't need the old credential.
		return s.replaceUnreadableCredential(staged, credential)
	}
	if err := credential.apply(s.secrets); err != nil {
		return err
	}
	if err := os.Rename(staged, s.path); err != nil {
		if existed {
			return errors.Join(err, s.secrets.Set(credential.account, previous))
		}
		return errors.Join(err, s.secrets.Delete(credential.account))
	}
	return nil
}

func stageConfig(dir string, data []byte) (string, error) {
	temp, err := os.CreateTemp(dir, ".lgtv-pairing-*")
	if err != nil {
		return "", err
	}
	_, err = temp.Write(data)
	closeErr := temp.Close()
	if err != nil || closeErr != nil {
		os.Remove(temp.Name())
		return "", errors.Join(err, closeErr)
	}
	return temp.Name(), nil
}

func (credential *credentialChange) apply(secrets Secrets) error {
	if credential.remove {
		return secrets.Delete(credential.account)
	}
	return secrets.Set(credential.account, credential.value)
}

func (s store) replaceUnreadableCredential(staged string, credential *credentialChange) error {
	original, err := os.ReadFile(s.path)
	var backup string
	switch {
	case err == nil:
		backup, err = stageConfig(filepath.Dir(s.path), original)
		if err != nil {
			return err
		}
		defer func() {
			if backup != "" {
				os.Remove(backup)
			}
		}()
	case errors.Is(err, os.ErrNotExist):
	default:
		return err
	}
	if err := os.Rename(staged, s.path); err != nil {
		return err
	}
	if err := credential.apply(s.secrets); err != nil {
		if backup == "" {
			return errors.Join(err, os.Remove(s.path))
		}
		if restoreErr := os.Rename(backup, s.path); restoreErr != nil {
			// The deferred cleanup must leave recovery data available.
			saved := backup
			backup = ""
			return errors.Join(err, fmt.Errorf("restore pairing config from %s: %w", saved, restoreErr))
		}
		return err
	}
	return nil
}

func (s store) secret(account string) (string, error) {
	value, err := s.secrets.Get(account)
	if err == nil && value != "" {
		return value, nil
	}
	raw, readErr := s.readRaw()
	if readErr != nil {
		return "", readErr
	}
	if inline := readString(raw, account); inline != "" {
		if errors.Is(err, ErrSecretNotFound) || err == nil {
			_ = s.change(func(latest map[string]json.RawMessage) (bool, error) {
				if readString(latest, account) != inline {
					return false, nil
				}
				delete(latest, account)
				return true, nil
			}, &credentialChange{account: account, value: inline})
		}
		return inline, nil
	}
	if errors.Is(err, ErrSecretNotFound) {
		return "", nil
	}
	return "", err
}

func (s store) saveSecret(account, value, host string, requireCurrentHost bool) error {
	return s.change(func(raw map[string]json.RawMessage) (bool, error) {
		if requireCurrentHost && normalizeHost(readString(raw, "host")) != host {
			return false, errors.New("paired TV changed while connecting; try again")
		}
		if host != "" {
			if old := normalizeHost(readString(raw, "host")); old != "" && old != host {
				for _, key := range []string{"model", "serial", "mac", "wakeMACs", "wakeMACsSavedAt"} {
					delete(raw, key)
				}
			}
			setField(raw, "host", host)
		}
		delete(raw, account)
		setField(raw, "savedAt", time.Now().UTC())
		return true, nil
	}, &credentialChange{account: account, value: value})
}

func (s store) clearSecret(account string) error {
	return s.change(func(raw map[string]json.RawMessage) (bool, error) {
		_, exists := raw[account]
		delete(raw, account)
		return exists, nil
	}, &credentialChange{account: account, remove: true})
}

func (s store) saveMACs(host string, macs []string) error {
	if len(macs) == 0 {
		return nil
	}
	return s.change(func(raw map[string]json.RawMessage) (bool, error) {
		if normalizeHost(readString(raw, "host")) != host {
			return false, nil
		}
		var previous []string
		_ = json.Unmarshal(raw["wakeMACs"], &previous)
		if readString(raw, "mac") == macs[0] && slices.Equal(previous, macs) {
			return false, nil
		}
		setField(raw, "mac", macs[0])
		setField(raw, "wakeMACs", macs)
		setField(raw, "wakeMACsSavedAt", time.Now().UTC())
		return true, nil
	}, nil)
}

func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(host), "["), "]")
}
