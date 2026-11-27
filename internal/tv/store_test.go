package tv

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type keychainFixture struct {
	values map[string]string
	getErr error
	set    func() error
}

func (fixture *keychainFixture) Get(account string) (string, error) {
	if fixture.getErr != nil {
		return "", fixture.getErr
	}
	if value, ok := fixture.values[account]; ok {
		return value, nil
	}
	return "", ErrSecretNotFound
}

func (fixture *keychainFixture) Set(account, value string) error {
	if fixture.set != nil {
		set := fixture.set
		fixture.set = nil
		if err := set(); err != nil {
			return err
		}
	}
	fixture.values[account] = value
	return nil
}

func (fixture *keychainFixture) Delete(account string) error {
	delete(fixture.values, account)
	return nil
}

func TestPairingSaveRestoresExistingKeyOnConfigReplaceFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pairing.json")
	const oldConfig = `{"host":"old.local","model":"known"}`
	if err := os.WriteFile(path, []byte(oldConfig), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := &keychainFixture{
		values: map[string]string{clientKeyAccount: "old-key"},
		set: func() error {
			if err := os.Rename(path, path+".previous"); err != nil {
				return err
			}
			return os.Mkdir(path, 0700)
		},
	}
	s := store{path: path, secrets: fixture}
	if err := s.saveSecret(clientKeyAccount, "new-key", "new.local", false); err == nil {
		t.Fatal("pairing succeeded despite the config replace failure")
	}
	if key, _ := fixture.Get(clientKeyAccount); key != "old-key" {
		t.Fatalf("previous pairing key was lost: %q", key)
	}
	s.path = path + ".previous"
	cfg, err := s.load()
	if err != nil || cfg.Host != "old.local" || cfg.Model != "known" {
		t.Fatalf("previous pairing config was lost: %#v, %v", cfg, err)
	}
}

func TestLegacyKeyStaysInConfigWhenKeychainWriteFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pairing.json")
	const contents = `{"host":"tv.local","clientKey":"legacy-key","custom":true}`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := &keychainFixture{values: map[string]string{}, set: func() error { return errors.New("keychain unavailable") }}
	s := store{path: path, secrets: fixture}
	if value, err := s.secret(clientKeyAccount); err != nil || value != "legacy-key" {
		t.Fatalf("legacy pairing became unusable: %q, %v", value, err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != contents {
		t.Fatalf("config was changed after failure: %s, %v", actual, err)
	}
}

func TestRePairCanWriteUnreadableCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pairing.json")
	const old = `{"host":"old.local","model":"known"}`
	if err := os.WriteFile(path, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := &keychainFixture{
		values: map[string]string{clientKeyAccount: "old-key"},
		getErr: errors.New("Keychain read denied"),
		set:    func() error { return errors.New("Keychain write denied") },
	}
	s := store{path: path, secrets: fixture}
	if err := s.saveSecret(clientKeyAccount, "new-key", "new.local", false); err == nil {
		t.Fatal("expected write denial")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != old {
		t.Fatalf("previous config was not restored: %s, %v", data, err)
	}
	if err := s.saveSecret(clientKeyAccount, "new-key", "new.local", false); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.load()
	if err != nil || cfg.Host != "new.local" || fixture.values[clientKeyAccount] != "new-key" {
		t.Fatalf("re-pair did not update the configuration and key: %#v, %v", cfg, err)
	}
}

func TestClearUnreadableKeycodeWithoutPairingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pairing.json")
	fixture := &keychainFixture{
		values: map[string]string{keycodeAccount: "TEST1234"},
		getErr: errors.New("Keychain read denied"),
	}
	s := store{path: path, secrets: fixture}
	if err := s.clearSecret(keycodeAccount); err != nil {
		t.Fatal(err)
	}
	if _, exists := fixture.values[keycodeAccount]; exists {
		t.Fatal("keycode was not cleared")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected pairing config: %v", err)
	}
}
