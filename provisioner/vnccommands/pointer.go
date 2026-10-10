package vnccommands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	vnc "github.com/mitchellh/go-vnc"
)

// Mouse-only sessions still request updates to receive DesktopSize changes,
// but skip pixel payloads instead of allocating images or encoding JPEGs.
type discardRawEncoding struct{}

func (*discardRawEncoding) Type() int32 { return 0 }

func (*discardRawEncoding) Read(c *vnc.ClientConn, rect *vnc.Rectangle, r io.Reader) (vnc.Encoding, error) {
	if int(rect.X)+int(rect.Width) > int(c.FrameBufferWidth) || int(rect.Y)+int(rect.Height) > int(c.FrameBufferHeight) {
		return nil, errors.New("VNC update rectangle is outside the screen")
	}
	if c.PixelFormat.BPP != 8 && c.PixelFormat.BPP != 16 && c.PixelFormat.BPP != 32 {
		return nil, fmt.Errorf("unsupported VNC pixel format: %d bits per pixel", c.PixelFormat.BPP)
	}
	_, err := io.CopyN(io.Discard, r, int64(rect.Width)*int64(rect.Height)*int64(c.PixelFormat.BPP/8))
	return &vnc.RawEncoding{}, err
}

type pointerTracking struct {
	stop context.CancelFunc
	done chan struct{}
}

func startPointerTracking(ctx context.Context, writer *vncWriter, messages <-chan vnc.ServerMessage, fps int, fail context.CancelCauseFunc) (*pointerTracking, error) {
	width, height := writer.size.get()
	if width == 0 || height == 0 {
		return nil, errors.New("VNC framebuffer dimensions must be nonzero")
	}
	if err := writer.client.SetEncodings([]vnc.Encoding{&discardRawEncoding{}, &desktopSizeEncoding{}}); err != nil {
		return nil, err
	}
	ctx, stop := context.WithCancel(ctx)
	s := &pointerTracking{stop: stop, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(time.Second / time.Duration(fps))
		defer ticker.Stop()
		pending := false
		request := func() bool {
			width, height := writer.size.get()
			if err := writer.request(true, int(width), int(height)); err != nil {
				if ctx.Err() == nil {
					fail(fmt.Errorf("track VNC framebuffer size: %w", err))
				}
				return false
			}
			pending = true
			return true
		}
		if !request() {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case message := <-messages:
				if _, ok := message.(*vnc.FramebufferUpdateMessage); ok {
					pending = false
				}
			case <-ticker.C:
				if !pending && !request() {
					return
				}
			}
		}
	}()
	return s, nil
}

func (s *pointerTracking) Close() {
	s.stop()
	<-s.done
}
