package vnccommands

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"sync"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/packer"
	vnc "github.com/mitchellh/go-vnc"
)

// KeyEvent writes several pieces of a packet in go-vnc. Lock the whole
// message, not individual socket writes, when capture also sends requests.
type vncWriter struct {
	client *vnc.ClientConn
	mu     sync.Mutex
}

func (w *vncWriter) KeyEvent(key uint32, down bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.client.KeyEvent(key, down)
}

func (w *vncWriter) request(incremental bool, width, height int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.client.FramebufferUpdateRequest(incremental, 0, 0, uint16(width), uint16(height))
}

func validateScreenSize(width, height int) error {
	if width <= 0 || height <= 0 || width > 16384 || height > 16384 || int64(width)*int64(height) > 16*1024*1024 {
		return fmt.Errorf("unsupported VNC screen dimensions %dx%d (maximum 16 megapixels and 16384 per side)", width, height)
	}
	return nil
}

// Keep the existing decoder, but reject invalid rectangles before allocating
// their pixel arrays. Raw is universally supported by RFB servers.
type boundedRawEncoding struct{ vnc.RawEncoding }

func (e *boundedRawEncoding) Read(c *vnc.ClientConn, rect *vnc.Rectangle, r io.Reader) (vnc.Encoding, error) {
	if int(rect.X)+int(rect.Width) > int(c.FrameBufferWidth) || int(rect.Y)+int(rect.Height) > int(c.FrameBufferHeight) {
		return nil, errors.New("VNC update rectangle is outside the screen")
	}
	if rect.Width == 0 || rect.Height == 0 {
		return &vnc.RawEncoding{}, nil
	}
	if err := validateScreenSize(int(rect.Width), int(rect.Height)); err != nil {
		return nil, err
	}
	return e.RawEncoding.Read(c, rect, r)
}

// go-vnc's standard update parser always installs its own raw decoder,
// overriding SetEncodings. Override the message parser to enforce bounds.
type screenUpdateMessage struct{}

func (*screenUpdateMessage) Type() uint8 { return 0 }

func (*screenUpdateMessage) Read(c *vnc.ClientConn, r io.Reader) (vnc.ServerMessage, error) {
	var header [3]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	update := &vnc.FramebufferUpdateMessage{Rectangles: make([]vnc.Rectangle, binary.BigEndian.Uint16(header[1:]))}
	for i := range update.Rectangles {
		var data [12]byte
		if _, err := io.ReadFull(r, data[:]); err != nil {
			return nil, err
		}
		rect := &update.Rectangles[i]
		rect.X, rect.Y = binary.BigEndian.Uint16(data[:2]), binary.BigEndian.Uint16(data[2:4])
		rect.Width, rect.Height = binary.BigEndian.Uint16(data[4:6]), binary.BigEndian.Uint16(data[6:8])
		var decoder vnc.Encoding
		switch encoding := int32(binary.BigEndian.Uint32(data[8:])); encoding {
		case 0:
			decoder = &boundedRawEncoding{}
		case -223:
			decoder = &desktopSizeEncoding{}
		default:
			return nil, fmt.Errorf("unsupported VNC screen encoding: %d", encoding)
		}
		var err error
		rect.Enc, err = decoder.Read(c, rect, r)
		if err != nil {
			return nil, err
		}
	}
	return update, nil
}

// DesktopSize is a pseudo-encoding whose dimensions are in the rectangle
// header. The network reader owns client dimensions; capture owns its copy.
type desktopSizeEncoding struct{ width, height uint16 }

func (*desktopSizeEncoding) Type() int32 { return -223 }

func (*desktopSizeEncoding) Read(c *vnc.ClientConn, rect *vnc.Rectangle, _ io.Reader) (vnc.Encoding, error) {
	if err := validateScreenSize(int(rect.Width), int(rect.Height)); err != nil {
		return nil, err
	}
	c.FrameBufferWidth, c.FrameBufferHeight = rect.Width, rect.Height
	return &desktopSizeEncoding{rect.Width, rect.Height}, nil
}

type screenSession struct {
	writer    *vncWriter
	messages  <-chan vnc.ServerMessage
	format    vnc.PixelFormat
	pixels    *image.RGBA
	preview   *previewServer
	recording *aviWriter
	fps       int
	stop      context.CancelFunc
	done      chan error
	ready     chan struct{}
}

func startScreen(ctx context.Context, c Config, writer *vncWriter, messages <-chan vnc.ServerMessage, ui packer.Ui, openBrowser func(context.Context, string) error, fail context.CancelCauseFunc) (*screenSession, error) {
	client := writer.client
	width, height := int(client.FrameBufferWidth), int(client.FrameBufferHeight)
	if err := validateScreenSize(width, height); err != nil {
		return nil, err
	}
	if client.PixelFormat.BPP != 8 && client.PixelFormat.BPP != 16 && client.PixelFormat.BPP != 32 {
		return nil, fmt.Errorf("unsupported VNC pixel format: %d bits per pixel", client.PixelFormat.BPP)
	}
	if client.PixelFormat.TrueColor && (client.PixelFormat.RedMax == 0 || client.PixelFormat.GreenMax == 0 || client.PixelFormat.BlueMax == 0) {
		return nil, errors.New("VNC true-color pixel format has a zero channel maximum")
	}
	if err := client.SetEncodings([]vnc.Encoding{&boundedRawEncoding{}, &desktopSizeEncoding{}}); err != nil {
		return nil, fmt.Errorf("request VNC screen encodings: %w", err)
	}
	ctx, stop := context.WithCancel(ctx)
	s := &screenSession{writer: writer, messages: messages, format: client.PixelFormat,
		pixels: image.NewRGBA(image.Rect(0, 0, width, height)), fps: *c.FrameRate, stop: stop,
		done: make(chan error, 1), ready: make(chan struct{})}
	if c.RecordingPath != "" {
		var err error
		s.recording, err = newAVI(c.RecordingPath, width, height, s.fps)
		if err != nil {
			stop()
			return nil, fmt.Errorf("create VNC recording: %w", err)
		}
		ui.Say("Recording VNC screen to " + c.RecordingPath)
	}
	if c.Preview {
		var err error
		s.preview, err = newPreview()
		if err != nil {
			stop()
			if s.recording != nil {
				err = errors.Join(err, s.recording.Close())
			}
			return nil, fmt.Errorf("start VNC preview: %w", err)
		}
		ui.Say("VNC preview (observe only): " + s.preview.url)
	}
	go func() {
		err := s.run(ctx)
		if s.recording != nil {
			err = errors.Join(err, s.recording.Close())
		}
		if s.preview != nil {
			err = errors.Join(err, s.preview.Close())
		}
		if err != nil {
			fail(fmt.Errorf("capture VNC screen: %w", err))
		}
		s.done <- err
	}()
	if c.PreviewAutoOpen {
		if openBrowser == nil {
			openBrowser = openPreviewBrowser
		}
		if err := openBrowser(ctx, s.preview.url); err != nil {
			ui.Message("Could not open VNC preview browser; open the printed URL manually: " + err.Error())
		}
	}
	return s, nil
}

func (s *screenSession) waitReady(ctx context.Context, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return errors.New("initial VNC screen update exceeded connect_timeout")
	case <-s.ready:
		return nil
	}
}

func (s *screenSession) Close() error {
	s.stop()
	return <-s.done
}

func (s *screenSession) run(ctx context.Context) (result error) {
	request := func(incremental bool) error {
		bounds := s.pixels.Bounds()
		return s.writer.request(incremental, bounds.Dx(), bounds.Dy())
	}
	if err := request(false); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	ticker := time.NewTicker(time.Second / time.Duration(s.fps))
	defer ticker.Stop()
	pending, full, started, dirty := true, false, false, false
	var latest, recordingJPEG []byte
	var recordingStart time.Time
	sample := func() error {
		previous := recordingJPEG
		if dirty {
			var err error
			latest, err = encodeScreen(s.pixels)
			if err != nil {
				return err
			}
			recordingJPEG = latest
			if s.recording != nil && (s.pixels.Bounds().Dx() != s.recording.width || s.pixels.Bounds().Dy() != s.recording.height) {
				recordingJPEG, err = encodeScreen(fitScreen(s.pixels, s.recording.width, s.recording.height))
				if err != nil {
					return err
				}
			}
			dirty = false
		}
		if s.recording != nil {
			if recordingStart.IsZero() {
				recordingStart = time.Now()
			}
			// Repeat the previous frame for missed sampling ticks too. Encoding
			// or network delays must not shorten the recorded elapsed time.
			target := int64(time.Since(recordingStart))*int64(s.fps)/int64(time.Second) + 1
			for int64(len(s.recording.index)) < target {
				frame := recordingJPEG
				if int64(len(s.recording.index))+1 < target && previous != nil {
					frame = previous
				}
				if err := s.recording.WriteFrame(frame); err != nil {
					return fmt.Errorf("write VNC recording: %w", err)
				}
			}
		}
		if s.preview != nil {
			s.preview.publish(latest)
		}
		return nil
	}
	defer func() {
		if started && s.recording != nil && result == nil {
			result = sample() // Include the final fraction of the step's waits.
		}
	}()
	var serveDone <-chan struct{}
	if s.preview != nil {
		serveDone = s.preview.done
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-serveDone:
			return fmt.Errorf("VNC preview server stopped: %w", s.preview.serveErr)
		case message := <-s.messages:
			update, ok := message.(*vnc.FramebufferUpdateMessage)
			if !ok {
				continue
			}
			pending = false
			hasPixels := false
			for _, rect := range update.Rectangles {
				switch enc := rect.Enc.(type) {
				case *desktopSizeEncoding:
					s.pixels = image.NewRGBA(image.Rect(0, 0, int(enc.width), int(enc.height)))
					full = true
					dirty = false // Keep the last image until the resized screen arrives.
					hasPixels = false
				case *vnc.RawEncoding:
					if rect.Width == 0 || rect.Height == 0 {
						continue
					}
					for i, c := range enc.Colors {
						x, y := int(rect.X)+i%int(rect.Width), int(rect.Y)+i/int(rect.Width)
						r, g, b := uint16(65535), uint16(65535), uint16(65535)
						if s.format.TrueColor {
							r, g, b = s.format.RedMax, s.format.GreenMax, s.format.BlueMax
						}
						s.pixels.SetRGBA(x, y, color.RGBA{uint8(uint32(c.R) * 255 / uint32(r)), uint8(uint32(c.G) * 255 / uint32(g)), uint8(uint32(c.B) * 255 / uint32(b)), 255})
					}
					hasPixels = true
				}
			}
			dirty = dirty || hasPixels
			full = full || (!started && !hasPixels)
			if !started && hasPixels {
				if err := sample(); err != nil {
					return err
				}
				started = true
				close(s.ready)
				ticker.Reset(time.Second / time.Duration(s.fps))
			}
		case <-ticker.C:
			if started {
				if err := sample(); err != nil {
					return err
				}
			}
			if !pending {
				if err := request(!full); err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
				pending, full = true, false
			}
		}
	}
}

func encodeScreen(pixels image.Image) ([]byte, error) {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, pixels, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// AVI has a fixed frame size. Fit resized desktops into the initial size,
// preserving aspect ratio with black borders; the preview uses native size.
func fitScreen(src *image.RGBA, width, height int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	w, h := width, height
	if int64(width)*int64(sh) <= int64(height)*int64(sw) {
		h = max(1, width*sh/sw)
	} else {
		w = max(1, height*sw/sh)
	}
	x0, y0 := (width-w)/2, (height-h)/2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.SetRGBA(x+x0, y+y0, src.RGBAAt(x*sw/w, y*sh/h))
		}
	}
	return dst
}
