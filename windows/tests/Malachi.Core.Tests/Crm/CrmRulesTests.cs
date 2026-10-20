// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CRMTests.swift, the counterpart of
// ui/internal/crm/crm_test.go (TestAvailable, TestCanSave, TestOfferFor,
// TestButton, TestIsSaved, TestInFlight, TestErrorData, TestURLsAndNames,
// TestConversationRowOffersNothing). The process-wide catalogue is English
// here, so every msgid is its own translation. Go's error data as the RPC
// client decodes it (a map) is the JSON of RpcError.Data here; Go's typed
// data (a struct) is the same JSON.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Crm;
using Malachi.Core.Tests.Api;
using Malachi.Core.Transport;
using Xunit;
using static Malachi.Core.Tests.Crm.CrmFixture;

namespace Malachi.Core.Tests.Crm;

public sealed class CrmRulesTests
{
    // The first enabled eway account in list order (D11, §5 Q12).
    [Fact]
    public void Available()
    {
        Account[] all = [Imap, EwayOff, JiraAcc, EwayA, EwayB];
        Assert.Equal(["e1", "e2"], CrmRules.Accounts(all).Select(a => a.Id.Value));
        Assert.Equal("e1", CrmRules.Available(all)?.Id.Value);
        Assert.Null(CrmRules.Available([Imap, EwayOff]));
        Assert.Null(CrmRules.Available([]));
    }

    public static TheoryData<string, Account, string, bool, bool> CanSaveCases => new()
    {
        { "inbox of imap", Imap, FolderRole.Inbox, false, true },
        { "folder of graph", Graph, FolderRole.None, false, true },
        { "sent", Imap, FolderRole.Sent, false, true },
        { "junk", Imap, FolderRole.Junk, false, true },
        { "archive", Imap, FolderRole.Archive, false, true },
        { "outbox", Imap, FolderRole.Outbox, false, false },
        { "drafts", Imap, FolderRole.Drafts, false, false },
        { "local draft", Imap, FolderRole.Inbox, true, false },
        { "jira", JiraAcc, FolderRole.None, false, false },
        { "eway itself", EwayA, FolderRole.None, false, false },
        { "caldav", CalDav, FolderRole.None, false, false },
    };

    // D6: mail accounts only, never Outbox, Drafts or a local draft; Junk is allowed.
    [Theory]
    [MemberData(nameof(CanSaveCases))]
    public void CanSave(string name, Account acc, string role, bool local, bool want) =>
        Assert.True(CrmRules.CanSave(acc, role, local) == want, name);

    private static CrmRow[] Rows(int n, CrmRow r) => [.. Enumerable.Repeat(r, n)];

    // One rule for the button, the context menu and the multi-selection.
    [Fact]
    public void OfferFor()
    {
        Account[] accs = [Imap, EwayA];
        var inbox = new CrmRow(Imap, FolderRole.Inbox);
        var saved = inbox with { Saved = true };
        var outbox = new CrmRow(Imap, FolderRole.Outbox);
        (string Name, Account[] Accounts, CrmRow[] Rows, CrmOffer Want)[] cases =
        [
            ("no eway account", [Imap], [inbox], default),
            ("nothing selected", accs, [], default),
            ("one message", accs, [inbox], new CrmOffer(true, true, 1, false)),
            ("one saved", accs, [saved], new CrmOffer(true, true, 1, true)),
            ("one in the outbox", accs, [outbox], default),
            ("one of jira", accs, [new CrmRow(JiraAcc, FolderRole.None)], default),
            ("several", accs, [inbox, saved], new CrmOffer(true, true, 2, false)),
            ("several, one a draft", accs, [inbox, new CrmRow(Imap, FolderRole.Drafts)], new CrmOffer(true, false, 2, false)),
            ("the most", accs, Rows(CrmRules.MaxMessages, inbox), new CrmOffer(true, true, CrmRules.MaxMessages, false)),
            ("too many", accs, Rows(CrmRules.MaxMessages + 1, inbox), new CrmOffer(true, false, CrmRules.MaxMessages + 1, false)),
            ("several of jira", accs, Rows(2, new CrmRow(JiraAcc, FolderRole.None)), default),
        ];
        foreach (var c in cases)
        {
            Assert.True(CrmRules.OfferFor(c.Accounts, c.Rows) == c.Want, $"{c.Name}: {CrmRules.OfferFor(c.Accounts, c.Rows)}, want {c.Want}");
        }
    }

    [Fact]
    public void Button()
    {
        Account[] accs = [Imap, EwayA];
        var inbox = new CrmRow(Imap, FolderRole.Inbox);
        Assert.Equal(CrmButtonState.NotSaved, CrmRules.ButtonStateFor(accs, inbox, false));
        Assert.Equal(CrmButtonState.Saving, CrmRules.ButtonStateFor(accs, inbox, true));
        inbox = inbox with { Saved = true };
        Assert.Equal(CrmButtonState.Saved, CrmRules.ButtonStateFor(accs, inbox, false));
        Assert.Equal(CrmButtonState.Hidden, CrmRules.ButtonStateFor([Imap], inbox, false));
        Assert.Equal(CrmButtonState.Hidden, CrmRules.ButtonStateFor(accs, new CrmRow(Imap, FolderRole.Outbox), true));
        Assert.Equal(new CrmButton(CrmButtonState.Hidden, "", "", false, false), CrmText.ButtonFor(CrmButtonState.Hidden));
        Assert.Equal(new CrmButton(CrmButtonState.NotSaved, "Save to eWay-CRM", "Save to eWay-CRM", true, false), CrmText.ButtonFor(CrmButtonState.NotSaved));
        Assert.Equal(new CrmButton(CrmButtonState.Saving, "Save to eWay-CRM", "Saving to eWay-CRM…", false, false), CrmText.ButtonFor(CrmButtonState.Saving));
        Assert.Equal(new CrmButton(CrmButtonState.Saved, "In eWay-CRM", "Saved in eWay-CRM", true, true), CrmText.ButtonFor(CrmButtonState.Saved));
        var menu = CrmText.SavedMenu();
        Assert.Equal([CrmMenuId.Open, CrmMenuId.EditLinks, CrmMenuId.Remove], menu.Select(m => m.Id));
        Assert.True(menu[2].Destructive && !menu[0].Destructive && !menu[1].Destructive);
        Assert.Equal(["Open in eWay-CRM", "Edit Links…", "Remove from eWay-CRM"], menu.Select(m => m.Label));
        Assert.Equal("", CrmText.MenuLabel((CrmMenuId)99));
    }

    private static MessageSummary Summary(string id, bool saved = false) => new()
    {
        Id = id,
        AccountId = "m1",
        FolderId = "f",
        Subject = "",
        Date = DateTimeOffset.UnixEpoch,
        Snippet = "",
        HasAttachments = false,
        Size = 0,
        CrmSaved = saved,
    };

    private static CrmRecord Record(string account, string item) => new()
    {
        AccountId = account,
        ItemGuid = item,
        SavedAt = DateTimeOffset.UnixEpoch,
        CheckedAt = DateTimeOffset.UnixEpoch,
        Private = false,
    };

    [Fact]
    public void IsSaved()
    {
        var s = Summary("x", saved: true);
        Assert.True(CrmRules.IsSaved(s, null, "e1"), "summary says saved");
        var m = new Message { Summary = Summary("x") };
        Assert.False(CrmRules.IsSaved(s, m, "e1"), "the loaded message has no record");
        m = m with { Crm = [Record("e2", "a"), Record("e1", "b")] };
        Assert.True(CrmRules.IsSaved(Summary("x"), m, "e1"), "the loaded message has a record");
        Assert.Equal("b", CrmRules.RecordOf(m, "e1")?.ItemGuid);
        Assert.Equal("a", CrmRules.RecordOf(m, null)?.ItemGuid);
        Assert.Equal("a", CrmRules.RecordOf(m, new AccountId(""))?.ItemGuid);
        Assert.Null(CrmRules.RecordOf(m, "e9"));
        Assert.Null(CrmRules.RecordOf(null, null));
        var other = new Message { Summary = Summary("y") };
        Assert.True(CrmRules.IsSaved(s, other, "e1"), "another message's record decides nothing");
    }

    [Fact]
    public void InFlight()
    {
        var f = new CrmInFlight();
        Assert.True(f.IsEmpty);
        Assert.False(f.Has("m1", "a") || f.Any("m1", ["a"]));
        f.Begin("m1", ["a", "b"]);
        f.Begin("m1", ["a"]);
        Assert.True(f.Has("m1", "a") && f.Has("m1", "b") && !f.Has("m2", "a") && f.Any("m1", ["z", "b"]));
        f.End("m1", ["a", "b"]);
        Assert.True(f.Has("m1", "a") && !f.Has("m1", "b"), "nested end");
        f.End("m1", ["a", "a"]);
        Assert.False(f.Has("m1", "a"));
        Assert.True(f.IsEmpty);
    }

    private static RpcException Daemon(int code, string? data = null) =>
        new(new RpcError { Code = code, Message = "x", Data = data is null ? null : ApiJson.Parse(data) });

    [Fact]
    public void ErrorData()
    {
        Assert.Equal<MessageId>(["m_1", "m_2"], CrmRules.NotDownloaded(Daemon(ErrorCode.PartNotDownloaded, """{"messageIds":["m_1","m_2","m_1",""]}""")));
        Assert.Equal<MessageId>(["m_3"], CrmRules.NotDownloaded(Daemon(ErrorCode.PartNotDownloaded, """{"messageIds":["m_3"]}""")));
        foreach (var e in new Exception?[]
        {
            null,
            Daemon(ErrorCode.PartNotDownloaded),
            Daemon(ErrorCode.ServerError, """{"messageIds":["m_1"]}"""),
            Daemon(ErrorCode.PartNotDownloaded, """{"messageIds":[]}"""),
            new InvalidOperationException("not the daemon's"),
        })
        {
            Assert.Null(CrmRules.NotDownloaded(e));
        }
        Assert.Equal(CrmSignInReason.Microsoft, CrmRules.SignInReason(Daemon(ErrorCode.CrmSignInUnsupported, """{"reason":"microsoft"}"""))?.Value);
        Assert.Equal(CrmSignInReason.Plan, CrmRules.SignInReason(Daemon(ErrorCode.CrmSignInUnsupported, """{"reason":"plan"}"""))?.Value);
        Assert.Null(CrmRules.SignInReason(Daemon(ErrorCode.CrmSignInUnsupported, """{"reason":"other"}""")));
        Assert.Null(CrmRules.SignInReason(Daemon(ErrorCode.CrmSignInUnsupported)));
    }

    public static TheoryData<string, string> OpenUrlCases => new()
    {
        { "https://open.eway-crm.com/?ws=aA&l=bB", "https://open.eway-crm.com/?ws=aA&l=bB" },
        { "HTTPS://open.eway-crm.com/", "HTTPS://open.eway-crm.com/" },
        { "http://open.eway-crm.com/", "" },
        { "javascript:alert(1)", "" },
        { "https://user@open.eway-crm.com/", "" },
        { "https:///nohost", "" },
        { "", "" },
    };

    [Theory]
    [MemberData(nameof(OpenUrlCases))]
    public void OpenUrl(string u, string want) => Assert.Equal(want, CrmRules.OpenUrl(u));

    public static TheoryData<string, string> DefaultNameCases => new()
    {
        { "https://hosting.eway-crm.com/acme", "acme" },
        { "https://hosting.eway-crm.com/a/b%20c/", "b c" },
        { "https://CRM.Example.org", "crm.example.org" },
        { "https://crm.example.org/‮evil", "evil" },
        { "not a url", "" },
    };

    [Theory]
    [MemberData(nameof(DefaultNameCases))]
    public void DefaultName(string u, string want) => Assert.Equal(want, CrmRules.DefaultName(u));

    [Fact]
    public void ServiceHost()
    {
        Assert.Equal("hosting.eway-crm.com/acme", CrmRules.ServiceHost(EwayA));
        Assert.Equal("", CrmRules.ServiceHost(Imap));
        Assert.Equal("EWAY", CrmRules.KindBadge);
        Assert.Equal("eWay", CrmRules.PillLabel);
        Assert.Equal(API.Limits.MaxCrmMessages, CrmRules.MaxMessages);
    }

    // A conversation's row stands for its members, not for one message: it
    // offers nothing, alone or among the selected rows (owner, 2026-10-17).
    [Fact]
    public void ConversationRowOffersNothing()
    {
        Account[] accs = [Imap, EwayA];
        var inbox = new CrmRow(Imap, FolderRole.Inbox);
        var conv = inbox with { Conversation = true };
        Assert.Equal(default, CrmRules.OfferFor(accs, [conv]));
        Assert.Equal(default, CrmRules.OfferFor(accs, [inbox, conv]));
        Assert.Equal(CrmButtonState.Hidden, CrmRules.ButtonStateFor(accs, conv, false));
        Assert.True(CrmRules.OfferFor(accs, [inbox]).Shown);
    }

    // The limits of the mirror are crm.go's (GoContractTests reads them too).
    [Fact]
    public void LimitsAreTheContracts()
    {
        Assert.Equal((20, 50, 2, 200, 256, 512), (API.Limits.MaxCrmMessages, API.Limits.MaxCrmItems, API.Limits.MinCrmQueryBytes, API.Limits.MaxCrmQueryBytes, API.Limits.MaxCrmNameBytes, API.Limits.MaxCrmDetailBytes));
        IReadOnlyList<string> kinds = [.. CrmDialog.Kinds.Select(k => k.Value)];
        Assert.Equal(["contact", "company", "deal", "project"], kinds);
    }
}
