package tv

import (
	"context"
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

//go:embed manifest.json
var legacyManifest json.RawMessage

var unsignedManifest = makeUnsignedManifest()

func makeUnsignedManifest() json.RawMessage {
	var manifest struct {
		Version     int      `json:"manifestVersion"`
		AppVersion  string   `json:"appVersion"`
		Permissions []string `json:"permissions"`
		Signed      *struct {
			Permissions []string `json:"permissions"`
		} `json:"signed,omitempty"`
	}
	if err := json.Unmarshal(legacyManifest, &manifest); err != nil {
		panic(err)
	}
	seen := make(map[string]bool)
	var permissions []string
	for _, permission := range append(manifest.Permissions, manifest.Signed.Permissions...) {
		if !seen[permission] {
			permissions = append(permissions, permission)
			seen[permission] = true
		}
	}
	manifest.Permissions, manifest.Signed = permissions, nil
	result, err := json.Marshal(manifest)
	if err != nil {
		panic(err)
	}
	return result
}

// ErrStandby reports that the TV closed the connection while in standby.
var ErrStandby = errors.New("TV is in standby (WebSocket 1008: Try Again Later (EWS)). Wake the TV and refresh.")

func webOSErr(operation string, err error) error {
	var closed websocket.CloseError
	if errors.As(err, &closed) {
		if closed.Code == websocket.StatusPolicyViolation && strings.Contains(strings.ToLower(closed.Reason), "try again later (ews)") {
			return ErrStandby
		}
		return fmt.Errorf("%s: WebSocket closed (%d: %s)", operation, closed.Code, closed.Reason)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// The TV uses a self-signed certificate, and supplies no CA or public key at pairing.
var tvDialer = net.Dialer{Timeout: 5 * time.Second}

var webOSHTTP = &http.Client{
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return retryRouteDial(ctx, tvDialer.DialContext, network, address)
		},
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		TLSHandshakeTimeout: 5 * time.Second,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func retryRouteDial(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), network, address string) (net.Conn, error) {
	conn, err := dial(ctx, network, address)
	if !errors.Is(err, syscall.EHOSTUNREACH) && !errors.Is(err, syscall.ENETUNREACH) {
		return conn, err
	}
	// macOS can reject the first socket while refreshing the app's Local Network access.
	if err := pause(ctx, 600*time.Millisecond); err != nil {
		return nil, err
	}
	return dial(ctx, network, address)
}

type session struct {
	conn   *websocket.Conn
	nextID int
}

type envelope struct {
	Type    string         `json:"type"`
	ID      string         `json:"id"`
	Error   string         `json:"error"`
	Payload map[string]any `json:"payload"`
}

func dialWebOS(ctx context.Context, target string) (*session, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, target, &websocket.DialOptions{
		HTTPClient: webOSHTTP,
		HTTPHeader: http.Header{"Origin": []string{"null"}},
	})
	if err != nil {
		return nil, webOSErr("Connect to "+target, err)
	}
	conn.SetReadLimit(2 << 20)
	return &session{conn: conn}, nil
}

func openRegistered(ctx context.Context, target, key string, force bool) (*session, string, error) {
	s, err := dialWebOS(ctx, target)
	if err != nil {
		return nil, "", err
	}
	newKey, err := s.register(ctx, key, force, legacyManifest)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "blacklisted certificate") {
		s.conn.CloseNow()
		s, err = dialWebOS(ctx, target)
		if err != nil {
			return nil, "", err
		}
		newKey, err = s.register(ctx, key, force, unsignedManifest)
	}
	if err != nil {
		s.conn.CloseNow()
		return nil, "", err
	}
	return s, newKey, nil
}

func (s *session) register(ctx context.Context, key string, force bool, manifest json.RawMessage) (string, error) {
	timeout := 20 * time.Second
	if force || key == "" {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	payload := map[string]any{"forcePairing": force, "pairingType": "PROMPT", "manifest": manifest}
	if key != "" && !force {
		payload["client-key"] = key
	}
	if err := s.write(ctx, map[string]any{"type": "register", "id": "register_0", "payload": payload}); err != nil {
		return "", webOSErr("Register with TV", err)
	}
	for {
		var msg envelope
		if err := wsjson.Read(ctx, s.conn, &msg); err != nil {
			return "", webOSErr("Register with TV", err)
		}
		switch msg.Type {
		case "registered":
			if value, _ := msg.Payload["client-key"].(string); value != "" {
				return value, nil
			}
			if !force && key != "" {
				return key, nil
			}
			return "", errors.New("TV did not return a pairing key.")
		case "error":
			return "", fmt.Errorf("webOS registration failed: %s", msg.Error)
		}
	}
}

func (s *session) write(ctx context.Context, msg map[string]any) error {
	err := wsjson.Write(ctx, s.conn, msg)
	if err != nil && websocket.CloseStatus(err) == -1 && ctx.Err() == nil {
		// The TV can send its close frame as soon as the upgrade succeeds.
		// Read it if the peer closed while we were writing the first request.
		readCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		var response envelope
		if readErr := wsjson.Read(readCtx, s.conn, &response); websocket.CloseStatus(readErr) != -1 {
			return readErr
		}
	}
	return err
}

func (s *session) request(ctx context.Context, uri string, payload map[string]any) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	s.nextID++
	id := fmt.Sprintf("request_%d", s.nextID)
	message := map[string]any{"type": "request", "id": id, "uri": uri}
	if payload != nil {
		message["payload"] = payload
	}
	if err := s.write(ctx, message); err != nil {
		return nil, webOSErr(uri, err)
	}
	for {
		var msg envelope
		if err := wsjson.Read(ctx, s.conn, &msg); err != nil {
			return nil, webOSErr(uri, err)
		}
		if msg.ID != id {
			continue
		}
		if msg.Type == "error" {
			return nil, fmt.Errorf("%s: %s", uri, msg.Error)
		}
		if msg.Payload == nil {
			msg.Payload = make(map[string]any)
		}
		return msg.Payload, nil
	}
}

func (s *session) command(ctx context.Context, uri string, payload map[string]any) error {
	reply, err := s.request(ctx, uri, payload)
	if err == nil && reply["returnValue"] == false {
		return fmt.Errorf("%s: TV declined the request", uri)
	}
	return err
}

func webOSTarget(host string) string {
	return (&url.URL{Scheme: "wss", Host: net.JoinHostPort(host, "3001")}).String()
}
