// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"strconv"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Preference keys in the store. Owned by this package.
const (
	prefSyncInterval          = "sync.interval_seconds"
	prefRemoteContent         = "remote_content"
	prefOfflineDays           = "sync.offline_days"
	prefCompressStore         = "store.compress"
	prefAttachmentOfflineDays = "attachments.offline_days"
	prefNeverStoreAttachments = "attachments.never_store"
)

type configService struct{ b *Backend }

// Get returns the effective preferences: stored value, else config.toml or
// the runtime default, else the built-in default.
func (s *configService) Get(ctx context.Context, _ api.ConfigGetParams) (*api.ConfigGetResult, error) {
	p, err := s.b.preferences(ctx)
	if err != nil {
		return nil, err
	}
	return &api.ConfigGetResult{Preferences: p}, nil
}

// Set validates and stores the preference set in one transaction (an
// absent pointer field stays as it is), then applies the effective values:
// the syncers wake so intervals and the retention window take effect, new
// raw files take the codec, and the raw maintenance loop re-evaluates what
// stored mail has to change. The result has every field set.
func (s *configService) Set(ctx context.Context, p api.ConfigSetParams) (*api.ConfigSetResult, error) {
	in := p.Preferences
	if err := ValidatePreferences(in); err != nil {
		return nil, err
	}
	prefs := []store.Pref{
		{Key: prefSyncInterval, Value: strconv.Itoa(in.SyncIntervalSeconds)},
		{Key: prefRemoteContent, Value: string(in.RemoteContent)},
		{Key: prefOfflineDays, Value: strconv.Itoa(in.OfflineDays)},
	}
	if in.CompressStore != nil {
		prefs = append(prefs, store.Pref{Key: prefCompressStore, Value: strconv.FormatBool(*in.CompressStore)})
	}
	if in.AttachmentOfflineDays != nil {
		prefs = append(prefs, store.Pref{Key: prefAttachmentOfflineDays, Value: strconv.Itoa(*in.AttachmentOfflineDays)})
	}
	if in.NeverStoreAttachments != nil {
		prefs = append(prefs, store.Pref{Key: prefNeverStoreAttachments, Value: strconv.FormatBool(*in.NeverStoreAttachments)})
	}
	s.b.prefMu.Lock()
	defer s.b.prefMu.Unlock()
	prev, err := s.b.preferences(ctx)
	if err != nil {
		prev = api.Preferences{} // unknown: whatever is set counts as a change
	}
	if err := s.b.store.SetPreferences(ctx, prefs); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	eff, err := s.b.preferences(ctx)
	if err != nil {
		// Stored, but not readable back: wake what reads them itself and
		// keep the codec as it is. What memory holds goes all the same
		// when the attachments are to be stored again, and the attachment
		// pass starts over when their preference was set.
		if off := in.NeverStoreAttachments; off != nil && !*off {
			s.b.mem.clear()
		}
		if in.NeverStoreAttachments != nil {
			s.b.restartRawStep(ctx, attachmentStepName)
		}
		s.b.Supervisor.Reload()
		s.b.kickRaw()
		return nil, err
	}
	s.b.preferencesChanged(prev, eff)
	return &api.ConfigSetResult{Preferences: eff}, nil
}

// ValidatePreferences returns invalidArgument for values the daemon does not
// accept. An absent (nil) pointer field is valid.
func ValidatePreferences(p api.Preferences) error {
	if p.SyncIntervalSeconds != 0 && p.SyncIntervalSeconds < api.SyncIntervalMin {
		return api.NewError(api.CodeInvalidArgument,
			"syncIntervalSeconds must be 0 (manual) or at least %d", api.SyncIntervalMin)
	}
	switch p.RemoteContent {
	case api.RemoteBlock, api.RemoteKnownSenders, api.RemoteAllow:
	default:
		return api.NewError(api.CodeInvalidArgument, "remoteContent must be block, knownSenders or allow")
	}
	if p.OfflineDays < 0 || p.OfflineDays > api.OfflineDaysMax {
		return api.NewError(api.CodeInvalidArgument,
			"offlineDays must be 0 (everything) or 1–%d", api.OfflineDaysMax)
	}
	if n := p.AttachmentOfflineDays; n != nil && !validAttachmentOfflineDays(*n) {
		return api.NewError(api.CodeInvalidArgument,
			"attachmentOfflineDays must be %d (small attachments only), 0 (everything) or 1–%d",
			api.AttachmentOfflineNone, api.AttachmentOfflineDaysMax)
	}
	return nil
}

// preferences resolves the effective values, every field set. A stored
// value that does not parse (a newer daemon's, say) counts as unset.
func (b *Backend) preferences(ctx context.Context) (api.Preferences, error) {
	rd := b.currentRuntimeDefaults()
	p := api.Preferences{
		SyncIntervalSeconds:   b.defaults.Sync.IntervalSeconds,
		RemoteContent:         api.RemoteBlock,
		OfflineDays:           b.defaults.Sync.OfflineDays,
		CompressStore:         api.Ptr(false),
		AttachmentOfflineDays: api.Ptr(0),
		NeverStoreAttachments: api.Ptr(false),
	}
	if rd.CompressStore != nil {
		p.CompressStore = api.Ptr(*rd.CompressStore)
	}
	if rd.AttachmentOfflineDays != nil {
		p.AttachmentOfflineDays = api.Ptr(*rd.AttachmentOfflineDays)
	}
	get := func(key string) (string, bool, error) {
		v, ok, err := b.store.GetPreference(ctx, key)
		if err != nil {
			return "", false, api.NewError(api.CodeStorageError, "%v", err)
		}
		return v, ok, nil
	}
	if v, ok, err := get(prefSyncInterval); err != nil {
		return p, err
	} else if ok {
		if n, err := strconv.Atoi(v); err == nil {
			p.SyncIntervalSeconds = n
		}
	}
	if v, ok, err := get(prefRemoteContent); err != nil {
		return p, err
	} else if ok {
		// Unknown stored values (from a newer daemon, say) fall back to block.
		if pol := api.RemoteContentPolicy(v); ValidatePreferences(api.Preferences{RemoteContent: pol}) == nil {
			p.RemoteContent = pol
		}
	}
	if v, ok, err := get(prefOfflineDays); err != nil {
		return p, err
	} else if ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= api.OfflineDaysMax {
			p.OfflineDays = n
		}
	}
	if v, ok, err := get(prefCompressStore); err != nil {
		return p, err
	} else if ok {
		if on, err := strconv.ParseBool(v); err == nil {
			p.CompressStore = api.Ptr(on)
		}
	}
	if v, ok, err := get(prefAttachmentOfflineDays); err != nil {
		return p, err
	} else if ok {
		if n, err := strconv.Atoi(v); err == nil && validAttachmentOfflineDays(n) {
			p.AttachmentOfflineDays = api.Ptr(n)
		}
	}
	// No runtime default: off unless config.set said otherwise.
	if v, ok, err := get(prefNeverStoreAttachments); err != nil {
		return p, err
	} else if ok {
		if on, err := strconv.ParseBool(v); err == nil {
			p.NeverStoreAttachments = api.Ptr(on)
		}
	}
	return p, nil
}

// preferencesChanged applies new effective preferences, eff, over the ones
// before, prev (fields nil when unknown): new raw files take the codec,
// every syncer wakes so a new interval or retention window takes effect
// without waiting for the next pass, and the raw maintenance loop
// re-evaluates its steps (a conversion, the attachments to keep). With
// NeverStoreAttachments off, the messages held in memory go (memCache) and
// the next switch-on judges the settled messages again
// (attachmentStep.reevaluateOnce). A change of NeverStoreAttachments
// either way starts the attachment pass from the start, even when a pass
// under the same key finished before (switched off and on again the same
// day).
func (b *Backend) preferencesChanged(prev, eff api.Preferences) {
	b.applyRawCodec(eff)
	on := eff.NeverStoreAttachments != nil && *eff.NeverStoreAttachments
	if !on {
		b.mem.clear()
		b.forgetReevaluation(context.Background())
	}
	if was := prev.NeverStoreAttachments; was == nil || *was != on {
		b.restartRawStep(context.Background(), attachmentStepName)
	}
	b.Supervisor.Reload()
	b.kickRaw()
}

// applyStoredPreferences runs at start (StartSync): it stores the runtime
// defaults that have no preference yet and sets the codec of new raw files,
// plain when the preferences cannot be read.
func (b *Backend) applyStoredPreferences(ctx context.Context) {
	b.prefMu.Lock()
	defer b.prefMu.Unlock()
	b.materializeRuntimeDefaults(ctx)
	p, err := b.preferences(ctx)
	if err != nil {
		b.log.Warn("read preferences: raw messages are stored uncompressed", "err", err)
		p = api.Preferences{}
	}
	b.applyRawCodec(p)
}

// applyRawCodec sets the codec the store writes new raw files in from the
// effective Preferences.CompressStore (plain when it is not set).
func (b *Backend) applyRawCodec(p api.Preferences) {
	c := store.RawPlain
	if p.CompressStore != nil && *p.CompressStore {
		c = store.RawZstd
	}
	if b.store.RawCodec() != c {
		b.log.Info("new raw message files use a new codec", "codec", c.String())
	}
	b.store.SetRawCodec(c)
}

// attachmentPolicy is the effective Preferences.AttachmentOfflineDays and
// NeverStoreAttachments the syncers, the attachment step and
// message.download apply. When the store cannot be read it keeps every
// attachment: a doubt never removes data.
func (b *Backend) attachmentPolicy() ingest.Policy {
	p, err := b.preferences(context.Background())
	if err != nil {
		b.log.Warn("read the attachment preferences", "err", err)
		return ingest.Policy{}
	}
	return ingest.Policy{AttachmentOfflineDays: *p.AttachmentOfflineDays, NeverStore: *p.NeverStoreAttachments}
}

// attachmentOfflineDays is the effective
// Preferences.AttachmentOfflineDays (attachmentPolicy).
func (b *Backend) attachmentOfflineDays() int { return b.attachmentPolicy().AttachmentOfflineDays }

// neverStoreAttachments is the effective
// Preferences.NeverStoreAttachments (attachmentPolicy).
func (b *Backend) neverStoreAttachments() bool { return b.attachmentPolicy().NeverStore }
