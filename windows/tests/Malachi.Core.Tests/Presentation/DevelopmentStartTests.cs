// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// DevelopmentStart (Core/Presentation): the port of the GTK client's
// devstart_test.go (TestParseDevStart, TestParseDevStartScenes,
// TestDevFindFolder, TestDevFindRow, TestDevRichestCase,
// TestParseDevInterval), with the steps the Windows port has.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Calendars;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class DevelopmentStartTests
{
    [Fact]
    public void ParsesTheModeSteps()
    {
        var steps = DevelopmentStart.Parse(" mail;board ; calendar;calendar:month;;size=1280x800;quit");
        Assert.Equal(
            [DevelopmentStepKind.Mail, DevelopmentStepKind.Board, DevelopmentStepKind.Calendar, DevelopmentStepKind.Calendar,
                DevelopmentStepKind.Size, DevelopmentStepKind.Quit],
            steps.Select(s => s.Kind));
        Assert.Equal("board", steps[1].Name);
        Assert.Null(steps[1].Style);
        Assert.Null(steps[2].View);
        Assert.Equal(Cal.View.Month, steps[3].View);
        Assert.Equal((1280, 800), (steps[4].Width, steps[4].Height));
        foreach (var bad in new[] { "nope", "calendar:year", "size=12", "size=0x5", "size=axb", "event-editor:new" })
        {
            Assert.Throws<FormatException>(() => DevelopmentStart.Parse("mail;" + bad));
        }
        Assert.Empty(DevelopmentStart.Parse(""));
        Assert.Empty(DevelopmentStart.Parse(null));
    }

    [Fact]
    public void ParsesTheSceneSteps()
    {
        var steps = DevelopmentStart.Parse(
            "group=on;chat=off;folder=Web;row=Q4 planning;board:list:select;board:columns;compose-open;crm-save;shot=C:\\shots\\a b.png;quit");
        DevelopmentStep[] want =
        [
            new() { Kind = DevelopmentStepKind.Group, Name = "group=on", On = true },
            new() { Kind = DevelopmentStepKind.Chat, Name = "chat=off" },
            new() { Kind = DevelopmentStepKind.Folder, Name = "folder=Web", Text = "Web" },
            new() { Kind = DevelopmentStepKind.Row, Name = "row=Q4 planning", Text = "Q4 planning" },
            new() { Kind = DevelopmentStepKind.Board, Name = "board:list:select", Style = BoardStyle.List, Select = true },
            new() { Kind = DevelopmentStepKind.Board, Name = "board:columns", Style = BoardStyle.Columns },
            new() { Kind = DevelopmentStepKind.ComposeOpen, Name = "compose-open" },
            new() { Kind = DevelopmentStepKind.CrmSave, Name = "crm-save" },
            new() { Kind = DevelopmentStepKind.Shot, Name = "shot=C:\\shots\\a b.png", Text = "C:\\shots\\a b.png" },
            new() { Kind = DevelopmentStepKind.Quit, Name = "quit" },
        ];
        Assert.Equal(want, steps);
        foreach (var bad in new[] { "group=yes", "chat=", "row=", "folder= ", "shot=", "board:grid", "board:list:pick", "board:list:select:x", "board:" })
        {
            Assert.Throws<FormatException>(() => DevelopmentStart.Parse("mail;" + bad));
        }
    }

    [Theory]
    [InlineData("", 3.0)]
    [InlineData(null, 3.0)]
    [InlineData("1", 1.0)]
    [InlineData("0.5", 0.5)]
    [InlineData(" 4 ", 4.0)]
    [InlineData("-1", 3.0)]
    [InlineData("x", 3.0)]
    public void ParsesTheInterval(string? value, double seconds) =>
        Assert.Equal(TimeSpan.FromSeconds(seconds), DevelopmentStart.ParseInterval(value));

    [Theory]
    [InlineData("1440x900", 1440, 900)]
    [InlineData(" 800x600 ", 800, 600)]
    [InlineData("1440", 0, 0)]
    [InlineData("0x5", 0, 0)]
    [InlineData("axb", 0, 0)]
    [InlineData("", 0, 0)]
    public void ParsesTheSize(string value, int width, int height)
    {
        var size = DevelopmentStart.ParseSize(value);
        if (width == 0)
        {
            Assert.Null(size);
        }
        else
        {
            Assert.Equal((width, height), size);
        }
    }

    [Fact]
    public void FindsAFolderByItsServerName()
    {
        Account[] accounts = [AccountOf("mail"), AccountOf("jira")];
        var folders = new Dictionary<AccountId, IReadOnlyList<Folder>>
        {
            [new AccountId("jira")] = [FolderOf("w", "jira", "Web")],
            [new AccountId("mail")] = [FolderOf("i", "mail", "INBOX"), FolderOf("s", "mail", "Sent"), FolderOf("x", "mail", "Webinars")],
        };
        foreach (var (text, want) in new[] { ("inbox", "i"), ("sent", "s"), ("Web", "x"), ("web", "x") })
        {
            Assert.Equal(new FolderId(want), DevelopmentStart.FindFolder(accounts, folders, text)?.Id);
        }
        Assert.Equal(new FolderId("w"), DevelopmentStart.FindFolder(accounts[1..], folders, "Web")?.Id);
        Assert.Null(DevelopmentStart.FindFolder(accounts, folders, "Trash"));
    }

    [Fact]
    public void FindsARowByItsSubject()
    {
        ListRow[] rows =
        [
            RowOf("Design Weekly #212"),
            RowOf("Re: Quotation for the warehouse extension"),
            RowOf("Quotation for the warehouse extension"),
        ];
        Assert.Equal(1, DevelopmentStart.FindRow(rows, "quotation"));
        Assert.Equal(0, DevelopmentStart.FindRow(rows, "DESIGN weekly"));
        Assert.Equal(-1, DevelopmentStart.FindRow(rows, "Nabídka"));
    }

    [Fact]
    public void PicksTheCaseWithTheMostText()
    {
        Board.Case[] cases =
        [
            CaseOf("a", "short"),
            CaseOf("b", "a longer one", "and more"),
            CaseOf("c", "twenty characters!!"),
            CaseOf("d"),
        ];
        Assert.Equal(new BoardCaseId("b"), DevelopmentStart.RichestCase(cases));
        Assert.Null(DevelopmentStart.RichestCase([]));
        // Bytes, not characters ("á" is two); the first of equals wins.
        Assert.Equal(new BoardCaseId("a"), DevelopmentStart.RichestCase([CaseOf("a", "ab"), CaseOf("b", "á")]));
        Assert.Equal(new BoardCaseId("b"), DevelopmentStart.RichestCase([CaseOf("a", "a"), CaseOf("b", "á")]));
    }

    private static Account AccountOf(string id) => new()
    {
        Id = new AccountId(id),
        Config = new AccountConfig { Name = id, Email = id + "@example.test" },
        Enabled = true,
        State = new SyncState { AccountId = new AccountId(id), Status = SyncStatus.Idle },
    };

    private static Folder FolderOf(string id, string account, string name) => new()
    {
        Id = new FolderId(id),
        AccountId = new AccountId(account),
        Name = name,
        Path = name,
        Role = FolderRole.None,
        Subscribed = true,
        Selectable = true,
        Synced = true,
        Unread = 0,
        Total = 0,
    };

    private static ListRow RowOf(string subject) => new()
    {
        Key = new ListKey(Message: new MessageId(subject)),
        Message = new MessageSummary
        {
            Id = new MessageId(subject),
            AccountId = new AccountId("mail"),
            FolderId = new FolderId("i"),
            Subject = subject,
            Date = DateTimeOffset.UnixEpoch,
            Snippet = "",
            Flags = [],
            HasAttachments = false,
            Size = 0,
        },
    };

    private static Board.Case CaseOf(string id, params string[] texts) => new()
    {
        Id = new BoardCaseId(id),
        Account = new AccountId("mail"),
        Person = "Ann",
        Date = DateTimeOffset.UnixEpoch,
        Subject = "Offer",
        RuleState = Board.State.You,
        Messages = texts.Length == 0 ? null : [.. texts.Select(t => new Board.CaseMessage { From = "x", Date = DateTimeOffset.UnixEpoch, Text = t })],
    };
}
