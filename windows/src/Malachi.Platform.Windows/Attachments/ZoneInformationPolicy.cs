// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the Attachment Manager policy "Do not preserve zone
// information in file attachments" (SaveZoneInformation), which an
// administrator sets to keep the Mark of the Web off every file; the client
// then writes none of its own either.

using System;
using System.Security;
using Microsoft.Win32;

namespace Malachi.Platform.Windows.Attachments;

/// <summary>Reads whether zone information is turned off by policy.</summary>
internal static class ZoneInformationPolicy
{
    private const string Key = @"Software\Microsoft\Windows\CurrentVersion\Policies\Attachments";
    private const string Value = "SaveZoneInformation";

    // 1 = do not preserve zone information; 2 or nothing = preserve it.
    private const int DoNotPreserve = 1;

    /// <summary>
    /// Whether the policy is set to 1 for the user or the machine; read
    /// afresh each time, as a policy refresh may change it.
    /// </summary>
    public static bool Disabled() => Read(Registry.CurrentUser) || Read(Registry.LocalMachine);

    private static bool Read(RegistryKey hive)
    {
        try
        {
            using var key = hive.OpenSubKey(Key);
            return key?.GetValue(Value) is int value && value == DoNotPreserve;
        }
        catch (Exception e) when (e is SecurityException or UnauthorizedAccessException or System.IO.IOException)
        {
            return false;
        }
    }
}
