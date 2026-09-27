// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/DraftStateTests.swift (every test):
// the draft lifecycle of a compose window (ui/internal/compose/draft.go)
// over a fake form and a fake daemon. After them, what Swift has no test
// for: the editor's flush echo inside the draft controller (draft.go
// editorChanged and save's record, whose rule TestFlushEcho checks in
// Html/FlushEchoTests; here over a real EditorChannel in both orders of the
// flush's answers), and the Windows addition of saving dirty drafts on Quit
// (docs/windows-port.md §0).
//
// Swift shortens the autosave to milliseconds and sleeps; here the
// controller's clock is a FakeTimeProvider that the test advances, and the
// test waits until everything is idle (IdleAsync) before it asserts. A save
// Swift slows down with a delay is held by a gate the test opens.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Html;
using Malachi.Core.Settings;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Tests.Model;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class DraftStateTests
{
    private static readonly RpcError Conflict = new() { Code = ErrorCode.Conflict, Message = "version" };
    private static readonly RpcError ServerError = new() { Code = ErrorCode.ServerError, Message = "500" };

    [Fact]
    public async Task EditArmsTheAutosaveWhichSavesOnce()
    {
        await using var h = await Harness.StartAsync();
        h.Form.SubjectText = "Hello";
        await h.Run(() => h.Draft.MarkDirty());
        Assert.True(h.Draft.Draft.Dirty);
        Assert.True(h.Draft.AutosaveArmed);
        Assert.Equal("Unsaved changes", h.Form.Statuses[^1]);
        // A second edit does not arm a second timer.
        await h.Run(() => h.Draft.MarkDirty());
        await h.AutosaveAsync();
        Assert.Single(h.Script.Saves);
        Assert.False(h.Draft.Draft.Saving);
        Assert.Equal("d1", h.Draft.Draft.DraftId?.Value);
        Assert.Equal(1, h.Draft.Draft.Version);
        Assert.False(h.Draft.Draft.Dirty);
        Assert.False(h.Draft.AutosaveArmed);
        Assert.Contains("Saving draft…", h.Form.Statuses);
        Assert.StartsWith("Draft saved ", h.Form.Statuses[^1], StringComparison.Ordinal);
        Assert.Equal(1, h.Form.Flushes);
        var wire = h.Script.Saves[0].Draft;
        Assert.Equal("acc1", wire.AccountId.Value);
        Assert.Null(wire.Id);
        Assert.Equal(0, wire.Version);
        Assert.Equal("Hello", wire.Subject);
        Assert.Equal("<p>hi</p>", wire.HtmlBody);
        Assert.Equal("hi", wire.TextBody);
        Assert.Empty(wire.To);
        Assert.Null(wire.Attachments);
        // Fires once.
        await h.AutosaveAsync();
        Assert.Single(h.Script.Saves);
        Assert.Empty(h.Form.Toasts);
    }

    [Fact]
    public async Task BuildCarriesTheRowsAndTheAttachmentIDs()
    {
        await using var h = await Harness.StartAsync();
        h.Form.To = "Alice <alice@example.org>, bob@example.org";
        h.Form.Cc = "carol@example.org";
        h.Form.AttachmentList = [Att("a1"), Att("a2", inline: true, cid: "c2")];
        var wire = await h.Ui.RunAsync(() =>
        {
            h.Draft.SetOriginal("m9", null);
            return h.Draft.Build();
        });
        Assert.NotNull(wire);
        Assert.Equal([new Address { Name = "Alice", Email = "alice@example.org" }, new Address { Email = "bob@example.org" }], wire.To);
        Assert.Equal([new Address { Email = "carol@example.org" }], wire.Cc!);
        Assert.Null(wire.Bcc);
        Assert.Equal("m9", wire.InReplyTo?.Value);
        Assert.Null(wire.Forwarding);
        Assert.Equal(["a1", "a2"], wire.Attachments!.Select(a => a.Id));
    }

    [Fact]
    public async Task SaveQueuesDoneBehindTheSaveInFlight()
    {
        await using var h = await Harness.StartAsync();
        var gate = h.Script.HoldSaves();
        var done = new List<int>();
        await h.Run(() =>
        {
            h.Draft.Save(SaveReason.Explicit, _ => done.Add(1));
            Assert.True(h.Draft.Draft.Saving);
            Assert.Equal("Saving draft…", h.Form.Statuses[^1]);
            h.Draft.Save(SaveReason.Explicit, _ => done.Add(2));
        });
        await Eventually.Holds(() => h.Script.Saves.Count == 1);
        gate.SetResult();
        await h.IdleAsync();
        Assert.Equal([1, 2], done);
        Assert.Single(h.Script.Saves);
        Assert.Equal(1, h.Form.Flushes);
        Assert.False(h.Draft.Draft.Saving);
        // Edits made during the save arm the autosave again afterwards.
        await h.Run(() =>
        {
            h.Draft.Save(SaveReason.Explicit);
            h.Draft.MarkDirty();
        });
        await h.IdleAsync();
        Assert.False(h.Draft.Draft.Saving);
        Assert.True(h.Draft.Draft.Dirty);
        Assert.True(h.Draft.AutosaveArmed);
    }

    [Fact]
    public async Task AttachmentsAreReplacedWhenTheCountDiffers()
    {
        await using var h = await Harness.StartAsync();
        h.Form.AttachmentList = [Att("a1")];
        h.Script.SaveAttachments = [Att("a1"), Att("a2")];
        Assert.Null(await h.SaveNowAsync());
        Assert.Equal([["a1", "a2"]], h.Form.Replaced.Select(l => l.Select(a => a.Id).ToArray()));
        // The same count: the window's list stands.
        Assert.Null(await h.SaveNowAsync());
        Assert.Single(h.Form.Replaced);
        // A daemon that lists none replaces one.
        h.Script.SaveAttachments = null;
        Assert.Null(await h.SaveNowAsync());
        Assert.Equal(2, h.Form.Replaced.Count);
        Assert.Empty(h.Form.AttachmentList);
    }

    [Fact]
    public async Task BlockedContentIsSaidOnce()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SaveBlocked = new BlockedContent { RemoteImages = 2, Scripts = 1 };
        Assert.Null(await h.SaveNowAsync());
        Assert.Equal(["3 unsafe elements were removed from the message"], h.Form.Toasts);
        h.Script.SaveBlocked = new BlockedContent();
        Assert.Null(await h.SaveNowAsync());
        Assert.Single(h.Form.Toasts);
    }

    [Fact]
    public async Task ConflictStartsOverWithAFreshDraft()
    {
        await using var h = await Harness.StartAsync();
        Assert.Null(await h.SaveNowAsync());
        Assert.Equal("d1", h.Draft.Draft.DraftId?.Value);
        h.Script.SaveError = Conflict;
        var err = await h.SaveNowAsync();
        Assert.Equal(ErrorCode.Conflict, Assert.IsType<RpcException>(err).Code.Value);
        Assert.Null(h.Draft.Draft.DraftId);
        Assert.Equal(0, h.Draft.Draft.Version);
        Assert.True(h.Draft.Draft.Dirty);
        Assert.Equal(["This draft was changed elsewhere; your text will be saved as a new draft"], h.Form.Toasts);
        Assert.False(h.Draft.AutosaveArmed, "a conflict waits for the next edit");
        Assert.Equal("Unsaved changes", h.Form.Statuses[^1]);
        // The next save creates a new draft with our text.
        h.Script.SaveError = null;
        Assert.Null(await h.SaveNowAsync());
        Assert.Null(h.Script.Saves[^1].Draft.Id);
        Assert.Equal("d1", h.Draft.Draft.DraftId?.Value);
    }

    /// <summary>
    /// A draft opened from the Drafts folder: <c>replaces</c> goes with the
    /// first save only, a draft deleted meanwhile starts over as a new one,
    /// and a Drafts message gone meanwhile is dropped from the next save
    /// (draft.go <c>saveFailed</c>).
    /// </summary>
    [Fact]
    public async Task OpenedDraftSavesAndStartsOver()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() => h.Draft.SetOpened(null, 0, "m9", fromDrafts: true));
        Assert.True(h.Draft.Draft.ExplicitSave);
        Assert.Null(await h.SaveNowAsync());
        Assert.Equal("m9", h.Script.Saves[^1].Draft.Replaces?.Value);
        Assert.Null(h.Draft.Draft.Replaces);
        Assert.Null(await h.SaveNowAsync());
        Assert.Null(h.Script.Saves[^1].Draft.Replaces);

        h.Script.SaveError = new RpcError { Code = ErrorCode.DraftNotFound, Message = "gone" };
        _ = await h.SaveNowAsync();
        Assert.True(h.Draft.Draft.DraftId is null && h.Draft.Draft.Version == 0);
        Assert.Equal(["This draft was removed elsewhere; your text will be saved as a new draft"], h.Form.Toasts);

        await using var g = await Harness.StartAsync();
        await g.Run(() => g.Draft.SetOpened("d7", 2, "m7", fromDrafts: true));
        g.Script.SaveError = new RpcError { Code = ErrorCode.MessageNotFound, Message = "gone" };
        _ = await g.SaveNowAsync();
        var last = g.Script.Saves[^1].Draft;
        Assert.True(last.Id?.Value == "d7" && last.Version == 2);
        Assert.Null(g.Draft.Draft.Replaces);
        Assert.True(g.Draft.AutosaveArmed, "saved again without the message");
        Assert.Empty(g.Form.Toasts);
    }

    /// <summary>
    /// Discard in the close question deletes what only the autosave stored,
    /// and keeps a draft the user saved or opened from the Drafts folder.
    /// </summary>
    [Fact]
    public async Task CloseDiscardDeletesOnlyAnAutosavedDraft()
    {
        await using var auto = await Harness.StartAsync();
        auto.Draft.SaveDraftQuestion = () => Task.FromResult(DraftCloseAnswer.Discard);
        await auto.Run(() => auto.Draft.MarkDirty());
        await auto.AutosaveAsync();
        Assert.Single(auto.Script.Saves);
        Assert.False(auto.Draft.Draft.ExplicitSave);
        await auto.Run(() => auto.Draft.MarkDirty());
        Assert.True(await auto.CloseRequestAsync());
        await auto.IdleAsync();
        var delete = Assert.Single(auto.Script.Deletes);
        Assert.Equal(("acc1", "d1"), (delete.AccountId.Value, delete.DraftId.Value));

        await using var saved = await Harness.StartAsync();
        saved.Draft.SaveDraftQuestion = () => Task.FromResult(DraftCloseAnswer.Discard);
        Assert.Null(await saved.SaveNowAsync());
        Assert.True(saved.Draft.Draft.ExplicitSave);
        await saved.Run(() => saved.Draft.MarkDirty());
        Assert.True(await saved.CloseRequestAsync());

        await using var opened = await Harness.StartAsync();
        opened.Draft.SaveDraftQuestion = () => Task.FromResult(DraftCloseAnswer.Discard);
        await opened.Run(() =>
        {
            opened.Draft.SetOpened("d7", 2, null, fromDrafts: true);
            opened.Draft.MarkDirty();
        });
        Assert.True(await opened.CloseRequestAsync());

        await saved.IdleAsync();
        await opened.IdleAsync();
        Assert.Empty(saved.Script.Deletes);
        Assert.Empty(opened.Script.Deletes);
    }

    [Fact]
    public async Task RepeatedAutosaveFailureToastsOnceExplicitSaveAlways()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SaveError = ServerError;
        await h.Run(() => h.Draft.MarkDirty());
        await h.AutosaveAsync();
        Assert.Single(h.Script.Saves);
        Assert.Equal(["Saving the draft failed: the server returned an error"], h.Form.Toasts);
        Assert.True(h.Draft.Draft.Dirty);
        Assert.True(h.Draft.AutosaveArmed, "retried later");
        await h.AutosaveAsync();
        Assert.Equal(2, h.Script.Saves.Count);
        Assert.True(h.Form.Toasts.Count == 1, "the same failure is not repeated");
        Assert.False(h.Draft.AutosaveArmed, "the repeat waits for the next edit");
        // An explicit save always says so.
        _ = await h.SaveNowAsync();
        Assert.Equal(2, h.Form.Toasts.Count);
        // A different failure is said again by the autosave.
        h.Script.SaveError = new RpcError { Code = ErrorCode.NetworkError, Message = "down" };
        await h.Run(() => h.Draft.MarkDirty());
        await h.AutosaveAsync();
        Assert.Equal(4, h.Script.Saves.Count);
        Assert.Equal("Saving the draft failed: the server could not be reached", h.Form.Toasts[^1]);
        Assert.Equal(3, h.Form.Toasts.Count);
    }

    [Fact]
    public async Task SendRefusesBadOrMissingRecipients()
    {
        await using var h = await Harness.StartAsync();
        h.Form.To = "not an address";
        await h.Run(() => h.Draft.Send());
        Assert.Equal(["Fix the highlighted recipients"], h.Form.Toasts);
        Assert.False(h.Draft.Draft.Sending);
        h.Form.To = "";
        await h.Run(() => h.Draft.Send());
        Assert.Equal("Add at least one recipient", h.Form.Toasts[^1]);
        await h.IdleAsync();
        Assert.Empty(h.Fake.Calls);
        Assert.Empty(h.Form.SendEnabled);
    }

    [Fact]
    public async Task SendSavesThenQueuesAndCloses()
    {
        await using var h = await Harness.StartAsync();
        h.Form.To = "bob@example.org";
        var sent = new List<string>();
        h.Draft.Sent += (_, text) => sent.Add(text);
        await h.Run(() =>
        {
            h.Draft.Send();
            Assert.True(h.Draft.Draft.Sending);
            Assert.Equal([false], h.Form.SendEnabled);
            Assert.Equal("Sending…", h.Form.Statuses[^1]);
            h.Draft.Send(); // a second click while sending does nothing
        });
        await h.IdleAsync();
        Assert.Equal(1, h.Form.Closes);
        Assert.Equal([API.DraftSave.Name, API.MessageSend.Name], h.Fake.Calls);
        var send = Assert.Single(h.Script.Sends);
        Assert.Equal(("acc1", "d1", 1), (send.AccountId.Value, send.DraftId.Value, send.Version));
        Assert.Equal(["Message queued for sending"], sent);
        Assert.True(h.Draft.Draft.Discard);
        Assert.Equal([false], h.Form.SendEnabled);
    }

    [Fact]
    public async Task SendConflictResetsTheDraftAndReenables()
    {
        await using var h = await Harness.StartAsync();
        h.Form.To = "bob@example.org";
        h.Script.SendError = Conflict;
        await h.Run(() => h.Draft.Send());
        await h.IdleAsync();
        Assert.Equal([false, true], h.Form.SendEnabled);
        Assert.Null(h.Draft.Draft.DraftId);
        Assert.Equal(0, h.Draft.Draft.Version);
        Assert.True(h.Draft.Draft.Dirty);
        Assert.False(h.Draft.Draft.Sending);
        Assert.Equal(["Sending conflicted with another change"], h.Form.Toasts);
        Assert.Equal(0, h.Form.Closes);
        Assert.False(h.Draft.Draft.Discard);
        Assert.Equal("Unsaved changes", h.Form.Statuses[^1]);

        // Any other failure keeps the draft.
        h.Script.SendError = ServerError;
        await h.Run(() => h.Draft.Send());
        await h.IdleAsync();
        Assert.Equal([false, true, false, true], h.Form.SendEnabled);
        Assert.Equal("d1", h.Draft.Draft.DraftId?.Value);
        Assert.Equal("Sending failed: the server returned an error", h.Form.Toasts[^1]);

        // A failed save never reaches message.send.
        h.Script.SaveError = ServerError;
        await h.Run(() => h.Draft.Send());
        await h.IdleAsync();
        Assert.Equal(6, h.Form.SendEnabled.Count);
        Assert.Equal("Saving the draft failed: the server returned an error", h.Form.Toasts[^1]);
        Assert.Equal(2, h.Script.Sends.Count);
        Assert.False(h.Draft.Draft.Sending);
    }

    [Fact]
    public async Task DiscardWithoutConfirmationDeletesTheDraft()
    {
        await using var h = await Harness.StartAsync();
        h.Settings.ConfirmDelete = false;
        Assert.Null(await h.SaveNowAsync());
        await h.Run(() =>
        {
            h.Draft.MarkDirty();
            h.Draft.Discard();
            Assert.Equal(1, h.Form.Closes);
            Assert.True(h.Draft.Draft.Discard);
        });
        await h.IdleAsync();
        var delete = Assert.Single(h.Script.Deletes);
        Assert.Equal(("acc1", "d1"), (delete.AccountId.Value, delete.DraftId.Value));
        Assert.Empty(h.Script.Removes);
    }

    [Fact]
    public async Task DiscardOfAnUnsavedDraftReleasesItsAttachments()
    {
        await using var h = await Harness.StartAsync();
        // Confirmation is on, but nothing was typed and nothing saved: no
        // question (the template's files are released all the same).
        var asked = 0;
        h.Draft.ConfirmDiscard = (_, _, _) =>
        {
            asked++;
            return Task.FromResult(true);
        };
        h.Form.AttachmentList = [Att("a1"), Att("a2", inline: true, cid: "c2")];
        await h.Run(() =>
        {
            h.Draft.Discard();
            Assert.Equal(0, asked);
            Assert.Equal(1, h.Form.Closes);
        });
        await h.IdleAsync();
        Assert.Equal(2, h.Script.Removes.Count);
        Assert.Equal(["a1", "a2"], h.Script.Removes.Select(r => r.AttachmentId).Order(StringComparer.Ordinal));
        Assert.All(h.Script.Removes, r => Assert.Equal("acc1", r.AccountId.Value));
        Assert.Empty(h.Script.Deletes);
    }

    [Fact]
    public async Task DiscardAsksWhileConfirmationIsOnAndThereIsSomethingToLose()
    {
        // No autosave comes: the clock is not advanced.
        await using var h = await Harness.StartAsync();
        var asked = new List<(string Heading, string Body, string Label)>();
        var answer = false;
        h.Draft.ConfirmDiscard = (heading, body, label) =>
        {
            asked.Add((heading, body, label));
            return Task.FromResult(answer);
        };
        await h.Run(() =>
        {
            h.Draft.MarkDirty();
            h.Draft.Discard();
        });
        await h.IdleAsync();
        Assert.Equal(("Discard this message?", "", "_Discard"), Assert.Single(asked));
        Assert.Equal(0, h.Form.Closes);
        Assert.False(h.Draft.Draft.Discard);

        answer = true;
        await h.Run(() => h.Draft.Discard());
        await h.IdleAsync();
        Assert.Equal(1, h.Form.Closes);
        Assert.True(h.Draft.Draft.Discard);
        // Never saved: nothing to delete, no attachments to release.
        Assert.Empty(h.Fake.Calls);
    }

    [Fact]
    public async Task CloseRequestAnswers()
    {
        // Clean: closes at once, cleaned up.
        await using var clean = await Harness.StartAsync();
        var questions = 0;
        clean.Draft.SaveDraftQuestion = () =>
        {
            questions++;
            return Task.FromResult(DraftCloseAnswer.Cancel);
        };
        Assert.True(clean.Draft.CanCloseWithoutAsking);
        Assert.True(await clean.CloseRequestAsync());
        Assert.True(clean.Draft.Draft.Closed);
        Assert.Equal(0, questions);

        // Dirty, Cancel: stays.
        await using var h = await Harness.StartAsync();
        var answer = DraftCloseAnswer.Cancel;
        h.Draft.SaveDraftQuestion = () => Task.FromResult(answer);
        await h.Run(() => h.Draft.MarkDirty());
        Assert.False(h.Draft.CanCloseWithoutAsking);
        Assert.False(await h.CloseRequestAsync());
        Assert.False(h.Draft.Draft.Closed);
        Assert.True(h.Draft.Draft.Dirty);

        // Dirty, Save Draft, the save fails: stays, the toast says why.
        answer = DraftCloseAnswer.Save;
        h.Script.SaveError = ServerError;
        Assert.False(await h.CloseRequestAsync());
        Assert.False(h.Draft.Draft.Closed);
        Assert.Equal(["Saving the draft failed: the server returned an error"], h.Form.Toasts);
        Assert.True(h.Draft.Draft.Dirty);

        // Dirty, Save Draft, the save succeeds: closes.
        h.Script.SaveError = null;
        Assert.True(await h.CloseRequestAsync());
        Assert.Equal(2, h.Script.Saves.Count);
        Assert.True(h.Draft.Draft.Discard);
        Assert.True(h.Draft.Draft.Closed);
        Assert.False(h.Draft.AutosaveArmed);

        // Dirty, Discard: closes without saving.
        await using var d = await Harness.StartAsync();
        d.Draft.SaveDraftQuestion = () => Task.FromResult(DraftCloseAnswer.Discard);
        await d.Run(() => d.Draft.MarkDirty());
        Assert.True(await d.CloseRequestAsync());
        Assert.True(d.Draft.Draft.Discard);
        Assert.True(d.Draft.Draft.Closed);
        await d.IdleAsync();
        Assert.Empty(d.Fake.Calls);

        // Saving in flight: the question is asked as well.
        await using var s = await Harness.StartAsync();
        var gate = s.Script.HoldSaves();
        s.Draft.SaveDraftQuestion = () => Task.FromResult(DraftCloseAnswer.Cancel);
        await s.Run(() => s.Draft.Save(SaveReason.Explicit));
        Assert.False(s.Draft.CanCloseWithoutAsking);
        Assert.False(await s.Ui.InvokeAsync(() => s.Draft.CloseRequestAsync()));
        gate.SetResult();
        await s.IdleAsync();
    }

    [Fact]
    public async Task CleanupDisarmsTheAutosaveAndForgetsInlinePictures()
    {
        await using var h = await Harness.StartAsync();
        h.Registry.Register("c1", "/tmp/x.png", "image/png");
        h.Registry.Register("other", "/tmp/y.png", "image/png");
        h.Form.AttachmentList = [Att("a1", inline: true, cid: "c1"), Att("a2")];
        await h.Run(() =>
        {
            h.Draft.MarkDirty();
            h.Draft.Cleanup();
        });
        Assert.True(h.Draft.Draft.Closed);
        Assert.False(h.Draft.AutosaveArmed);
        Assert.False(h.Registry.IsRegistered("c1"));
        Assert.True(h.Registry.IsRegistered("other"), "only the window's own pictures");
        await h.Run(() => h.Draft.Cleanup()); // idempotent
        await h.AutosaveAsync();
        Assert.True(h.Fake.Calls.Count == 0, "the autosave never fired");
    }

    [Fact]
    public async Task RepliesAfterTheWindowClosedAreDropped()
    {
        await using var h = await Harness.StartAsync();
        var gate = h.Script.HoldSaves();
        var outcomes = new List<Exception?>();
        var statuses = 0;
        await h.Run(() =>
        {
            h.Draft.Save(SaveReason.Explicit, outcomes.Add);
            statuses = h.Form.Statuses.Count;
            h.Draft.Cleanup();
            // The awaited outcome is answered with a cancellation at once…
            Assert.IsType<OperationCanceledException>(Assert.Single(outcomes));
        });
        // …the save still reaches the daemon (GTK: a closed window does not
        // cancel a save in flight), and its late reply changes nothing.
        await Eventually.Holds(() => h.Script.Saves.Count == 1);
        gate.SetResult();
        await h.IdleAsync();
        Assert.Null(h.Draft.Draft.DraftId);
        Assert.True(h.Draft.Draft.Saving, "left as it was");
        Assert.Equal(statuses, h.Form.Statuses.Count);
        Assert.Empty(h.Form.Toasts);
        Assert.Single(outcomes);
    }

    [Fact]
    public async Task StatusLadder()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() => h.Draft.RefreshStatus());
        Assert.Equal("", h.Form.Statuses[^1]);
        // The placeholder identity is announced while nothing else is.
        await using var p = await Harness.StartAsync(placeholder: true);
        await p.Run(() => p.Draft.RefreshStatus());
        Assert.Equal("Using placeholder account", p.Form.Statuses[^1]);
        await p.Run(() => p.Draft.MarkDirty());
        Assert.Equal("Unsaved changes", p.Form.Statuses[^1]);
        await p.Run(() => p.Draft.Cleanup());
    }

    [Fact]
    public async Task WithoutADaemonTheSaveFailsAndTheAutosaveRetries()
    {
        await using var h = await Harness.StartAsync(connect: false);
        await h.Run(() => h.Draft.MarkDirty());
        await h.AutosaveAsync();
        Assert.Equal(["Saving the draft needs a running mail backend"], h.Form.Toasts);
        Assert.True(h.Draft.Draft.Dirty);
        Assert.True(h.Draft.AutosaveArmed);
    }

    // draft.go editorChanged and save's record (Go TestFlushEcho is
    // Html/FlushEchoTests; Swift keeps the rule in its window and has no
    // test): the editor's changed that a save's flush produces is the echo
    // of what is saved, in either order of the flush's two answers, and any
    // other content is an edit.

    [Theory]
    [InlineData(true)] // WebView2's order: the changed before the script's result
    [InlineData(false)] // the result first
    public async Task TheFlushEchoLeavesTheSavedDraftClean(bool changedFirst)
    {
        await using var h = await Harness.StartAsync(editor: true);
        var editor = h.Form.Editor!;
        await h.Run(() =>
        {
            editor.Receive("{\"type\":\"ready\"}");
            editor.Receive(Changed(1, "<p>a</p>"));
        });
        Assert.True(h.Draft.Draft.Dirty, "new content is an edit");

        await h.Run(() => h.Draft.Save(SaveReason.Explicit));
        await h.Run(() => h.Form.AnswerFlush(Changed(2, "<p>a</p>"), 2, changedFirst));
        await h.IdleAsync();
        Assert.Equal("<p>a</p>", Assert.Single(h.Script.Saves).Draft.HtmlBody);
        Assert.False(h.Draft.Draft.Dirty, "the flush's own changed marked the saved draft dirty");
        Assert.False(h.Draft.AutosaveArmed);
        Assert.StartsWith("Draft saved ", h.Form.Statuses[^1], StringComparison.Ordinal);

        // A late debounced changed with the same content stays an echo.
        await h.Run(() => editor.Receive(Changed(3, "<p>a</p>")));
        Assert.False(h.Draft.Draft.Dirty);
        // New content is an edit, and so is going back to the saved text.
        await h.Run(() => editor.Receive(Changed(4, "<p>ab</p>")));
        Assert.True(h.Draft.Draft.Dirty);
        Assert.True(h.Draft.AutosaveArmed);
        Assert.Single(h.Script.Saves);
    }

    // Saving on Quit (docs/windows-port.md §0, a Windows addition): without
    // a question, with the flush first, and false only when a save failed.

    [Fact]
    public async Task SaveForQuitSavesADirtyDraftWithoutAsking()
    {
        await using var h = await Harness.StartAsync();
        var questions = 0;
        h.Draft.SaveDraftQuestion = () =>
        {
            questions++;
            return Task.FromResult(DraftCloseAnswer.Cancel);
        };
        await h.Run(() => h.Draft.MarkDirty());
        Assert.True(await h.SaveForQuitAsync());
        Assert.Equal(0, questions);
        Assert.Single(h.Script.Saves);
        Assert.False(h.Draft.Draft.Dirty);
        Assert.True(h.Draft.Draft.ExplicitSave, "the draft is the user's to keep");
        Assert.False(h.Draft.AutosaveArmed);
        // The window stays open for the app to close; nothing is asked then.
        Assert.Equal(0, h.Form.Closes);
        Assert.False(h.Draft.Draft.Closed);
        Assert.True(h.Draft.CanCloseWithoutAsking);
        Assert.Empty(h.Form.Toasts);
    }

    [Fact]
    public async Task SaveForQuitOfACleanDraftSavesNothing()
    {
        await using var h = await Harness.StartAsync();
        Assert.True(await h.SaveForQuitAsync());
        Assert.Equal(1, h.Form.Flushes);
        await h.IdleAsync();
        Assert.Empty(h.Fake.Calls);

        // A window being discarded, or closed, has nothing left to save.
        await h.Run(() => h.Draft.MarkDirty());
        await h.Run(() => h.Draft.Cleanup());
        Assert.True(await h.SaveForQuitAsync());
        await h.IdleAsync();
        Assert.Empty(h.Fake.Calls);
    }

    /// <summary>
    /// An edit the editor has not reported yet (its changed is debounced) is
    /// fetched with a flush and saved, whichever of the flush's answers
    /// comes first.
    /// </summary>
    [Theory]
    [InlineData(true)]
    [InlineData(false)]
    public async Task SaveForQuitSavesAnEditTheEditorHasNotReported(bool changedFirst)
    {
        await using var h = await Harness.StartAsync(editor: true);
        var editor = h.Form.Editor!;
        await h.Run(() =>
        {
            editor.Receive("{\"type\":\"ready\"}");
            editor.Receive(Changed(1, "<p>a</p>"));
            h.Draft.Save(SaveReason.Explicit);
            h.Form.AnswerFlush(Changed(2, "<p>a</p>"), 2, changedFirst);
        });
        await h.IdleAsync();
        Assert.Single(h.Script.Saves);
        Assert.False(h.Draft.Draft.Dirty);

        // "b" typed; the page has not posted it yet.
        var quit = h.Ui.InvokeAsync(() => h.Draft.SaveForQuitAsync());
        await h.IdleAsync();
        Assert.Equal(2, h.Form.PendingFlushes);
        await h.Run(() => h.Form.AnswerFlush(Changed(3, "<p>ab</p>"), 3, changedFirst));
        await h.IdleAsync();
        Assert.True(h.Draft.Draft.Saving, "the unreported edit is being saved");
        Assert.Equal(3, h.Form.PendingFlushes);
        await h.Run(() => h.Form.AnswerFlush(Changed(4, "<p>ab</p>"), 4, changedFirst));
        Assert.True(await quit);
        Assert.Equal(2, h.Script.Saves.Count);
        Assert.Equal("<p>ab</p>", h.Script.Saves[^1].Draft.HtmlBody);
        Assert.False(h.Draft.Draft.Dirty);
        Assert.False(h.Draft.AutosaveArmed);
    }

    [Fact]
    public async Task SaveForQuitWaitsForASaveInFlightAndSavesTheEditsAfterIt()
    {
        await using var h = await Harness.StartAsync();
        var gate = h.Script.HoldSaves();
        await h.Run(() =>
        {
            h.Form.SubjectText = "first";
            h.Draft.Save(SaveReason.Autosave);
        });
        await Eventually.Holds(() => h.Script.Saves.Count == 1);
        // An edit while the autosave is in flight.
        h.Form.SubjectText = "second";
        await h.Run(() => h.Draft.MarkDirty());
        var quit = h.Ui.InvokeAsync(() => h.Draft.SaveForQuitAsync());
        gate.SetResult();
        Assert.True(await quit);
        Assert.Equal(["first", "second"], h.Script.Saves.Select(s => s.Draft.Subject));
        Assert.False(h.Draft.Draft.Dirty);
        Assert.False(h.Draft.Draft.Saving);
        Assert.False(h.Draft.AutosaveArmed);
    }

    /// <summary>
    /// A failed save is reported, so that the app asks this window's
    /// question, and only then; the window stays open and dirty.
    /// </summary>
    [Fact]
    public async Task SaveForQuitReportsAFailedSaveAndTheQuestionFollows()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SaveError = ServerError;
        var questions = 0;
        h.Draft.SaveDraftQuestion = () =>
        {
            questions++;
            return Task.FromResult(DraftCloseAnswer.Discard);
        };
        await h.Run(() => h.Draft.MarkDirty());
        Assert.False(await h.SaveForQuitAsync());
        Assert.Equal(0, questions);
        Assert.Equal(["Saving the draft failed: the server returned an error"], h.Form.Toasts);
        Assert.True(h.Draft.Draft.Dirty);
        Assert.False(h.Draft.Draft.Closed);
        Assert.Equal(0, h.Form.Closes);

        // The app asks now; Discard closes without another save.
        Assert.True(await h.CloseRequestAsync());
        Assert.Equal(1, questions);
        Assert.True(h.Draft.Draft.Closed);
        Assert.Single(h.Script.Saves);
    }

    // Helpers

    private static DraftAttachment Att(string id, bool inline = false, string? cid = null) =>
        new() { Id = id, Filename = id + ".bin", ContentType = "application/octet-stream", Size = 10, Inline = inline, ContentId = cid };

    private static string Changed(long seq, string html) =>
        "{\"type\":\"changed\",\"seq\":" + seq + ",\"html\":\"" + html + "\",\"text\":\"t" + seq + "\"}";

    /// <summary>The daemon's answers and what it was asked, off the UI thread.</summary>
    private sealed class Script
    {
        private readonly Lock gate = new();
        private readonly List<DraftSaveParams> saves = [];
        private readonly List<MessageSendParams> sends = [];
        private readonly List<DraftDeleteParams> deletes = [];
        private readonly List<AttachmentRemoveParams> removes = [];
        private TaskCompletionSource? held;

        public RpcError? SaveError { get; set; }

        public BlockedContent SaveBlocked { get; set; } = new();

        public IReadOnlyList<DraftAttachment>? SaveAttachments { get; set; }

        public RpcError? SendError { get; set; }

        public IReadOnlyList<DraftSaveParams> Saves => Locked(saves);

        public IReadOnlyList<MessageSendParams> Sends => Locked(sends);

        public IReadOnlyList<DraftDeleteParams> Deletes => Locked(deletes);

        public IReadOnlyList<AttachmentRemoveParams> Removes => Locked(removes);

        /// <summary>Holds every draft.save answer until the returned gate opens (Swift's saveDelay).</summary>
        public TaskCompletionSource HoldSaves()
        {
            var g = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            lock (gate)
            {
                held = g;
            }
            return g;
        }

        public async Task<string> Save(string p)
        {
            int version;
            Task wait;
            lock (gate)
            {
                saves.Add(JsonCoding.Decode<DraftSaveParams>(p));
                version = saves.Count;
                wait = held?.Task ?? Task.CompletedTask;
            }
            await wait;
            if (SaveError is { } e)
            {
                throw new RpcException(e);
            }
            return JsonCoding.EncodeToString(new DraftSaveResult
            {
                DraftId = "d1",
                Version = version,
                TextBody = "",
                Blocked = SaveBlocked,
                Attachments = SaveAttachments,
            });
        }

        public string Send(string p)
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

        public string Delete(string p)
        {
            lock (gate)
            {
                deletes.Add(JsonCoding.Decode<DraftDeleteParams>(p));
            }
            return "{}";
        }

        public string Remove(string p)
        {
            lock (gate)
            {
                removes.Add(JsonCoding.Decode<AttachmentRemoveParams>(p));
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

    /// <summary>
    /// The compose window as the controller sees it. With an editor its
    /// content and flushes are an EditorChannel's, whose Changed reaches the
    /// controller as the window forwards it; without one a flush answers at
    /// once, as Swift's fake does.
    /// </summary>
    private sealed class FakeForm : IComposeForm
    {
        private readonly List<long> flushIds = [];

        public FakeForm(bool editor)
        {
            Editor = editor ? new EditorChannel() : null;
        }

        public EditorChannel? Editor { get; }

        public Account Account { get; set; } = MailModelTests.TestAccount("acc1", email: "me@example.invalid", displayName: "Me");

        public string To { get; set; } = "";

        public string Cc { get; set; } = "";

        public string Bcc { get; set; } = "";

        public string SubjectText { get; set; } = "";

        public IReadOnlyList<DraftAttachment> AttachmentList { get; set; } = [];

        public string Html { get; set; } = "<p>hi</p>";

        public string Text { get; set; } = "hi";

        public int Flushes { get; private set; }

        /// <summary>The flushes started on the editor so far.</summary>
        public int PendingFlushes => flushIds.Count;

        public List<string> Statuses { get; } = [];

        public List<string> Toasts { get; } = [];

        public List<bool> SendEnabled { get; } = [];

        public int Closes { get; private set; }

        public List<IReadOnlyList<DraftAttachment>> Replaced { get; } = [];

        public string Subject => SubjectText;

        public IReadOnlyList<DraftAttachment> Attachments => AttachmentList;

        public (IReadOnlyList<Address> To, IReadOnlyList<Address> Cc, IReadOnlyList<Address> Bcc, bool Ok) Recipients()
        {
            var t = AddressList.Parse(To);
            var c = AddressList.Parse(Cc);
            var b = AddressList.Parse(Bcc);
            return (t.Addresses, c.Addresses, b.Addresses, t.Invalid.Count == 0 && c.Invalid.Count == 0 && b.Invalid.Count == 0);
        }

        public string EditorHtml() => Editor?.Html ?? Html;

        public string EditorText() => Editor?.Text ?? Text;

        public void FlushEditor(Action done)
        {
            Flushes++;
            if (Editor is null)
            {
                done();
                return;
            }
            if (Editor.BeginFlush(done) is { } id)
            {
                flushIds.Add(id);
            }
        }

        /// <summary>
        /// The page answers the latest flush: its changed and the script's
        /// result <paramref name="seq"/>, in the order given.
        /// </summary>
        public void AnswerFlush(string changed, long seq, bool changedFirst)
        {
            var id = flushIds[^1];
            var result = seq.ToString(System.Globalization.CultureInfo.InvariantCulture);
            if (changedFirst)
            {
                Editor!.Receive(changed);
                Editor.Flushed(id, result);
            }
            else
            {
                Editor!.Flushed(id, result);
                Editor.Receive(changed);
            }
        }

        public void SetAttachments(IReadOnlyList<DraftAttachment> attachments)
        {
            AttachmentList = attachments;
            Replaced.Add(attachments);
        }

        public void SetStatus(string text) => Statuses.Add(text);

        public void Toast(string text) => Toasts.Add(text);

        public void SetSendEnabled(bool enabled) => SendEnabled.Add(enabled);

        public void CloseWindow() => Closes++;
    }

    /// <summary>A fake daemon, a client (connected unless asked otherwise), the form and the draft controller on a test UI thread.</summary>
    private sealed class Harness : IAsyncDisposable
    {
        private Harness(bool editor)
        {
            Form = new FakeForm(editor);
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeTimeProvider Time { get; } = new(new DateTimeOffset(2026, 9, 27, 10, 0, 0, TimeSpan.Zero));

        public FakeDaemon Fake { get; } = new();

        public Script Script { get; } = new();

        public RpcClient Client { get; private set; } = null!;

        public SettingsStore Settings { get; } = new(new InMemorySettingsBackend(), null);

        public FakeForm Form { get; }

        public CidRegistry Registry { get; } = new();

        public ComposeDraftController Draft { get; private set; } = null!;

        public static async Task<Harness> StartAsync(bool connect = true, bool placeholder = false, bool editor = false)
        {
            var h = new Harness(editor);
            h.Fake.On(API.DraftSave.Name, h.Script.Save);
            h.Fake.On(API.MessageSend.Name, h.Script.Send);
            h.Fake.On(API.DraftDelete.Name, h.Script.Delete);
            h.Fake.On(API.AttachmentRemove.Name, h.Script.Remove);
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            if (connect)
            {
                await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            }
            h.Draft = await h.Ui.RunAsync(() =>
            {
                var draft = new ComposeDraftController(h.Client, h.Settings, () => placeholder, h.Registry, h.Time, h.Pending) { Form = h.Form };
                if (h.Form.Editor is { } channel)
                {
                    // The window forwards the editor's Changed (compose.go).
                    channel.Changed += (_, _) => draft.EditorChanged();
                }
                return draft;
            });
            return h;
        }

        public Task Run(Action action) => Ui.RunAsync(action);

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake);

        /// <summary>The autosave's 30 s pass: the timer armed by now fires, and everything it started settles.</summary>
        public async Task AutosaveAsync()
        {
            await IdleAsync();
            Time.Advance(ComposeDraftController.AutosaveDelay);
            await IdleAsync();
        }

        /// <summary>One explicit save, awaited.</summary>
        public async Task<Exception?> SaveNowAsync()
        {
            var done = new TaskCompletionSource<Exception?>(TaskCreationOptions.RunContinuationsAsynchronously);
            await Run(() => Draft.Save(SaveReason.Explicit, e => done.TrySetResult(e)));
            var error = await done.Task;
            await IdleAsync();
            return error;
        }

        public async Task<bool> CloseRequestAsync()
        {
            var closed = await Ui.InvokeAsync(() => Draft.CloseRequestAsync());
            await IdleAsync();
            return closed;
        }

        public async Task<bool> SaveForQuitAsync()
        {
            var saved = await Ui.InvokeAsync(() => Draft.SaveForQuitAsync());
            await IdleAsync();
            return saved;
        }

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(() => Draft.Dispose());
            Client.Dispose();
            await Fake.DisposeAsync();
            Ui.Dispose();
        }
    }
}
