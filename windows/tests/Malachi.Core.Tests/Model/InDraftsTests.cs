// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/DraftsTests.swift (inDraftsTest), the
// counterpart of ui/internal/window/drafts_test.go (TestInDrafts). The
// suite's other test, draftOpenErrorsTest, belongs to the port of
// Model/DraftOpen.swift, which is not part of the mailbox model.

using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Tests.Model.MailModelTests;
using FolderMap = System.Collections.Generic.Dictionary<Malachi.Core.Api.AccountId, System.Collections.Generic.IReadOnlyList<Malachi.Core.Api.Folder>>;

namespace Malachi.Core.Tests.Model;

public sealed class InDraftsTests
{
    [Fact]
    public void InDraftsTest()
    {
        var m = new MailModel(
            [TestAccount("a")],
            new FolderMap
            {
                ["a"] = [TestFolder("in", "INBOX", FolderRole.Inbox), TestFolder("dr", "Drafts", FolderRole.Drafts)],
            });
        var inbox = Summary("1") with { AccountId = "a", FolderId = "in" };
        Assert.False(m.InDrafts(inbox), "inbox message reported as a draft");
        var draft = Summary("2") with { AccountId = "a", FolderId = "dr" };
        Assert.True(m.InDrafts(draft), "message of the Drafts folder not recognised");
        // The folder of another account with the same id is not this one.
        var other = Summary("3") with { AccountId = "b", FolderId = "dr" };
        Assert.False(m.InDrafts(other), "draft of an unknown account");
    }
}
