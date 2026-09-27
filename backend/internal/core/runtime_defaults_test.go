// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/pkg/api"
)

// fakeEnv is a lookup over a fixed environment.
func fakeEnv(vars map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	}
}

func TestRuntimeDefaultsFromEnv(t *testing.T) {
	cases := []struct {
		name     string
		vars     map[string]string
		compress *bool
		days     *int
		bad      []string // variables the error must name
	}{
		{name: "unset", vars: nil},
		{name: "empty is unset", vars: map[string]string{EnvDefaultCompressStore: "", EnvDefaultAttachmentOfflineDays: ""}},
		{name: "the macOS app", vars: map[string]string{EnvDefaultCompressStore: "1", EnvDefaultAttachmentOfflineDays: "30"},
			compress: api.Ptr(true), days: api.Ptr(30)},
		{name: "words", vars: map[string]string{EnvDefaultCompressStore: "false"}, compress: api.Ptr(false)},
		{name: "true", vars: map[string]string{EnvDefaultCompressStore: "true"}, compress: api.Ptr(true)},
		{name: "zero", vars: map[string]string{EnvDefaultCompressStore: "0", EnvDefaultAttachmentOfflineDays: "0"},
			compress: api.Ptr(false), days: api.Ptr(0)},
		{name: "small attachments only", vars: map[string]string{EnvDefaultAttachmentOfflineDays: "-1"}, days: api.Ptr(api.AttachmentOfflineNone)},
		{name: "the maximum", vars: map[string]string{EnvDefaultAttachmentOfflineDays: "3650"}, days: api.Ptr(api.AttachmentOfflineDaysMax)},
		{name: "not a boolean", vars: map[string]string{EnvDefaultCompressStore: "yes", EnvDefaultAttachmentOfflineDays: "7"},
			days: api.Ptr(7), bad: []string{EnvDefaultCompressStore}},
		{name: "out of range", vars: map[string]string{EnvDefaultCompressStore: "1", EnvDefaultAttachmentOfflineDays: "3651"},
			compress: api.Ptr(true), bad: []string{EnvDefaultAttachmentOfflineDays}},
		{name: "below -1", vars: map[string]string{EnvDefaultAttachmentOfflineDays: "-2"}, bad: []string{EnvDefaultAttachmentOfflineDays}},
		{name: "not a number", vars: map[string]string{EnvDefaultAttachmentOfflineDays: "30d"}, bad: []string{EnvDefaultAttachmentOfflineDays}},
		{name: "padded", vars: map[string]string{EnvDefaultCompressStore: " 1", EnvDefaultAttachmentOfflineDays: "30 "},
			bad: []string{EnvDefaultCompressStore, EnvDefaultAttachmentOfflineDays}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := RuntimeDefaultsFromEnv(fakeEnv(c.vars))
			if (d.CompressStore == nil) != (c.compress == nil) || d.CompressStore != nil && *d.CompressStore != *c.compress {
				t.Errorf("compressStore = %v, want %v", d.CompressStore, c.compress)
			}
			if (d.AttachmentOfflineDays == nil) != (c.days == nil) || d.AttachmentOfflineDays != nil && *d.AttachmentOfflineDays != *c.days {
				t.Errorf("attachmentOfflineDays = %v, want %v", d.AttachmentOfflineDays, c.days)
			}
			if len(c.bad) == 0 {
				if err != nil {
					t.Errorf("err = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("no error")
			}
			for _, name := range c.bad {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("error %q does not name %s", err, name)
				}
			}
		})
	}
}

// SetRuntimeDefaults keeps a copy and drops what config.set would refuse.
func TestSetRuntimeDefaults(t *testing.T) {
	b := newTestBackend(t, config.Default())
	d := RuntimeDefaults{CompressStore: api.Ptr(true), AttachmentOfflineDays: api.Ptr(30)}
	b.SetRuntimeDefaults(d)
	*d.CompressStore, *d.AttachmentOfflineDays = false, 90
	if got := getPrefs(t, b); !*got.CompressStore || *got.AttachmentOfflineDays != 30 {
		t.Fatalf("the caller's pointers leaked in: %s", prefString(got))
	}

	b.SetRuntimeDefaults(RuntimeDefaults{CompressStore: api.Ptr(true), AttachmentOfflineDays: api.Ptr(api.AttachmentOfflineDaysMax + 1)})
	got := getPrefs(t, b)
	if !*got.CompressStore || *got.AttachmentOfflineDays != 0 {
		t.Fatalf("an invalid default applied: %s", prefString(got))
	}
	if d := b.currentRuntimeDefaults(); d.AttachmentOfflineDays != nil {
		t.Fatalf("invalid default kept: %d", *d.AttachmentOfflineDays)
	}
}
