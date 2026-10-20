// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CRMTextTests.swift, the counterpart
// of ui/internal/crm/text_test.go (TestContextMenuLabel, TestDialogTexts,
// TestBar, TestPill, TestSaveToast, TestRemoveQuestion, TestErrorText,
// TestCheckServiceInput, TestFailureOf). The process-wide catalogue is
// English here, so every msgid is its own translation; the msgids
// themselves are checked against po/malachi.pot by
// Malachi.Conventions.Tests (StringsCheckTests). Go's general error
// sentence (errText) is RpcErrorText's here.

using System;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Crm;
using Malachi.Core.Tests.Api;
using Malachi.Core.Transport;
using Xunit;
using static Malachi.Core.Tests.Crm.CrmFixture;

namespace Malachi.Core.Tests.Crm;

public sealed class CrmTextTests
{
    private static RpcError Error(int code, string? data = null) =>
        new() { Code = code, Message = "x", Data = data is null ? null : ApiJson.Parse(data) };

    private static RpcException Daemon(int code, string? data = null) => new(Error(code, data));

    [Fact]
    public void ContextMenuLabel()
    {
        Assert.Equal("Save to eWay-CRM…", CrmText.SaveMenuLabel(1));
        Assert.Equal("Save to eWay-CRM…", CrmText.SaveMenuLabel(0));
        Assert.Equal("Save 3 Messages to eWay-CRM…", CrmText.SaveMenuLabel(3));
    }

    [Fact]
    public void DialogTexts()
    {
        Assert.Equal(
            new CrmDialogStrings
            {
                Title = "Save to eWay-CRM",
                Search = "Search eWay-CRM",
                SuperiorItem = "Superior Item",
                Private = "Private",
                PrivateHint = "Only you will see this message in eWay-CRM.",
                Empty = "Nothing in eWay-CRM matches these addresses.",
                Partial = "eWay-CRM did not answer in time; some suggestions may be missing.",
                SearchHint = "Deals and projects are added by searching above",
                Save = "_Save",
            },
            CrmText.DialogTexts(false));
        Assert.Equal("Edit Links", CrmText.DialogTexts(true).Title);
        Assert.Equal("Contacts · Companies · Deals · Projects", string.Join(" · ", CrmDialog.Kinds.Select(CrmText.GroupTitle)));
        Assert.Equal("", CrmText.GroupTitle("lead"));
    }

    [Fact]
    public void Bar()
    {
        Assert.Null(CrmText.BarFor(null));
        var rec = new CrmRecord
        {
            AccountId = "e1",
            ItemGuid = Guid(100),
            Url = "https://open.eway-crm.com/?l=e",
            SavedAt = DateTimeOffset.UnixEpoch,
            CheckedAt = DateTimeOffset.UnixEpoch,
            Private = false,
            Items =
            [
                new CrmItem { Kind = CrmItemKind.Deal, ItemGuid = Guid(5), Name = "Fleet renewal", Superior = true, Url = "https://open.eway-crm.com/?l=5" },
                new CrmItem { Kind = CrmItemKind.Contact, ItemGuid = Guid(1), Name = "Novák,​ Jan", Url = "http://evil.example/" },
                new CrmItem { Kind = CrmItemKind.Company, ItemGuid = Guid(2), Name = "Acme" },
                new CrmItem { Kind = CrmItemKind.Company, ItemGuid = Guid(3), Name = " ‮ " },
                new CrmItem { Kind = "lead", ItemGuid = Guid(4), Name = "unknown" },
            ],
        };
        var b = CrmText.BarFor(rec);
        Assert.NotNull(b);
        Assert.Equal(("Saved in eWay-CRM", "https://open.eway-crm.com/?l=e", "Open in eWay-CRM", "", "Novák, Jan, Acme, Fleet renewal"), (b.Title, b.Url, b.OpenLabel, b.Note, b.Names));
        Assert.Equal(
            [
                new CrmBarItem(CrmItemKind.Contact, "Novák, Jan", false, ""),
                new CrmBarItem(CrmItemKind.Company, "Acme", false, ""),
                new CrmBarItem(CrmItemKind.Deal, "Fleet renewal", true, "https://open.eway-crm.com/?l=5"),
            ],
            b.Items);
        rec = rec with
        {
            Private = true,
            Items = [.. Enumerable.Range(0, 40).Select(i => new CrmItem { Kind = CrmItemKind.Contact, ItemGuid = Guid(i), Name = new string('n', 30) })],
        };
        b = CrmText.BarFor(rec)!;
        Assert.Equal("Only you will see this message in eWay-CRM.", b.Note);
        Assert.True(Encoding.UTF8.GetByteCount(b.Names) <= API.Limits.MaxCrmDetailBytes && b.Names.EndsWith('…'), b.Names);
        Assert.Equal(40, b.Items.Count);
    }

    [Fact]
    public void Pill()
    {
        MessageSummary S(bool saved) => new()
        {
            Id = "x",
            AccountId = "m1",
            FolderId = "f",
            Subject = "",
            Date = DateTimeOffset.UnixEpoch,
            Snippet = "",
            HasAttachments = false,
            Size = 0,
            CrmSaved = saved,
        };
        Assert.Null(CrmText.PillFor(S(false)));
        Assert.Equal(new CrmPill("eWay", "Saved in eWay-CRM"), CrmText.PillFor(S(true)));
    }

    private static CrmSaveResult Res(params RpcError?[] errors) => new()
    {
        Results =
        [
            .. errors.Select((e, i) => new CrmSaveOutcome
            {
                MessageId = ((char)('a' + i)).ToString(),
                Error = e,
                Record = e is null ? new CrmRecord { AccountId = "e1", ItemGuid = "g", SavedAt = DateTimeOffset.UnixEpoch, CheckedAt = DateTimeOffset.UnixEpoch, Private = false } : null,
            }),
        ],
    };

    [Fact]
    public void SaveToast()
    {
        var dup = Error(ErrorCode.CrmDuplicate);
        var net = Error(ErrorCode.NetworkError);
        (string Name, CrmSaveResult Res, string? Text, RpcError? Err)[] cases =
        [
            ("one", Res((RpcError?)null), "Saved to eWay-CRM", null),
            ("three", Res(null, null, null), "3 messages saved to eWay-CRM", null),
            ("one duplicate", Res(dup), "eWay-CRM already has this message.", null),
            ("one network error", Res(net), null, net),
            ("some failed", Res(null, dup, net), "2 messages were not saved to eWay-CRM.", null),
            ("one of two failed", Res(null, net), "1 message was not saved to eWay-CRM.", null),
        ];
        foreach (var c in cases)
        {
            var (text, err) = CrmText.SaveToast(CrmText.Summarize(c.Res));
            Assert.True(text == c.Text && Equals(err, c.Err), $"{c.Name}: {text} {err}");
        }
        Assert.Equal(new CrmSaveSummary(1, 2, dup), CrmText.Summarize(Res(null, dup, net)));
        Assert.Equal("Removed from eWay-CRM", CrmText.RemovedToast());
    }

    [Fact]
    public void RemoveQuestion()
    {
        Assert.Equal(
            new CrmQuestion(
                "Remove from eWay-CRM?",
                "“Offer for 2027” will be deleted from eWay-CRM with its links, for everyone who uses it. The message in Malachi Mail stays.",
                "_Remove"),
            CrmText.RemoveQuestion("Offer‮  for 2027"));
        Assert.StartsWith("“(No subject)”", CrmText.RemoveQuestion(" ").Body, StringComparison.Ordinal);
        Assert.True(Encoding.UTF8.GetByteCount(CrmText.RemoveQuestion(new string('s', 500)).Body) <= 400);
    }

    [Fact]
    public void ErrorText()
    {
        (Exception? Err, string Want)[] cases =
        [
            (Daemon(ErrorCode.CrmSignInUnsupported, """{"reason":"microsoft"}"""), "This eWay-CRM signs in with a Microsoft account, which Malachi Mail cannot do yet."),
            (Daemon(ErrorCode.CrmSignInUnsupported, """{"reason":"plan"}"""), "This eWay-CRM does not allow access through its API."),
            (Daemon(ErrorCode.CrmSignInUnsupported), ""),
            (Daemon(ErrorCode.CrmDuplicate), "eWay-CRM already has this message."),
            (Daemon(ErrorCode.CrmItemNotFound), "eWay-CRM no longer has this item."),
            (Daemon(ErrorCode.PartNotDownloaded), ""),
            (Daemon(ErrorCode.AuthFailed), ""),
            (new TimeoutException(), ""),
            (null, ""),
        ];
        foreach (var c in cases)
        {
            Assert.Equal(c.Want, CrmText.ErrorText(c.Err));
        }
        // A wrapped error.
        Assert.NotEqual("", CrmText.ErrorText(new InvalidOperationException("rpc", Daemon(ErrorCode.CrmDuplicate))));
        Assert.Equal("Saving to eWay-CRM", CrmText.ActionText(CrmAction.Save));
        Assert.Equal("Contacting eWay-CRM", CrmText.ActionText(CrmAction.Lookup));
        Assert.Equal("Searching eWay-CRM", CrmText.ActionText(CrmAction.Search));
        Assert.Equal("Removing from eWay-CRM", CrmText.ActionText(CrmAction.Remove));
        Assert.Equal("Downloading the message", CrmText.ActionText(CrmAction.Download));
        Assert.Equal("Downloading the message first…", CrmText.DownloadingText(1));
        Assert.Equal("Downloading 4 messages first…", CrmText.DownloadingText(4));
    }

    public static TheoryData<string, bool, string> ServiceInputCases => new()
    {
        { "", false, "" },
        { "   ", false, "" },
        { "hosting.eway-crm.com/acme", true, "" },
        { "https://hosting.eway-crm.com/acme/API.svc", true, "" },
        { "HTTPS://crm.example.org", true, "" },
        { "http://crm.example.org", false, "Malachi Mail connects to eWay-CRM over https only." },
        { "HTTP://crm.example.org", false, "Malachi Mail connects to eWay-CRM over https only." },
        { "ftp://crm.example.org", false, "This is not a web address" },
        { "crm example.org", false, "This is not a web address" },
        { "https://user@crm.example.org", false, "This is not a web address" },
        { "https://", false, "This is not a web address" },
        { "crm.example.org/‮", false, "This is not a web address" },
    };

    [Theory]
    [MemberData(nameof(ServiceInputCases))]
    public void CheckServiceInput(string input, bool ok, string problem) =>
        Assert.Equal((ok, problem), CrmText.CheckServiceInput(input));

    [Fact]
    public void FailureOf()
    {
        const string Microsoft = "This eWay-CRM signs in with a Microsoft account, which Malachi Mail cannot do yet.";
        var ms = Daemon(ErrorCode.CrmSignInUnsupported, """{"reason":"microsoft"}""");
        var plan = Daemon(ErrorCode.CrmSignInUnsupported, """{"reason":"plan"}""");
        (string Name, CrmWizardStep Step, Exception? Err, bool Editing, CrmFailure Want)[] cases =
        [
            ("microsoft at detect", CrmWizardStep.Detect, ms, false, new(CrmWizardPage.Service, Microsoft, "Contacting eWay-CRM", CrmWizardField.Service)),
            ("microsoft at add", CrmWizardStep.Save, ms, false, new(CrmWizardPage.Service, Microsoft, "Adding the account", CrmWizardField.Service)),
            ("microsoft when editing", CrmWizardStep.Test, ms, true, new(CrmWizardPage.Credentials, Microsoft, "Signing in", CrmWizardField.None)),
            ("plan at test", CrmWizardStep.Test, plan, false, new(CrmWizardPage.Credentials, "This eWay-CRM does not allow access through its API.", "Signing in", CrmWizardField.None)),
            ("wrong password", CrmWizardStep.Test, Daemon(ErrorCode.AuthFailed), false, new(CrmWizardPage.Credentials, "The server rejected the user name or password", "Signing in", CrmWizardField.Password)),
            ("no password", CrmWizardStep.Test, Daemon(ErrorCode.AuthRequired), true, new(CrmWizardPage.Credentials, "Enter the password for this account", "Signing in", CrmWizardField.Password)),
            ("not an address", CrmWizardStep.Detect, Daemon(ErrorCode.InvalidArgument), false, new(CrmWizardPage.Service, "This is not a web address", "Contacting eWay-CRM", CrmWizardField.Service)),
            ("not eWay-CRM", CrmWizardStep.Detect, Daemon(ErrorCode.ServerError), false, new(CrmWizardPage.Service, "", "Contacting eWay-CRM", CrmWizardField.Service)),
            ("offline at detect", CrmWizardStep.Detect, Daemon(ErrorCode.NetworkError), false, new(CrmWizardPage.Service, "", "Contacting eWay-CRM", CrmWizardField.None)),
            ("conflict at add", CrmWizardStep.Save, Daemon(ErrorCode.Conflict), false, new(CrmWizardPage.Credentials, "", "Adding the account", CrmWizardField.None)),
            ("timeout at update", CrmWizardStep.Save, new TimeoutException(), true, new(CrmWizardPage.Credentials, "", "Saving the account", CrmWizardField.None)),
        ];
        foreach (var c in cases)
        {
            var got = CrmText.FailureOf(c.Step, c.Err, c.Editing);
            Assert.True(got == c.Want, $"{c.Name}:\n got {got}\nwant {c.Want}");
        }
        var w = CrmText.WizardTexts();
        Assert.Equal(
            ("Add eWay-CRM Account", "Web Service Address", "User Name", "Password", "Contacting eWay-CRM", "_Save"),
            (w.Title, w.ServiceLabel, w.UserLabel, w.PasswordLabel, w.Contacting, w.Save));
        Assert.Equal("Also save later messages of saved conversations", w.FollowThreads);
        Assert.Equal("Replies and new messages in a conversation you saved are saved to eWay-CRM with the same links.", w.FollowThreadsHint);
        foreach (var p in typeof(CrmWizardStrings).GetProperties().Where(p => p.PropertyType == typeof(string)))
        {
            Assert.False(string.IsNullOrEmpty((string?)p.GetValue(w)), $"CrmWizardStrings.{p.Name} is empty");
        }
    }
}
