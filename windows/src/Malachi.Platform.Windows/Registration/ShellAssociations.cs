// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: what the shell makes of the mailto: registration
// (docs/windows-port.md §10). The shell caches associations, so a change to
// them ends with SHChangeNotify(SHCNE_ASSOCCHANGED); the current default
// handler of a protocol is AssocQueryString(ASSOCF_IS_PROTOCOL,
// ASSOCSTR_PROGID); the handlers Windows offers for it (Settings → Default
// apps, "How do you want to open this?") are
// SHAssocEnumHandlersForProtocolByApplication (all three measured in
// APP-SPIKES §7).

using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.UI.Shell;

namespace Malachi.Platform.Windows.Registration;

/// <summary>The shell's view of protocol associations.</summary>
public static class ShellAssociations
{
    /// <summary>Tells the shell that associations changed, so that it reads them again.</summary>
    public static unsafe void NotifyChanged() =>
        PInvoke.SHChangeNotify(SHCNE_ID.SHCNE_ASSOCCHANGED, SHCNF_FLAGS.SHCNF_IDLIST, null, null);

    /// <summary>
    /// The ProgID of the user's default handler of <paramref name="protocol"/>
    /// (<c>mailto</c>); null when there is none or the shell cannot say.
    /// </summary>
    public static string? DefaultProgId(string protocol) =>
        Query(ASSOCF.ASSOCF_IS_PROTOCOL, ASSOCSTR.ASSOCSTR_PROGID, protocol, null);

    /// <summary>
    /// The command the shell runs for <paramref name="progId"/>'s open verb;
    /// null when it has none.
    /// </summary>
    public static string? OpenCommand(string progId) =>
        Query(ASSOCF.ASSOCF_NONE, ASSOCSTR.ASSOCSTR_COMMAND, progId, "open");

    /// <summary>
    /// The name the shell shows for <paramref name="progId"/>'s application;
    /// null when it has none.
    /// </summary>
    public static string? FriendlyAppName(string progId) =>
        Query(ASSOCF.ASSOCF_NONE, ASSOCSTR.ASSOCSTR_FRIENDLYAPPNAME, progId, "open");

    /// <summary>
    /// The handlers Windows offers for <paramref name="protocol"/>, as their
    /// names (the executable or ProgID) and the names it shows.
    /// </summary>
    public static unsafe IReadOnlyList<(string Name, string UIName)> Handlers(string protocol)
    {
        ArgumentException.ThrowIfNullOrEmpty(protocol);
        var handlers = new List<(string, string)>();
        var hr = PInvoke.SHAssocEnumHandlersForProtocolByApplication(protocol, out IEnumAssocHandlers enumerator);
        if (hr.Failed)
        {
            return handlers;
        }
        try
        {
            var one = new IAssocHandler[1];
            while (true)
            {
                uint fetched = 0;
                enumerator.Next(1, one, &fetched);
                if (fetched == 0)
                {
                    break;
                }
                var handler = one[0];
                try
                {
                    PWSTR name;
                    PWSTR uiName;
                    handler.GetName(&name);
                    handler.GetUIName(&uiName);
                    handlers.Add((Take(name), Take(uiName)));
                }
                finally
                {
                    Marshal.ReleaseComObject(handler);
                }
            }
        }
        finally
        {
            Marshal.ReleaseComObject(enumerator);
        }
        return handlers;
    }

    private static unsafe string Take(PWSTR s)
    {
        var value = s.ToString();
        Marshal.FreeCoTaskMem((nint)s.Value);
        return value;
    }

    private static string? Query(ASSOCF flags, ASSOCSTR what, string assoc, string? extra)
    {
        ArgumentException.ThrowIfNullOrEmpty(assoc);
        uint length = 0;
        var hr = PInvoke.AssocQueryString(flags | ASSOCF.ASSOCF_NOTRUNCATE, what, assoc, extra, default, ref length);
        // S_FALSE: only the length was asked for.
        if (hr.Failed || length == 0)
        {
            return null;
        }
        var buffer = new char[length];
        hr = PInvoke.AssocQueryString(flags | ASSOCF.ASSOCF_NOTRUNCATE, what, assoc, extra, buffer, ref length);
        if (hr.Failed)
        {
            return null;
        }
        var end = Array.IndexOf(buffer, '\0');
        return new string(buffer, 0, end < 0 ? (int)Math.Min(length, (uint)buffer.Length) : end);
    }
}
