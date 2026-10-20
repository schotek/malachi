// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/QuickReplyControllerTests.swift (every
// test), the counterpart of ui/internal/quickreply/controller_test.go
// (TestTargetAndModes, TestVisibility, TestBlank, TestOnChange,
// TestTextAndModePerThread, TestSend, TestSaveDraftToNeverNil,
// TestSendComment, TestSendFailures, TestSendGuards,
// TestSendingPerThreadAcrossASwitch, TestFailureAfterASwitch,
// TestTextTypedWhileSendingStays, TestOpenWindowAndForward, TestOpenGuards,
// and of the single-message page, docs/plans/chat-view.md §12:
// TestSingleMessage, TestSetShown, TestTextKeptAcrossPages,
// TestOutboxAndDraftsHaveNoBar, TestSendEndsWhileHidden,
// TestSameThreadOfTwoAccounts, TestUpdateAdoptsTheAccount,
// TestEmptyModelHidesAndKeepsText):
// the quick-reply bar's controller over a fake host whose calls go to a fake
// daemon with scripted handlers for draft.create, draft.save, message.send
// and draft.delete. The host's UI thread is the test: Post queues, DrainAsync
// runs what was posted (Go's goroutine and Host.Post). A hold, when set for a
// method, keeps that call until the test releases it. Beyond Swift: the
// draft.save params are read as they went over the wire ("to": [] and the
// attachments by id).

using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Linq;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Malachi.Core.Transport;
using Xunit;
using static Malachi.Core.Tests.Model.MailModelTests;

namespace Malachi.Core.Tests.Controllers;

public sealed class QuickReplyControllerTests
{
    private const string Original = "<blockquote type=\"cite\"><p>Hi</p><img src=\"cid:pic1\"></blockquote>";

    private static readonly Account MailAcc = TestAccount("acc", name: "Work", email: "petr@example.org");
    private static readonly Account JiraAcc = TestAccount("jira", name: "Jira", email: "petr@example.org") with
    {
        Config = new AccountConfig { Name = "Jira", Email = "petr@example.org", Kind = AccountKind.Jira },
        Capabilities = [Capability.Comment, Capability.Forward, Capability.Transition],
    };

    private static readonly CapabilityActions MailActions = new() { Reply = true, ReplyAll = true, Forward = true };
    private static readonly CapabilityActions CommentActions = new() { Reply = true, Forward = true, Comment = true };

    [Fact]
    public async Task TargetAndModes()
    {
        await using var h = await Harness.StartAsync();
        var c = h.Shown();
        var s = c.State;
        Assert.True(s.Visible && s.Target?.Id?.Value == "m2" && s.Thread == "t1" && !s.Comment, s.ToString());
        Assert.Equal([QuickReplyMode.Reply, QuickReplyMode.ReplyAll, QuickReplyMode.Forward], s.Modes);
        Assert.Equal(QuickReplyMode.Reply, s.Mode);

        c.Update(Model("t1", JiraAcc.Id), CommentActions, JiraAcc);
        s = c.State;
        Assert.True(s.Visible && s.Comment && s.Mode == QuickReplyMode.Reply, s.ToString());
        Assert.Equal([QuickReplyMode.Reply], s.Modes);

        c.Update(Model("t1", MailAcc.Id), default, MailAcc);
        s = c.State;
        Assert.False(s.Visible);
        Assert.Empty(s.Modes);

        // Forward without Reply: the menu would have it, the bar is hidden.
        c.Update(Model("t1", MailAcc.Id), new CapabilityActions { Forward = true }, MailAcc);
        Assert.False(c.State.Visible);
    }

    [Fact]
    public async Task Visibility()
    {
        await using var h = await Harness.StartAsync();
        var c = new QuickReplyController(h.Host);
        c.Update(Model("t1", MailAcc.Id), MailActions, MailAcc);
        Assert.False(c.State.Visible, "visible in the timeline");
        c.SetShown(true);
        Assert.True(c.State.Visible && c.State.Thread == "t1", "Update did not show t1");
        c.Show(MailAcc.Id, "t2");
        Assert.True(!c.State.Visible && c.State.Target is null, "a target before Update");
        c.Update(new ConversationModel { Thread = "t2" }, MailActions, MailAcc);
        Assert.False(c.State.Visible, "visible without a target");
        c.Show(null, "");
        c.SetText("lost");
        Assert.True(!c.State.Visible && c.State.Text.Length == 0 && c.KeptThreads.Count == 0);
    }

    public static TheoryData<string, bool> BlankCases => new()
    {
        { "", true },
        { " \n\t", true },
        { "\u200B\u2066", true },
        { "\0", true },
        { "a", false },
        { " ok ", false },
    };

    [Theory]
    [MemberData(nameof(BlankCases))]
    public async Task Blank(string text, bool blank)
    {
        await using var h = await Harness.StartAsync();
        var c = h.Shown();
        c.SetText(text);
        Assert.Equal(blank, c.State.Blank);
    }

    [Fact]
    public async Task OnChange()
    {
        await using var h = await Harness.StartAsync();
        var c = h.Shown();
        var before = h.Changes;
        c.SetText("a");
        c.SetText("a"); // no change
        c.SetMode(QuickReplyMode.ReplyAll);
        c.SetMode(QuickReplyMode.ReplyAll);
        c.SetMode(QuickReplyMode.Forward); // ignored
        c.SetShown(true); // no change
        c.Show(MailAcc.Id, "t1"); // no change
        Assert.Equal(2, h.Changes - before);
    }

    [Fact]
    public async Task TextAndModePerThread()
    {
        await using var h = await Harness.StartAsync();
        var c = h.Shown();
        c.SetText("for t1");
        c.SetMode(QuickReplyMode.ReplyAll);

        c.Show(MailAcc.Id, "t2");
        c.Update(Model("t2", MailAcc.Id), MailActions, MailAcc);
        Assert.True(c.State.Text.Length == 0 && c.State.Mode == QuickReplyMode.Reply, $"t2 starts with {c.State}");
        c.SetText("for t2");

        c.Show(MailAcc.Id, "t1");
        c.Update(Model("t1", MailAcc.Id), MailActions, MailAcc);
        Assert.Equal("for t1", c.State.Text);
        Assert.Equal(QuickReplyMode.ReplyAll, c.State.Mode);
        // A mode no longer offered falls back to the first.
        c.Update(Model("t1", MailAcc.Id), new CapabilityActions { Reply = true }, MailAcc);
        Assert.Equal(QuickReplyMode.Reply, c.State.Mode);
        c.SetMode(QuickReplyMode.ReplyAll); // not offered: ignored
        c.Update(Model("t1", MailAcc.Id), MailActions, MailAcc);
        Assert.True(c.State.Mode == QuickReplyMode.ReplyAll, "the chosen mode was forgotten");
        c.Show(MailAcc.Id, "t2");
        Assert.Equal("for t2", c.State.Text);
        // An emptied conversation is forgotten.
        c.SetText("");
        Assert.DoesNotContain(new ThreadId("t2"), c.KeptThreads);
    }

    [Fact]
    public async Task Send()
    {
        await using var h = await Harness.StartAsync();
        var c = h.Shown();
        c.SetMode(QuickReplyMode.ReplyAll);
        const string text = "Sure,\nsee you <there> & then";
        c.SetText(text);
        c.Send();
        Assert.True(c.State.Sending);
        c.Send(); // a second Send while one is on its way
        await h.Host.DrainAsync();

        Assert.Equal([API.DraftCreate.Name, API.DraftSave.Name, API.MessageSend.Name], h.Host.Methods);
        Assert.Equal([RpcTimeouts.Compose, RpcTimeouts.Compose, RpcTimeouts.Compose], h.Host.Timeouts);
        Assert.Equal(TimeSpan.FromSeconds(30), QuickReplyController.CallTimeout);
        Assert.Equal(["before", "attribution", "sent"], h.Host.Log);
        Assert.Equal(["m2"], h.Host.Before.Select(id => id.Value));
        var create = Assert.Single(h.Daemon.Params.All<DraftCreateParams>(API.DraftCreate.Name));
        Assert.Equal((new AccountId("acc"), ComposeMode.ReplyAll, (MessageId?)"m2", "On Monday, Jana wrote:"), (create.AccountId, create.Mode.Value, create.MessageId, create.Attribution));

        var template = Created(MailAcc.Id).Draft;
        var got = Assert.Single(h.Daemon.Params.All<DraftSaveParams>(API.DraftSave.Name)).Draft;
        var want = new Draft
        {
            AccountId = "acc",
            To = template.To,
            Cc = template.Cc,
            Subject = "Re: Lunch",
            TextBody = text,
            HtmlBody = Conversation.QuickReplyBody(ComposeMode.ReplyAll, text, template),
            InReplyTo = "m2",
            Attachments = [new DraftAttachment { Id = "att1", Filename = "", ContentType = "", Size = 0, Inline = false }],
        };
        Malachi.Core.Tests.Api.ApiJson.AssertSameValue(want, got);
        Assert.Equal("<p>Sure,<br>see you &lt;there&gt; &amp; then</p>\n" + Original, got.HtmlBody);
        // On the wire: the attachments by id only.
        using (var wire = JsonDocument.Parse(h.Daemon.Params.LastRaw(API.DraftSave.Name)!))
        {
            var attachment = Assert.Single(wire.RootElement.GetProperty("draft").GetProperty("attachments").EnumerateArray());
            Assert.Equal("att1", attachment.GetProperty("id").GetString());
        }
        var send = Assert.Single(h.Daemon.Params.All<MessageSendParams>(API.MessageSend.Name));
        Assert.Equal((new AccountId("acc"), new DraftId("d1"), 1), (send.AccountId, send.DraftId, send.Version));
        var s = c.State;
        Assert.True(!s.Sending && s.Text.Length == 0, s.ToString());
        Assert.Equal([false], h.Host.Sent);
        Assert.Empty(h.Host.Failures);
        // The mode stays for the next reply.
        Assert.Equal(QuickReplyMode.ReplyAll, s.Mode);
        Assert.Equal(0, h.Daemon.Params.Count(API.DraftDelete.Name));
    }

    /// <summary>"to" is never null on the wire (the daemon reads "to": null as no field).</summary>
    [Fact]
    public void SaveDraftToNeverNil()
    {
        var d = QuickReplyController.SaveDraft(ComposeMode.Reply, "x", new Draft { AccountId = "acc" });
        Assert.Empty(d.To);
        Assert.Null(d.Attachments);
        Assert.Null(d.Comment);
        using var json = JsonDocument.Parse(JsonCoding.EncodeToString(d));
        var to = json.RootElement.GetProperty("to");
        Assert.Equal(JsonValueKind.Array, to.ValueKind);
        Assert.Equal(0, to.GetArrayLength());
        Assert.False(json.RootElement.TryGetProperty("attachments", out _));
    }

    [Fact]
    public async Task SendComment()
    {
        await using var h = await Harness.StartAsync(mail: false);
        var issue = new IssueInfo { Key = "PRJ-1", Url = "", Summary = "", Status = "" };
        var template = new Draft
        {
            AccountId = "jira",
            Subject = "PRJ-1: Bug",
            InReplyTo = "m2",
            Comment = new DraftComment { Issue = issue, Visibility = CommentVisibility.Internal },
        };
        h.Script.Set(API.DraftCreate.Name, new DraftCreateResult { Draft = template, Quoted = QuoteForm.None });
        h.Script.Set(API.DraftSave.Name, new DraftSaveResult { DraftId = "d1", Version = 1, TextBody = "" });
        h.Script.Set(API.MessageSend.Name, new MessageSendResult { OutboxId = "o1" });
        var c = new QuickReplyController(h.Host);
        c.SetShown(true);
        c.Update(Model("t1", JiraAcc.Id), CommentActions, JiraAcc);
        c.SetText("Fixed in 1.2\n\nThanks");
        c.Send();
        await h.Host.DrainAsync();

        Assert.True(h.Host.Log.SequenceEqual(["sent"]), "no download, no attribution");
        var create = Assert.Single(h.Daemon.Params.All<DraftCreateParams>(API.DraftCreate.Name));
        Assert.Equal((new AccountId("jira"), ComposeMode.Reply, (MessageId?)"m2", (string?)null), (create.AccountId, create.Mode.Value, create.MessageId, create.Attribution));
        var d = Assert.Single(h.Daemon.Params.All<DraftSaveParams>(API.DraftSave.Name)).Draft;
        Assert.Equal("<p>Fixed in 1.2</p><p>Thanks</p>", d.HtmlBody);
        Assert.Equal(CommentVisibility.Public, d.Comment?.Visibility.Value);
        Assert.Equal("PRJ-1", d.Comment?.Issue.Key);
        Assert.Equal([true], h.Host.Sent);
    }

    [Theory]
    [InlineData("draft.create")]
    [InlineData("draft.save")]
    [InlineData("message.send")]
    public async Task SendFailures(string method)
    {
        await using var h = await Harness.StartAsync();
        var fail = new RpcError { Code = ErrorCode.InternalError, Message = "boom" };
        h.Script.Fail(method, fail);
        h.Script.Fail(API.DraftDelete.Name, new RpcError { Code = ErrorCode.ServerError, Message = "ignored" });
        var c = h.Shown();
        c.SetText("keep me");
        c.Send();
        await h.Host.DrainAsync();
        string[] calls = method switch
        {
            "draft.create" => [API.DraftCreate.Name],
            "draft.save" => [API.DraftCreate.Name, API.DraftSave.Name],
            _ => [API.DraftCreate.Name, API.DraftSave.Name, API.MessageSend.Name, API.DraftDelete.Name],
        };
        Assert.Equal(calls, h.Host.Methods);
        var failure = Assert.IsType<RpcException>(Assert.Single(h.Host.Failures));
        Assert.Equal((fail.Code, fail.Message), (failure.Error.Code, failure.Error.Message));
        Assert.Empty(h.Host.Sent);
        Assert.Equal("keep me", c.State.Text);
        Assert.False(c.State.Sending);
        if (method == API.MessageSend.Name)
        {
            var delete = Assert.Single(h.Daemon.Params.All<DraftDeleteParams>(API.DraftDelete.Name));
            Assert.Equal((new AccountId("acc"), new DraftId("d1")), (delete.AccountId, delete.DraftId));
        }
    }

    /// <summary>What Send does not do: blank text, a hidden bar, a Send on its way.</summary>
    [Fact]
    public async Task SendGuards()
    {
        await using var h = await Harness.StartAsync();
        var c = h.Shown();
        c.SetText(" \n ");
        c.Send();
        c.SetText("text");
        c.SetShown(false);
        c.Send();
        c.SetShown(true);
        c.Update(Model("t1", MailAcc.Id), default, MailAcc);
        c.Send();
        await h.IdleAsync();
        Assert.Empty(h.Host.Log);
        Assert.Empty(h.Host.Methods);
        Assert.False(h.Host.HasPosted);
    }

    /// <summary>
    /// A Send ends in the conversation it started in, even when another is
    /// shown by then; that one's text and bar are left alone.
    /// </summary>
    [Fact]
    public async Task SendingPerThreadAcrossASwitch()
    {
        await using var h = await Harness.StartAsync();
        var held = h.Script.Hold(API.MessageSend.Name, h.Daemon);
        var c = h.Shown();
        c.SetText("to t1");
        c.Send();

        c.Show(MailAcc.Id, "t2");
        c.Update(Model("t2", MailAcc.Id), MailActions, MailAcc);
        Assert.True(!c.State.Sending && c.State.Text.Length == 0, "t2 sending");
        c.SetText("to t2");
        c.Send(); // t2 sends on its own
        c.Show(MailAcc.Id, "t1");
        Assert.True(c.State.Sending, "t1 not sending");
        c.Show(MailAcc.Id, "t2");
        held.Release();
        await h.Host.DrainAsync();
        await h.Host.DrainAsync();
        Assert.True(!c.State.Sending && c.State.Text.Length == 0, "t2 after");
        c.Show(MailAcc.Id, "t1");
        Assert.True(!c.State.Sending && c.State.Text.Length == 0, "t1 after");
        Assert.Equal(2, h.Host.Sent.Count);
        Assert.Empty(c.KeptThreads);
    }

    /// <summary>One conversation's failure while another is shown keeps its text there.</summary>
    [Fact]
    public async Task FailureAfterASwitch()
    {
        await using var h = await Harness.StartAsync();
        h.Host.HoldBefore = true;
        h.Script.Fail(API.DraftCreate.Name, new RpcError { Code = ErrorCode.ServerError, Message = "down" });
        var c = h.Shown();
        c.SetText("to t1");
        c.Send();
        c.Show(MailAcc.Id, "t2");
        c.Update(Model("t2", MailAcc.Id), MailActions, MailAcc);
        c.SetText("to t2");
        h.Host.BeforeDone[0](); // the download ends while t2 is shown
        Assert.Equal(["m2"], h.Host.Attributions.Select(id => id.Value));
        await h.Host.DrainAsync();
        Assert.True(c.State.Text == "to t2" && !c.State.Sending, "t2");
        c.Show(MailAcc.Id, "t1");
        Assert.True(c.State.Text == "to t1" && !c.State.Sending && h.Host.Failures.Count == 1, "t1");
    }

    /// <summary>What was typed while sending stays after a success.</summary>
    [Fact]
    public async Task TextTypedWhileSendingStays()
    {
        await using var h = await Harness.StartAsync();
        var held = h.Script.Hold(API.DraftSave.Name, h.Daemon);
        var c = h.Shown();
        c.SetText("first");
        c.Send();
        c.SetText("first and more");
        held.Release();
        await h.Host.DrainAsync();
        Assert.Equal("first and more", c.State.Text);
        Assert.Single(h.Host.Sent);
    }

    [Fact]
    public async Task OpenWindowAndForward()
    {
        await using var h = await Harness.StartAsync();
        var c = h.Shown();
        c.SetMode(QuickReplyMode.ReplyAll);
        c.SetText("a < b\n\nc");

        c.OpenWindow();
        var o = Assert.Single(h.Host.Opens);
        Assert.Equal((QuickReplyMode.ReplyAll, new MessageId("m2"), "<p>a &lt; b</p><p>c</p>"), (o.Mode, o.Id, o.OwnHtml));
        // No window yet: the text stays.
        Assert.True(c.State.Text == "a < b\n\nc", "cleared before the window opened");
        o.Opened();
        Assert.True(c.State.Text.Length == 0, "not cleared once opened");

        // Forward: the forward mode, the text above it; a window that never
        // opens leaves the text.
        c.SetText("fyi");
        c.Forward();
        o = h.Host.Opens[1];
        Assert.Equal((QuickReplyMode.Forward, "<p>fyi</p>"), (o.Mode, o.OwnHtml));
        Assert.Equal("fyi", c.State.Text);
        Assert.Equal(QuickReplyMode.ReplyAll, c.State.Mode);

        // Opened after the conversation changed: it clears its own text only.
        c.Show(MailAcc.Id, "t2");
        c.Update(Model("t2", MailAcc.Id), MailActions, MailAcc);
        c.SetText("t2 text");
        o.Opened();
        Assert.Equal("t2 text", c.State.Text);
        c.Show(MailAcc.Id, "t1");
        Assert.Equal("", c.State.Text);

        // An empty bar opens an empty editor; no draft is saved by the bar.
        c.Update(Model("t1", MailAcc.Id), MailActions, MailAcc);
        c.OpenWindow();
        Assert.Equal("", h.Host.Opens[2].OwnHtml);
        Assert.Empty(h.Host.Methods);
    }

    [Fact]
    public async Task OpenGuards()
    {
        await using var h = await Harness.StartAsync();
        var c = new QuickReplyController(h.Host);
        c.Update(Model("t1", MailAcc.Id), MailActions, MailAcc);
        c.OpenWindow(); // timeline: hidden
        c.SetShown(true);
        c.Update(Model("t1", MailAcc.Id), new CapabilityActions { Reply = true }, MailAcc);
        c.Forward(); // the account does not forward
        Assert.Empty(h.Host.Opens);
        // While sending neither opens.
        var held = h.Script.Hold(API.DraftCreate.Name, h.Daemon);
        c.SetText("x");
        c.Send();
        c.OpenWindow();
        held.Release();
        await h.Host.DrainAsync();
        Assert.True(h.Host.Opens.Count == 0, "opened while sending");
        // A comment opens the window as a reply (the window decides it is a
        // comment).
        c.Update(Model("t1", JiraAcc.Id), CommentActions, JiraAcc);
        c.OpenWindow();
        Assert.Equal(QuickReplyMode.Reply, Assert.Single(h.Host.Opens).Mode);
    }

    // The page of a single message (docs/plans/chat-view.md §12;
    // TestSingleMessage): the bar answers the message shown, the user's own
    // too, and Send goes to it.
    [Fact]
    public async Task SingleMessage()
    {
        await using var h = await Harness.StartAsync();
        var c = new QuickReplyController(h.Host);
        ShowSingle(c, SingleOf("m7", "t1", false));
        var s = c.State;
        Assert.True(s.Visible && s.Thread == "t1" && s.Account == MailAcc.Id && s.Target?.Id?.Value == "m7" && s.Target?.Sender == "Jana m7" && !s.Comment, s.ToString());
        Assert.Equal(3, s.Modes.Count);
        c.SetText("Thanks");
        c.Send();
        await h.Host.DrainAsync();
        var create = Assert.Single(h.Daemon.Params.All<DraftCreateParams>(API.DraftCreate.Name));
        Assert.Equal((MailAcc.Id, ComposeMode.Reply, (MessageId?)"m7"), (create.AccountId, create.Mode.Value, create.MessageId));
        Assert.Single(h.Host.Sent);
        Assert.Equal("", c.State.Text);

        // The user's own message (Sent): a reply to it.
        ShowSingle(c, SingleOf("s1", "t2", true));
        s = c.State;
        Assert.True(s.Visible && s.Target?.Id?.Value == "s1" && s.Target?.Mine == true, s.ToString());

        // An issue's event alone has no target: hidden whatever the host says.
        var ev = Conversation.SingleModel(IssueSummary("e1", IssueItemKind.Event), false);
        c.Show(JiraAcc.Id, ev.Thread);
        c.Update(ev, CommentActions, JiraAcc);
        Assert.False(c.State.Visible, c.State.ToString());
        // An issue's comment: a comment.
        var cm = Conversation.SingleModel(IssueSummary("c1", IssueItemKind.Comment), false);
        c.Update(cm, CommentActions, JiraAcc);
        s = c.State;
        Assert.True(s.Visible && s.Comment && s.Target?.Id?.Value == "c1", s.ToString());
    }

    // SetShown alone hides and shows the bar; what was typed stays
    // (TestSetShown).
    [Fact]
    public async Task SetShown()
    {
        await using var h = await Harness.StartAsync();
        var c = new QuickReplyController(h.Host);
        var n = 0;
        c.Changed += (_, _) => n++;
        ShowSingle(c, SingleOf("m1", "t1", false));
        c.SetText("draft");
        var before = n;
        c.SetShown(false);
        Assert.True(!c.State.Visible && c.State.Text == "draft", c.State.ToString());
        c.SetShown(false); // no change
        c.SetShown(true);
        Assert.True(c.State.Visible && c.State.Text == "draft", c.State.ToString());
        Assert.Equal(2, n - before);
    }

    // The text typed under a single message is its thread's: it stays
    // through the conversation of the same thread in the timeline (no bar)
    // and in the chat (the bar with the text), back to another message of the
    // thread, and through a message of another thread
    // (TestTextKeptAcrossPages).
    [Fact]
    public async Task TextKeptAcrossPages()
    {
        await using var h = await Harness.StartAsync();
        var c = new QuickReplyController(h.Host);
        ShowSingle(c, SingleOf("m2", "t1", false));
        c.SetText("half a reply");
        c.SetMode(QuickReplyMode.ReplyAll);

        // The grouped list: the conversation t1, in the timeline.
        c.Show(MailAcc.Id, "t1");
        c.Update(Model("t1", MailAcc.Id), MailActions, MailAcc);
        c.SetShown(false);
        Assert.True(!c.State.Visible && c.State.Text == "half a reply", $"timeline: {c.State}");
        // The chat layout.
        c.SetShown(true);
        var s = c.State;
        Assert.True(s.Visible && s.Text == "half a reply" && s.Mode == QuickReplyMode.ReplyAll && s.Target?.Id?.Value == "m2", $"chat: {s}");
        // The timeline again, then the flat list with m1 of t1 alone.
        c.SetShown(false);
        ShowSingle(c, SingleOf("m1", "t1", false));
        s = c.State;
        Assert.True(s.Visible && s.Text == "half a reply" && s.Target?.Id?.Value == "m1", $"m1: {s}");
        // A message of another thread starts empty; back to t1 the text is there.
        ShowSingle(c, SingleOf("x1", "t9", false));
        Assert.True(c.State.Text.Length == 0 && c.State.Mode == QuickReplyMode.Reply, $"t9: {c.State}");
        ShowSingle(c, SingleOf("m2", "t1", false));
        Assert.True(c.State.Text == "half a reply" && c.State.Mode == QuickReplyMode.ReplyAll, $"t1 back: {c.State}");
    }

    // The Outbox and Drafts have no bar: the host says SetShown(false) for a
    // message there, and nothing sends or opens; the text of its thread stays
    // (TestOutboxAndDraftsHaveNoBar).
    [Fact]
    public async Task OutboxAndDraftsHaveNoBar()
    {
        await using var h = await Harness.StartAsync();
        var c = new QuickReplyController(h.Host);
        ShowSingle(c, SingleOf("m1", "t1", false));
        c.SetText("kept");
        // A queued message of the same thread in the Outbox.
        c.Show(MailAcc.Id, "t1");
        c.Update(SingleOf("o1", "t1", true), MailActions, MailAcc);
        c.SetShown(false);
        c.Send();
        c.OpenWindow();
        c.Forward();
        Assert.True(!c.State.Visible && c.State.Text == "kept", c.State.ToString());
        await h.IdleAsync();
        Assert.Empty(h.Host.Log);
        Assert.Empty(h.Host.Methods);
        Assert.Empty(h.Host.Opens);
    }

    // A Send on its way when the host hides the bar (the user selected the
    // empty page or several messages) ends as usual: the text clears, the
    // toast says so (TestSendEndsWhileHidden).
    [Fact]
    public async Task SendEndsWhileHidden()
    {
        await using var h = await Harness.StartAsync();
        var held = h.Script.Hold(API.DraftCreate.Name, h.Daemon);
        var c = new QuickReplyController(h.Host);
        ShowSingle(c, SingleOf("m1", "t1", false));
        c.SetText("go");
        c.Send();
        c.Show(null, "");
        c.SetShown(false);
        held.Release();
        await h.Host.DrainAsync();
        Assert.Single(h.Host.Sent);
        Assert.Empty(h.Host.Failures);
        ShowSingle(c, SingleOf("m1", "t1", false));
        var s = c.State;
        Assert.True(s.Text.Length == 0 && !s.Sending && s.Visible, $"after: {s}");
    }

    // A thread id is unique only within its account (an issue's jira:<id> of
    // two sites, a search over all accounts): the same id of two accounts has
    // its own text, mode and Send, and a Send ends in the account it started
    // in (TestSameThreadOfTwoAccounts).
    [Fact]
    public async Task SameThreadOfTwoAccounts()
    {
        var other = TestAccount("acc2", name: "Home", email: "petr@example.net");
        await using var h = await Harness.StartAsync();
        var held = h.Script.Hold(API.DraftCreate.Name, h.Daemon);
        var c = h.Shown();
        c.SetText("for acc");
        c.SetMode(QuickReplyMode.ReplyAll);

        c.Show(other.Id, "t1");
        c.Update(Model("t1", other.Id), MailActions, other);
        var s = c.State;
        Assert.True(s.Account == other.Id && s.Thread == "t1" && s.Text.Length == 0 && s.Mode == QuickReplyMode.Reply && !s.Sending, $"acc2 starts with {s}");
        c.SetText("for acc2");
        c.Send();
        Assert.True(c.State.Sending, "acc2 not sending");

        c.Show(MailAcc.Id, "t1");
        c.Update(Model("t1", MailAcc.Id), MailActions, MailAcc);
        s = c.State;
        Assert.True(s.Account == MailAcc.Id && s.Text == "for acc" && s.Mode == QuickReplyMode.ReplyAll && !s.Sending, $"acc back: {s}");
        held.Release();
        await h.Host.DrainAsync();
        var create = Assert.Single(h.Daemon.Params.All<DraftCreateParams>(API.DraftCreate.Name));
        Assert.Equal(other.Id, create.AccountId);
        Assert.True(c.State.Text == "for acc" && !c.State.Sending, $"acc after acc2's send: {c.State}");
        c.Show(other.Id, "t1");
        c.Update(Model("t1", other.Id), MailActions, other);
        Assert.True(c.State.Text.Length == 0 && !c.State.Sending, $"acc2 after its send: {c.State}");
        Assert.False(c.IsKept(other.Id, "t1"), "acc2 kept after its send");
        Assert.True(c.IsKept(MailAcc.Id, "t1"));
    }

    // A conversation shown without its account takes the account of the
    // first Update; one shown with its account keeps it
    // (TestUpdateAdoptsTheAccount).
    [Fact]
    public async Task UpdateAdoptsTheAccount()
    {
        await using var h = await Harness.StartAsync();
        var c = new QuickReplyController(h.Host);
        c.SetShown(true);
        c.Update(Model("t1", MailAcc.Id), MailActions, MailAcc);
        Assert.True(c.State.Account == MailAcc.Id && c.State.Thread == "t1", $"adopted {c.State}");
        c.Show(null, "t2");
        c.Update(Model("t2", MailAcc.Id), MailActions, MailAcc);
        Assert.True(c.State.Account == MailAcc.Id && c.State.Thread == "t2", $"adopted the account: {c.State}");
        c.Show(JiraAcc.Id, "t3");
        c.Update(Model("t3", MailAcc.Id), MailActions, MailAcc);
        Assert.True(c.State.Account == JiraAcc.Id, $"replaced the account: {c.State}");
        c.Show(JiraAcc.Id, "");
        Assert.True(c.State.Account.Value.Length == 0 && c.State.Thread.Value.Length == 0, $"a thread \"\" is none: {c.State}");
    }

    // While a conversation's members are on their way the host gives the
    // empty model: the bar hides (it must not answer what was shown before,
    // such as the single message of the same thread) and the text stays
    // (TestEmptyModelHidesAndKeepsText).
    [Fact]
    public async Task EmptyModelHidesAndKeepsText()
    {
        await using var h = await Harness.StartAsync();
        var c = h.Shown();
        c.SetText("draft");
        c.Update(new ConversationModel(), default, null);
        var s = c.State;
        Assert.True(!s.Visible && s.Target is null && s.Text == "draft" && s.Thread == "t1" && s.Account == MailAcc.Id, $"empty model: {s}");
        c.Send();
        c.OpenWindow();
        c.Update(Model("t1", MailAcc.Id), MailActions, MailAcc);
        s = c.State;
        Assert.True(s.Visible && s.Text == "draft" && s.Target?.Id?.Value == "m2", $"model back: {s}");
        await h.IdleAsync();
        Assert.Empty(h.Host.Methods);
        Assert.Empty(h.Host.Opens);
    }

    // quickreply single: message id of thread t of the mail account shown
    // alone.
    private static ConversationModel SingleOf(string id, ThreadId thread, bool mine) => Conversation.SingleModel(
        new MessageSummary
        {
            Id = id,
            AccountId = MailAcc.Id,
            FolderId = "f",
            ThreadId = thread,
            From = [new Address { Name = "Jana " + id, Email = "jana@example.org" }],
            Subject = "",
            Date = DateTimeOffset.GoZero,
            Snippet = "",
            HasAttachments = false,
            Size = 0,
        },
        mine);

    // An item of an issue of the Jira account (thread tj), of kind.
    private static MessageSummary IssueSummary(string id, IssueItemKind kind) => new()
    {
        Id = id,
        AccountId = JiraAcc.Id,
        FolderId = "space",
        ThreadId = "tj",
        Subject = "",
        Date = DateTimeOffset.GoZero,
        Snippet = "",
        HasAttachments = false,
        Size = 0,
        Issue = MessageIssue.Of(new IssueInfo { Key = "PRJ-1", Url = "", Summary = "", Status = "" }, kind),
    };

    // quickreply showSingle: what the host does for the page of a single
    // message.
    private static void ShowSingle(QuickReplyController c, ConversationModel m)
    {
        c.Show(MailAcc.Id, m.Thread);
        c.Update(m, MailActions, MailAcc);
        c.SetShown(true);
    }

    // A message card of someone else (or the user's).
    private static ConversationItem Item(string id, AccountId acc, bool mine) => new()
    {
        Kind = ConversationItemKind.Message,
        Message = new MessageSummary
        {
            Id = id,
            AccountId = acc,
            FolderId = "f",
            Subject = "",
            Date = DateTimeOffset.GoZero,
            Snippet = "",
            HasAttachments = false,
            Size = 0,
        },
        Sender = "Jana " + id,
        Mine = mine,
    };

    // A conversation whose newest message of someone else is m2 (m3 is the
    // user's).
    private static ConversationModel Model(ThreadId thread, AccountId acc) => new()
    {
        Thread = thread,
        Items = [Item("m1", acc, false), Item("m2", acc, false), Item("m3", acc, true)],
    };

    private static DraftCreateResult Created(AccountId acc) => new()
    {
        Draft = new Draft
        {
            AccountId = acc,
            To = [new Address { Name = "Jana", Email = "jana@example.org" }],
            Cc = [new Address { Email = "petr@example.org" }],
            Subject = "Re: Lunch",
            TextBody = "> Hi",
            HtmlBody = Original,
            InReplyTo = "m2",
            Attachments = [new DraftAttachment { Id = "att1", Filename = "pic.png", ContentType = "image/png", Size = 12, Inline = true, ContentId = "pic1" }],
        },
        Quoted = QuoteForm.Html,
    };

    // The daemon's answers and what holds them, off the test's thread.
    private sealed class Script
    {
        private readonly Lock gate = new();
        private readonly Dictionary<string, string> results = [];
        private readonly Dictionary<string, RpcError> errors = [];
        private readonly Dictionary<string, HeldAnswer> holds = [];

        public void Set<T>(string method, T result)
        {
            var json = JsonCoding.EncodeToString(result);
            lock (gate)
            {
                results[method] = json;
            }
        }

        public void Fail(string method, RpcError error)
        {
            lock (gate)
            {
                errors[method] = error;
            }
        }

        // Holds method until the hold is released (at the end at the latest).
        public HeldAnswer Hold(string method, DaemonHarness daemon)
        {
            var held = daemon.Hold();
            lock (gate)
            {
                holds[method] = held;
            }
            return held;
        }

        public async Task<string> AnswerAsync(string method)
        {
            HeldAnswer? held;
            lock (gate)
            {
                holds.TryGetValue(method, out held);
            }
            if (held is not null)
            {
                await held.WaitAsync();
            }
            lock (gate)
            {
                if (errors.TryGetValue(method, out var error))
                {
                    throw new RpcException(error);
                }
                return results.TryGetValue(method, out var json) ? json : "{}";
            }
        }
    }

    // quickreply fakeHost: records what the controller asks of the window.
    // The calls may come from any thread; everything else comes from the
    // test's.
    private sealed class FakeHost(RpcClient client) : IQuickReplyHost
    {
        private readonly Lock gate = new();
        private readonly List<string> methods = [];
        private readonly List<TimeSpan> timeouts = [];
        private readonly ConcurrentQueue<Action> posted = new();

        /// <summary>The calls, in order, by method name.</summary>
        public IReadOnlyList<string> Methods
        {
            get
            {
                lock (gate)
                {
                    return [.. methods];
                }
            }
        }

        public IReadOnlyList<TimeSpan> Timeouts
        {
            get
            {
                lock (gate)
                {
                    return [.. timeouts];
                }
            }
        }

        /// <summary>The host's own methods, in order.</summary>
        public List<string> Log { get; } = [];

        public List<MessageId> Attributions { get; } = [];

        public List<MessageId> Before { get; } = [];

        public bool HoldBefore { get; set; }

        public List<Action> BeforeDone { get; } = [];

        public List<(QuickReplyMode Mode, MessageId Id, string OwnHtml, Action Opened)> Opens { get; } = [];

        public List<bool> Sent { get; } = [];

        public List<Exception> Failures { get; } = [];

        public bool HasPosted => !posted.IsEmpty;

        public Task<TResult> CallAsync<TParams, TResult>(RpcMethod<TParams, TResult> method, TParams parameters, TimeSpan timeout)
        {
            lock (gate)
            {
                methods.Add(method.Name);
                timeouts.Add(timeout);
            }
            return client.CallAsync(method, parameters, timeout);
        }

        public void Post(Action action) => posted.Enqueue(action);

        public string Attribution(QuickReplyMode mode, MessageId id)
        {
            Log.Add("attribution");
            Attributions.Add(id);
            return "On Monday, Jana wrote:";
        }

        public void BeforeCreate(MessageId id, Action done)
        {
            Log.Add("before");
            Before.Add(id);
            if (HoldBefore)
            {
                BeforeDone.Add(done);
                return;
            }
            done();
        }

        public void OpenCompose(QuickReplyMode mode, MessageId id, string ownHtml, Action opened)
        {
            Log.Add("open");
            Opens.Add((mode, id, ownHtml, opened));
        }

        void IQuickReplyHost.Sent(bool comment)
        {
            Log.Add("sent");
            Sent.Add(comment);
        }

        public void Failed(Exception failure)
        {
            Log.Add("failed");
            Failures.Add(failure);
        }

        /// <summary>Runs one posted callback (the end of a Send), waiting for it.</summary>
        public async Task DrainAsync()
        {
            var deadline = DateTime.UtcNow + TimeSpan.FromSeconds(10);
            Action? f;
            while (!posted.TryDequeue(out f))
            {
                if (DateTime.UtcNow > deadline)
                {
                    throw new TimeoutException("nothing was posted");
                }
                await Task.Delay(5);
            }
            f();
        }
    }

    // The fake daemon with the four handlers, a connected client and the
    // fake host.
    private sealed class Harness : IAsyncDisposable
    {
        private Harness(DaemonHarness daemon, FakeHost host)
        {
            Daemon = daemon;
            Host = host;
        }

        public DaemonHarness Daemon { get; }

        public Script Script { get; } = new();

        public FakeHost Host { get; }

        public int Changes { get; private set; }

        public static async Task<Harness> StartAsync(bool mail = true)
        {
            var daemon = await DaemonHarness.StartAsync();
            var client = await daemon.ConnectAsync();
            var h = new Harness(daemon, new FakeHost(client));
            foreach (var method in new[] { API.DraftCreate.Name, API.DraftSave.Name, API.MessageSend.Name, API.DraftDelete.Name })
            {
                daemon.On(method, _ => h.Script.AnswerAsync(method));
            }
            if (mail)
            {
                h.Script.Set(API.DraftCreate.Name, Created(MailAcc.Id));
                h.Script.Set(API.DraftSave.Name, new DraftSaveResult { DraftId = "d1", Version = 1, TextBody = "" });
                h.Script.Set(API.MessageSend.Name, new MessageSendResult { OutboxId = "o1" });
            }
            h.Script.Set(API.DraftDelete.Name, new EmptyResult());
            return h;
        }

        // quickreply newShown: a controller showing thread t1 of the mail
        // account as chat.
        public QuickReplyController Shown()
        {
            var c = new QuickReplyController(Host);
            c.Changed += (_, _) => Changes++;
            c.SetShown(true);
            c.Show(MailAcc.Id, "t1");
            c.Update(Model("t1", MailAcc.Id), MailActions, MailAcc);
            return c;
        }

        // Nothing left to happen at the daemon.
        public Task IdleAsync() => Daemon.Fake.IdleAsync();

        public ValueTask DisposeAsync() => Daemon.DisposeAsync();
    }
}
