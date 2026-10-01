// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package extract

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestFactsCountsCoverEveryInt(t *testing.T) {
	var f Facts
	listed := map[string]bool{}
	for _, c := range f.counts() {
		listed[c.name] = true
	}
	typ := reflect.TypeFor[Facts]()
	ints := 0
	for i := range typ.NumField() {
		field := typ.Field(i)
		name := field.Tag.Get("json")
		if name == "" || name[0] < 'a' || name[0] > 'z' || strings.ContainsAny(name, ",_-") {
			t.Errorf("%s: JSON name %q is not lowerCamelCase", field.Name, name)
		}
		if field.Type.Kind() != reflect.Int {
			continue
		}
		ints++
		if !listed[name] {
			t.Errorf("%s (%s) is missing from Facts.counts", field.Name, name)
		}
	}
	if ints != len(listed) {
		t.Errorf("counts lists %d fields, Facts has %d ints", len(listed), ints)
	}
}

func TestFactsValidate(t *testing.T) {
	for _, f := range []Facts{
		{},
		{Pages: MaxFact, Rows: MaxFact},
		{Cut: true, CutAt: CutTextBytes, CutPage: 301},
		{Cut: true, CutAt: CutNotes},
		fullFacts(),
	} {
		if err := f.Validate(); err != nil {
			t.Errorf("%+v: %v", f, err)
		}
	}
	for _, f := range []Facts{
		{Pages: -1},
		{CutRow: MaxFact + 1},
		{Cut: true},
		{CutAt: CutPages},
		{Cut: true, CutAt: "chapters"},
	} {
		if err := f.Validate(); err == nil {
			t.Errorf("%+v accepted", f)
		}
	}
}

func TestClosedSets(t *testing.T) {
	for _, c := range []Code{
		WrongFormat, Encrypted, OfficeCFB, Damaged, NoText, Garbled, TooBig,
		Expands, TooManyParts, TooDeep, TooManyTokens, Unsupported, ReaderFailed,
	} {
		if !c.Known() || string(c) == "" || strings.ToLower(string(c[:1])) != string(c[:1]) {
			t.Errorf("code %q", c)
		}
	}
	for _, c := range []Code{"", "WrongFormat", "wrongformat", "other"} {
		if c.Known() {
			t.Errorf("code %q is known", c)
		}
	}
	for _, w := range []string{
		WhatNone, WhatZipEncryption, WhatCompressionMethod, WhatXMLEncoding, WhatDoctype,
		WhatMacroEnabled, WhatTemplate, WhatPresentation, WhatBinaryWorkbook, WhatOtherZip, WhatPassword,
	} {
		if !KnownWhat(w) {
			t.Errorf("detail %q", w)
		}
	}
	for _, w := range []string{"Doctype", "doctype ", "anything"} {
		if KnownWhat(w) {
			t.Errorf("detail %q is known", w)
		}
	}
	if (&Refusal{Code: TooBig}).Error() != "extract: refused: tooBig" ||
		(&Refusal{Code: Unsupported, What: WhatDoctype}).Error() != "extract: refused: unsupported (doctype)" {
		t.Error("Refusal.Error")
	}
	if (&Refusal{Code: Unsupported, What: "x"}).Valid() || (&Refusal{Code: "x"}).Valid() {
		t.Error("Refusal.Valid accepts outside the closed sets")
	}
}

func TestFormatValid(t *testing.T) {
	for _, f := range []Format{PDF, DOCX, XLSX} {
		if !f.Valid() {
			t.Errorf("%q", f)
		}
	}
	for _, f := range []Format{"", "doc", "xls", "PDF", "pptx"} {
		if f.Valid() {
			t.Errorf("%q is valid", f)
		}
	}
}

func TestDefaultLimits(t *testing.T) {
	lim := DefaultLimits()
	if lim.MaxInputBytes != 16<<20 || lim.MaxTextBytes != 1<<20 || lim.MaxPages != 500 ||
		lim.MaxPageBytes != 256<<10 || lim.MaxZipEntries != 2000 || lim.MaxEntryBytes != 64<<20 ||
		lim.MaxExpandBytes != 128<<20 || lim.MaxXMLDepth != 128 || lim.MaxGarbledPercent != 10 {
		t.Errorf("%+v", lim)
	}
	// Every count a reader can reach stays within what a reply carries.
	for name, n := range map[string]int{
		"MaxPages": lim.MaxPages, "MaxSheets": lim.MaxSheets, "MaxCells": lim.MaxCells,
		"MaxNotes": lim.MaxNotes, "MaxTextBytes": lim.MaxTextBytes,
	} {
		if n > MaxFact {
			t.Errorf("%s %d over MaxFact", name, n)
		}
	}
}

func TestExtractDispatch(t *testing.T) {
	ctx := context.Background()
	lim := DefaultLimits()
	refusal := func(err error) Code {
		var r *Refusal
		if !errors.As(err, &r) {
			return "not a refusal: " + Code(err.Error())
		}
		return r.Code
	}
	if _, err := Extract(ctx, "odt", []byte("PK\x03\x04"), lim); refusal(err) != Unsupported {
		t.Errorf("unknown format: %v", err)
	}
	if _, err := Extract(ctx, PDF, nil, lim); refusal(err) != Damaged {
		t.Errorf("empty: %v", err)
	}
	small := lim
	small.MaxInputBytes = 3
	if _, err := Extract(ctx, PDF, []byte("%PDF"), small); refusal(err) != TooBig {
		t.Errorf("too big: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Extract(cancelled, PDF, []byte("%PDF-1.7"), lim); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	// Each format reaches its own reader: a PDF header that leads nowhere
	// is damaged, and the same bytes are no ZIP for the OOXML readers.
	for f, want := range map[Format]Code{PDF: Damaged, DOCX: WrongFormat, XLSX: WrongFormat} {
		if _, err := Extract(ctx, f, []byte("%PDF-1.7 PK\x03\x04"), lim); refusal(err) != want {
			t.Errorf("%s: %v, want %s", f, err, want)
		}
	}
}
