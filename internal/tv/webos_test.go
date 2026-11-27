package tv

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestRetryRouteDial(t *testing.T) {
	for _, test := range []struct {
		name     string
		first    error
		wantErr  error
		attempts int
	}{
		{"refresh route once", syscall.EHOSTUNREACH, nil, 2},
		{"return connection refusal", syscall.ECONNREFUSED, syscall.ECONNREFUSED, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			count := 0
			client, peer := net.Pipe()
			defer client.Close()
			defer peer.Close()
			conn, err := retryRouteDial(context.Background(), func(context.Context, string, string) (net.Conn, error) {
				count++
				if count == 1 {
					return nil, &net.OpError{Op: "dial", Err: test.first}
				}
				return client, nil
			}, "tcp", "localhost:3001")
			if count != test.attempts || !errors.Is(err, test.wantErr) || (test.wantErr == nil && conn != client) {
				t.Fatalf("connection=%v, error=%v, attempts=%d", conn != nil, err, count)
			}
		})
	}
}

func TestRegistrationFallsBackToUnsignedOnce(t *testing.T) {
	for _, failAgain := range []bool{false, true} {
		name := "register after fallback"
		if failAgain {
			name = "return the second failure"
		}
		t.Run(name, func(t *testing.T) {
			var connections atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next := connections.Add(1)
				conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				var msg struct {
					Type    string `json:"type"`
					Payload struct {
						Key      string          `json:"client-key"`
						Force    bool            `json:"forcePairing"`
						Manifest json.RawMessage `json:"manifest"`
					} `json:"payload"`
				}
				if err := wsjson.Read(r.Context(), conn, &msg); err != nil {
					t.Error(err)
					return
				}
				if msg.Type != "register" || msg.Payload.Key != "test-key" || msg.Payload.Force {
					t.Error("fallback changed registration semantics")
				}
				expected := legacyManifest
				if next == 2 {
					expected = unsignedManifest
				}
				var got, want map[string]any
				if err := json.Unmarshal(msg.Payload.Manifest, &got); err != nil {
					t.Error(err)
					return
				}
				if err := json.Unmarshal(expected, &want); err != nil {
					t.Error(err)
					return
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("unexpected manifest on connection %d", next)
				}
				reply := map[string]any{"type": "registered", "payload": map[string]string{"client-key": "updated-key"}}
				if next == 1 || failAgain {
					reply = map[string]any{"type": "error", "error": "403 Pairing rejected: blacklisted certificate detected"}
				}
				if err := wsjson.Write(r.Context(), conn, reply); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			s, key, err := openRegistered(ctx, "wss"+strings.TrimPrefix(server.URL, "https"), "test-key", false)
			if s != nil {
				s.conn.CloseNow()
			}
			if failAgain {
				if err == nil || !strings.Contains(err.Error(), "blacklisted certificate") {
					t.Fatalf("wanted the TV's second rejection, got %v", err)
				}
			} else if err != nil || key != "updated-key" {
				t.Fatalf("registration returned key %q and error %v", key, err)
			}
			if connections.Load() != 2 {
				t.Fatalf("got %d connections, want 2", connections.Load())
			}
		})
	}
}

func TestUnsignedManifestPreservesPermissions(t *testing.T) {
	var signed struct {
		Permissions []string
		Signed      struct{ Permissions []string }
	}
	var unsigned map[string]json.RawMessage
	if err := json.Unmarshal(legacyManifest, &signed); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(unsignedManifest, &unsigned); err != nil {
		t.Fatal(err)
	}
	if unsigned["signed"] != nil || unsigned["signatures"] != nil {
		t.Fatal("unsigned manifest contains signature metadata")
	}
	var permissions []string
	if err := json.Unmarshal(unsigned["permissions"], &permissions); err != nil {
		t.Fatal(err)
	}
	want := make(map[string]bool)
	for _, permission := range append(signed.Permissions, signed.Signed.Permissions...) {
		want[permission] = true
	}
	for _, permission := range permissions {
		if !want[permission] {
			t.Fatalf("unexpected or duplicate permission %q", permission)
		}
		delete(want, permission)
	}
	if len(want) != 0 {
		t.Fatalf("permissions lost in unsigned registration: %v", want)
	}
}

func TestStandbyCloseBeforeRegistration(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connections.Add(1)
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close(websocket.StatusPolicyViolation, "Try Again Later (EWS)")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := openRegistered(ctx, "wss"+strings.TrimPrefix(server.URL, "https"), "test-key", false)
	if !errors.Is(err, ErrStandby) || connections.Load() != 1 {
		t.Fatalf("got %v from %d connection attempts", err, connections.Load())
	}
}
