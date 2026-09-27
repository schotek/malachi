// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestUsage(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	u, err := s.Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if u.DatabaseBytes <= 0 || u.Messages != 0 || u.MessageBytes != 0 || u.Estimated || u.TotalBytes() != u.DatabaseBytes {
		t.Fatalf("empty store: %+v", u)
	}

	inbox := seedFolder(t, s, "acc", "INBOX", api.RoleInbox)
	text := rawContent(100<<10, 1)
	plain := seedRow(t, s, inbox, 1)
	if _, err := s.WriteMessageRaw(ctx, "acc", plain.ID, bytes.NewReader(text), 0); err != nil {
		t.Fatal(err)
	}
	s.SetRawCodec(RawZstd)
	var zstdDisk int64
	for uid := uint32(2); uid <= 3; uid++ {
		m := seedRow(t, s, inbox, uid)
		info, err := s.WriteMessageRaw(ctx, "acc", m.ID, bytes.NewReader(text), 0)
		if err != nil {
			t.Fatal(err)
		}
		st, _ := os.Stat(s.MessageRawPath("acc", m.ID) + RawZstSuffix)
		zstdDisk += st.Size()
		_ = info
	}
	// A partial message: its large attachment stays on the server.
	partial := seedRow(t, s, inbox, 4)
	st, _ := s.StageRaw(ctx, 0)
	st.Write([]byte("Subject: skeleton\r\n\r\nbody"))
	if _, err := s.CommitMessageRaw(ctx, "acc", partial.ID, RawCommit{Source: st, RemoteParts: []string{"2"},
		RemoteBytes: 5 << 20, StrippableBytes: 0}); err != nil {
		t.Fatal(err)
	}
	st.Remove()
	partialDisk, _ := os.Stat(s.MessageRawPath("acc", partial.ID) + RawZstSuffix)
	// The compose side.
	a := Attachment{AccountID: "acc", Filename: "a.txt", ContentType: "text/plain"}
	if err := s.ImportAttachment(ctx, &a, bytes.NewReader([]byte("attachment data")), 1<<20); err != nil {
		t.Fatal(err)
	}

	u, err = s.Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	skeleton := int64(len("Subject: skeleton\r\n\r\nbody"))
	want := Usage{
		DatabaseBytes:   u.DatabaseBytes,
		MessageBytes:    int64(len(text)) + zstdDisk + partialDisk.Size(),
		ContentBytes:    3*int64(len(text)) + skeleton,
		AttachmentBytes: int64(len("attachment data")),
		Messages:        4,
		Compressed:      3,
		Partial:         1,
		RemoteBytes:     5 << 20,
	}
	if u != want {
		t.Errorf("usage = %+v\nwant    %+v", u, want)
	}
	if u.SavedBytes() != want.ContentBytes-want.MessageBytes || u.SavedBytes() <= 0 {
		t.Errorf("saved %d", u.SavedBytes())
	}
	if u.TotalBytes() != u.DatabaseBytes+u.MessageBytes+u.AttachmentBytes {
		t.Errorf("total %d", u.TotalBytes())
	}
	if (Usage{ContentBytes: 10, MessageBytes: 12}).SavedBytes() != 0 {
		t.Error("negative saving")
	}

	// A file from before the accounting counts by its message's size until
	// the sweep has accounted for it.
	legacy := seedRow(t, s, inbox, 5)
	if err := s.SetMessageBody(ctx, legacy.ID, BodyUpdate{Text: "x", Size: 1234}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(s.MessageRawPath("acc", legacy.ID), make([]byte, 1000), 0o600)
	u, _ = s.Usage(ctx)
	if !u.Estimated || u.Messages != 5 || u.MessageBytes != want.MessageBytes+1234 {
		t.Errorf("estimated: %+v", u)
	}
	if _, err := s.SweepMessageFiles(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	u, _ = s.Usage(ctx)
	if u.Estimated || u.Messages != 5 || u.MessageBytes != want.MessageBytes+1000 {
		t.Errorf("after the sweep: %+v", u)
	}
}
