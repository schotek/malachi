// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Download fetches the content of a message that is not stored locally
// (docs/api.md §4.3, message.download). Not implemented yet.
func (s *messageService) Download(ctx context.Context, p api.MessageDownloadParams) (*api.MessageDownloadResult, error) {
	return nil, api.ErrNotImplemented
}
