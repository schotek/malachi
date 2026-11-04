// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"

	"github.com/schotek/malachi/backend/pkg/api"
)

// systemService answers system.storage; system.info stays the embedded
// stub's.
type systemService struct {
	api.SystemService
	b *Backend
}

func (b *Backend) System() api.SystemService {
	return &systemService{SystemService: b.StubBackend.System(), b: b}
}

// Storage reports how much disk the store takes (store.Usage) and the
// state of the raw maintenance loop; a few aggregate queries, cheap
// enough for the preferences dialog to poll.
func (s *systemService) Storage(ctx context.Context, _ api.SystemStorageParams) (*api.SystemStorageResult, error) {
	u, err := s.b.store.Usage(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	conv, err := s.b.rawConversion(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	return &api.SystemStorageResult{
		TotalBytes:               u.TotalBytes(),
		DatabaseBytes:            u.DatabaseBytes,
		MessageBytes:             u.MessageBytes,
		MessageUncompressedBytes: u.ContentBytes,
		SavedBytes:               u.SavedBytes(),
		AttachmentBytes:          u.AttachmentBytes,
		RemoteAttachmentBytes:    u.RemoteBytes,
		Messages:                 u.Messages,
		CompressedMessages:       u.Compressed,
		PartialMessages:          u.Partial,
		Conversion:               conv,
	}, nil
}
