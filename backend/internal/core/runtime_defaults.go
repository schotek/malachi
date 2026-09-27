// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The environment variables that carry the defaults of the preferences
// added after the others (api.Preferences), read by malachid from the
// process that starts it: the macOS app sets them, other clients do not.
// The name says what the value is; the daemon never branches on a
// platform.
const (
	EnvDefaultCompressStore         = "MALACHI_DEFAULT_COMPRESS_STORE"          // strconv.ParseBool
	EnvDefaultAttachmentOfflineDays = "MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS" // -1, 0 or 1..3650
)

// RuntimeDefaults are defaults chosen when the daemon starts rather than
// built in; nil is none. Precedence: the stored preference, then these,
// then the built-in default (off, 0). StartSync stores each of them whose
// preference has no value yet, so that a daemon started later without
// them keeps what applied (docs/api.md §4.8).
type RuntimeDefaults struct {
	CompressStore         *bool
	AttachmentOfflineDays *int
}

// RuntimeDefaultsFromEnv reads the defaults through lookup (os.LookupEnv).
// An unset or empty variable is no default. An invalid value is left out
// and described in the error; the other variable still counts.
func RuntimeDefaultsFromEnv(lookup func(string) (string, bool)) (RuntimeDefaults, error) {
	var d RuntimeDefaults
	var errs []error
	if v, ok := lookup(EnvDefaultCompressStore); ok && v != "" {
		if on, err := strconv.ParseBool(v); err == nil {
			d.CompressStore = &on
		} else {
			errs = append(errs, fmt.Errorf("%s=%q is not a boolean", EnvDefaultCompressStore, v))
		}
	}
	if v, ok := lookup(EnvDefaultAttachmentOfflineDays); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil && validAttachmentOfflineDays(n) {
			d.AttachmentOfflineDays = &n
		} else {
			errs = append(errs, fmt.Errorf("%s=%q must be %d, 0 or 1–%d", EnvDefaultAttachmentOfflineDays, v,
				api.AttachmentOfflineNone, api.AttachmentOfflineDaysMax))
		}
	}
	return d, errors.Join(errs...)
}

// validAttachmentOfflineDays reports whether n is a valid
// Preferences.AttachmentOfflineDays.
func validAttachmentOfflineDays(n int) bool {
	return n == api.AttachmentOfflineNone || n >= 0 && n <= api.AttachmentOfflineDaysMax
}

// SetRuntimeDefaults installs the defaults (a copy); malachid calls it
// before StartSync. An out-of-range AttachmentOfflineDays is dropped.
func (b *Backend) SetRuntimeDefaults(d RuntimeDefaults) {
	var c RuntimeDefaults
	if d.CompressStore != nil {
		c.CompressStore = api.Ptr(*d.CompressStore)
	}
	if n := d.AttachmentOfflineDays; n != nil {
		if validAttachmentOfflineDays(*n) {
			c.AttachmentOfflineDays = api.Ptr(*n)
		} else {
			b.log.Warn("ignoring an invalid default for attachmentOfflineDays", "value", *n)
		}
	}
	b.runtimeDefaults.Store(&c)
}

// currentRuntimeDefaults returns the installed defaults (none before
// SetRuntimeDefaults). The pointers are shared: never write through them.
func (b *Backend) currentRuntimeDefaults() RuntimeDefaults {
	if d := b.runtimeDefaults.Load(); d != nil {
		return *d
	}
	return RuntimeDefaults{}
}

// materializeRuntimeDefaults stores every runtime default whose preference
// has no value yet, the existing stores' too: from then on the store
// decides, and a daemon started without the environment (from a terminal,
// say) does not change what applied.
func (b *Backend) materializeRuntimeDefaults(ctx context.Context) {
	d := b.currentRuntimeDefaults()
	var prefs []store.Pref
	if d.CompressStore != nil {
		prefs = append(prefs, store.Pref{Key: prefCompressStore, Value: strconv.FormatBool(*d.CompressStore)})
	}
	if d.AttachmentOfflineDays != nil {
		prefs = append(prefs, store.Pref{Key: prefAttachmentOfflineDays, Value: strconv.Itoa(*d.AttachmentOfflineDays)})
	}
	if len(prefs) == 0 {
		return
	}
	stored, err := b.store.InitPreferences(ctx, prefs)
	if err != nil {
		// They still apply through the precedence; the next start retries.
		b.log.Warn("store the default preferences", "err", err)
		return
	}
	for _, p := range prefs {
		if slices.Contains(stored, p.Key) {
			b.log.Info("preference set from its default", "key", p.Key, "value", p.Value)
		}
	}
}
