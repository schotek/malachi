// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the look-alikes of the reserved characters in this
// machine's ANSI code page, for Malachi.Core.Platform.WindowsFileNames
// (Sanitize, FileName) and OpenDir (docs/windows-port.md §10). A program
// that reads its command line in the ANSI code page gets the path of the
// file it opens through Windows' best-fit conversion, and a '"' that comes
// back splits the command line (WorstFit). WindowsFileNames always
// replaces what code page 1252 maps to a reserved character and every
// look-alike of '"'; this adds what the machine's own code page maps on top
// (¥ in 932, ↕ ► ♂ in 1250, ´ in 1253, …), characters that are ordinary in
// names wherever the code page is another.

using System;
using System.Collections.Frozen;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Security;
using Microsoft.Win32;
using Windows.Win32;
using Windows.Win32.Foundation;

namespace Malachi.Platform.Windows.Files;

/// <summary>
/// The characters that the best-fit conversion into an ANSI code page turns
/// into one of <c>&lt; &gt; : " / \ | ? *</c>.
/// </summary>
public static class AnsiLookAlikes
{
    /// <summary>The characters Windows keeps out of file names.</summary>
    public const string Reserved = "<>:\"/\\|?*";

    private const string CodePageKey = @"SYSTEM\CurrentControlSet\Control\Nls\CodePage";

    private static readonly Lazy<FrozenSet<char>> Machine = new(
        () => CodePagesOfThisMachine().SelectMany(cp => BestFits(cp).Keys).ToFrozenSet());

    /// <summary>
    /// The look-alikes of this machine's ANSI code pages
    /// (<see cref="CodePagesOfThisMachine"/>), for
    /// <c>WindowsFileNames.Sanitize</c>; worked out once, in a few
    /// milliseconds per code page.
    /// </summary>
    public static IReadOnlySet<char> OfThisMachine => Machine.Value;

    /// <summary>
    /// The code pages the programs of this machine read their command line
    /// in: that of this process (<c>GetACP</c>) and the system's, which are
    /// the same unless a manifest opts a process into UTF-8.
    /// </summary>
    public static IReadOnlyList<int> CodePagesOfThisMachine()
    {
        var pages = new List<int> { (int)PInvoke.GetACP() };
        var system = SystemCodePage();
        if (system is int page && !pages.Contains(page))
        {
            pages.Add(page);
        }
        return pages;
    }

    /// <summary>
    /// Every character above U+007F that <paramref name="codePage"/> turns
    /// into one of <see cref="Reserved"/> by best fit, with the one it
    /// becomes; not what it cannot map at all (that becomes the default
    /// character, '?', and is not a look-alike). Empty for a code page
    /// without best fit (UTF-8) or one this machine does not have.
    /// </summary>
    public static unsafe IReadOnlyDictionary<char, char> BestFits(int codePage)
    {
        var fits = new Dictionary<char, char>();
        var bytes = stackalloc byte[8];
        for (var c = 0x80; c <= 0xFFFF; c++)
        {
            if (char.IsSurrogate((char)c))
            {
                continue;
            }
            var ch = (char)c;
            BOOL usedDefault = false;
            // Flags 0: best fit on, as for a program's command line.
            var n = PInvoke.WideCharToMultiByte((uint)codePage, 0, &ch, 1, new PSTR(bytes), 8, default, &usedDefault);
            if (n == 1 && !usedDefault && Reserved.Contains((char)bytes[0], StringComparison.Ordinal))
            {
                fits[ch] = (char)bytes[0];
            }
        }
        return fits.ToFrozenDictionary();
    }

    // The system's ANSI code page (Nls\CodePage ACP), null where it cannot
    // be read.
    private static int? SystemCodePage()
    {
        try
        {
            using var key = Registry.LocalMachine.OpenSubKey(CodePageKey);
            return key?.GetValue("ACP") is string value
                && int.TryParse(value, NumberStyles.None, CultureInfo.InvariantCulture, out var page)
                ? page
                : null;
        }
        catch (Exception e) when (e is SecurityException or UnauthorizedAccessException or System.IO.IOException)
        {
            return null;
        }
    }
}
