package vnccommands

import (
	"bytes"
	"crypto/aes"
	"crypto/des"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
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
	username        string
	password        string
	securityTypes   []byte
	appleKeyLength  int
	stallApple      bool
	failureReason   string
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
	if options.username != "" {
		// Offer weaker methods first to detect accidental fallback.
		methods = []byte{3, 1, 2, 30}
	}
	if options.securityTypes != nil {
		methods = append([]byte{byte(len(options.securityTypes))}, options.securityTypes...)
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
	if options.username != "" {
		expected = 30
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
	if expected == 30 {
		if options.stallApple {
			close(s.ready)
			_, err := io.Copy(io.Discard, c)
			return err
		}
		ok, err := serveAppleAuthentication(c, options)
		if err != nil {
			return err
		}
		if !ok {
			reason := options.failureReason
			if reason == "" {
				reason = "incorrect Apple account credentials"
			}
			binary.Write(c, binary.BigEndian, uint32(1))
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

// Independently act as the Apple server: receive ciphertext followed by the
// client's DH public key, derive the secret, and decrypt the two NUL-terminated
// 64-byte fields. No client encryption helpers are reused.
func serveAppleAuthentication(c net.Conn, options rfbOptions) (bool, error) {
	keyLength := options.appleKeyLength
	if keyLength == 0 {
		keyLength = 16
	}
	// 2^128 - 159 is prime. A fixed server exponent is sufficient for tests.
	prime, _ := new(big.Int).SetString("ffffffffffffffffffffffffffffff61", 16)
	private := big.NewInt(7)
	public := new(big.Int).Exp(big.NewInt(2), private, prime)
	challenge := make([]byte, 4+2*keyLength)
	binary.BigEndian.PutUint16(challenge[:2], 2)
	binary.BigEndian.PutUint16(challenge[2:4], uint16(keyLength))
	prime.FillBytes(challenge[4 : 4+keyLength])
	public.FillBytes(challenge[4+keyLength:])
	if _, err := c.Write(challenge); err != nil {
		return false, err
	}
	response := make([]byte, 128+keyLength)
	if _, err := io.ReadFull(c, response); err != nil {
		return false, err
	}
	peer := new(big.Int).SetBytes(response[128:])
	if peer.Cmp(big.NewInt(1)) <= 0 || peer.Cmp(prime) >= 0 {
		return false, fmt.Errorf("invalid client DH public key")
	}
	shared := new(big.Int).Exp(peer, private, prime)
	key := md5.Sum(shared.FillBytes(make([]byte, keyLength)))
	cipher, err := aes.NewCipher(key[:])
	if err != nil {
		return false, err
	}
	var plaintext [128]byte
	for offset := 0; offset < len(plaintext); offset += aes.BlockSize {
		cipher.Decrypt(plaintext[offset:offset+aes.BlockSize], response[offset:offset+aes.BlockSize])
	}
	usernameEnd := bytes.IndexByte(plaintext[:64], 0)
	passwordEnd := bytes.IndexByte(plaintext[64:], 0)
	if usernameEnd < 0 || passwordEnd < 0 {
		return false, fmt.Errorf("Apple credentials are not NUL-terminated")
	}
	return string(plaintext[:usernameEnd]) == options.username && string(plaintext[64:64+passwordEnd]) == options.password, nil
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
