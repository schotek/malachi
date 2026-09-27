// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// The zstd side of the raw message files (raw.go): how a frame is written
// and checked, and the reader that decompresses a stored message.
//
// A compressed message is <id>.zst, exactly one zstd frame that records
// the size of its content and, unless the message is empty, a checksum of
// it. Only the name says that a file is compressed: mail is hostile input
// and may begin with the zstd magic number on purpose, so a plain file is
// never sniffed, and a .zst file that is not such a frame is ErrRawCorrupt,
// never read as the message itself.

// zstdWindow is the encoder's window, the history a frame's matches may
// reach back into: 4 MiB instead of the level's 8 MiB halves what a pooled
// encoder keeps (about 10 MB instead of 19 MB after a large message), and
// what a decoder needs, at no cost in speed or size on the benchmark
// corpus (BenchmarkZstdOptions, which runs on a real store's messages
// too): only messages over 4 MiB can lose a match, and those are mostly a
// large attachment that repeats nothing so far back.
const zstdWindow = 4 << 20

// maxZstdWindow bounds the history a stored frame may make the decoder
// keep: four times the encoder's window, so no frame this store wrote is
// ever refused (nor one written with the level's default), while a damaged
// header cannot ask for gigabytes. A single-segment frame's window is its
// content size, bounded alike.
const maxZstdWindow = 16 << 20

// zstdEncoderOptions: SpeedDefault (zstd level 3) with entropy coding of
// literal-only blocks, which the library leaves out at this level. Base64
// attachments are such blocks (64 symbols, hardly a repeat worth a match),
// so without it they are stored as they came: on a real store the files
// shrank to 72 % instead of 60 % (TestZstdEntropyCodesBase64 guards the
// option). Concurrency 1 keeps an encoder synchronous, with no goroutines
// of its own, so a pooled one needs no Close. The CRC is the content
// checksum the decoder verifies. A single-segment frame, which the encoder
// writes for content under one block (128 KiB), always records its size;
// a larger one records it because every write passes the size it knows
// (ResetContentSize). The empty message is zstdEmptyFrame, the frame the
// encoder's zero-frames option describes. The lower-memory mode is left
// off: it slides the whole window along every block, and large messages
// compressed a quarter to a third slower (BenchmarkZstdOptions).
var zstdEncoderOptions = []zstd.EOption{
	zstd.WithEncoderLevel(zstd.SpeedDefault),
	zstd.WithWindowSize(zstdWindow),
	zstd.WithAllLitEntropyCompression(true),
	zstd.WithEncoderConcurrency(1),
	zstd.WithEncoderCRC(true),
	zstd.WithZeroFrames(true),
	zstd.WithSingleSegment(true),
}

// zstdDecoderOptions: synchronous (no goroutines, see above), the smaller
// buffers of the low-memory mode, and the window and size bounds of a
// frame this store can have written.
var zstdDecoderOptions = []zstd.DOption{
	zstd.WithDecoderConcurrency(1),
	zstd.WithDecoderLowmem(true),
	zstd.WithDecoderMaxWindow(maxZstdWindow),
	zstd.WithDecoderMaxMemory(MaxRawBytes),
}

// zstdEmptyFrame is the frame of an empty message: magic number, a
// single-segment descriptor with the content size 0, one empty last raw
// block and no checksum. It is what the encoder's EncodeAll writes for no
// input under WithZeroFrames (TestZstdEmptyFrame pins that); a stream
// written through the encoder would record no size for it.
var zstdEmptyFrame = []byte{0x28, 0xb5, 0x2f, 0xfd, 0x20, 0x00, 0x01, 0x00, 0x00}

// Encoders and decoders are pooled: each holds tables and buffers of
// several MiB that are worth reusing, and with concurrency 1 a dropped
// one leaves no goroutine behind. Reset(nil) lets go of the last stream
// before one goes back.
var (
	zstdEncoders = sync.Pool{New: func() any {
		enc, err := zstd.NewWriter(nil, zstdEncoderOptions...)
		if err != nil {
			panic("store: zstd encoder options: " + err.Error()) // constant options
		}
		return enc
	}}
	zstdDecoders = sync.Pool{New: func() any {
		dec, err := zstd.NewReader(nil, zstdDecoderOptions...)
		if err != nil {
			panic("store: zstd decoder options: " + err.Error()) // constant options
		}
		return dec
	}}
)

func getEncoder() *zstd.Encoder { return zstdEncoders.Get().(*zstd.Encoder) }

func putEncoder(enc *zstd.Encoder) {
	enc.Reset(nil)
	zstdEncoders.Put(enc)
}

func getDecoder() *zstd.Decoder { return zstdDecoders.Get().(*zstd.Decoder) }

func putDecoder(dec *zstd.Decoder) {
	_ = dec.Reset(nil)
	zstdDecoders.Put(dec)
}

// errSizeMismatch: a producer wrote another number of bytes than the size
// its write declared.
var errSizeMismatch = errors.New("store: message length differs from its declared size")

// corrupt wraps a decoding failure as ErrRawCorrupt. The cause is kept as
// text only: a caller such as the MIME parser must not see an
// io.ErrUnexpectedEOF from a damaged file and take it for a message that
// merely ends early.
func corrupt(what string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", ErrRawCorrupt, what)
	}
	return fmt.Errorf("%w: %s: %v", ErrRawCorrupt, what, cause)
}

// zstdFrameSize reads the frame header at the start of r and checks that
// it is one this store could have written: a real frame, not a skippable
// one, with no dictionary, a window within maxZstdWindow and a recorded
// content size within MaxRawBytes. It returns that size, or -1 when the
// frame records none (a frame this store did not write; it is still read,
// up to MaxRawBytes).
func zstdFrameSize(r io.ReaderAt) (int64, error) {
	var buf [zstd.HeaderMaxSize]byte
	n, err := r.ReadAt(buf[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, fmt.Errorf("read compressed message: %w", err)
	}
	var h zstd.Header
	switch err := h.Decode(buf[:n]); {
	case err != nil:
		return 0, corrupt("frame header", err)
	case h.Skippable:
		return 0, corrupt("skippable frame instead of a message", nil)
	case h.DictionaryID != 0:
		return 0, corrupt("frame needs a dictionary", nil)
	case h.SingleSegment && h.FrameContentSize > maxZstdWindow:
		return 0, corrupt("single segment too large", nil)
	case !h.SingleSegment && h.WindowSize > maxZstdWindow:
		return 0, corrupt("window too large", nil)
	case h.HasFCS && h.FrameContentSize > MaxRawBytes:
		return 0, corrupt("content size over the cap", nil)
	case !h.HasFCS:
		return -1, nil
	}
	return int64(h.FrameContentSize), nil
}

// writeZstdFrame compresses what fn writes into w as one frame that
// records size, which is exactly what fn must write: more or less fails
// with errSizeMismatch, and nothing past size is taken. It returns the
// bytes fn wrote. On error w holds garbage; the caller removes its file.
func writeZstdFrame(w io.Writer, size int64, fn func(io.Writer) error) (int64, error) {
	if size == 0 {
		lw := &limitedWriter{w: io.Discard, limit: 0, over: errSizeMismatch}
		if err := lw.result(fn(lw), 0); err != nil {
			return lw.n, err
		}
		_, err := w.Write(zstdEmptyFrame)
		return 0, err
	}
	enc := getEncoder()
	defer putEncoder(enc)
	enc.ResetContentSize(w, size)
	lw := &limitedWriter{w: enc, limit: size, over: errSizeMismatch}
	if err := lw.result(fn(lw), size); err != nil {
		return lw.n, err
	}
	if err := enc.Close(); err != nil {
		return lw.n, err
	}
	return lw.n, nil
}

// verifyZstd decodes the whole file (diskSize bytes of r) and checks that
// it is one intact frame recording want bytes and holding exactly them:
// the check before a new .zst file is renamed into place.
func verifyZstd(r io.ReaderAt, diskSize, want int64) error {
	size, err := zstdFrameSize(r)
	switch {
	case err != nil:
		return err
	case size != want:
		return corrupt(fmt.Sprintf("frame records %d bytes, %d written", size, want), nil)
	}
	n, err := decodeCount(r, diskSize, want)
	if err != nil {
		return err
	}
	if n != want {
		return corrupt(fmt.Sprintf("frame holds %d bytes, %d written", n, want), nil)
	}
	return nil
}

// decodeCount decodes the frame in the first diskSize bytes of r without
// keeping the content, verifying its checksum, and returns its length. It
// stops with ErrRawCorrupt past max bytes.
func decodeCount(r io.ReaderAt, diskSize, max int64) (int64, error) {
	dec := getDecoder()
	defer putDecoder(dec)
	if err := dec.Reset(io.NewSectionReader(r, 0, diskSize)); err != nil {
		return 0, corrupt("decoder", err)
	}
	n, err := io.Copy(io.Discard, io.LimitReader(dec, max+1))
	switch {
	case err != nil:
		return n, corrupt("frame", err)
	case n > max:
		return n, corrupt(fmt.Sprintf("more than %d bytes", max), nil)
	}
	return n, nil
}

// zstdRaw is a stored compressed message being read (RawMessage). It never
// returns more than the frame records, nor more than MaxRawBytes, and a
// frame that ends early, carries a bad checksum or runs on past its size
// fails the read with ErrRawCorrupt instead of yielding a shorter or
// longer message.
type zstdRaw struct {
	f       *os.File
	dec     *zstd.Decoder // nil once closed
	size    int64         // content size the frame records; -1 if none
	counted int64         // size found by decoding, when the frame records none; -1 until then
	limit   int64         // most Read may return: size, else MaxRawBytes
	n       int64         // returned since the last Rewind
	err     error         // sticky end of the current pass
}

// openZstdRaw takes over f, a .zst file opened for reading, and checks its
// frame header; on error f is closed.
func openZstdRaw(f *os.File) (*zstdRaw, error) {
	size, err := zstdFrameSize(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	z := &zstdRaw{f: f, size: size, counted: -1, limit: MaxRawBytes}
	if size >= 0 {
		z.limit = size
	}
	z.dec = getDecoder()
	if err := z.dec.Reset(f); err != nil {
		z.Close()
		return nil, corrupt("decoder", err)
	}
	return z, nil
}

func (z *zstdRaw) Read(p []byte) (int, error) {
	if z.dec == nil {
		return 0, os.ErrClosed
	}
	if z.err != nil {
		return 0, z.err
	}
	// Ask for at most one byte past what the message may hold, which tells
	// its end from more data without returning the extra byte.
	if room := z.limit - z.n + 1; int64(len(p)) > room {
		p = p[:room]
	}
	n, err := z.dec.Read(p)
	if z.n+int64(n) > z.limit {
		n = int(z.limit - z.n)
		z.n = z.limit
		z.err = corrupt(fmt.Sprintf("content runs past %d bytes", z.limit), nil)
		return n, z.err
	}
	z.n += int64(n)
	switch {
	case err == io.EOF:
		if z.size >= 0 && z.n != z.size {
			z.err = corrupt(fmt.Sprintf("frame ends after %d of %d bytes", z.n, z.size), nil)
			return n, z.err
		}
		z.err = io.EOF
		return n, io.EOF
	case err != nil:
		z.err = corrupt("frame", err)
		return n, z.err
	}
	return n, nil
}

// Size is the size the frame records; a frame that records none (not one
// of this store's) is decoded once to count it.
func (z *zstdRaw) Size() (int64, error) {
	switch {
	case z.size >= 0:
		return z.size, nil
	case z.counted >= 0:
		return z.counted, nil
	case z.dec == nil:
		return 0, os.ErrClosed
	}
	info, err := z.f.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat compressed message: %w", err)
	}
	n, err := decodeCount(z.f, info.Size(), MaxRawBytes)
	if err != nil {
		return 0, err
	}
	z.counted = n
	return n, nil
}

func (z *zstdRaw) Rewind() error {
	if z.dec == nil {
		return os.ErrClosed
	}
	if _, err := z.f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind compressed message: %w", err)
	}
	z.n, z.err = 0, nil
	if err := z.dec.Reset(z.f); err != nil {
		return corrupt("decoder", err)
	}
	return nil
}

// Close returns the decoder to its pool; closing twice is harmless.
func (z *zstdRaw) Close() error {
	if z.dec == nil {
		return nil
	}
	putDecoder(z.dec)
	z.dec = nil
	return z.f.Close()
}
