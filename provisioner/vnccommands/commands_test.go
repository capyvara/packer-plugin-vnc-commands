package vnccommands

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/packer"
)

func TestMouseWireEventsAndKeyboardCompatibility(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(map[bool]string{false: "mouse only", true: "with preview and recording"}[capture], func(t *testing.T) {
			s := startRFB(t, rfbOptions{screen: true})
			raw := map[string]interface{}{
				// Tokens and held modifiers may span list boundaries. Unknown
				// keyboard tokens must retain the SDK's literal-text behavior.
				"commands": []string{"a<leftCtrl", "On><cli", "ck 12,7><RIGHTCLICK 100%, 0%><leftCtrlOff><click .5%,75%><unknown>"},
			}
			path := filepath.Join(t.TempDir(), "mouse.avi")
			if capture {
				raw["preview"], raw["recording_path"] = true, path
			}
			p := configured(t, s, raw)
			ui := new(packer.MockUi)
			if err := p.Provision(context.Background(), ui, nil, nil); err != nil {
				t.Fatal(err)
			}
			s.awaitClosed(t)
			var got []any
			for len(s.inputs) > 0 {
				got = append(got, <-s.inputs)
			}
			want := []any{
				keyEvent{'a', true}, keyEvent{'a', false}, keyEvent{0xffe3, true},
				pointerEvent{0, 12, 7}, pointerEvent{1, 12, 7}, pointerEvent{0, 12, 7},
				pointerEvent{0, 63, 0}, pointerEvent{4, 63, 0}, pointerEvent{0, 63, 0},
				keyEvent{0xffe3, false},
				pointerEvent{0, 0, 23}, pointerEvent{1, 0, 23}, pointerEvent{0, 0, 23},
			}
			// The SDK synthesizes Shift for '<' and '>'; verify it still
			// interprets the trailing unknown token without our intervention.
			unknown := []keyEvent{
				{0xffe1, true}, {'<', true}, {'<', false}, {0xffe1, false},
				{'u', true}, {'u', false}, {'n', true}, {'n', false}, {'k', true}, {'k', false},
				{'n', true}, {'n', false}, {'o', true}, {'o', false}, {'w', true}, {'w', false}, {'n', true}, {'n', false},
				{0xffe1, true}, {'>', true}, {'>', false}, {0xffe1, false},
			}
			for _, event := range unknown {
				want = append(want, event)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("input events: got %v, want %v", got, want)
			}
			if capture {
				if len(readAVI(t, path, 64, 32, 5)) == 0 {
					t.Fatal("mouse commands prevented recording")
				}
			}
		})
	}
}

func TestMouseCoordinatesFollowResize(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(map[bool]string{false: "without capture", true: "with capture"}[capture], func(t *testing.T) {
			s := startRFB(t, rfbOptions{screen: true, resizeScreen: true})
			p := configured(t, s, map[string]interface{}{
				"preview": capture, "frame_rate": 20,
				"commands": []string{"<click 100%,100%><wait550ms><rightClick 50%,50%><click 100%,100%>"},
			})
			if err := p.Provision(context.Background(), new(packer.MockUi), nil, nil); err != nil {
				t.Fatal(err)
			}
			s.awaitClosed(t)
			var got []pointerEvent
			for len(s.pointers) > 0 {
				got = append(got, <-s.pointers)
			}
			want := []pointerEvent{
				{0, 63, 31}, {1, 63, 31}, {0, 63, 31},
				{0, 16, 32}, {4, 16, 32}, {0, 16, 32},
				{0, 31, 63}, {1, 31, 63}, {0, 31, 63},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("resize coordinates: got %v, want %v", got, want)
			}
		})
	}
}

func TestInvalidMouseCommands(t *testing.T) {
	for _, command := range []string{
		"<click>", "<click 1,2", "<rightClick>", "<click1,2>", "<click(1,2)>",
		"<click 1>", "<click 1,2,3>", "<click ,1>", "<rightClick 1,>",
		"<click -1,0>", "<click 1.5,0>", "<click 65536,0>", "<click 101%,0>",
		"<click -1%,0>", "<click NaN%,0>", "<click Inf%,0>", "<click 1e2%,0>",
		"<click sensitive-coordinate,0>", "<click 0,0><wait0s>",
	} {
		t.Run(command, func(t *testing.T) {
			err := new(Provisioner).Prepare(map[string]interface{}{"host": "localhost", "commands": []string{"sensitive-text" + command}})
			if err == nil {
				t.Fatal("invalid mouse command was accepted")
			}
			if strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("error exposed command contents: %v", err)
			}
		})
	}
}

func TestMouseCoordinatesInterpolation(t *testing.T) {
	for _, coordinate := range []string{"50%", "sensitive-invalid", "{{.Unrendered}}"} {
		t.Run(coordinate, func(t *testing.T) {
			s := startRFB(t, rfbOptions{screen: true})
			p := configured(t, s, map[string]interface{}{"commands": []string{"<rightClick {{.X}},{{user `y`}}>"}, "packer_user_variables": map[string]string{"y": "100%"}})
			err := p.Provision(context.Background(), new(packer.MockUi), nil, map[string]interface{}{"X": coordinate})
			if coordinate != "50%" {
				if err == nil || strings.Contains(err.Error(), "sensitive") {
					t.Fatalf("unexpected validation result: %v", err)
				}
				// Invalid rendered commands fail before opening a connection.
				select {
				case <-s.ready:
					t.Fatal("invalid mouse command connected to VNC")
				default:
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			s.awaitClosed(t)
			want := []pointerEvent{{0, 32, 31}, {4, 32, 31}, {0, 32, 31}}
			var got []pointerEvent
			for len(s.pointers) > 0 {
				got = append(got, <-s.pointers)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("rendered coordinates: got %v, want %v", got, want)
			}
		})
	}
}

func TestMouseBounds(t *testing.T) {
	for _, command := range []string{"<click 64,0>", "<rightClick 0,32>"} {
		t.Run(command, func(t *testing.T) {
			s := startRFB(t, rfbOptions{screen: true})
			p := configured(t, s, map[string]interface{}{"commands": []string{command}})
			err := p.Provision(context.Background(), new(packer.MockUi), nil, nil)
			if err == nil || !strings.Contains(err.Error(), "outside") {
				t.Fatalf("expected out-of-bounds error: %v", err)
			}
			s.awaitClosed(t)
			if len(s.pointers) != 0 {
				t.Fatal("sent an out-of-bounds pointer event")
			}
		})
	}
	s := startRFB(t, rfbOptions{}) // A 1x1 framebuffer: all percentages map to 0.
	p := configured(t, s, map[string]interface{}{"commands": []string{"<click 100%,0%><rightClick 0,100%>"}})
	if err := p.Provision(context.Background(), new(packer.MockUi), nil, nil); err != nil {
		t.Fatal(err)
	}
	s.awaitClosed(t)
	for len(s.pointers) > 0 {
		if event := <-s.pointers; event.x != 0 || event.y != 0 {
			t.Fatalf("1x1 framebuffer: %v", event)
		}
	}
}

func TestMouseCancellationAndDisconnect(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "disconnect"}[disconnect], func(t *testing.T) {
			options := rfbOptions{}
			if disconnect {
				options.disconnectAfter = 1
			}
			s := startRFB(t, options)
			p := configured(t, s, map[string]interface{}{"commands": []string{"<rightClick 0,0>"}, "key_interval": "1h"})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- p.Provision(ctx, new(packer.MockUi), nil, nil) }()
			select {
			case <-s.pointers:
			case <-time.After(time.Second):
				t.Fatal("no pointer movement received")
			}
			if !disconnect {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil || (!disconnect && !errors.Is(err, context.Canceled)) {
					t.Fatalf("unexpected interruption error: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("interruption did not stop mouse pacing")
			}
			s.awaitClosed(t)
			if len(s.pointers) != 0 {
				t.Fatal("sent or replayed a click after interruption")
			}
		})
	}
}

func TestMouseDisconnectWhilePressed(t *testing.T) {
	s := startRFB(t, rfbOptions{disconnectAfter: 2})
	p := configured(t, s, map[string]interface{}{"commands": []string{"<rightClick 0,0>sensitive-text<wait1h>"}, "key_interval": "10ms"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := p.Provision(ctx, new(packer.MockUi), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "disconnected") {
		t.Fatalf("unexpected disconnect result: %v", err)
	}
	s.awaitClosed(t)
	if len(s.pointers) != 2 || len(s.events) != 0 {
		t.Fatal("sent or replayed commands after button-down disconnect")
	}
}

func TestMouseTrackingRejectsInvalidScreen(t *testing.T) {
	s := startRFB(t, rfbOptions{screen: true, invalidRectangle: true})
	p := configured(t, s, map[string]interface{}{"commands": []string{"<wait1h><click 0,0>"}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := p.Provision(ctx, new(packer.MockUi), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "disconnected") {
		t.Fatalf("invalid rectangle did not stop resize tracking: %v", err)
	}
	s.awaitClosed(t)
	if len(s.pointers) != 0 {
		t.Fatal("clicked after resize tracking failed")
	}
}
