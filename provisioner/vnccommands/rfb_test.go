package vnccommands

import (
	"bytes"
	"crypto/des"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net"
	"strconv"
	"syscall"
	"testing"
	"time"
)

type keyEvent struct {
	key  uint32
	down bool
}

type rfbOptions struct {
	password        string
	stall           bool
	version         string
	disconnectAfter int
}

type rfbServer struct {
	host   string
	port   int
	events chan keyEvent
	ready  chan struct{}
	done   chan error
}

// A deliberately small RFB 3.8 server exercises the wire protocol rather
// than mocking the SDK driver or VNC client. All socket operations are bounded.
func startRFB(t *testing.T, options rfbOptions) *rfbServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	p, _ := strconv.Atoi(port)
	s := &rfbServer{host: host, port: p, events: make(chan keyEvent, 1024), ready: make(chan struct{}), done: make(chan error, 1)}
	t.Cleanup(func() { listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			s.done <- err
			return
		}
		defer conn.Close()
		defer listener.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		err = s.serve(conn, options)
		s.done <- err
	}()
	return s
}

func (s *rfbServer) serve(c net.Conn, options rfbOptions) error {
	if options.stall {
		close(s.ready)
		_, err := io.Copy(io.Discard, c)
		return err
	}
	version := options.version
	if version == "" {
		version = "RFB 003.008\n"
	}
	if _, err := io.WriteString(c, version); err != nil {
		return err
	}
	buf := make([]byte, 12)
	if _, err := io.ReadFull(c, buf); err != nil {
		if options.version != "" {
			return nil
		}
		return err
	}
	if string(buf) != "RFB 003.008\n" {
		return fmt.Errorf("unexpected client version: %q", buf)
	}
	// Offer both methods when using a password: the client must not downgrade.
	methods := []byte{1, 1}
	if options.password != "" {
		methods = []byte{2, 1, 2}
	}
	if _, err := c.Write(methods); err != nil {
		return err
	}
	selection := []byte{0}
	if _, err := io.ReadFull(c, selection); err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}
	expected := byte(1)
	if options.password != "" {
		expected = 2
	}
	if selection[0] != expected {
		return fmt.Errorf("authentication downgraded: got %d, want %d", selection[0], expected)
	}
	if expected == 2 {
		challenge := []byte("0123456789abcdef")
		if _, err := c.Write(challenge); err != nil {
			return err
		}
		response := make([]byte, 16)
		if _, err := io.ReadFull(c, response); err != nil {
			return err
		}
		var key [8]byte
		copy(key[:], options.password)
		for i := range key {
			key[i] = bits.Reverse8(key[i])
		}
		cipher, _ := des.NewCipher(key[:])
		want := make([]byte, 16)
		cipher.Encrypt(want[:8], challenge[:8])
		cipher.Encrypt(want[8:], challenge[8:])
		if !bytes.Equal(response, want) {
			binary.Write(c, binary.BigEndian, uint32(1))
			reason := "incorrect VNC password"
			binary.Write(c, binary.BigEndian, uint32(len(reason)))
			io.WriteString(c, reason)
			_, err := io.Copy(io.Discard, c)
			return err
		}
	}
	if err := binary.Write(c, binary.BigEndian, uint32(0)); err != nil {
		return err
	}
	if _, err := io.ReadFull(c, selection); err != nil {
		return err
	}
	if selection[0] != 1 {
		return fmt.Errorf("connection must be shared")
	}
	// 1x1 framebuffer, 32bpp true color, name "test".
	init := []byte{0, 1, 0, 1, 32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0, 0, 0, 0, 4, 't', 'e', 's', 't'}
	if _, err := c.Write(init); err != nil {
		return err
	}
	close(s.ready)
	count := 0
	for {
		packet := make([]byte, 8)
		if _, err := io.ReadFull(c, packet); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if packet[0] != 4 {
			return fmt.Errorf("unexpected client message: %d", packet[0])
		}
		s.events <- keyEvent{binary.BigEndian.Uint32(packet[4:]), packet[1] == 1}
		count++
		if options.disconnectAfter > 0 && count == options.disconnectAfter {
			return nil
		}
	}
}

func (s *rfbServer) awaitClosed(t *testing.T) {
	t.Helper()
	select {
	case err := <-s.done:
		if err != nil && !errors.Is(err, syscall.ECONNRESET) && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("RFB server: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not close its VNC connection")
	}
}
