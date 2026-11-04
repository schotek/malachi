// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlview

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/printing"
)

// TestPrintGTKSmoke prints a fragment to PDF through htmlview.Print (the
// PrintPDFEnv path, no dialog, no printer) with a GTK display and checks
// that a PDF with its pages came out. With MALACHI_PRINT_PDF set the PDF
// is written there and kept, to look at.
func TestPrintGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the print smoke test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	gtk.Init()
	pdf := os.Getenv(PrintPDFEnv)
	if pdf == "" {
		pdf = filepath.Join(t.TempDir(), "print.pdf")
		t.Setenv(PrintPDFEnv, pdf)
	}
	fetch := func(context.Context, api.AccountID, api.MessageID, string) (string, []byte, error) {
		return "", nil, errors.New("no parts in the smoke test")
	}
	// Three messages of a selection, each on a page of its own (P5): the
	// header rows escaped, an HTML body verbatim, a long one, a text one.
	var tr testTr
	long := ""
	for i := 0; i < 120; i++ {
		long += "<p>Line of an invented message body.</p>"
	}
	head := func(subject string) []printing.Row {
		return printing.HeaderRows(tr, printing.Header{
			Subject: subject, From: "Jana <jana@example.com>", To: "Petr", Cc: "Eva & Adam",
			Date: "4 November 2026 12:00", Attachments: []string{"report.pdf (41.1 kB)"},
		})
	}
	frag := printing.Fragment([]string{
		printing.Message(head("First <b>not bold</b>"), printing.Body{HTML: "<p>Hello &amp; <b>welcome</b>.</p>"}),
		printing.Message(head("Second"), printing.Body{HTML: long}),
		printing.Message(head("Third"), printing.Body{Text: "plain <text>\n  indented"}),
	}, printing.Pages)
	loop := glib.NewMainLoop(nil, false)
	result := -1
	Print(slog.Default(), fetch, frag, "Smoke print", nil, func(ok bool) {
		result = 0
		if ok {
			result = 1
		}
		loop.Quit()
	})
	glib.TimeoutAdd(30000, func() bool { loop.Quit(); return false })
	loop.Run()
	if result != 1 {
		t.Fatalf("print ended with %d (want 1)", result)
	}
	// The print backend may write the file a moment after "finished".
	var data []byte
	for i := 0; i < 50; i++ {
		data, _ = os.ReadFile(pdf)
		if len(data) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		t.Fatalf("no PDF at %s (%d bytes)", pdf, len(data))
	}
	pages := bytes.Count(data, []byte("/Type /Page\n")) + bytes.Count(data, []byte("/Type /Page>>")) + bytes.Count(data, []byte("/Type /Page "))
	t.Logf("LAYOUT: print pdf %d bytes, about %d page objects, at %s", len(data), pages, pdf)
	if len(printJobs) != 0 {
		t.Fatalf("%d print jobs left", len(printJobs))
	}
}

// testTr returns the msgids.
type testTr struct{}

func (testTr) T(msgid string) string                { return msgid }
func (testTr) N(msgid, plural string, n int) string { return msgid }
func (testTr) C(_, msgid string) string             { return msgid }
