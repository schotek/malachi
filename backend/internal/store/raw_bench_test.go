// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Benchmarks of the raw message files. By default they run on generated
// mail. MALACHI_BENCH_RAW_DIR names a directory of raw messages instead
// (a real store's messages/<account>): it is only read, never written, and
// it holds someone's mail, so point it at a real store only with its
// owner's consent.
//
//	go test -run '^$' -bench . -benchtime 2s ./internal/store/

// benchSet is a named corpus of raw messages.
type benchSet struct {
	name string
	msgs [][]byte
}

func (s benchSet) bytes() int64 {
	var n int64
	for _, m := range s.msgs {
		n += int64(len(m))
	}
	return n
}

// benchSets returns the generated sets, or the messages of
// MALACHI_BENCH_RAW_DIR as one set.
func benchSets(b *testing.B) []benchSet {
	b.Helper()
	if dir := os.Getenv("MALACHI_BENCH_RAW_DIR"); dir != "" {
		return []benchSet{{name: "real", msgs: realMessages(b, dir)}}
	}
	return []benchSet{
		{"text-8KiB", [][]byte{benchText(8<<10, 1)}},
		{"html-64KiB", [][]byte{benchHTML(64<<10, 2)}},
		{"attach-1MiB", [][]byte{benchAttachment(1<<20, 3)}},
		{"attach-16MiB", [][]byte{benchAttachment(16<<20, 4)}},
	}
}

// realMessages reads the plain raw files of dir (at most 4 GiB), read only.
func realMessages(b *testing.B, dir string) [][]byte {
	b.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		b.Fatal(err)
	}
	var out [][]byte
	var total int64
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || strings.HasSuffix(name, tmpSuffix) || strings.HasSuffix(name, RawZstSuffix) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || len(data) > MaxRawBytes {
			continue
		}
		out = append(out, data)
		if total += int64(len(data)); total > 4<<30 {
			break
		}
	}
	if len(out) == 0 {
		b.Fatalf("no raw messages in %s", dir)
	}
	return out
}

const benchHeader = "Received: from mx.example.invalid by mail.example.invalid; Tue, 1 Sep 2026 10:00:00 +0200\r\n" +
	"From: Alice Example <alice@example.invalid>\r\nTo: Bob <bob@example.invalid>\r\n" +
	"Subject: Quarterly report and the pictures from Friday\r\nDate: Tue, 1 Sep 2026 10:00:00 +0200\r\n" +
	"Message-ID: <bench@example.invalid>\r\nMIME-Version: 1.0\r\n"

// benchText is a plain-text message of about n bytes.
func benchText(n int, seed int64) []byte {
	var b bytes.Buffer
	b.WriteString(benchHeader + "Content-Type: text/plain; charset=utf-8\r\n\r\n")
	writeWords(&b, n, seed)
	return b.Bytes()
}

// benchHTML is a newsletter-like HTML message of about n bytes.
func benchHTML(n int, seed int64) []byte {
	var b bytes.Buffer
	b.WriteString(benchHeader + "Content-Type: text/html; charset=utf-8\r\n\r\n<html><body>")
	rng := rand.New(rand.NewSource(seed))
	for b.Len() < n {
		fmt.Fprintf(&b, `<table width="100%%" style="border:0;padding:12px;font-family:Helvetica,Arial,sans-serif"><tr><td class="c%d">`, rng.Intn(9))
		writeWords(&b, b.Len()+300, rng.Int63())
		fmt.Fprintf(&b, `<a href="https://news.example.invalid/l/%x">Read more</a></td></tr></table>`+"\r\n", rng.Int63())
	}
	b.WriteString("</body></html>\r\n")
	return b.Bytes()
}

// benchAttachment is a short text with an attachment of about n bytes of
// incompressible data (a picture, a PDF), base64 as sent.
func benchAttachment(n int, seed int64) []byte {
	var b bytes.Buffer
	b.WriteString(benchHeader + "Content-Type: multipart/mixed; boundary=\"b1\"\r\n\r\n--b1\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n")
	writeWords(&b, 2<<10, seed)
	b.WriteString("\r\n--b1\r\nContent-Type: application/pdf; name=\"report.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"report.pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	b.Write(base64Attachment(n*3/4, seed))
	b.WriteString("--b1--\r\n")
	return b.Bytes()
}

func writeWords(b *bytes.Buffer, n int, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	words := strings.Fields("the report shows that our quarterly numbers grew and the pictures from Friday are attached " +
		"přílohy zpráva schůzka díky pozdravy meeting tomorrow please find below regards invoice payment")
	for col := 0; b.Len() < n; {
		w := words[rng.Intn(len(words))]
		b.WriteString(w)
		if col += len(w) + 1; col > 70 {
			b.WriteString("\r\n")
			col = 0
		} else {
			b.WriteByte(' ')
		}
	}
}

// BenchmarkPutMessageRaw writes fresh messages (not flushed, as a download
// is) in each codec; ratio is file bytes per message byte.
func BenchmarkPutMessageRaw(b *testing.B) {
	for _, codec := range []RawCodec{RawPlain, RawZstd} {
		for _, set := range benchSets(b) {
			b.Run(codec.String()+"/"+set.name, func(b *testing.B) {
				s := openTestStore(b)
				s.SetRawCodec(codec)
				ctx := context.Background()
				b.SetBytes(set.bytes() / int64(len(set.msgs)))
				var disk, content int64
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					m := set.msgs[i%len(set.msgs)]
					id := fmt.Sprintf("m_%d", i%64)
					if i%64 == 0 && i > 0 {
						b.StopTimer()
						os.RemoveAll(s.accountDir("acc"))
						b.StartTimer()
					}
					info, err := s.PutMessageRaw(ctx, "acc", id, RawWrite{Size: int64(len(m))}, func(w io.Writer) error {
						_, err := w.Write(m)
						return err
					})
					if err != nil {
						b.Fatal(err)
					}
					disk += info.DiskBytes
					content += info.Bytes
				}
				b.ReportMetric(float64(disk)/float64(content), "ratio")
			})
		}
	}
}

// BenchmarkOpenParse is what opening a message costs: the raw file opened
// and parsed, in each codec.
func BenchmarkOpenParse(b *testing.B) {
	for _, codec := range []RawCodec{RawPlain, RawZstd} {
		for _, set := range benchSets(b) {
			b.Run(codec.String()+"/"+set.name, func(b *testing.B) {
				s := openTestStore(b)
				s.SetRawCodec(codec)
				ctx := context.Background()
				n := min(len(set.msgs), 256)
				for i := 0; i < n; i++ {
					if _, err := s.WriteMessageRaw(ctx, "acc", fmt.Sprintf("m_%d", i), bytes.NewReader(set.msgs[i]), 0); err != nil {
						b.Fatal(err)
					}
				}
				b.SetBytes(set.bytes() / int64(len(set.msgs)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					r, err := s.OpenMessageRaw(ctx, "acc", fmt.Sprintf("m_%d", i%n))
					if err != nil {
						b.Fatal(err)
					}
					if _, err := mime.Parse(r, mime.DefaultLimits()); err != nil {
						b.Fatal(err)
					}
					r.Close()
				}
			})
		}
	}
}

// BenchmarkConvertRawBatch converts batches of 64 messages of about 64 KiB
// back and forth, each file flushed to disk as the conversion does.
func BenchmarkConvertRawBatch(b *testing.B) {
	s := openTestStore(b)
	ctx := context.Background()
	inbox := seedFolderB(b, s)
	var total int64
	for i := 0; i < 64; i++ {
		m := seedRow(b, s, inbox, uint32(i+1))
		data := benchHTML(64<<10, int64(i))
		if i%4 == 0 {
			data = benchAttachment(64<<10, int64(i))
		}
		if _, err := s.WriteMessageRaw(ctx, "acc", m.ID, bytes.NewReader(data), 0); err != nil {
			b.Fatal(err)
		}
		total += int64(len(data))
	}
	b.SetBytes(total)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		to := RawCodec((i + 1) % 2)
		s.SetRawCodec(to)
		if _, res, err := s.ConvertRawBatch(ctx, "", to, 64); err != nil || res.Converted != 64 {
			b.Fatalf("convert: %+v %v", res, err)
		}
	}
}

// seedFolderB is seedFolder for a benchmark.
func seedFolderB(b *testing.B, s *Store) Folder {
	stored, _, err := s.UpsertFolders(context.Background(), "acc",
		[]Folder{{Mailbox: "INBOX", Name: "INBOX", Path: "INBOX", Role: api.RoleInbox, Selectable: true, Subscribed: true}})
	if err != nil {
		b.Fatal(err)
	}
	return stored[0]
}

// zstdVariants are the encoder settings BenchmarkZstdOptions compares: the
// store's (4 MiB window), the level's default 8 MiB window the store was
// first measured with, and the lower-memory mode with either window.
// Later options win, so each variant is the store's with changes.
var zstdVariants = []struct {
	name string
	opts []zstd.EOption
}{
	{"store", zstdEncoderOptions},
	{"window8MiB", withOptions(zstd.WithWindowSize(8 << 20))},
	{"lowmem", withOptions(zstd.WithLowerEncoderMem(true))},
	{"lowmem-window8MiB", withOptions(zstd.WithLowerEncoderMem(true), zstd.WithWindowSize(8<<20))},
}

func withOptions(extra ...zstd.EOption) []zstd.EOption {
	return append(append([]zstd.EOption(nil), zstdEncoderOptions...), extra...)
}

// BenchmarkZstdOptions compresses each set with each encoder setting, one
// reused encoder as the pool keeps them; ratio is compressed per original
// byte.
func BenchmarkZstdOptions(b *testing.B) {
	for _, v := range zstdVariants {
		for _, set := range benchSets(b) {
			b.Run(v.name+"/"+set.name, func(b *testing.B) {
				enc, err := zstd.NewWriter(nil, v.opts...)
				if err != nil {
					b.Fatal(err)
				}
				var out countWriter
				out.w = io.Discard
				var in int64
				b.SetBytes(set.bytes() / int64(len(set.msgs)))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					m := set.msgs[i%len(set.msgs)]
					enc.ResetContentSize(&out, int64(len(m)))
					if _, err := enc.Write(m); err != nil {
						b.Fatal(err)
					}
					if err := enc.Close(); err != nil {
						b.Fatal(err)
					}
					in += int64(len(m))
				}
				b.ReportMetric(float64(out.n)/float64(in), "ratio")
			})
		}
	}
}

// BenchmarkZstdEncoderMemory reports the heap one encoder keeps after it
// wrote a large message, per setting (B/encoder).
func BenchmarkZstdEncoderMemory(b *testing.B) {
	msg := benchAttachment(16<<20, 5)
	for _, v := range zstdVariants {
		b.Run(v.name, func(b *testing.B) {
			var kept []*zstd.Encoder
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			for i := 0; i < b.N; i++ {
				enc, err := zstd.NewWriter(nil, v.opts...)
				if err != nil {
					b.Fatal(err)
				}
				enc.ResetContentSize(io.Discard, int64(len(msg)))
				enc.Write(msg)
				enc.Close()
				enc.Reset(nil)
				kept = append(kept, enc)
			}
			runtime.GC()
			runtime.ReadMemStats(&after)
			b.ReportMetric(float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/float64(len(kept)), "B/encoder")
			runtime.KeepAlive(kept)
		})
	}
}
