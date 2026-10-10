package vnccommands

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/packer"
)

type previewUI struct {
	packer.MockUi
	urls chan string
}

func (u *previewUI) Say(message string) {
	u.MockUi.Say(message)
	if url, ok := strings.CutPrefix(message, "VNC preview (observe only): "); ok {
		u.urls <- url
	}
}

func TestPreviewStreamAndRecording(t *testing.T) {
	for _, autoOpen := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual browser", true: "automatic browser"}[autoOpen], func(t *testing.T) {
			s := startRFB(t, rfbOptions{screen: true, resizeScreen: true})
			path := filepath.Join(t.TempDir(), "artifacts", "screen.avi")
			p := configured(t, s, map[string]interface{}{
				"preview": true, "preview_auto_open": autoOpen, "recording_path": path,
				"frame_rate": 20, "commands": []string{"a<wait550ms>"},
			})
			opened := make(chan string, 1)
			p.openPreview = func(_ context.Context, url string) error {
				opened <- url
				return nil
			}
			ui := &previewUI{urls: make(chan string, 1)}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- p.Provision(ctx, ui, nil, nil) }()
			var url string
			select {
			case url = <-ui.urls:
			case <-ctx.Done():
				t.Fatal("preview URL was not printed")
			}
			if !strings.HasPrefix(url, "http://127.0.0.1:") {
				t.Fatalf("preview must listen on loopback: %s", url)
			}
			client := &http.Client{Timeout: time.Second}
			page, err := client.Get(url)
			if err != nil {
				t.Fatal(err)
			}
			html, err := io.ReadAll(page.Body)
			page.Body.Close()
			if err != nil || page.StatusCode != http.StatusOK || !bytes.Contains(html, []byte("Observe only")) {
				t.Fatalf("preview page: %s, %v", html, err)
			}
			// There is no endpoint accepting user input from the observer.
			post, err := client.Post(url+"stream", "application/json", strings.NewReader(`{"key":"x"}`))
			if err != nil {
				t.Fatal(err)
			}
			post.Body.Close()
			if post.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("preview accepts POST: %d", post.StatusCode)
			}
			response, err := client.Get(url + "stream")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			_, params, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
			if err != nil {
				t.Fatal(err)
			}
			stream := multipart.NewReader(response.Body, params["boundary"])
			sawPatch, sawResize := false, false
			for !sawResize {
				part, err := stream.NextPart()
				if err != nil {
					t.Fatal(err)
				}
				frame, err := jpeg.Decode(part)
				part.Close()
				if err != nil {
					t.Fatal(err)
				}
				if frame.Bounds().Dx() == 64 && channel(frame, 2, 2, 1) > 50000 {
					sawPatch = true
					if channel(frame, 32, 16, 0) < 50000 {
						t.Fatal("incremental patch erased unchanged pixels")
					}
				}
				sawResize = frame.Bounds().Dx() == 32 && frame.Bounds().Dy() == 64 && channel(frame, 16, 32, 2) > 50000
			}
			if !sawPatch {
				t.Fatal("missing incremental screen update")
			}
			// Closing the viewer must leave the command sequence running.
			response.Body.Close()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			s.awaitClosed(t)
			if autoOpen {
				select {
				case got := <-opened:
					if got != url {
						t.Fatalf("opened %q, printed %q", got, url)
					}
				default:
					t.Fatal("preview_auto_open did not open browser")
				}
			} else if len(opened) != 0 {
				t.Fatal("preview opened browser by default")
			}
			var events []keyEvent
			for len(s.events) > 0 {
				events = append(events, <-s.events)
			}
			if !reflect.DeepEqual(events, []keyEvent{{'a', true}, {'a', false}}) {
				t.Fatalf("screen capture interfered with typing: %v", events)
			}
			requests := make([]screenRequest, 0, len(s.requests))
			for len(s.requests) > 0 {
				requests = append(requests, <-s.requests)
			}
			if len(requests) < 4 || requests[0].incremental || !requests[1].incremental || requests[3].incremental || requests[3].width != 32 || requests[3].height != 64 {
				t.Fatalf("full/incremental/resize requests: %v", requests)
			}
			frames := readAVI(t, path, 64, 32, 20)
			if len(frames) < 10 || channel(frames[0], 32, 16, 0) < 50000 {
				t.Fatal("recording lost timing or initial frame")
			}
			last := frames[len(frames)-1]
			if channel(last, 32, 16, 2) < 50000 || channel(last, 2, 16, 2) > 10000 {
				t.Fatal("resized recording was not fitted with black borders")
			}
			if _, err := client.Get(url); err == nil {
				t.Fatal("preview server survived provisioner completion")
			}
		})
	}
}

func TestRecordingCancellationAndBrowserFailure(t *testing.T) {
	s := startRFB(t, rfbOptions{screen: true})
	path := filepath.Join(t.TempDir(), "cancelled.avi")
	p := configured(t, s, map[string]interface{}{"preview": true, "preview_auto_open": true, "recording_path": path, "commands": []string{"a<wait1h>"}, "frame_rate": 20})
	p.openPreview = func(context.Context, string) error { return errors.New("no desktop browser") }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ui := new(packer.MockUi)
	done := make(chan error, 1)
	go func() { done <- p.Provision(ctx, ui, nil, nil) }()
	select {
	case <-s.events:
	case <-time.After(time.Second):
		t.Fatal("browser failure prevented typing")
	}
	// Cancellation must finalize even a very short recording.
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("capture blocked cancellation")
	}
	s.awaitClosed(t)
	if len(readAVI(t, path, 64, 32, 20)) == 0 {
		t.Fatal("cancelled recording has no complete frame")
	}
}

func TestRecordingWithoutPreviewRepeatsStaticScreen(t *testing.T) {
	s := startRFB(t, rfbOptions{screen: true, onlyFirstFrame: true})
	path := filepath.Join(t.TempDir(), "static.avi")
	p := configured(t, s, map[string]interface{}{"recording_path": "{{.Path}}", "frame_rate": 20, "commands": []string{"a<wait250ms>"}})
	p.openPreview = func(context.Context, string) error {
		t.Fatal("recording-only step opened a browser")
		return nil
	}
	ui := new(packer.MockUi)
	if err := p.Provision(context.Background(), ui, nil, map[string]interface{}{"Path": path}); err != nil {
		t.Fatal(err)
	}
	s.awaitClosed(t)
	for _, message := range ui.SayMessages {
		if strings.Contains(message.Message, "http://") {
			t.Fatal("recording-only step started a preview")
		}
	}
	frames := readAVI(t, path, 64, 32, 20)
	if len(frames) < 6 {
		t.Fatal("recording compressed time while screen was unchanged")
	}
	for _, frame := range frames {
		if channel(frame, 32, 16, 0) < 50000 {
			t.Fatal("recording lost unchanged screen")
		}
	}
}

func TestCaptureErrorsBeforeTyping(t *testing.T) {
	for _, tc := range []struct {
		name     string
		options  rfbOptions
		existing bool
		want     string
	}{
		{"missing initial frame", rfbOptions{}, false, "initial VNC screen"},
		{"invalid rectangle", rfbOptions{screen: true, invalidRectangle: true}, false, "disconnected"},
		{"existing recording", rfbOptions{screen: true}, true, "create VNC recording"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := startRFB(t, tc.options)
			path := filepath.Join(t.TempDir(), "screen.avi")
			if tc.existing {
				if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			p := configured(t, s, map[string]interface{}{"recording_path": path, "connect_timeout": "100ms"})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := p.Provision(ctx, new(packer.MockUi), nil, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected capture error: %v", err)
			}
			s.awaitClosed(t)
			if len(s.events) != 0 {
				t.Fatal("typed after capture failed")
			}
			if tc.existing {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "keep" {
					t.Fatal("overwrote existing recording")
				}
			} else {
				width, height := 1, 1
				if tc.options.screen {
					width, height = 64, 32
				}
				readAVI(t, path, width, height, 5)
			}
		})
	}
}

func channel(frame image.Image, x, y, index int) uint32 {
	r, g, b, _ := frame.At(x, y).RGBA()
	return [3]uint32{r, g, b}[index]
}

// Parse the resulting RIFF file as a consumer would: walk chunk boundaries,
// decode every JPEG, and resolve every index offset back to a frame chunk.
func readAVI(t *testing.T, path string, width, height, fps int) []image.Image {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "AVI " || int(binary.LittleEndian.Uint32(data[4:8])) != len(data)-8 {
		t.Fatal("invalid AVI RIFF header")
	}
	var frames []image.Image
	var index []byte
	var declaredFrames uint32
	var movi int
	var walk func([]byte, int)
	walk = func(chunks []byte, offset int) {
		for len(chunks) > 0 {
			if len(chunks) < 8 {
				t.Fatal("truncated chunk header")
			}
			n := int(binary.LittleEndian.Uint32(chunks[4:8]))
			padded := n + n%2
			if n > len(chunks)-8 || padded > len(chunks)-8 {
				t.Fatal("truncated chunk payload")
			}
			body := chunks[8 : 8+n]
			switch string(chunks[:4]) {
			case "LIST":
				if len(body) < 4 {
					t.Fatal("invalid LIST")
				}
				if string(body[:4]) == "movi" {
					movi = offset + 8
				}
				walk(body[4:], offset+12)
			case "avih":
				declaredFrames = binary.LittleEndian.Uint32(body[16:20])
				if int(binary.LittleEndian.Uint32(body[32:36])) != width || int(binary.LittleEndian.Uint32(body[36:40])) != height {
					t.Fatal("incorrect AVI dimensions")
				}
			case "strh":
				if string(body[:8]) != "vidsMJPG" || binary.LittleEndian.Uint32(body[20:24]) != 1 || int(binary.LittleEndian.Uint32(body[24:28])) != fps {
					t.Fatal("incorrect AVI codec or frame rate")
				}
			case "00dc":
				frame, err := jpeg.Decode(bytes.NewReader(body))
				if err != nil || frame.Bounds().Dx() != width || frame.Bounds().Dy() != height {
					t.Fatalf("invalid recorded JPEG: %v", err)
				}
				frames = append(frames, frame)
			case "idx1":
				index = body
			}
			chunks, offset = chunks[8+padded:], offset+8+padded
		}
	}
	walk(data[12:], 12)
	if int(declaredFrames) != len(frames) || len(index) != 16*len(frames) {
		t.Fatal("AVI frame counts or index do not match")
	}
	for i := 0; i < len(index); i += 16 {
		position := movi + int(binary.LittleEndian.Uint32(index[i+8:i+12]))
		if position+8 > len(data) || string(data[position:position+4]) != "00dc" || binary.LittleEndian.Uint32(data[position+4:position+8]) != binary.LittleEndian.Uint32(index[i+12:i+16]) {
			t.Fatal("invalid AVI index offset")
		}
	}
	return frames
}
