// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the user's own switch for a Run entry, which Windows
// Settings (Apps → Startup) and Task Manager keep under
// HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run
// as a REG_BINARY value of the entry's name. Read on the development
// machine (APP-SPIKES §6): 12 bytes, byte 0 even for enabled (02, 06) and
// odd for disabled (03, 07), bytes 1 to 3 zero, bytes 4 to 11 the FILETIME
// (UTC) of the change to disabled, which may be zero. An entry without an
// approval value is enabled (never toggled).

using System;

namespace Malachi.Platform.Windows.Startup;

/// <summary>A decoded <c>StartupApproved\Run</c> value.</summary>
/// <param name="Enabled">Whether Windows starts the entry at sign-in.</param>
/// <param name="DisabledAt">When the user disabled it (UTC), if Windows recorded it.</param>
public readonly record struct StartupApproval(bool Enabled, DateTime? DisabledAt)
{
    /// <summary>
    /// The approval in <paramref name="value"/>; null when there is none (no
    /// value, or an empty or unreadable one), which Windows treats as
    /// enabled.
    /// </summary>
    public static StartupApproval? Decode(object? value)
    {
        if (value is not byte[] { Length: > 0 } bytes)
        {
            return null;
        }
        var enabled = (bytes[0] & 1) == 0;
        DateTime? at = null;
        if (!enabled && bytes.Length >= 12)
        {
            var filetime = BitConverter.ToInt64(bytes, 4);
            if (filetime > 0 && filetime <= DateTime.MaxValue.ToFileTimeUtc())
            {
                at = DateTime.FromFileTimeUtc(filetime);
            }
        }
        return new StartupApproval(enabled, at);
    }
}
