// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

func storageOf(t *testing.T, b *Backend) api.SystemStorageResult {
	t.Helper()
	res, err := b.System().Storage(context.Background(), api.SystemStorageParams{})
	if err != nil {
		t.Fatalf("system.storage: %v", err)
	}
	return *res
}

// checkStorageSums checks the relations docs/api.md promises.
func checkStorageSums(t *testing.T, r api.SystemStorageResult) {
	t.Helper()
	if r.TotalBytes != r.DatabaseBytes+r.MessageBytes+r.AttachmentBytes {
		t.Errorf("totalBytes %d != database %d + messages %d + attachments %d", r.TotalBytes, r.DatabaseBytes, r.MessageBytes, r.AttachmentBytes)
	}
	if r.SavedBytes != max(0, r.MessageUncompressedBytes-r.MessageBytes) {
		t.Errorf("savedBytes %d, messages %d of %d uncompressed", r.SavedBytes, r.MessageBytes, r.MessageUncompressedBytes)
	}
	if r.DatabaseBytes <= 0 {
		t.Errorf("databaseBytes = %d", r.DatabaseBytes)
	}
}

// system.info still answers after System was overridden.
func TestSystemInfo(t *testing.T) {
	b := newTestBackend(t, config.Default())
	res, err := b.System().Info(context.Background(), api.SystemInfoParams{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "test" || res.ProtocolVersion != api.ProtocolVersion || res.PID != os.Getpid() || res.StorePath != b.store.Path() {
		t.Fatalf("info = %+v", res)
	}
}

// system.storage maps the store's usage and the maintenance loop's state:
// plain, compressed, with attachments on the server, and stopped by a
// full disk.
func TestSystemStorage(t *testing.T) {
	ctx := context.Background()
	empty := storageOf(t, newTestBackend(t, config.Default()))
	checkStorageSums(t, empty)
	if empty.Messages != 0 || empty.MessageBytes != 0 || empty.SavedBytes != 0 || empty.PartialMessages != 0 {
		t.Fatalf("empty store = %+v", empty)
	}

	fastRawLoop(t, time.Millisecond, 5*time.Millisecond, time.Hour)
	m := seedMailbox(t)
	b, acc, id := m.b, string(m.acc), string(m.msgs[0])
	importAttachment(t, b, m.acc, "notes.txt", []byte("0123456789"))

	plain := storageOf(t, b)
	checkStorageSums(t, plain)
	if plain.Messages != 1 || plain.MessageBytes != int64(len(seedRawHTML)) || plain.MessageUncompressedBytes != plain.MessageBytes ||
		plain.CompressedMessages != 0 || plain.SavedBytes != 0 || plain.AttachmentBytes != 10 {
		t.Fatalf("plain store = %+v", plain)
	}
	if plain.Conversion != api.StorageConversionRunning {
		t.Fatalf("before the loop ran: conversion %s, want running", plain.Conversion)
	}

	startRawLoop(t, b, newCodecStep(b))
	waitIdle(t, b)
	p := basePrefs()
	p.CompressStore = api.Ptr(true)
	setPrefs(t, b, p)
	// Running until every message is converted; the loop, on a 1 ms tick
	// here, may already have converted the one message when the test asks,
	// so idle is right then, and only then.
	if got := storageOf(t, b); got.Conversion != api.StorageConversionRunning && got.CompressedMessages != got.Messages {
		t.Fatalf("after compressStore: conversion %s with %d of %d messages compressed, want running", got.Conversion, got.CompressedMessages, got.Messages)
	}
	waitIdle(t, b)
	zst := storageOf(t, b)
	checkStorageSums(t, zst)
	if zst.Messages != 1 || zst.CompressedMessages != 1 || zst.MessageUncompressedBytes != int64(len(seedRawHTML)) ||
		zst.MessageBytes >= zst.MessageUncompressedBytes || zst.SavedBytes <= 0 || zst.Conversion != api.StorageConversionIdle {
		t.Fatalf("compressed store = %+v", zst)
	}

	// A message whose large attachment stays on the server.
	skeleton := []byte("Subject: first\r\n\r\nthe rest stays on the server\r\n")
	staged, err := b.store.StageRaw(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer staged.Remove()
	if _, err := staged.Write(skeleton); err != nil {
		t.Fatal(err)
	}
	if _, err := b.store.CommitMessageRaw(ctx, acc, id, store.RawCommit{Source: staged, RemoteParts: []string{"2"}, RemoteBytes: 12345}); err != nil {
		t.Fatal(err)
	}
	partial := storageOf(t, b)
	checkStorageSums(t, partial)
	if partial.PartialMessages != 1 || partial.RemoteAttachmentBytes != 12345 || partial.MessageUncompressedBytes != int64(len(skeleton)) {
		t.Fatalf("partial store = %+v", partial)
	}

	b.rawNoSpace.Store(true)
	if got := storageOf(t, b); got.Conversion != api.StorageConversionNoSpace {
		t.Fatalf("after a full disk: conversion %s", got.Conversion)
	}
	b.kickRaw()
	eventually(t, "the loop to try again", func() bool { return !b.rawNoSpace.Load() })
	if got := storageOf(t, b); got.Conversion != api.StorageConversionIdle {
		t.Fatalf("after the kick: conversion %s", got.Conversion)
	}
}
