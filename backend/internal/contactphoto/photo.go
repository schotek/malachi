// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package contactphoto turns a contact's picture as a server sends it into
// the small image the daemon keeps and gives clients
// (docs/plans/address-book.md §9, F5, F6). The input is a third party's
// image data, hostile (CLAUDE.md rule 3): its dimensions are read and
// checked before anything is decoded, only the standard library's JPEG,
// PNG and GIF decoders run (no cgo), and the result is encoded anew, so
// nothing of the original bytes (EXIF, GPS, colour profiles, comments,
// further GIF frames) leaves the daemon.
package contactphoto

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"

	"golang.org/x/image/draw"
)

// Limits (F5).
const (
	// MaxInputBytes: a larger input is refused unread.
	MaxInputBytes = 4 << 20
	// MaxPixels: a picture of more pixels is refused before decoding.
	MaxPixels = 40_000_000
	// maxDecodedBytes caps the memory of the decoded picture (pixels ×
	// bytes per pixel of its colour model), so a 16-bit PNG within
	// MaxPixels is refused too.
	maxDecodedBytes = 160 << 20
	// Size is the largest side of the result, in pixels.
	Size = 256
	// MaxOutputBytes: the result is never larger (a JPEG is encoded again
	// at a lower quality, a PNG smaller or as a JPEG).
	MaxOutputBytes = 64 << 10
	// Quality of the JPEG encoding.
	Quality = 85
	// prescale: a square crop larger than this is first shrunk to it by
	// a cheap filter, so the Catmull-Rom pass costs the same for any input.
	prescale = 4 * Size
)

// Media types of the result.
const (
	MIMEJPEG = "image/jpeg"
	MIMEPNG  = "image/png"
)

// Errors of Normalize; Class names them for a log.
var (
	ErrTooLarge   = errors.New("contactphoto: input too large")
	ErrTooBig     = errors.New("contactphoto: picture has too many pixels")
	ErrFormat     = errors.New("contactphoto: not a JPEG, PNG or GIF picture")
	ErrDecode     = errors.New("contactphoto: picture cannot be decoded")
	ErrOutputSize = errors.New("contactphoto: picture does not fit the output limit")
)

// Class is a short, fixed name of what failed ("tooLarge", "tooBig",
// "format", "decode", "output", "other"): what the daemon may log of a
// refused picture.
func Class(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrTooLarge):
		return "tooLarge"
	case errors.Is(err, ErrTooBig):
		return "tooBig"
	case errors.Is(err, ErrFormat):
		return "format"
	case errors.Is(err, ErrDecode):
		return "decode"
	case errors.Is(err, ErrOutputSize):
		return "output"
	}
	return "other"
}

// sem bounds the decodes running at once in the process: each may hold
// up to maxDecodedBytes.
var sem = make(chan struct{}, 2)

// Normalize decodes a picture (JPEG, PNG or GIF, told by its bytes; for a
// GIF the first frame), crops the centred square, scales it to at most
// Size × Size with Catmull-Rom and encodes it again: as PNG when the
// result has transparent pixels, else as JPEG at Quality; never more than
// MaxOutputBytes. declared is the media type the source claims: one that
// is not an image (text/html) is refused, an image type that does not
// match the bytes is ignored. It never panics.
func Normalize(data []byte, declared string) (out []byte, mime string, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, mime, err = nil, "", fmt.Errorf("%w: %v", ErrDecode, r)
		}
	}()
	if len(data) > MaxInputBytes {
		return nil, "", ErrTooLarge
	}
	if d := strings.ToLower(strings.TrimSpace(declared)); d != "" && !strings.HasPrefix(d, "image/") {
		return nil, "", fmt.Errorf("%w: declared %q", ErrFormat, capString(d, 64))
	}
	format := sniff(data)
	if format == "" {
		return nil, "", ErrFormat
	}
	cfg, err := decodeConfig(format, data)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s config: %v", ErrDecode, format, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", fmt.Errorf("%w: %dx%d", ErrDecode, cfg.Width, cfg.Height)
	}
	px := int64(cfg.Width) * int64(cfg.Height)
	if px > MaxPixels || px*bytesPerPixel(cfg.ColorModel) > maxDecodedBytes {
		return nil, "", fmt.Errorf("%w: %dx%d", ErrTooBig, cfg.Width, cfg.Height)
	}
	sem <- struct{}{}
	defer func() { <-sem }()
	src, err := decode(format, data)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s: %v", ErrDecode, format, err)
	}
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 || int64(b.Dx())*int64(b.Dy()) > MaxPixels {
		return nil, "", fmt.Errorf("%w: decoded %dx%d", ErrDecode, b.Dx(), b.Dy())
	}
	return encode(square(src, Size))
}

// sniff names the format of data by its signature: "jpeg", "png", "gif"
// or "".
func sniff(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		return "jpeg"
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "gif"
	}
	return ""
}

func decodeConfig(format string, data []byte) (image.Config, error) {
	r := bytes.NewReader(data)
	switch format {
	case "jpeg":
		return jpeg.DecodeConfig(r)
	case "png":
		return png.DecodeConfig(r)
	default:
		return gif.DecodeConfig(r)
	}
}

func decode(format string, data []byte) (image.Image, error) {
	r := bytes.NewReader(data)
	switch format {
	case "jpeg":
		return jpeg.Decode(r)
	case "png":
		return png.Decode(r)
	default:
		return gif.Decode(r) // the first frame only
	}
}

// bytesPerPixel is what a decoded pixel of the model takes at most.
func bytesPerPixel(m color.Model) int64 {
	switch m {
	case color.GrayModel, color.AlphaModel:
		return 1
	case color.Gray16Model, color.Alpha16Model:
		return 2
	case color.YCbCrModel:
		return 3
	case color.RGBA64Model, color.NRGBA64Model:
		return 8
	}
	if _, ok := m.(color.Palette); ok {
		return 1
	}
	return 4
}

// square is the centred square of src scaled to at most size × size (a
// smaller picture keeps its size).
func square(src image.Image, size int) *image.RGBA {
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	at := image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2)
	crop := image.Rectangle{Min: at, Max: at.Add(image.Pt(side, side))}
	if side > prescale {
		mid := image.NewRGBA(image.Rect(0, 0, prescale, prescale))
		draw.ApproxBiLinear.Scale(mid, mid.Bounds(), src, crop, draw.Src, nil)
		src, crop, side = mid, mid.Bounds(), prescale
	}
	n := min(side, size)
	dst := image.NewRGBA(image.Rect(0, 0, n, n))
	if n == side {
		draw.Draw(dst, dst.Bounds(), src, crop.Min, draw.Src)
	} else {
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Src, nil)
	}
	return dst
}

// encode writes img as PNG when it has transparent pixels, else as JPEG,
// within MaxOutputBytes.
func encode(img *image.RGBA) ([]byte, string, error) {
	if !img.Opaque() {
		for _, side := range []int{img.Bounds().Dx(), 192, 128, 96} {
			small := img
			if side < img.Bounds().Dx() {
				small = image.NewRGBA(image.Rect(0, 0, side, side))
				draw.CatmullRom.Scale(small, small.Bounds(), img, img.Bounds(), draw.Src, nil)
			} else if side > img.Bounds().Dx() {
				continue
			}
			var buf bytes.Buffer
			if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, small); err != nil {
				return nil, "", fmt.Errorf("%w: png: %v", ErrOutputSize, err)
			}
			if buf.Len() <= MaxOutputBytes {
				return buf.Bytes(), MIMEPNG, nil
			}
		}
		// Too detailed for a PNG that small: flatten onto white.
		flat := image.NewRGBA(img.Bounds())
		draw.Draw(flat, flat.Bounds(), image.White, image.Point{}, draw.Src)
		draw.Draw(flat, flat.Bounds(), img, img.Bounds().Min, draw.Over)
		img = flat
	}
	for _, q := range []int{Quality, 70, 55, 40, 25} {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
			return nil, "", fmt.Errorf("%w: jpeg: %v", ErrOutputSize, err)
		}
		if buf.Len() <= MaxOutputBytes {
			return buf.Bytes(), MIMEJPEG, nil
		}
	}
	return nil, "", ErrOutputSize
}

func capString(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
