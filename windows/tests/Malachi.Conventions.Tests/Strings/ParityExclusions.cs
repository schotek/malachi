// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// windows/parity-exclusions.txt (docs/windows-port.md §9): the msgids of
// the template the Windows client does not use, with the reason; the
// counterpart of the misses research 05 explains for macOS (its Appendix
// A). Format: one msgid per line with C escapes (\n, \t, \", \\, and \004
// between a context and its msgid), a tab, the reason; # starts a comment.

using System;
using System.Collections.Generic;
using System.IO;
using System.Text;

namespace Malachi.Conventions.Tests.Strings;

/// <summary>The exclusion list of the msgid coverage.</summary>
internal static class ParityExclusions
{
    /// <summary>The file.</summary>
    public static string FilePath => Path.Combine(RepositoryTree.Windows, "parity-exclusions.txt");

    /// <summary>The entries: key (the msgid, with its context before U+0004), reason, line.</summary>
    public static IReadOnlyList<(string Key, string Reason, int Line)> Load()
    {
        var result = new List<(string, string, int)>();
        var lines = File.ReadAllLines(FilePath, Encoding.UTF8);
        for (var i = 0; i < lines.Length; i++)
        {
            var line = lines[i];
            if (line.Length == 0 || line[0] == '#')
            {
                continue;
            }
            var tab = line.IndexOf('\t', StringComparison.Ordinal);
            var key = tab < 0 ? line : line[..tab];
            var reason = tab < 0 ? "" : line[(tab + 1)..].Trim();
            result.Add((Unescape(key), reason, i + 1));
        }
        return result;
    }

    /// <summary>The C escapes of the file: \n, \t, \", \\ and \004.</summary>
    public static string Unescape(string s)
    {
        var sb = new StringBuilder(s.Length);
        for (var i = 0; i < s.Length; i++)
        {
            if (s[i] != '\\' || i + 1 >= s.Length)
            {
                sb.Append(s[i]);
                continue;
            }
            var next = s[++i];
            switch (next)
            {
                case 'n':
                    sb.Append('\n');
                    break;
                case 't':
                    sb.Append('\t');
                    break;
                case '0' when i + 2 < s.Length && s[i + 1] == '0' && s[i + 2] == '4':
                    sb.Append((char)4);
                    i += 2;
                    break;
                default:
                    sb.Append(next);
                    break;
            }
        }
        return sb.ToString();
    }
}
