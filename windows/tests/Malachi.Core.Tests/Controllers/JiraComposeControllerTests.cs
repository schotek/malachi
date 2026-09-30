// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JiraComposeControllerTests.swift, the
// counterpart of the comment cases of ui/internal/compose/draft_test.go
// (TestDraftStatus, TestClosesUnasked, TestDeletesOnClose,
// TestCommentSaveFailure, TestQueuedText): the comment mode of the compose
// window, the controller half (ComposeDraftController over a fake form and
// a fake daemon). A comment draft from draft.create opens it
// (Prefill.FromDraft), it is sent without recipients but not empty,
// draft.save carries the chosen visibility, and there is no Save Draft:
// closing asks whether to discard, and the copy the autosave kept goes with
// the window. The choice of visibility itself is JiraComposeTests. Swift
// shortens the autosave and sleeps; here the controller's clock is moved.
// Added: Quit leaves an empty comment alone and asks about one with text,
// which no Drafts folder would keep.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Html;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Settings;
using Malachi.Core.Tests.Api;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Tests.Model;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class JiraComposeControllerTests
{
    private static readonly AccountId JiraAccount = "j";

    // A service-desk request: its comments may be public or internal.
    private static readonly IssueInfo Request = new()
    {
        Key = "ITSD-42",
        Url = "https://acme.atlassian.net/browse/ITSD-42",
        Summary = "The printer on the third floor",
        Status = "Waiting for Support",
        StatusCategory = IssueStatusCategory.InProgress,
        CommentVisibilities = [CommentVisibility.Public, CommentVisibility.Internal],
    };

    [Fact]
    public void FromDraftCarriesTheComment()
    {
        var d = CommentDraft(visibility: CommentVisibility.Internal);
        var p = Prefill.FromDraft(ComposeKind.Reply, d, new BlockedContent());
        ApiJson.AssertSameValue(new DraftComment { Issue = Request, Visibility = CommentVisibility.Internal }, p.Comment);
        Assert.Equal(JiraAccount, p.AccountId);
        Assert.Equal("w1", p.InReplyTo?.Value);
        Assert.True(p.To.Count == 0 && p.Cc.Count == 0 && p.Bcc.Count == 0);
        Assert.Equal("ITSD-42: The printer on the third floor", p.Subject);
        Assert.Empty(p.BodyHtml); // a comment starts empty
        Assert.Equal("Comment on ITSD-42", Jira.CommentCompose(d)?.Title);

        // A mail draft opens no comment mode.
        Assert.Null(Prefill.FromDraft(ComposeKind.Reply, new Draft { AccountId = "a", Subject = "Re: x", InReplyTo = "m1" }, new BlockedContent()).Comment);
        Assert.Null(new ComposeParams { Kind = ComposeKind.Reply }.Comment);
    }

    [Fact]
    public async Task BuildCarriesTheVisibilityAndNoRecipients()
    {
        await using var h = await Harness.StartAsync();
        h.Form.CommentVisibility = CommentVisibility.Internal;
        var d = (await h.Ui.RunAsync(h.Draft.Build))!;
        Assert.Equal(JiraAccount, d.AccountId);
        ApiJson.AssertSameValue(new DraftComment { Issue = Request, Visibility = CommentVisibility.Internal }, d.Comment);
        Assert.Equal("w1", d.InReplyTo?.Value);
        Assert.True(d.To.Count == 0 && d.Cc is null && d.Bcc is null);
        Assert.Null(d.Attachments);
        Assert.Null(d.Forwarding);
        Assert.Equal("<p>Replaced the toner.</p>", d.HtmlBody);

        h.Form.CommentVisibility = CommentVisibility.Public;
        Assert.Equal(CommentVisibility.Public, (await h.Ui.RunAsync(h.Draft.Build))?.Comment?.Visibility);

        // An e-mail window sends no comment, whatever the controller holds.
        h.Form.Comment = false;
        Assert.Null((await h.Ui.RunAsync(h.Draft.Build))?.Comment);
    }

    [Fact]
    public async Task ACommentIsSentWithoutRecipients()
    {
        await using var h = await Harness.StartAsync();
        h.Form.CommentVisibility = CommentVisibility.Internal;
        await h.Ui.RunAsync(h.Draft.Send);
        await h.IdleAsync();
        Assert.Equal(1, h.Form.Closes);
        var save = Assert.Single(h.Saves);
        Assert.Equal(CommentVisibility.Internal, save.Draft.Comment?.Visibility);
        Assert.Empty(save.Draft.To);
        var send = Assert.Single(h.Sends);
        Assert.Equal((JiraAccount, "d1", 1), (send.AccountId, send.DraftId.Value, send.Version));
        Assert.Equal(["Comment queued"], h.Sent);
        Assert.Empty(h.Form.Toasts);
        // Sent: closing leaves nothing behind to delete.
        Assert.True(h.Draft.CanCloseWithoutAsking);
        await h.Ui.RunAsync(h.Draft.Cleanup);
        await h.IdleAsync();
        Assert.Empty(h.Deletes);
    }

    [Fact]
    public async Task AnEmptyCommentIsRefused()
    {
        await using var h = await Harness.StartAsync();
        // Spaces and invisible characters only.
        h.Form.Text = " \n\t" + (char)0x200B + (char)0xFEFF + " ";
        h.Form.Html = "<p> </p>";
        await h.Ui.RunAsync(h.Draft.Send);
        Assert.Equal(["Write a comment first"], h.Form.Toasts);
        Assert.Equal([false, true], h.Form.SendEnabled); // Send comes back
        Assert.False(h.Draft.Draft.Sending);
        await h.IdleAsync();
        Assert.Empty(h.Saves);
        Assert.Empty(h.Sends);
        Assert.True(h.Sent.Count == 0 && h.Form.Closes == 0);
    }

    [Fact]
    public async Task ARefusedSendIsShownAndTheWindowStays()
    {
        await using var h = await Harness.StartAsync();
        h.SendError = new RpcError { Code = ErrorCode.InvalidArgument, Message = "comment longer than 32767 characters" };
        await h.Ui.RunAsync(h.Draft.Send);
        await h.IdleAsync();
        Assert.Equal(["Sending was rejected: comment longer than 32767 characters"], h.Form.Toasts);
        Assert.True(h.Form.SendEnabled[^1]);
        Assert.True(h.Form.Closes == 0 && h.Sent.Count == 0);

        // A draft.save refused on the way says Sending, not the draft.
        h.SendError = null;
        h.SaveError = new RpcError { Code = ErrorCode.InvalidArgument, Message = "issue WEB-7 takes no internal comment" };
        await h.Ui.RunAsync(h.Draft.Send);
        await h.IdleAsync();
        Assert.Equal(2, h.Form.Toasts.Count);
        Assert.Equal("Sending was rejected: issue WEB-7 takes no internal comment", h.Form.Toasts[^1]);
        Assert.False(h.Draft.Draft.Sending);
    }

    [Fact]
    public async Task ThereIsNoSaveDraftForAComment()
    {
        await using var h = await Harness.StartAsync();
        // The autosave keeps a copy against a crash, silently.
        await h.Ui.RunAsync(h.Draft.MarkDirty);
        await h.AutosaveAsync();
        Assert.Single(h.Saves);
        Assert.False(h.Draft.Draft.Saving);
        Assert.Equal("d1", h.Draft.Draft.DraftId?.Value);
        Assert.All(h.Form.Statuses, s => Assert.Empty(s));

        // Closing with text asks whether to discard, never Save Draft; Cancel
        // keeps the window.
        Assert.False(h.Draft.CanCloseWithoutAsking);
        h.DiscardAnswer = false;
        Assert.False(await h.CloseRequestAsync());
        Assert.Equal(1, h.DiscardQuestions);
        Assert.Equal(0, h.SaveQuestions);
        Assert.Empty(h.Deletes);

        // Discard: the saved copy goes with the window.
        h.DiscardAnswer = true;
        Assert.True(await h.CloseRequestAsync());
        Assert.Equal(2, h.DiscardQuestions);
        Assert.Equal(0, h.SaveQuestions);
        var delete = Assert.Single(h.Deletes);
        Assert.Equal((JiraAccount, "d1"), (delete.AccountId, delete.DraftId.Value));
        Assert.True(h.Draft.Draft.Closed);
    }

    [Fact]
    public async Task AnEmptyCommentClosesUnaskedAndLeavesNothing()
    {
        await using var h = await Harness.StartAsync();
        await h.Ui.RunAsync(h.Draft.MarkDirty);
        await h.AutosaveAsync();
        Assert.Single(h.Saves);
        // The text was deleted again: unsaved, but nothing to lose.
        h.Form.Text = "  ";
        await h.Ui.RunAsync(h.Draft.MarkDirty);
        Assert.True(h.Draft.CanCloseWithoutAsking);
        Assert.True(await h.CloseRequestAsync());
        Assert.True(h.DiscardQuestions == 0 && h.SaveQuestions == 0);
        Assert.Equal("d1", Assert.Single(h.Deletes).DraftId.Value);
    }

    [Fact]
    public async Task ASaveUnderWayWhenTheWindowGoesIsDeletedToo()
    {
        await using var h = await Harness.StartAsync();
        var hold = h.HoldSaves();
        await h.Ui.RunAsync(() => h.Draft.Save(SaveReason.Autosave));
        await hold.ArrivedAsync();
        await h.Ui.RunAsync(h.Draft.Cleanup);
        hold.Release();
        // The first save had no id to delete; its answer brings one.
        await h.IdleAsync();
        var delete = Assert.Single(h.Deletes);
        Assert.Equal((JiraAccount, "d1"), (delete.AccountId, delete.DraftId.Value));
    }

    [Fact]
    public async Task AFailedAutosaveOfACommentSaysNothing()
    {
        await using var h = await Harness.StartAsync();
        h.SaveError = new RpcError { Code = ErrorCode.StorageError, Message = "disk" };
        await h.Ui.RunAsync(() => h.Draft.Save(SaveReason.Autosave));
        await h.IdleAsync();
        Assert.Single(h.Saves);
        Assert.False(h.Draft.Draft.Saving);
        Assert.Empty(h.Form.Toasts);
        Assert.True(h.Draft.AutosaveArmed); // tried again later
    }

    [Fact]
    public void AFormThatKnowsNothingOfCommentsIsAMailWindow()
    {
        // The defaults of IComposeForm, which the older fake forms rely on.
        IComposeForm form = new MailForm();
        Assert.False(form.IsComment);
        Assert.Equal(CommentVisibility.Public, form.CommentVisibility);
    }

    [Fact]
    public async Task AMailWindowIsUnchanged()
    {
        await using var h = await Harness.StartAsync();
        h.Form.Comment = false;
        await h.Ui.RunAsync(h.Draft.Send);
        Assert.Equal(["Add at least one recipient"], h.Form.Toasts);
        await h.Ui.RunAsync(h.Draft.MarkDirty);
        Assert.Equal("Unsaved changes", h.Form.Statuses[^1]);
        Assert.False(h.Draft.CanCloseWithoutAsking);
    }

    // Windows-only: Quit saves dirty drafts without asking, but no Drafts
    // folder keeps a comment: an empty one is left alone, one with text is
    // asked about (the close question, Discard this message?).
    [Fact]
    public async Task QuitAsksAboutACommentWithText()
    {
        await using var h = await Harness.StartAsync();
        await h.Ui.RunAsync(h.Draft.MarkDirty);
        Assert.False(await await h.Ui.RunAsync(h.Draft.SaveForQuitAsync));
        h.Form.Text = " ";
        Assert.True(await await h.Ui.RunAsync(h.Draft.SaveForQuitAsync));
        await h.IdleAsync();
        Assert.Empty(h.Saves);
    }

    // The comment draft draft.create returns for a reply to w1 of the request.
    private static Draft CommentDraft(IssueInfo? info = null, CommentVisibility? visibility = null)
    {
        info ??= Request;
        return new Draft
        {
            AccountId = JiraAccount,
            Subject = info.Key + ": " + info.Summary,
            InReplyTo = "w1",
            Comment = new DraftComment { Issue = info, Visibility = visibility ?? "" },
        };
    }

    // The compose window in comment mode, as the controller sees it: no
    // recipient rows (they would parse as empty), the visibility control.
    private sealed class CommentForm : IComposeForm
    {
        public Account Account { get; } = new()
        {
            Id = JiraAccount,
            Config = new AccountConfig { Name = "Acme Jira", Email = "jana@acme.example", Kind = AccountKind.Jira },
            Enabled = true,
            State = new SyncState { AccountId = JiraAccount, Status = SyncStatus.Idle },
            Capabilities = [Capability.Comment, Capability.Forward],
        };

        public bool Comment { get; set; } = true;

        public CommentVisibility CommentVisibility { get; set; } = CommentVisibility.Public;

        public string Subject => "ITSD-42: The printer on the third floor";

        public IReadOnlyList<DraftAttachment> Attachments { get; private set; } = [];

        public string Html { get; set; } = "<p>Replaced the toner.</p>";

        public string Text { get; set; } = "Replaced the toner.";

        public List<string> Statuses { get; } = [];

        public List<string> Toasts { get; } = [];

        public List<bool> SendEnabled { get; } = [];

        public int Closes { get; private set; }

        bool IComposeForm.IsComment => Comment;

        public (IReadOnlyList<Address> To, IReadOnlyList<Address> Cc, IReadOnlyList<Address> Bcc, bool Ok) Recipients() => ([], [], [], true);

        public string EditorHtml() => Html;

        public string EditorText() => Text;

        public void FlushEditor(Action done) => done();

        public void SetAttachments(IReadOnlyList<DraftAttachment> attachments) => Attachments = attachments;

        public void SetStatus(string text) => Statuses.Add(text);

        public void Toast(string text) => Toasts.Add(text);

        public void SetSendEnabled(bool enabled) => SendEnabled.Add(enabled);

        public void CloseWindow() => Closes++;
    }

    // An e-mail window written before comments: none of their members.
    private sealed class MailForm : IComposeForm
    {
        public Account Account { get; } = MailModelTests.TestAccount("a", email: "me@example.invalid");

        public string Subject => "";

        public IReadOnlyList<DraftAttachment> Attachments => [];

        public (IReadOnlyList<Address> To, IReadOnlyList<Address> Cc, IReadOnlyList<Address> Bcc, bool Ok) Recipients() => ([], [], [], true);

        public string EditorHtml() => "";

        public string EditorText() => "";

        public void FlushEditor(Action done) => done();

        public void SetAttachments(IReadOnlyList<DraftAttachment> attachments)
        {
        }

        public void SetStatus(string text)
        {
        }

        public void Toast(string text)
        {
        }

        public void SetSendEnabled(bool enabled)
        {
        }

        public void CloseWindow()
        {
        }
    }

    // A fake daemon (draft.save, message.send, draft.delete), the comment
    // form and the draft controller on a test UI thread, as the window sets
    // it up from the comment draft's params.
    private sealed class Harness : IAsyncDisposable
    {
        private readonly Lock gate = new();
        private readonly List<DraftSaveParams> saves = [];
        private readonly List<MessageSendParams> sends = [];
        private readonly List<DraftDeleteParams> deletes = [];
        private HeldAnswer? held;

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeTimeProvider Time { get; } = new(new DateTimeOffset(2026, 9, 27, 10, 0, 0, TimeSpan.Zero));

        public FakeDaemon Fake { get; } = new();

        public CommentForm Form { get; } = new();

        public ComposeDraftController Draft { get; private set; } = null!;

        public RpcError? SaveError { get; set; }

        public RpcError? SendError { get; set; }

        public List<string> Sent { get; } = [];

        public int DiscardQuestions { get; private set; }

        public int SaveQuestions { get; private set; }

        public bool DiscardAnswer { get; set; } = true;

        public IReadOnlyList<DraftSaveParams> Saves => Locked(saves);

        public IReadOnlyList<MessageSendParams> Sends => Locked(sends);

        public IReadOnlyList<DraftDeleteParams> Deletes => Locked(deletes);

        private RpcClient Client { get; set; } = null!;

        public static async Task<Harness> StartAsync()
        {
            var h = new Harness();
            h.Fake.On(API.DraftSave.Name, h.SaveAsync);
            h.Fake.On(API.MessageSend.Name, h.Send);
            h.Fake.On(API.DraftDelete.Name, h.Delete);
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            h.Draft = await h.Ui.RunAsync(() =>
            {
                var draft = new ComposeDraftController(h.Client, new SettingsStore(new InMemorySettingsBackend(), null), () => false, new CidRegistry(), h.Time, h.Pending) { Form = h.Form };
                // As the window does with its parameters.
                var p = Prefill.FromDraft(ComposeKind.Reply, CommentDraft(), new BlockedContent());
                draft.SetOriginal(p.InReplyTo, p.Forwarding, p.Comment);
                draft.Sent += (_, text) => h.Sent.Add(text);
                draft.ConfirmDiscard = (heading, _, label) =>
                {
                    Assert.Equal(("Discard this message?", "_Discard"), (heading, label));
                    h.DiscardQuestions++;
                    return Task.FromResult(h.DiscardAnswer);
                };
                draft.SaveDraftQuestion = () =>
                {
                    h.SaveQuestions++;
                    return Task.FromResult(DraftCloseAnswer.Save);
                };
                return draft;
            });
            return h;
        }

        // Holds every draft.save answer until released (Swift's saveDelay).
        public HeldAnswer HoldSaves()
        {
            var hold = new HeldAnswer();
            lock (gate)
            {
                held = hold;
            }
            return hold;
        }

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake);

        // The autosave's 30 s pass.
        public async Task AutosaveAsync()
        {
            await IdleAsync();
            Time.Advance(ComposeDraftController.AutosaveDelay);
            await IdleAsync();
        }

        public async Task<bool> CloseRequestAsync()
        {
            var closed = await Ui.InvokeAsync(() => Draft.CloseRequestAsync());
            await IdleAsync();
            return closed;
        }

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(Draft.Cleanup);
            held?.Release();
            try
            {
                await Pending.IdleAsync().WaitAsync(TimeSpan.FromSeconds(10));
            }
            catch (TimeoutException)
            {
                // Torn down all the same.
            }
            Client.Dispose();
            await Fake.DisposeAsync();
            Ui.Dispose();
        }

        private async Task<string> SaveAsync(string p)
        {
            int n;
            HeldAnswer? hold;
            var q = JsonCoding.Decode<DraftSaveParams>(p);
            lock (gate)
            {
                saves.Add(q);
                n = saves.Count;
                hold = held;
            }
            if (hold is not null)
            {
                await hold.WaitAsync();
            }
            if (SaveError is { } e)
            {
                throw new RpcException(e);
            }
            return JsonCoding.EncodeToString(new DraftSaveResult { DraftId = "d" + n, Version = 1, TextBody = q.Draft.TextBody });
        }

        private string Send(string p)
        {
            lock (gate)
            {
                sends.Add(JsonCoding.Decode<MessageSendParams>(p));
            }
            if (SendError is { } e)
            {
                throw new RpcException(e);
            }
            return JsonCoding.EncodeToString(new MessageSendResult { OutboxId = "o1" });
        }

        private string Delete(string p)
        {
            lock (gate)
            {
                deletes.Add(JsonCoding.Decode<DraftDeleteParams>(p));
            }
            return "{}";
        }

        private IReadOnlyList<T> Locked<T>(List<T> list)
        {
            lock (gate)
            {
                return [.. list];
            }
        }
    }
}
