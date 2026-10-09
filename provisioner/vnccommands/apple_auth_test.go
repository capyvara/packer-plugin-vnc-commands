package vnccommands

import (
	"bytes"
	"context"
	"encoding/binary"
	"math/big"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/packer"
)

func TestAppleAuthenticationOnWire(t *testing.T) {
	for _, tc := range []struct {
		name, version, username, password string
		keyLength                         int
	}{
		{"RFB 3.8", "", "admin", "account-password", 16},
		{"Apple version", "RFB 003.889\n", "admin", "account-password", 16},
		// A 17-byte field guarantees a leading zero in the shared secret.
		{"padded shared secret", "", "admin", "account-password", 17},
		{"63-byte credentials", "", strings.Repeat("u", 63), strings.Repeat("é", 31) + "x", 16},
		{"empty password", "", "admin", "", 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := startRFB(t, rfbOptions{username: tc.username, password: tc.password, version: tc.version, appleKeyLength: tc.keyLength})
			p := configured(t, s, map[string]interface{}{"username": "{{.Username}}", "password": "{{.Password}}"})
			ui := new(packer.MockUi)
			if err := p.Provision(context.Background(), ui, nil, map[string]interface{}{"Username": tc.username, "Password": tc.password}); err != nil {
				t.Fatal(err)
			}
			s.awaitClosed(t)
			var got []keyEvent
			for len(s.events) > 0 {
				got = append(got, <-s.events)
			}
			want := []keyEvent{{'a', true}, {'a', false}, {0xff0d, true}, {0xff0d, false}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("events: got %v, want %v", got, want)
			}
			for _, status := range ui.SayMessages {
				if strings.Contains(status.Message, tc.username) || (tc.password != "" && strings.Contains(status.Message, tc.password)) {
					t.Fatal("status exposed Apple account credentials")
				}
			}
		})
	}
}

func TestAppleAuthenticationFailure(t *testing.T) {
	for _, tc := range []struct{ name, username, password string }{
		{"wrong username", "other-account", "account-password"},
		{"wrong password", "admin", "account-wrong"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := startRFB(t, rfbOptions{
				username: "admin", password: "account-password",
				failureReason: "rejected " + tc.username + " with " + tc.password,
			})
			p := configured(t, s, map[string]interface{}{"username": tc.username, "password": tc.password})
			err := p.Provision(context.Background(), new(packer.MockUi), nil, nil)
			if err == nil || !strings.Contains(err.Error(), "authentication") || strings.Contains(err.Error(), tc.username) || strings.Contains(err.Error(), tc.password) {
				t.Fatalf("unexpected authentication error: %v", err)
			}
			s.awaitClosed(t)
			if len(s.events) != 0 {
				t.Fatal("typed despite failed Apple authentication")
			}
		})
	}
}

func TestAppleAuthenticationDoesNotDowngrade(t *testing.T) {
	for _, methods := range [][]byte{{1}, {2}, {1, 2}} {
		s := startRFB(t, rfbOptions{securityTypes: methods})
		p := configured(t, s, map[string]interface{}{"username": "admin", "password": "account-password"})
		err := p.Provision(context.Background(), new(packer.MockUi), nil, nil)
		if err == nil || !strings.Contains(err.Error(), "no suitable auth schemes") {
			t.Fatalf("unexpected unsupported authentication error: %v", err)
		}
		s.awaitClosed(t)
		if len(s.events) != 0 {
			t.Fatal("typed without Apple authentication")
		}
	}
}

func TestAppleHandshakeTimeout(t *testing.T) {
	s := startRFB(t, rfbOptions{username: "admin", stallApple: true})
	p := configured(t, s, map[string]interface{}{"username": "admin", "password": "secret", "connect_timeout": "40ms"})
	start := time.Now()
	if err := p.Provision(context.Background(), new(packer.MockUi), nil, nil); err == nil || time.Since(start) > time.Second {
		t.Fatalf("Apple handshake was not bounded: %v", err)
	}
	s.awaitClosed(t)
}

type authTestConn struct {
	net.Conn
	input  *bytes.Reader
	output bytes.Buffer
}

func (c *authTestConn) Read(p []byte) (int, error)  { return c.input.Read(p) }
func (c *authTestConn) Write(p []byte) (int, error) { return c.output.Write(p) }

func TestAppleRejectsMalformedChallenges(t *testing.T) {
	prime, _ := new(big.Int).SetString("ffffffffffffffffffffffffffffff61", 16)
	for _, tc := range []struct {
		name      string
		keyLength int
		generator uint16
		prime     *big.Int
		peer      *big.Int
	}{
		{"zero length", 0, 2, prime, big.NewInt(128)},
		{"short length", 15, 2, prime, big.NewInt(128)},
		{"large length", 513, 2, prime, big.NewInt(128)},
		{"maximum length", 65535, 2, prime, big.NewInt(128)},
		{"zero generator", 16, 0, prime, big.NewInt(128)},
		{"unit generator", 16, 1, prime, big.NewInt(128)},
		{"zero modulus", 16, 2, big.NewInt(0), big.NewInt(128)},
		{"small modulus", 16, 2, big.NewInt(127), big.NewInt(4)},
		{"composite modulus", 16, 2, new(big.Int).Sub(prime, big.NewInt(1)), big.NewInt(128)},
		{"zero peer", 16, 2, prime, big.NewInt(0)},
		{"unit peer", 16, 2, prime, big.NewInt(1)},
		{"peer minus one", 16, 2, prime, new(big.Int).Sub(prime, big.NewInt(1))},
		{"peer equals modulus", 16, 2, prime, prime},
		{"peer exceeds modulus", 16, 2, prime, new(big.Int).Add(prime, big.NewInt(1))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var challenge bytes.Buffer
			binary.Write(&challenge, binary.BigEndian, tc.generator)
			binary.Write(&challenge, binary.BigEndian, uint16(tc.keyLength))
			if tc.keyLength == 16 {
				challenge.Write(tc.prime.FillBytes(make([]byte, 16)))
				challenge.Write(tc.peer.FillBytes(make([]byte, 16)))
			}
			conn := &authTestConn{input: bytes.NewReader(challenge.Bytes())}
			err := (&appleAuth{username: "admin", password: "secret"}).Handshake(conn)
			if err == nil || !strings.Contains(err.Error(), "Apple authentication") || conn.output.Len() != 0 {
				t.Fatalf("malformed challenge accepted or credentials sent: %v", err)
			}
		})
	}
	for _, challenge := range [][]byte{nil, {0, 2}, {0, 2, 0, 16}, {0, 2, 0, 16, 1, 2}} {
		conn := &authTestConn{input: bytes.NewReader(challenge)}
		if err := (&appleAuth{username: "admin", password: "secret"}).Handshake(conn); err == nil || conn.output.Len() != 0 {
			t.Fatalf("truncated challenge accepted or credentials sent: %v", err)
		}
	}
}
