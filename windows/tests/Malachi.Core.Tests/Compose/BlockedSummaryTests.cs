// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BlockedSummaryTests.swift, the
// counterpart of ui/internal/compose/draft_test.go TestBlockedSummary and
// TestSkippedSummary (English catalogue).

using Malachi.Core.Api;
using Malachi.Core.Compose;
using Xunit;

namespace Malachi.Core.Tests.Compose;

public sealed class BlockedSummaryTests
{
    [Fact]
    public void BlockedSummaryText()
    {
        Assert.Equal("", BlockedSummary.Text(new BlockedContent()));
        Assert.Equal("3 unsafe elements were removed from the message", BlockedSummary.Text(new BlockedContent { RemoteImages = 2, Scripts = 1 }));
        Assert.Equal("1 unsafe element was removed from the message", BlockedSummary.Text(new BlockedContent { Forms = 1 }));
        // Every counter is summed.
        var all = new BlockedContent
        {
            RemoteImages = 1,
            RemoteStyles = 1,
            RemoteFonts = 1,
            Scripts = 1,
            Forms = 1,
            EventHandlers = 1,
            DangerousUrls = 1,
            EmbeddedFrames = 1,
            TrackingPixels = 1,
        };
        Assert.Equal("9 unsafe elements were removed from the message", BlockedSummary.Text(all));
    }

    [Fact]
    public void SkippedSummaryText()
    {
        Assert.Equal("", BlockedSummary.Skipped(0));
        Assert.Equal("", BlockedSummary.Skipped(-1));
        Assert.Equal("1 attachment of the original could not be attached", BlockedSummary.Skipped(1));
        Assert.Equal("3 attachments of the original could not be attached", BlockedSummary.Skipped(3));
    }
}
