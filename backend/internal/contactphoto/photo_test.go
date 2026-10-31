// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package contactphoto

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/vcard"
)

// corpus is the photos of testdata/vcard/photos as their cards carry
// them (file → photo; cards without an embedded photo are left out).
func corpus(t testing.TB) map[string]*vcard.Photo {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "vcard", "photos", "*.vcf"))
	if len(files) < 10 {
		t.Fatalf("corpus has %d files", len(files))
	}
	out := map[string]*vcard.Photo{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		c, err := vcard.Parse(raw)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if c.Photo != nil {
			out[filepath.Base(f)] = c.Photo
		}
	}
	return out
}

// check decodes a result and verifies the contract: the media type, a
// square of at most Size, at most MaxOutputBytes.
func check(t *testing.T, name string, out []byte, mime string, wantMIME string, wantSide int) {
	t.Helper()
	if mime != wantMIME {
		t.Errorf("%s: mime %q, want %q", name, mime, wantMIME)
	}
	if len(out) > MaxOutputBytes {
		t.Errorf("%s: %d bytes", name, len(out))
	}
	img, format, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("%s: result does not decode: %v", name, err)
	}
	if "image/"+format != mime {
		t.Errorf("%s: says %q, is %q", name, mime, format)
	}
	if b := img.Bounds(); b.Dx() != b.Dy() || b.Dx() != wantSide {
		t.Errorf("%s: %v, want %d square", name, b, wantSide)
	}
}

func TestCorpus(t *testing.T) {
	photos := corpus(t)
	for _, tc := range []struct {
		file string
		mime string
		side int
		err  error
	}{
		{file: "jpeg-3.0.vcf", mime: MIMEJPEG, side: 200},
		{file: "png-alpha-4.0.vcf", mime: MIMEPNG, side: 100},
		{file: "gif-2.1.vcf", mime: MIMEJPEG, side: 40},
		{file: "many-frames-gif.vcf", mime: MIMEJPEG, side: 8},
		{file: "type-mismatch.vcf", mime: MIMEJPEG, side: 200},
		{file: "bomb-png-40000.vcf", err: ErrTooBig},
		{file: "bomb-jpeg-65000.vcf", err: ErrTooBig},
		{file: "truncated-jpeg.vcf", err: ErrDecode},
		{file: "wrong-mime.vcf", err: ErrFormat},
		{file: "not-an-image.vcf", err: ErrFormat},
	} {
		p := photos[tc.file]
		if p == nil {
			t.Fatalf("%s: no photo in the card", tc.file)
		}
		start := time.Now()
		out, mime, err := Normalize(p.Data, p.MIME)
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s: took %v", tc.file, d)
		}
		if tc.err != nil {
			if !errors.Is(err, tc.err) || out != nil || mime != "" {
				t.Errorf("%s: %v (%d bytes), want %v", tc.file, err, len(out), tc.err)
			}
			if Class(err) == "other" {
				t.Errorf("%s: unclassified %v", tc.file, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		check(t, tc.file, out, mime, tc.mime, tc.side)
	}
}

func encodeJPEG(t testing.TB, img image.Image, q int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodePNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func noise(w, h int, alpha bool) *image.NRGBA {
	r := rand.New(rand.NewPCG(1, 2))
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(r.Uint32()), uint8(r.Uint32()), uint8(r.Uint32()), 255
		if alpha {
			img.Pix[i+3] = uint8(r.Uint32())
		}
	}
	return img
}

// TestCropAndScale: a tall picture becomes its centred square, scaled.
func TestCropAndScale(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 600, 1800))
	for y := 0; y < 1800; y++ {
		for x := 0; x < 600; x++ {
			c := color.RGBA{0, 0, 255, 255} // blue bands top and bottom
			if y >= 600 && y < 1200 {
				c = color.RGBA{255, 0, 0, 255} // the red middle square
			}
			img.Set(x, y, c)
		}
	}
	out, mime, err := Normalize(encodeJPEG(t, img, 95), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "tall", out, mime, MIMEJPEG, Size)
	got, _ := jpeg.Decode(bytes.NewReader(out))
	for _, p := range []image.Point{{5, 5}, {128, 128}, {250, 250}} {
		r, g, b, _ := got.At(p.X, p.Y).RGBA()
		if r>>8 < 200 || g>>8 > 60 || b>>8 > 60 {
			t.Errorf("pixel %v is %d,%d,%d, not the red middle", p, r>>8, g>>8, b>>8)
		}
	}
}

// TestOutputLimit: noise, which compresses badly, still fits.
func TestOutputLimit(t *testing.T) {
	out, mime, err := Normalize(encodePNG(t, noise(700, 700, false)), "")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "noise", out, mime, MIMEJPEG, Size)
	out, mime, err = Normalize(encodePNG(t, noise(300, 300, true)), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > MaxOutputBytes || (mime != MIMEPNG && mime != MIMEJPEG) {
		t.Errorf("alpha noise: %s of %d bytes", mime, len(out))
	}
}

// TestMetadataDropped: an EXIF segment of the source is not in the result.
func TestMetadataDropped(t *testing.T) {
	src := encodeJPEG(t, noise(64, 64, false), 90)
	payload := []byte("Exif\x00\x00GPS 50.0755N 14.4378E SECRET-MARKER")
	seg := append([]byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}, payload...)
	withExif := append(append(append([]byte{}, src[:2]...), seg...), src[2:]...)
	out, _, err := Normalize(withExif, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("SECRET-MARKER")) || bytes.Contains(out, []byte("Exif")) {
		t.Error("the EXIF segment survived")
	}
}

func TestRefusals(t *testing.T) {
	big := make([]byte, MaxInputBytes+1)
	copy(big, "\xff\xd8\xff")
	if _, _, err := Normalize(big, "image/jpeg"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("4 MiB + 1: %v", err)
	}
	for _, in := range [][]byte{nil, {}, []byte("\xff\xd8"), []byte("BM\x00\x00"), []byte("RIFF....WEBPVP8 ")} {
		if _, _, err := Normalize(in, ""); !errors.Is(err, ErrFormat) {
			t.Errorf("%q: %v", in, err)
		}
	}
	// A 16-bit picture within MaxPixels but over the memory cap.
	wide := image.NewNRGBA64(image.Rect(0, 0, 5000, 5000))
	if _, _, err := Normalize(encodePNG(t, wide), "image/png"); !errors.Is(err, ErrTooBig) {
		t.Errorf("16-bit 25 Mpx: %v", err)
	}
	if Class(nil) != "" || Class(errors.New("x")) != "other" {
		t.Error("Class")
	}
}

// TestBombTiming: what is refused is refused at once, and a picture just
// within the limits (a few kilobytes that decode to 36 Mpx) takes bounded
// time.
func TestBombTiming(t *testing.T) {
	photos := corpus(t)
	for _, f := range []string{"bomb-png-40000.vcf", "bomb-jpeg-65000.vcf"} {
		start := time.Now()
		for range 20 {
			if _, _, err := Normalize(photos[f].Data, photos[f].MIME); !errors.Is(err, ErrTooBig) {
				t.Fatalf("%s: %v", f, err)
			}
		}
		if d := time.Since(start) / 20; d > 50*time.Millisecond {
			t.Errorf("%s: %v per refusal", f, d)
		}
	}
	if testing.Short() {
		t.Skip("the 36 Mpx picture in -short")
	}
	gray := image.NewGray(image.Rect(0, 0, 6000, 6000))
	src := encodePNG(t, gray)
	if len(src) > 200<<10 {
		t.Fatalf("bomb of %d bytes", len(src))
	}
	start := time.Now()
	out, mime, err := Normalize(src, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("36 Mpx took %v", d)
	}
	check(t, "36 Mpx", out, mime, MIMEJPEG, Size)
}

// FuzzNormalize: Normalize never panics, and what it returns is a JPEG or
// PNG square of at most Size within MaxOutputBytes.
func FuzzNormalize(f *testing.F) {
	for _, p := range corpus(f) {
		if len(p.Data) <= 64<<10 {
			f.Add(p.Data, p.MIME)
		}
	}
	f.Add(encodePNG(f, noise(3, 5, true)), "image/png")
	f.Fuzz(func(t *testing.T, data []byte, mime string) {
		out, got, err := Normalize(data, mime)
		if err != nil {
			if out != nil || got != "" || Class(err) == "other" {
				t.Fatalf("failure with output or unclassified: %v", err)
			}
			return
		}
		if len(out) > MaxOutputBytes || (got != MIMEJPEG && got != MIMEPNG) {
			t.Fatalf("%s of %d bytes", got, len(out))
		}
		img, _, err := image.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("result does not decode: %v", err)
		}
		if b := img.Bounds(); b.Dx() != b.Dy() || b.Dx() > Size || b.Dx() < 1 {
			t.Fatalf("result %v", b)
		}
	})
}
