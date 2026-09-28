// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// CommandLine (Core/Presentation): a redirected activation's command line
// splits as the C runtime and CommandLineToArgvW split it, the cases of
// Microsoft's "Parsing C++ command-line arguments" table included.

using System.Collections.Generic;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class CommandLineTests
{
    public static TheoryData<string, string[]> Cases => new()
    {
        { "", [] },
        { "   ", [] },
        { "app.exe", ["app.exe"] },
        { "\"C:\\Program Files\\Malachi Mail\\MalachiMail.exe\" \"mailto:alice@example.com?subject=Hi%20there\"", ["C:\\Program Files\\Malachi Mail\\MalachiMail.exe", "mailto:alice@example.com?subject=Hi%20there"] },
        // The program's word: quotes only group, backslashes are literal.
        { "C:\\a\\\"b c\\\" d", ["C:\\a\\b c\\", "d"] },
        // The table of the C runtime's rules.
        { "p \"a b c\" d e", ["p", "a b c", "d", "e"] },
        { "p \"ab\\\"c\" \"\\\\\" d", ["p", "ab\"c", "\\", "d"] },
        { "p a\\\\\\b d\"e f\"g h", ["p", "a\\\\\\b", "de fg", "h"] },
        { "p a\\\\\\\"b c d", ["p", "a\\\"b", "c", "d"] },
        { "p a\\\\\\\\\"b c\" d e", ["p", "a\\\\b c", "d", "e"] },
        { "p a\"b\"\" c d", ["p", "ab\" c d"] },
        { "p\t--background  ----AppNotificationActivated: -Embedding", ["p", "--background", "----AppNotificationActivated:", "-Embedding"] },
        { "p \"\"", ["p", ""] },
    };

    [Theory]
    [MemberData(nameof(Cases))]
    public void Splits(string line, string[] expected) => Assert.Equal<IEnumerable<string>>(expected, CommandLine.Split(line));

    [Fact]
    public void NullIsEmpty() => Assert.Empty(CommandLine.Split(null));
}
