package tv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

type State struct {
	Host         string  `json:"host"`
	Model        *string `json:"model"`
	Volume       *int    `json:"volume"`
	Muted        *bool   `json:"muted"`
	CurrentAppID *string `json:"currentAppId"`
}

type Input struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	AppID     *string `json:"appId"`
	Connected bool    `json:"connected"`
	HasSignal bool    `json:"hasSignal"`
}

type Controller struct {
	store store
}

func New(path string, secrets Secrets) Controller {
	return Controller{store{path: ConfigPath(path), secrets: secrets}}
}

func (c Controller) ConfiguredHost() string {
	cfg, _ := c.store.load()
	return cfg.Host
}

func (c Controller) Pair(ctx context.Context, host string, force bool) error {
	host = normalizeHost(host)
	if host == "" {
		return errors.New("enter the TV's IP address or hostname")
	}
	if net.ParseIP(host) == nil && strings.ContainsAny(host, "/\\?#@%:[] \t\r\n") {
		return errors.New("enter a TV hostname or IP address without a URL or port")
	}
	var key string
	if !force && c.ConfiguredHost() == host {
		var err error
		key, err = c.store.secret(clientKeyAccount)
		if err != nil {
			return err
		}
	}
	s, fresh, err := openRegistered(ctx, webOSTarget(host), key, force)
	if err != nil {
		return err
	}
	defer s.conn.CloseNow()
	if err := c.store.saveSecret(clientKeyAccount, fresh, host, false); err != nil {
		return err
	}
	c.refreshMACs(ctx, s, host)
	return nil
}

func (c Controller) registered(ctx context.Context) (*session, config, error) {
	cfg, err := c.store.load()
	if err != nil {
		return nil, cfg, err
	}
	key, err := c.store.secret(clientKeyAccount)
	if err != nil {
		return nil, cfg, fmt.Errorf("read the saved pairing key: %w", err)
	}
	if key == "" {
		return nil, cfg, errors.New("pair the TV first")
	}
	s, fresh, err := openRegistered(ctx, webOSTarget(cfg.Host), key, false)
	if err != nil {
		return nil, cfg, err
	}
	if fresh != key {
		if err := c.store.saveSecret(clientKeyAccount, fresh, cfg.Host, true); err != nil {
			s.conn.CloseNow()
			return nil, cfg, err
		}
	}
	return s, cfg, nil
}

func (c Controller) Status(ctx context.Context) (State, error) {
	state, _, err := c.snapshot(ctx, false)
	return state, err
}

func (c Controller) Snapshot(ctx context.Context) (State, []Input, error) {
	return c.snapshot(ctx, true)
}

func (c Controller) snapshot(ctx context.Context, withInputs bool) (State, []Input, error) {
	s, cfg, err := c.registered(ctx)
	if err != nil {
		return State{}, nil, err
	}
	defer s.conn.CloseNow()
	payload, err := s.request(ctx, "ssap://audio/getVolume", nil)
	if err != nil {
		return State{}, nil, err
	}
	volumeStatus, _ := payload["volumeStatus"].(map[string]any)
	state := State{
		Host: cfg.Host, Model: nonEmpty(cfg.Model),
		Volume: intValue(first(volumeStatus["volume"], payload["volume"])),
		Muted:  boolValue(first(volumeStatus["muteStatus"], payload["muted"], payload["mute"])),
	}
	foreground, _ := s.request(ctx, "ssap://com.webos.applicationManager/getForegroundAppInfo", nil)
	state.CurrentAppID = stringValue(foreground["appId"])
	var inputs []Input
	if withInputs {
		inputs, _ = readInputs(ctx, s)
	}
	c.refreshMACs(ctx, s, cfg.Host)
	return state, inputs, nil
}

func (c Controller) InputList(ctx context.Context) ([]Input, error) {
	s, _, err := c.registered(ctx)
	if err != nil {
		return nil, err
	}
	defer s.conn.CloseNow()
	return readInputs(ctx, s)
}

func readInputs(ctx context.Context, s *session) ([]Input, error) {
	payload, err := s.request(ctx, "ssap://tv/getExternalInputList", nil)
	if err != nil {
		return nil, err
	}
	devices, _ := payload["devices"].([]any)
	inputs := make([]Input, 0, len(devices))
	for _, device := range devices {
		raw, _ := device.(map[string]any)
		id, _ := raw["id"].(string)
		if id == "" {
			continue
		}
		label, _ := raw["label"].(string)
		if label == "" {
			label = id
		}
		inputs = append(inputs, Input{
			ID: id, Label: label, AppID: stringValue(raw["appId"]),
			Connected: isTrue(raw["connected"]), HasSignal: isTrue(raw["hdmiSignalExist"]),
		})
	}
	return inputs, nil
}

func (c Controller) Raw(ctx context.Context, uri string, payload map[string]any) (map[string]any, error) {
	s, _, err := c.registered(ctx)
	if err != nil {
		return nil, err
	}
	defer s.conn.CloseNow()
	return s.request(ctx, uri, payload)
}

func (c Controller) command(ctx context.Context, uri string, payload map[string]any) error {
	s, _, err := c.registered(ctx)
	if err != nil {
		return err
	}
	defer s.conn.CloseNow()
	return s.command(ctx, uri, payload)
}

func (c Controller) VolumeSteps(ctx context.Context, up bool, steps int) error {
	s, _, err := c.registered(ctx)
	if err != nil {
		return err
	}
	defer s.conn.CloseNow()
	uri := "ssap://audio/volumeDown"
	if up {
		uri = "ssap://audio/volumeUp"
	}
	for range steps {
		if err := s.command(ctx, uri, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c Controller) SetVolume(ctx context.Context, value int) error {
	return c.command(ctx, "ssap://audio/setVolume", map[string]any{"volume": max(0, min(100, value))})
}

func (c Controller) ToggleMute(ctx context.Context) error {
	s, _, err := c.registered(ctx)
	if err != nil {
		return err
	}
	defer s.conn.CloseNow()
	payload, err := s.request(ctx, "ssap://audio/getVolume", nil)
	if err != nil {
		return err
	}
	volume, _ := payload["volumeStatus"].(map[string]any)
	muted := isTrue(first(volume["muteStatus"], payload["muted"], payload["mute"]))
	return s.command(ctx, "ssap://audio/setMute", map[string]any{"mute": !muted})
}

func (c Controller) SetMute(ctx context.Context, muted bool) error {
	return c.command(ctx, "ssap://audio/setMute", map[string]any{"mute": muted})
}

func (c Controller) SwitchInput(ctx context.Context, id string) error {
	return c.command(ctx, "ssap://tv/switchInput", map[string]any{"inputId": id})
}

func (c Controller) PowerOff(ctx context.Context) error {
	return c.command(ctx, "ssap://system/turnOff", nil)
}

func (c Controller) KeycodeSet() (bool, error) {
	key, err := c.store.secret(keycodeAccount)
	return key != "", err
}

func (c Controller) SaveKeycode(key string) error {
	key = strings.ToUpper(strings.TrimSpace(key))
	if len(key) != 8 || strings.IndexFunc(key, func(r rune) bool {
		return (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	}) >= 0 {
		return errors.New("IP control keycode must be 8 alphanumeric characters")
	}
	return c.store.saveSecret(keycodeAccount, key, "", false)
}

func (c Controller) ClearKeycode() error {
	return c.store.clearSecret(keycodeAccount)
}

func (c Controller) refreshMACs(ctx context.Context, s *session, host string) {
	payload, err := s.request(ctx, "ssap://com.webos.service.connectionmanager/getinfo", nil)
	if err != nil {
		return
	}
	var macs []string
	for _, key := range []string{"wifiInfo", "wiredInfo", "p2pInfo"} {
		info, _ := payload[key].(map[string]any)
		if mac, ok := info["macAddress"].(string); ok {
			macs = append(macs, mac)
		}
	}
	_ = c.store.saveMACs(host, validMACs(macs))
}

func first(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func nonEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringValue(value any) *string {
	if str, ok := value.(string); ok {
		return &str
	}
	return nil
}

func intValue(value any) *int {
	switch v := value.(type) {
	case float64:
		n := int(v)
		return &n
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return &n
		}
	}
	return nil
}

func boolValue(value any) *bool {
	var result bool
	switch v := value.(type) {
	case bool:
		result = v
	case float64:
		result = v != 0
	case string:
		result = strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") || v == "1"
	default:
		return nil
	}
	return &result
}

func isTrue(value any) bool {
	result := boolValue(value)
	return result != nil && *result
}
