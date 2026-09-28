// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/StorageUsage.swift; GTK:
// ui/internal/window/preferences.go (storageTexts). The texts of the Disk
// Space Used row of the preferences.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Text;

namespace Malachi.Core.Model;

/// <summary>The Disk Space Used row's texts for a <c>system.storage</c> answer.</summary>
public static class StorageUsage
{
    /// <summary>
    /// The row's value, the total size of the mail store, and its details,
    /// one line each for a background conversion that runs or stopped, what
    /// compression saves and what is kept on the mail server; a line only
    /// when there is something to say, empty details when nothing applies
    /// (preferences.go <c>storageTexts</c>).
    /// </summary>
    public static (string Value, string Details) StorageTexts(SystemStorageResult r)
    {
        ArgumentNullException.ThrowIfNull(r);
        var lines = new List<string>();
        if (r.Conversion == StorageConversion.Running)
        {
            lines.Add(L10n.T("Converting the stored mail in the background"));
        }
        else if (r.Conversion == StorageConversion.NoSpace)
        {
            lines.Add(L10n.T("Converting stopped: the disk is full"));
        }
        if (r.SavedBytes > 0)
        {
            // TRANSLATORS: %s is a size such as "1.2 GiB".
            lines.Add(L10n.T("Compression saves %s", Format.FormatSize(r.SavedBytes)));
        }
        if (r.RemoteAttachmentBytes > 0)
        {
            // TRANSLATORS: %s is a size such as "1.2 GiB".
            lines.Add(L10n.T("%s of attachments are on the server only", Format.FormatSize(r.RemoteAttachmentBytes)));
        }
        return (Format.FormatSize(r.TotalBytes), string.Join('\n', lines));
    }
}
