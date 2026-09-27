// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/compose/draft_test.go TestFlushEcho (the Swift port
// has no counterpart: macOS keeps the rule in its AppKit window).

using Malachi.Core.Html;
using Xunit;

namespace Malachi.Core.Tests.Html;

public sealed class FlushEchoTests
{
    [Fact]
    public void TestFlushEcho()
    {
        var f = new FlushEcho();
        Assert.False(f.Echo(""), "nothing recorded: an empty body is an edit, not an echo");

        f.Record("");
        Assert.True(f.Echo(""), "an empty body flushed: its changed is the echo");

        f.Record("<p>a</p>");
        Assert.True(f.Echo("<p>a</p>"), "the flush's own changed must be an echo");
        Assert.True(f.Echo("<p>a</p>"), "a late debounced changed with the same content must stay an echo");
        Assert.False(f.Echo("<p>ab</p>"), "new content is an edit");
        Assert.False(f.Echo("<p>a</p>"), "an edit forgets the record: going back to the saved text is an edit too");
    }
}
