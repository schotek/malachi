// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"testing"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/pkg/api"
)

func TestOfflineDaysPreference(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.Sync.OfflineDays = 90
	b := newTestBackend(t, cfg)

	got, err := b.Config().Get(ctx, api.ConfigGetParams{})
	if err != nil || got.Preferences.OfflineDays != 90 {
		t.Fatalf("toml default: %+v %v", got, err)
	}

	for _, days := range []int{-1, api.OfflineDaysMax + 1} {
		p := api.Preferences{SyncIntervalSeconds: 300, RemoteContent: api.RemoteBlock, OfflineDays: days}
		if _, err := b.Config().Set(ctx, api.ConfigSetParams{Preferences: p}); errCode(t, err) != api.CodeInvalidArgument {
			t.Fatalf("offlineDays %d: %v", days, err)
		}
	}
	for _, days := range []int{0, 1, api.OfflineDaysMax} {
		p := api.Preferences{SyncIntervalSeconds: 300, RemoteContent: api.RemoteBlock, OfflineDays: days}
		if _, err := b.Config().Set(ctx, api.ConfigSetParams{Preferences: p}); err != nil {
			t.Fatalf("offlineDays %d: %v", days, err)
		}
		got, _ = b.Config().Get(ctx, api.ConfigGetParams{})
		if got.Preferences.OfflineDays != days {
			t.Fatalf("stored %d, got %d", days, got.Preferences.OfflineDays)
		}
	}
}
