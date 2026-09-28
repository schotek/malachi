// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/pkg/api"
)

// zstdFrame compresses data the way the store does.
func zstdFrame(t testing.TB, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := writeZstdFrame(&buf, int64(len(data)), chunked(data)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestZstdEmptyFrame(t *testing.T) {
	enc, err := zstd.NewWriter(nil, zstdEncoderOptions...)
	if err != nil {
		t.Fatal(err)
	}
	if got := enc.EncodeAll(nil, nil); !bytes.Equal(got, zstdEmptyFrame) {
		t.Errorf("EncodeAll(nil) = % x, want % x", got, zstdEmptyFrame)
	}
	var h zstd.Header
	if err := h.Decode(zstdEmptyFrame); err != nil || !h.HasFCS || h.FrameContentSize != 0 {
		t.Errorf("header %+v %v", h, err)
	}
	if err := verifyZstd(bytes.NewReader(zstdEmptyFrame), int64(len(zstdEmptyFrame)), 0); err != nil {
		t.Errorf("verify: %v", err)
	}
	// An empty message written by the store is that frame.
	if got := zstdFrame(t, nil); !bytes.Equal(got, zstdEmptyFrame) {
		t.Errorf("empty message = % x", got)
	}
}

// base64Attachment is n random bytes as a MIME part body: base64 in lines
// of 76 characters.
func base64Attachment(n int, seed int64) []byte {
	raw := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(raw)
	enc := base64.StdEncoding.EncodeToString(raw)
	var b bytes.Buffer
	for len(enc) > 0 {
		k := min(76, len(enc))
		b.WriteString(enc[:k])
		b.WriteString("\r\n")
		enc = enc[k:]
	}
	return b.Bytes()
}

// The encoder must entropy-code literal-only blocks: a base64 attachment of
// random bytes has no repeats worth a match, and without the option it is
// stored as it came.
func TestZstdEntropyCodesBase64(t *testing.T) {
	data := base64Attachment(768<<10, 1)
	ratio := float64(len(zstdFrame(t, data))) / float64(len(data))
	if ratio >= 0.8 {
		t.Errorf("base64 compressed to %.3f of its size, want below 0.8", ratio)
	}
	// What the test guards against: the library's default at this level.
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault), zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	if without := float64(len(enc.EncodeAll(data, nil))) / float64(len(data)); without < 0.95 {
		t.Errorf("without the option %.3f: the test data no longer shows the difference", without)
	}
	t.Logf("base64: %.3f with entropy-coded literals", ratio)
}

func TestMaxRawBytesCoversWriters(t *testing.T) {
	if MaxRawBytes < api.MaxOutgoingMessageBytes {
		t.Errorf("MaxRawBytes %d below api.MaxOutgoingMessageBytes %d", MaxRawBytes, api.MaxOutgoingMessageBytes)
	}
	if MaxRawBytes < mime.MaxInputBytes {
		t.Errorf("MaxRawBytes %d below mime.MaxInputBytes %d", MaxRawBytes, mime.MaxInputBytes)
	}
	if maxZstdWindow > MaxRawBytes {
		t.Errorf("window cap %d above the content cap", maxZstdWindow)
	}
}

// frameHeader builds a frame header by hand: descriptor fhd, then the
// window byte (unless single segment), then the content size field.
func frameHeader(fhd byte, window []byte, fcs []byte) []byte {
	out := []byte{0x28, 0xb5, 0x2f, 0xfd, fhd}
	out = append(out, window...)
	return append(out, fcs...)
}

func le32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

func TestZstdDamage(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	big := rawContent(300<<10, 5)
	bigFrame := zstdFrame(t, big)
	small := []byte("Subject: small\r\n\r\nbody")
	smallFrame := zstdFrame(t, small)
	// The streamed frame's content size sits after magic, descriptor and
	// window byte, 4 bytes wide.
	withFCS := func(frame []byte, fcs uint32) []byte {
		out := slices.Clone(frame)
		binary.LittleEndian.PutUint32(out[6:10], fcs)
		return out
	}
	var zeros bytes.Buffer
	enc, err := zstd.NewWriter(&zeros, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for i := 0; i < 65; i++ {
		enc.Write(chunk)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	skippable := append([]byte{0x50, 0x2a, 0x4d, 0x18}, le32(4)...)
	skippable = append(skippable, "abcd"...)
	flipped := slices.Clone(bigFrame)
	flipped[len(flipped)/2] ^= 0x40
	flippedCRC := slices.Clone(smallFrame)
	flippedCRC[len(flippedCRC)-1] ^= 0x01
	cases := map[string][]byte{
		"truncated":            bigFrame[:len(bigFrame)-10],
		"header only":          bigFrame[:10],
		"flipped byte":         flipped,
		"flipped checksum":     flippedCRC,
		"empty":                {},
		"bad magic":            append([]byte{0x29}, bigFrame[1:]...),
		"huge window":          append(frameHeader(0x00, []byte{20 << 3}, nil), 0x01, 0x00, 0x00),
		"size over the cap":    append(frameHeader(0x80, []byte{13 << 3}, le32(100<<20)), 0x01, 0x00, 0x00),
		"large single segment": append(frameHeader(0xa0, nil, le32(32<<20)), 0x01, 0x00, 0x00),
		"dictionary":           append(frameHeader(0x21, nil, []byte{7, 0}), 0x01, 0x00, 0x00),
		"reserved bit":         append(frameHeader(0x28, nil, []byte{0}), 0x01, 0x00, 0x00),
		"size too small":       withFCS(bigFrame, uint32(len(big)-1)),
		"size too large":       withFCS(bigFrame, uint32(len(big)+1)),
		"trailing garbage":     append(slices.Clone(bigFrame), "garbage"...),
		"two frames":           append(slices.Clone(smallFrame), smallFrame...),
		"skippable only":       skippable,
		"65 MiB unsized":       zeros.Bytes(),
	}
	dir := filepath.Join(s.MessageDir(), "acc")
	os.MkdirAll(dir, 0o700)
	for name, data := range cases {
		id := "m_" + strings.ReplaceAll(name, " ", "_")
		if err := os.WriteFile(filepath.Join(dir, id+RawZstSuffix), data, 0o600); err != nil {
			t.Fatal(err)
		}
		r, err := s.OpenMessageRaw(ctx, "acc", id)
		if err != nil {
			if !errors.Is(err, ErrRawCorrupt) {
				t.Errorf("%s: open: %v", name, err)
			}
			continue
		}
		got, err := io.ReadAll(r)
		if !errors.Is(err, ErrRawCorrupt) {
			t.Errorf("%s: read %d bytes, %v", name, len(got), err)
		}
		if len(got) > MaxRawBytes {
			t.Errorf("%s: %d bytes returned", name, len(got))
		}
		// The error sticks.
		if _, err := r.Read(make([]byte, 16)); !errors.Is(err, ErrRawCorrupt) {
			t.Errorf("%s: read after the damage: %v", name, err)
		}
		if name == "65 MiB unsized" {
			if _, err := r.Size(); !errors.Is(err, ErrRawCorrupt) {
				t.Errorf("%s: size: %v", name, err)
			}
		}
		r.Close()
		// A pass trying to convert it keeps it as it is.
		if err := verifyZstd(bytes.NewReader(data), int64(len(data)), int64(len(big))); err == nil {
			t.Errorf("%s: verified", name)
		}
	}
	// A frame of another writer that records no size is still a message.
	var unsized bytes.Buffer
	enc.Reset(&unsized)
	enc.Write(big)
	enc.Close()
	os.WriteFile(filepath.Join(dir, "m_unsized"+RawZstSuffix), unsized.Bytes(), 0o600)
	if got := readRaw(t, s, "acc", "m_unsized"); !bytes.Equal(got, big) {
		t.Errorf("unsized frame: %d bytes", len(got))
	}
}

func FuzzOpenMessageRawZst(f *testing.F) {
	s := openTestStore(f)
	for _, n := range []int{0, 1, 255, 300, 130 << 10} {
		f.Add(zstdFrame(f, rawContent(n, int64(n))))
	}
	frame := zstdFrame(f, rawContent(200<<10, 9))
	f.Add(frame[:len(frame)/2])
	f.Add(append(slices.Clone(frame), frame...))
	f.Add([]byte{})
	f.Add([]byte("Subject: plain\r\n\r\nbody"))
	f.Add(append([]byte{0x50, 0x2a, 0x4d, 0x18}, le32(0)...))
	dir := filepath.Join(s.MessageDir(), "acc")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		f.Fatal(err)
	}
	path := filepath.Join(dir, "m_fuzz"+RawZstSuffix)
	f.Fuzz(func(t *testing.T, data []byte) {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		r, err := s.OpenMessageRaw(context.Background(), "acc", "m_fuzz")
		if err != nil {
			if !errors.Is(err, ErrRawCorrupt) {
				t.Fatalf("open: %v", err)
			}
			return
		}
		defer r.Close()
		got, err := io.ReadAll(r)
		if len(got) > MaxRawBytes {
			t.Fatalf("%d bytes returned", len(got))
		}
		if err != nil {
			if !errors.Is(err, ErrRawCorrupt) {
				t.Fatalf("read: %v", err)
			}
			return
		}
		if n, err := r.Size(); err != nil || n != int64(len(got)) {
			t.Fatalf("size %d %v, read %d", n, err, len(got))
		}
		if err := r.Rewind(); err != nil {
			t.Fatal(err)
		}
		if again, err := io.ReadAll(r); err != nil || !bytes.Equal(again, got) {
			t.Fatalf("second pass: %d bytes, %v", len(again), err)
		}
	})
}
