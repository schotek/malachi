// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"syscall"
	"time"
)

// Connection authentication (docs/api.md §1.4). At every start the daemon
// makes a random key and writes it beside its socket (KeyPath); every
// connection then proves, in both directions and with HMAC-SHA256 over
// two fresh nonces, that its ends hold that key before any other call.

const (
	// KeyFileSuffix names the key file: the socket path plus this suffix.
	KeyFileSuffix = ".key"

	// KeyFileSize is the exact size of a key file: the key as 64 lowercase
	// hex digits and "\n".
	KeyFileSize = 65

	// HandshakeTimeout bounds a client's whole handshake, both exchanges
	// together (ClientHandshake). The daemon allows 10 s from accept.
	HandshakeTimeout = 5 * time.Second

	// MaxHandshakeBytes is how much the daemon reads from a connection
	// before it is authenticated, all lines together; a connection that
	// sends more is closed.
	MaxHandshakeBytes = 4 << 10
)

// authLabel starts the message of every proof: it keeps these MACs apart
// from any other use of a key and names the construction's version.
const authLabel = "malachi-rpc-auth-v1"

// The roles of DaemonProof and ClientProof: a proof made for one side is
// never valid for the other, so a peer cannot reflect a proof it was sent.
const (
	roleDaemon = "daemon"
	roleClient = "client"
)

// redacted is what every printing and logging path shows for an AuthKey.
const redacted = "[redacted]"

// AuthKey is the daemon's connection key, new at every start. It exists
// in the key file and in the memory of the daemon and its clients, and
// nowhere else: String, GoString and Format print "[redacted]" for every
// verb, LogValue does the same for log/slog, MarshalJSON and MarshalText
// fail. fmt cannot call these methods for %p and %w (both invalid for a
// key) or for an unexported struct field, and then prints the bytes: keep
// a key out of structs that are printed whole.
type AuthKey [32]byte

// String returns "[redacted]".
func (AuthKey) String() string { return redacted }

// GoString returns "[redacted]".
func (AuthKey) GoString() string { return redacted }

// Format writes "[redacted]" whatever the verb and the flags.
func (AuthKey) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, redacted) }

// LogValue makes log/slog show "[redacted]".
func (AuthKey) LogValue() slog.Value { return slog.StringValue(redacted) }

// errKeyMarshal is returned by every attempt to encode an AuthKey.
var errKeyMarshal = errors.New("api.AuthKey is never encoded")

// MarshalJSON always fails: the key is never sent.
func (AuthKey) MarshalJSON() ([]byte, error) { return nil, errKeyMarshal }

// MarshalText always fails (encoding/json map keys, encoding/gob, slog
// handlers). FormatKeyFile is the only encoding of a key.
func (AuthKey) MarshalText() ([]byte, error) { return nil, errKeyMarshal }

// Equal reports whether k and o are the same key, in constant time.
func (k AuthKey) Equal(o AuthKey) bool { return hmac.Equal(k[:], o[:]) }

// AuthNonce is a handshake nonce: 32 random bytes, a fresh one from each
// side for every connection (NewAuthNonce). Nonces are public.
type AuthNonce [32]byte

// Hex returns the nonce as 64 lowercase hex digits, its form on the wire.
func (n AuthNonce) Hex() string { return hex.EncodeToString(n[:]) }

// AuthProof is one side's proof that it holds the key (DaemonProof,
// ClientProof).
type AuthProof [32]byte

// Hex returns the proof as 64 lowercase hex digits, its form on the wire.
func (p AuthProof) Hex() string { return hex.EncodeToString(p[:]) }

// Equal reports whether p and o are the same proof, in constant time. A
// received proof is checked only with Equal.
func (p AuthProof) Equal(o AuthProof) bool { return hmac.Equal(p[:], o[:]) }

// ErrKeyUnavailable is wrapped by every error of ReadKeyFile, together
// with the cause (errors.Is(err, fs.ErrNotExist) holds for a missing file).
var ErrKeyUnavailable = errors.New("rpc key unavailable")

// NewAuthKey returns a new random key.
func NewAuthKey() AuthKey {
	var k AuthKey
	fillRandom(k[:])
	return k
}

// NewAuthNonce returns a new random nonce.
func NewAuthNonce() AuthNonce {
	var n AuthNonce
	fillRandom(n[:])
	return n
}

func fillRandom(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
}

// KeyPath returns the path of the key file that belongs to the socket at
// socketPath: the same path with KeyFileSuffix appended, so the file lies
// beside the socket in its private directory.
func KeyPath(socketPath string) string { return socketPath + KeyFileSuffix }

// FormatKeyFile returns the content of a key file for k: exactly
// KeyFileSize bytes, 64 lowercase hex digits and "\n".
func FormatKeyFile(k AuthKey) []byte {
	b := make([]byte, KeyFileSize)
	hex.Encode(b, k[:])
	b[KeyFileSize-1] = '\n'
	return b
}

// ParseKeyFile decodes the content of a key file: exactly 64 lowercase hex
// digits and one "\n", nothing before, between or after. Its errors never
// quote the input.
func ParseKeyFile(b []byte) (AuthKey, error) {
	switch {
	case len(b) > KeyFileSize:
		return AuthKey{}, fmt.Errorf("key file is longer than %d bytes", KeyFileSize)
	case len(b) < KeyFileSize:
		return AuthKey{}, fmt.Errorf("key file is %d bytes, want %d", len(b), KeyFileSize)
	case b[KeyFileSize-1] != '\n':
		return AuthKey{}, errors.New("key file does not end in a newline")
	}
	k, ok := decodeLowerHex32(b[:KeyFileSize-1])
	if !ok {
		return AuthKey{}, errors.New("key file is not 64 lowercase hex digits")
	}
	return AuthKey(k), nil
}

// ReadKeyFile reads the key file at path. It accepts only a regular file
// (no symbolic link, directory, pipe or device), reads at most one byte
// more than KeyFileSize and parses it with ParseKeyFile. It never waits: a
// named pipe put in the file's place is refused, not opened for a writer.
// Every error wraps ErrKeyUnavailable and its cause.
func ReadKeyFile(path string) (AuthKey, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return AuthKey{}, fmt.Errorf("%w: %w", ErrKeyUnavailable, err)
	}
	if !fi.Mode().IsRegular() {
		return AuthKey{}, fmt.Errorf("%w: %s is not a regular file", ErrKeyUnavailable, path)
	}
	f, err := openKeyFile(path)
	if err != nil {
		return AuthKey{}, fmt.Errorf("%w: %w", ErrKeyUnavailable, err)
	}
	defer f.Close()
	// The path may have been replaced between Lstat and Open (a link to
	// another file): read only the file that was checked.
	opened, err := f.Stat()
	if err != nil {
		return AuthKey{}, fmt.Errorf("%w: %w", ErrKeyUnavailable, err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(fi, opened) {
		return AuthKey{}, fmt.Errorf("%w: %s changed while it was opened", ErrKeyUnavailable, path)
	}
	b, err := io.ReadAll(io.LimitReader(f, KeyFileSize+1))
	if err != nil {
		return AuthKey{}, fmt.Errorf("%w: read %s: %w", ErrKeyUnavailable, path, err)
	}
	k, err := ParseKeyFile(b)
	if err != nil {
		return AuthKey{}, fmt.Errorf("%w: %s: %w", ErrKeyUnavailable, path, err)
	}
	return k, nil
}

// openKeyFile opens the key file for ReadKeyFile. O_NONBLOCK: when the path
// has turned into a named pipe since Lstat, open(2) must not wait for a
// writer (forever, perhaps); the checks after the open refuse the pipe.
// Windows ignores the flag. A variable so that tests can change the path
// between Lstat and the open.
var openKeyFile = func(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

// ParseAuthHex decodes a nonce or a proof from the wire: exactly 64
// lowercase hex digits, nothing else (no prefix, no white space, no upper
// case).
func ParseAuthHex(s string) ([32]byte, bool) { return decodeLowerHex32(s) }

// DaemonProof is the daemon's proof on a connection: HMAC-SHA256 under k of
// the 91 bytes "malachi-rpc-auth-v1" 0x00 "daemon" 0x00 clientNonce
// daemonNonce, the nonces as raw bytes (docs/api.md §1.4).
func DaemonProof(k AuthKey, clientNonce, daemonNonce AuthNonce) AuthProof {
	return proof(k, roleDaemon, clientNonce, daemonNonce)
}

// ClientProof is the client's proof on a connection: as DaemonProof with
// the role "client".
func ClientProof(k AuthKey, clientNonce, daemonNonce AuthNonce) AuthProof {
	return proof(k, roleClient, clientNonce, daemonNonce)
}

func proof(k AuthKey, role string, clientNonce, daemonNonce AuthNonce) AuthProof {
	mac := hmac.New(sha256.New, k[:])
	mac.Write([]byte(authLabel))
	mac.Write([]byte{0})
	mac.Write([]byte(role))
	mac.Write([]byte{0})
	mac.Write(clientNonce[:])
	mac.Write(daemonNonce[:])
	var p AuthProof
	copy(p[:], mac.Sum(nil))
	return p
}

// decodeLowerHex32 decodes exactly 64 lowercase hex digits. It works on
// bytes, so a multi-byte character is never a digit.
func decodeLowerHex32[T ~string | ~[]byte](s T) ([32]byte, bool) {
	var out [32]byte
	if len(s) != 2*len(out) {
		return [32]byte{}, false
	}
	for i := range out {
		hi, ok1 := lowerHexDigit(s[2*i])
		lo, ok2 := lowerHexDigit(s[2*i+1])
		if !ok1 || !ok2 {
			return [32]byte{}, false
		}
		out[i] = hi<<4 | lo
	}
	return out, true
}

func lowerHexDigit(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}
