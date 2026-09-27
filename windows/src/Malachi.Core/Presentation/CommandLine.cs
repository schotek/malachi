// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the activation of a second instance arrives as the
// command line it was started with, one string (AppInstance.Activated,
// LaunchActivatedEventArgs.Arguments), where GApplication and macOS hand
// the first instance a list of arguments. This splits it as the C runtime
// and CommandLineToArgvW split a command line (the rules of "Parsing C++
// command-line arguments"): the first word, the program, by quotes alone;
// every later one by white space, where quotes group, 2n backslashes
// before a quote give n and a delimiting quote, 2n+1 give n and a literal
// quote, other backslashes are literal, and "" inside a quoted part is a
// literal quote. ChildProcess (Malachi.Platform.Windows) quotes by the
// same rules.

using System.Collections.Generic;
using System.Text;

namespace Malachi.Core.Presentation;

/// <summary>Splits a Windows command line into its words.</summary>
public static class CommandLine
{
    /// <summary>
    /// The words of <paramref name="commandLine"/>, the program's first;
    /// empty for an empty or blank line.
    /// </summary>
    public static IReadOnlyList<string> Split(string? commandLine)
    {
        var words = new List<string>();
        if (string.IsNullOrEmpty(commandLine))
        {
            return words;
        }
        var i = 0;
        var n = commandLine.Length;
        while (i < n && IsSpace(commandLine[i]))
        {
            i++;
        }
        if (i == n)
        {
            return words;
        }

        // The program: up to the next white space outside quotes; quotes
        // only group and are dropped, backslashes are literal.
        var program = new StringBuilder();
        var quoted = false;
        for (; i < n; i++)
        {
            var c = commandLine[i];
            if (c == '"')
            {
                quoted = !quoted;
                continue;
            }
            if (!quoted && IsSpace(c))
            {
                break;
            }
            program.Append(c);
        }
        words.Add(program.ToString());

        // The arguments.
        while (true)
        {
            while (i < n && IsSpace(commandLine[i]))
            {
                i++;
            }
            if (i >= n)
            {
                break;
            }
            var word = new StringBuilder();
            quoted = false;
            while (i < n)
            {
                var c = commandLine[i];
                if (c == '\\')
                {
                    var slashes = 0;
                    while (i < n && commandLine[i] == '\\')
                    {
                        slashes++;
                        i++;
                    }
                    if (i < n && commandLine[i] == '"')
                    {
                        word.Append('\\', slashes / 2);
                        if (slashes % 2 == 1)
                        {
                            word.Append('"');
                            i++;
                        }
                    }
                    else
                    {
                        word.Append('\\', slashes);
                    }
                    continue;
                }
                if (c == '"')
                {
                    if (quoted && i + 1 < n && commandLine[i + 1] == '"')
                    {
                        // "" inside quotes: a literal quote.
                        word.Append('"');
                        i += 2;
                        continue;
                    }
                    quoted = !quoted;
                    i++;
                    continue;
                }
                if (!quoted && IsSpace(c))
                {
                    break;
                }
                word.Append(c);
                i++;
            }
            words.Add(word.ToString());
        }
        return words;
    }

    private static bool IsSpace(char c) => c is ' ' or '\t';
}
