// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestMessagePart(t *testing.T) {
	m := seedMailbox(t)
	ctx := context.Background()
	svc := m.b.Messages()

	png, err := svc.Part(ctx, api.MessagePartParams{AccountID: m.acc, MessageID: m.msgs[0], PartID: "1.2"})
	if err != nil {
		t.Fatal(err)
	}
	wantPNG := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if png.PartID != "1.2" || png.ContentType != "image/png" || png.Filename != "logo.png" || png.Size != int64(len(wantPNG)) || !bytes.Equal(png.Data, wantPNG) {
		t.Fatalf("png part = %+v", png)
	}
	pdf, err := svc.Part(ctx, api.MessagePartParams{AccountID: m.acc, MessageID: m.msgs[0], PartID: "2"})
	if err != nil {
		t.Fatal(err)
	}
	if pdf.ContentType != "application/pdf" || pdf.Filename != "a.pdf" || string(pdf.Data) != "%PDF-1.4\n" {
		t.Fatalf("pdf part = %+v", pdf)
	}

	cases := []struct {
		name string
		p    api.MessagePartParams
		code api.ErrorCode
	}{
		{"missing ids", api.MessagePartParams{AccountID: m.acc, MessageID: m.msgs[0]}, api.CodeInvalidArgument},
		{"junk part id", api.MessagePartParams{AccountID: m.acc, MessageID: m.msgs[0], PartID: "../1"}, api.CodeInvalidArgument},
		{"no such part", api.MessagePartParams{AccountID: m.acc, MessageID: m.msgs[0], PartID: "9"}, api.CodePartNotFound},
		{"container", api.MessagePartParams{AccountID: m.acc, MessageID: m.msgs[0], PartID: "1"}, api.CodePartNotFound},
		{"no raw message", api.MessagePartParams{AccountID: m.acc, MessageID: m.msgs[1], PartID: "1"}, api.CodePartNotFound},
		{"unknown message", api.MessagePartParams{AccountID: m.acc, MessageID: "m_nope", PartID: "1"}, api.CodeMessageNotFound},
		{"unknown account", api.MessagePartParams{AccountID: "acc_nope", MessageID: m.msgs[0], PartID: "1"}, api.CodeAccountNotFound},
	}
	for _, c := range cases {
		_, err := svc.Part(ctx, c.p)
		if got := errCode(t, err); got != c.code {
			t.Errorf("%s: code %v, want %v", c.name, got, c.code)
		}
	}
}
