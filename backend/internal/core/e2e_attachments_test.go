// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/pkg/api"
)

// TestEndToEndAttachmentsOnDemand runs attachments on demand through the
// production stack against an in-memory IMAP server: under "small
// attachments only" the syncer stores the message without its large parts,
// message.part answers partNotDownloaded, message.download fetches the
// message on a connection of its own without marking it read, the part is
// then served, the background pass reduces it again once the grace is
// over, and a message deleted on the server is messageGone.
func TestEndToEndAttachmentsOnDemand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	mem := imapmemserver.New()
	user := imapmemserver.NewUser("me", "pw")
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapMove: {}, imap.CapUIDPlus: {}},
		InsecureAuth: true,
		Logger:       discardLogger{},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	addr := ln.Addr().String()
	appendRawTestMessage(t, addr, string(largeMessage("report@example.org")))

	cfg := config.Default()
	cfg.Sync.IntervalSeconds = 0
	b := newTestBackend(t, cfg)
	b.Keyring = newMemKeyring()
	if _, err := b.Config().Set(ctx, api.ConfigSetParams{Preferences: api.Preferences{
		RemoteContent: api.RemoteBlock, OfflineDays: 0, AttachmentOfflineDays: api.Ptr(api.AttachmentOfflineNone),
	}}); err != nil {
		t.Fatal(err)
	}
	syncCtx, stopSync := context.WithCancel(ctx)
	done := b.StartSync(syncCtx)
	t.Cleanup(func() {
		stopSync()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("supervisor did not stop")
		}
	})
	acc := validConfig()
	acc.IMAP = &api.ServerConfig{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, Security: api.SecurityNone, Username: "me", AuthMethod: api.AuthPassword}
	added, err := b.Accounts().Add(ctx, api.AccountAddParams{Config: acc, Credentials: api.Credentials{Password: "pw"}})
	if err != nil {
		t.Fatal(err)
	}
	id := added.AccountID

	var msg api.Message
	waitUntil(t, ctx, "the message stored without its large parts", func() bool {
		folders, err := b.Folders().List(ctx, api.FolderListParams{AccountID: id})
		if err != nil {
			return false
		}
		for _, f := range folders.Folders {
			if f.Role != api.RoleInbox {
				continue
			}
			list, err := b.Messages().List(ctx, api.MessageListParams{AccountID: id, FolderID: f.ID})
			if err != nil || len(list.Messages) != 1 {
				return false
			}
			got, err := b.Messages().Get(ctx, api.MessageGetParams{AccountID: id, MessageID: list.Messages[0].ID})
			if err != nil || len(got.Message.Attachments) != 4 {
				return false
			}
			msg = got.Message
			return msg.Attachments[2].Remote
		}
		return false
	})
	if msg.Attachments[0].Remote || msg.Attachments[1].Remote || !msg.Attachments[3].Remote || msg.Attachments[2].Size != int64(len(bigPDF)) {
		t.Fatalf("attachments %+v", msg.Attachments)
	}
	part := func() (*api.MessagePartResult, error) {
		return b.Messages().Part(ctx, api.MessagePartParams{AccountID: id, MessageID: msg.ID, PartID: "3"})
	}
	if _, err := part(); errCode(t, err) != api.CodePartNotDownloaded {
		t.Fatalf("part before the download: %v", err)
	}

	res, err := b.Messages().Download(ctx, api.MessageDownloadParams{AccountID: id, MessageID: msg.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range res.Message.Attachments {
		if a.Remote {
			t.Fatalf("still remote after the download: %+v", a)
		}
	}
	if got, err := part(); err != nil || !bytes.Equal(got.Data, bigPDF) {
		t.Fatalf("part after the download: %v", err)
	}
	if serverHasFlag(t, addr, "Report", imap.FlagSeen) {
		t.Fatal("the download marked the message read")
	}

	// The grace over, the background pass leaves the parts on the server
	// again.
	var step *attachmentStep
	for _, s := range b.rawSteps {
		if s.Name() == attachmentStepName {
			step = s.(*attachmentStep)
		}
	}
	if step == nil {
		t.Fatal("the attachment step is not registered")
	}
	step.now = func() time.Time { return time.Now().Add(ingest.HydratedKeep + time.Hour) }
	for cursor := ""; ; {
		next, err := step.Batch(ctx, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if _, err := part(); errCode(t, err) != api.CodePartNotDownloaded {
		t.Fatalf("part after the pass: %v", err)
	}

	// Deleted on the server by another client: messageGone.
	expungeAll(t, addr)
	if _, err := b.Messages().Download(ctx, api.MessageDownloadParams{AccountID: id, MessageID: msg.ID}); errCode(t, err) != api.CodeMessageGone {
		t.Fatalf("download of a deleted message: %v", err)
	}
}

// expungeAll deletes every message of the INBOX on the server.
func expungeAll(t *testing.T, addr string) {
	t.Helper()
	c, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Login("me", "pw").Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	var all imap.SeqSet
	all.AddRange(1, 0)
	if err := c.Store(all, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Expunge().Close(); err != nil {
		t.Fatal(err)
	}
	c.Logout().Wait()
}
