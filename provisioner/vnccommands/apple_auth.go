package vnccommands

import (
	"crypto/aes"
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"

	vnc "github.com/mitchellh/go-vnc"
)

// Apple Remote Desktop (security type 30) authenticates a macOS account using
// Diffie-Hellman, MD5 of the fixed-width shared secret, and AES-128-ECB.
// These legacy algorithms are required by the wire protocol. They encrypt
// credentials only; subsequent RFB traffic is not encrypted.
type appleAuth struct {
	username string
	password string
}

var _ vnc.ClientAuth = (*appleAuth)(nil)

func (*appleAuth) SecurityType() uint8 { return 30 }

func validateAppleCredentials(username, password string) error {
	if strings.TrimSpace(username) == "" {
		return errors.New("Apple authentication requires a nonempty username")
	}
	for _, field := range []struct{ name, value string }{{"username", username}, {"password", password}} {
		// Each field occupies 64 bytes, including its terminating NUL. Reject
		// oversized strings rather than silently authenticating truncated ones.
		if len(field.value) > 63 || strings.ContainsRune(field.value, '\x00') {
			return fmt.Errorf("Apple authentication %s must be at most 63 bytes and contain no NUL", field.name)
		}
	}
	return nil
}

func (a *appleAuth) Handshake(conn net.Conn) error {
	if err := validateAppleCredentials(a.username, a.password); err != nil {
		return err
	}
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return fmt.Errorf("read Apple authentication header: %w", err)
	}
	generator := binary.BigEndian.Uint16(header[:2])
	keyLength := int(binary.BigEndian.Uint16(header[2:]))
	// macOS commonly uses 16-byte DH keys. Bound allocation and arithmetic
	// before reading server-controlled parameters (up to 4096 bits).
	if keyLength < 16 || keyLength > 512 {
		return errors.New("Apple authentication DH key length must be between 16 and 512 bytes")
	}
	parameters := make([]byte, 2*keyLength)
	if _, err := io.ReadFull(conn, parameters); err != nil {
		return fmt.Errorf("read Apple authentication DH parameters: %w", err)
	}
	prime := new(big.Int).SetBytes(parameters[:keyLength])
	peer := new(big.Int).SetBytes(parameters[keyLength:])
	g := new(big.Int).SetUint64(uint64(generator))
	one := big.NewInt(1)
	upper := new(big.Int).Sub(prime, one)
	if prime.BitLen() < 128 || !prime.ProbablyPrime(20) ||
		g.Cmp(one) <= 0 || g.Cmp(upper) >= 0 || peer.Cmp(one) <= 0 || peer.Cmp(upper) >= 0 {
		return errors.New("invalid Apple authentication DH parameters")
	}
	// Uniform private exponent in [2, prime-2], using the OS random source.
	private, err := rand.Int(rand.Reader, new(big.Int).Sub(prime, big.NewInt(3)))
	if err != nil {
		return fmt.Errorf("generate Apple authentication private key: %w", err)
	}
	private.Add(private, big.NewInt(2))
	public := new(big.Int).Exp(g, private, prime)
	shared := new(big.Int).Exp(peer, private, prime)
	if public.Cmp(one) <= 0 || shared.Cmp(one) <= 0 {
		return errors.New("invalid Apple authentication DH shared key")
	}
	// Leading zero bytes are part of the hash input, even when big.Int would
	// otherwise omit them.
	key := md5.Sum(shared.FillBytes(make([]byte, keyLength)))
	cipher, err := aes.NewCipher(key[:])
	if err != nil {
		return fmt.Errorf("create Apple authentication cipher: %w", err)
	}
	var credentials [128]byte
	if _, err := rand.Read(credentials[:]); err != nil {
		return fmt.Errorf("pad Apple authentication credentials: %w", err)
	}
	copy(credentials[:64], a.username)
	credentials[len(a.username)] = 0
	copy(credentials[64:], a.password)
	credentials[64+len(a.password)] = 0
	response := make([]byte, len(credentials)+keyLength)
	for offset := 0; offset < len(credentials); offset += aes.BlockSize {
		cipher.Encrypt(response[offset:offset+aes.BlockSize], credentials[offset:offset+aes.BlockSize])
	}
	clear(credentials[:])
	public.FillBytes(response[len(credentials):])
	if n, err := conn.Write(response); err != nil {
		return fmt.Errorf("write Apple authentication response: %w", err)
	} else if n != len(response) {
		return io.ErrShortWrite
	}
	// go-vnc reads SecurityResult and performs the remaining RFB handshake.
	return nil
}
