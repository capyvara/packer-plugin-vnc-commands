package vnccommands

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/packer"
)

func configured(t *testing.T, s *rfbServer, overrides map[string]interface{}) *Provisioner {
	t.Helper()
	raw := map[string]interface{}{"host": s.host, "port": s.port, "commands": []string{"a<enter>"}, "key_interval": "1ms", "connect_timeout": "1s"}
	for k, v := range overrides {
		raw[k] = v
	}
	p := new(Provisioner)
	if err := p.Prepare(raw); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWireEventsAndAuthentication(t *testing.T) {
	for _, password := range []string{"", "secret", "long-password"} {
		t.Run("password="+password, func(t *testing.T) {
			s := startRFB(t, rfbOptions{password: password})
			p := configured(t, s, map[string]interface{}{
				"password": password,
				// Split a token as well as a held modifier across list boundaries.
				"commands": []string{"a<leftCtrl", "On>", "c", "<leftCtrlOff><wait20ms><enter>"},
			})
			ui := new(packer.MockUi)
			if err := p.Provision(context.Background(), ui, nil, nil); err != nil {
				t.Fatal(err)
			}
			s.awaitClosed(t)
			var got []keyEvent
			for len(s.events) > 0 {
				got = append(got, <-s.events)
			}
			want := []keyEvent{{'a', true}, {'a', false}, {0xffe3, true}, {'c', true}, {'c', false}, {0xffe3, false}, {0xff0d, true}, {0xff0d, false}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("events: got %v, want %v", got, want)
			}
			for _, status := range ui.SayMessages {
				if strings.Contains(status.Message, "leftCtrl") || (password != "" && strings.Contains(status.Message, password)) {
					t.Fatal("status exposed password or commands")
				}
			}
		})
	}
}

func TestAuthenticationFailure(t *testing.T) {
	s := startRFB(t, rfbOptions{password: "correct"})
	p := configured(t, s, map[string]interface{}{"password": "wrong"})
	err := p.Provision(context.Background(), new(packer.MockUi), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "authentication") || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("unexpected authentication error: %v", err)
	}
	s.awaitClosed(t)
	if len(s.events) != 0 {
		t.Fatal("typed despite failed authentication")
	}
}

func TestUnsupportedProtocol(t *testing.T) {
	s := startRFB(t, rfbOptions{version: "RFB 003.003\n"})
	err := configured(t, s, nil).Provision(context.Background(), new(packer.MockUi), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "RFB 3.8") {
		t.Fatalf("unexpected protocol error: %v", err)
	}
	s.awaitClosed(t)
}

func TestHandshakeTimeout(t *testing.T) {
	s := startRFB(t, rfbOptions{stall: true})
	p := configured(t, s, map[string]interface{}{"connect_timeout": "40ms"})
	start := time.Now()
	err := p.Provision(context.Background(), new(packer.MockUi), nil, nil)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("handshake was not bounded: %v", err)
	}
	s.awaitClosed(t)
}

func TestCancellation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		options   rfbOptions
		overrides map[string]interface{}
	}{
		{"handshake", rfbOptions{stall: true}, nil},
		{"wait", rfbOptions{}, map[string]interface{}{"commands": []string{"a<wait1h>"}}},
		{"key interval", rfbOptions{}, map[string]interface{}{"key_interval": "1h"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := startRFB(t, tc.options)
			p := configured(t, s, tc.overrides)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- p.Provision(ctx, new(packer.MockUi), nil, nil) }()
			select {
			case <-s.ready:
			case <-time.After(time.Second):
				t.Fatal("server did not become ready")
			}
			// Make sure cancellation exercises execution, not just the handshake.
			if tc.name != "handshake" {
				count := 1
				if tc.name == "wait" {
					count = 2
				}
				for i := 0; i < count; i++ {
					select {
					case <-s.events:
					case <-time.After(time.Second):
						t.Fatal("typing did not start")
					}
				}
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("unexpected cancellation error: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation did not interrupt execution")
			}
			s.awaitClosed(t)
		})
	}
}

func TestExecutionDeadline(t *testing.T) {
	s := startRFB(t, rfbOptions{})
	p := configured(t, s, map[string]interface{}{"commands": []string{"<wait1h>"}})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := p.Provision(ctx, new(packer.MockUi), nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected timeout error: %v", err)
	}
	s.awaitClosed(t)
}

func TestDisconnectInterruptsWait(t *testing.T) {
	s := startRFB(t, rfbOptions{disconnectAfter: 2})
	p := configured(t, s, map[string]interface{}{"commands": []string{"a<wait1h>"}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := p.Provision(ctx, new(packer.MockUi), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "disconnected") {
		t.Fatalf("unexpected disconnect error: %v", err)
	}
	s.awaitClosed(t)
}

func TestDisconnectStopsTyping(t *testing.T) {
	s := startRFB(t, rfbOptions{disconnectAfter: 1})
	p := configured(t, s, map[string]interface{}{"commands": []string{strings.Repeat("a", 100)}, "key_interval": "10ms"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := p.Provision(ctx, new(packer.MockUi), nil, nil)
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("disconnect did not stop typing: %v", err)
	}
	s.awaitClosed(t)
	if len(s.events) != 1 {
		t.Fatal("unexpected events after disconnect")
	}
}

func TestConnectionRefused(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().(*net.TCPAddr)
	listener.Close()
	p := new(Provisioner)
	if err := p.Prepare(map[string]interface{}{"host": "127.0.0.1", "port": address.Port, "commands": []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	err = p.Provision(context.Background(), new(packer.MockUi), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "connect to VNC at 127.0.0.1:") {
		t.Fatalf("unexpected connection error: %v", err)
	}
}

func TestRuntimeValuesOnWire(t *testing.T) {
	s := startRFB(t, rfbOptions{password: "secret"})
	p := configured(t, s, map[string]interface{}{"host": "{{.Host}}", "password": "{{.Password}}", "commands": []string{"{{.Text}}", "<enter>"}})
	err := p.Provision(context.Background(), new(packer.MockUi), nil, map[string]interface{}{"Host": s.host, "Password": "secret", "Text": "x"})
	if err != nil {
		t.Fatal(err)
	}
	s.awaitClosed(t)
	var got []keyEvent
	for len(s.events) > 0 {
		got = append(got, <-s.events)
	}
	want := []keyEvent{{'x', true}, {'x', false}, {0xff0d, true}, {0xff0d, false}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime events: got %v, want %v", got, want)
	}
}
