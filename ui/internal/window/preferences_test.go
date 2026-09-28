// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The Disk Space Used row: the total as the value, one line of details
// per thing worth saying. i18n is not bound in tests, so the English
// msgids come back verbatim.
func TestStorageTexts(t *testing.T) {
	cases := []struct {
		name           string
		r              api.SystemStorageResult
		value, details string
	}{
		{"empty store", api.SystemStorageResult{}, "0 B", ""},
		{"uncompressed, everything local",
			api.SystemStorageResult{TotalBytes: 700 << 20, MessageBytes: 650 << 20, MessageUncompressedBytes: 650 << 20},
			"700.0 MiB", ""},
		{"compressed",
			api.SystemStorageResult{TotalBytes: 3 << 30, SavedBytes: 512 << 20},
			"3.0 GiB", "Compression saves 512.0 MiB"},
		{"attachments on the server",
			api.SystemStorageResult{TotalBytes: 3 << 30, RemoteAttachmentBytes: 2 << 30},
			"3.0 GiB", "2.0 GiB of attachments are on the server only"},
		{"both",
			api.SystemStorageResult{TotalBytes: 1<<30 + 512<<20, SavedBytes: 300 << 10, RemoteAttachmentBytes: 40 << 20},
			"1.5 GiB", "Compression saves 300 KiB\n40.0 MiB of attachments are on the server only"},
		{"no saving to speak of", api.SystemStorageResult{TotalBytes: 5, SavedBytes: -3}, "5 B", ""},
		{"converting",
			api.SystemStorageResult{TotalBytes: 3 << 30, SavedBytes: 512 << 20, Conversion: api.StorageConversionRunning},
			"3.0 GiB", "Converting the stored mail in the background\nCompression saves 512.0 MiB"},
		{"disk full",
			api.SystemStorageResult{TotalBytes: 3 << 30, Conversion: api.StorageConversionNoSpace},
			"3.0 GiB", "Converting stopped: the disk is full"},
		{"idle says nothing", api.SystemStorageResult{TotalBytes: 5, Conversion: api.StorageConversionIdle}, "5 B", ""},
	}
	for _, c := range cases {
		value, details := storageTexts(c.r)
		if value != c.value || details != c.details {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, value, details, c.value, c.details)
		}
	}
}

// The Keep Attachments Offline For row is insensitive only while the
// daemon confirms that attachments are never stored.
func TestAttachmentDaysApply(t *testing.T) {
	cases := []struct {
		name string
		p    api.Preferences
		want bool
	}{
		{"older daemon, field absent", api.Preferences{AttachmentOfflineDays: api.Ptr(30)}, true},
		{"never store off", api.Preferences{AttachmentOfflineDays: api.Ptr(30), NeverStoreAttachments: api.Ptr(false)}, true},
		{"never store on", api.Preferences{AttachmentOfflineDays: api.Ptr(30), NeverStoreAttachments: api.Ptr(true)}, false},
		{"on, days unknown", api.Preferences{NeverStoreAttachments: api.Ptr(true)}, false},
	}
	for _, c := range cases {
		if got := attachmentDaysApply(c.p); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
