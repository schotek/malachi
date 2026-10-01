// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/DraftStateTests.swift (every test):
// the draft lifecycle of a compose window (ui/internal/compose/draft.go)
// over a fake form and a fake daemon. After them, what Swift has no test
// for: the editor's flush echo inside the draft controller (draft.go
// editorChanged and save's record, whose rule TestFlushEcho checks in
// Html/FlushEchoTests; here over a real EditorChannel in both orders of the
// flush's answers), and the Windows addition of saving dirty drafts on Quit
// (docs/windows-port.md §0) with the echo's baseline it needs (EditorReady):
// untouched windows are left alone, unreported edits are saved, a hanging
// editor is given up on.
//
// Swift shortens the autosave to milliseconds and sleeps; here the
// controller's clock is a FakeTimeProvider that the test advances, and the
// test waits until everything is idle (IdleAsync) before it asserts. A save
// Swift slows down with a delay is held by a gate the test opens.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text.Json;
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
using Microsoft.Extensions.Logging;
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
    // other content is an edit. The window forwards the editor's Ready too
    // (EditorReady, a Windows addition for Quit's flush), whose flush the
    // page answers first.

    [Theory]
    [InlineData(true)] // WebView2's order: the changed before the script's result
    [InlineData(false)] // the result first
    public async Task TheFlushEchoLeavesTheSavedDraftClean(bool changedFirst)
    {
        await using var h = await Harness.StartAsync(editor: true);
        await h.Run(() =>
        {
            h.Form.Ready("", changedFirst);
            h.Form.Post("<p>a</p>");
        });
        Assert.True(h.Draft.Draft.Dirty, "new content is an edit");

        await h.Run(() => h.Draft.Save(SaveReason.Explicit));
        await h.Run(() => h.Form.Answer("<p>a</p>", changedFirst));
        await h.IdleAsync();
        Assert.Equal("<p>a</p>", Assert.Single(h.Script.Saves).Draft.HtmlBody);
        Assert.False(h.Draft.Draft.Dirty, "the flush's own changed marked the saved draft dirty");
        Assert.False(h.Draft.AutosaveArmed);
        Assert.StartsWith("Draft saved ", h.Form.Statuses[^1], StringComparison.Ordinal);

        // A late debounced changed with the same content stays an echo.
        await h.Run(() => h.Form.Post("<p>a</p>"));
        Assert.False(h.Draft.Draft.Dirty);
        // New content is an edit, and so is going back to the saved text.
        await h.Run(() => h.Form.Post("<p>ab</p>"));
        Assert.True(h.Draft.Draft.Dirty);
        Assert.True(h.Draft.AutosaveArmed);
        Assert.Single(h.Script.Saves);
    }

    /// <summary>
    /// EditorReady: what the page reports for the document it was given is
    /// the baseline of the echo, however it serialises the loaded body (here
    /// the backend's <c>&amp;#39;</c> comes back as an apostrophe), in either
    /// order of the flush's answers; content after it is an edit.
    /// </summary>
    [Theory]
    [InlineData(true)]
    [InlineData(false)]
    public async Task TheEditorsFirstReportIsTheBaselineNotAnEdit(bool changedFirst)
    {
        await using var h = await Harness.StartAsync(editor: true);
        await h.Run(() =>
        {
            h.Form.Load(QuotedLoaded);
            h.Form.Ready(QuotedSerialised, changedFirst);
        });
        Assert.Equal(0, h.Form.Unanswered);
        Assert.Equal(QuotedSerialised, h.Form.EditorHtml());
        Assert.False(h.Draft.Draft.Dirty, "the page's serialisation of the loaded body is no edit");
        Assert.False(h.Draft.AutosaveArmed);
        // A debounced report of the same content (typed and deleted) stays
        // the baseline; anything else is an edit.
        await h.Run(() => h.Form.Post(QuotedSerialised));
        Assert.False(h.Draft.Draft.Dirty);
        await h.Run(() => h.Form.Post("<p>Thanks</p>" + QuotedSerialised));
        Assert.True(h.Draft.Draft.Dirty);
        Assert.True(h.Draft.AutosaveArmed);
        await h.IdleAsync();
        Assert.Empty(h.Fake.Calls);
    }

    /// <summary>
    /// A report that arrives after the window closed (the editor's debounce
    /// outlives the window by up to 250 ms) marks nothing: the status line
    /// of a window on its way out is not changed, and no autosave is armed.
    /// </summary>
    [Fact]
    public async Task ALateReportAfterTheWindowClosedChangesNothing()
    {
        await using var h = await Harness.StartAsync(editor: true);
        await h.Run(() =>
        {
            h.Form.Load("");
            h.Form.Ready("", changedFirst: true);
            h.Draft.Cleanup();
            var statuses = h.Form.Statuses.Count;
            h.Form.Post("<p>late</p>");
            h.Draft.MarkDirty();
            h.Draft.EditorReady();
            Assert.False(h.Draft.Draft.Dirty);
            Assert.False(h.Draft.AutosaveArmed);
            Assert.Equal(statuses, h.Form.Statuses.Count);
            Assert.Equal(0, h.Form.Unanswered);
        });
        await h.AutosaveAsync();
        Assert.Empty(h.Fake.Calls);
    }

    // Saving on Quit (docs/windows-port.md §0, a Windows addition): without
    // a question, with the flush first, and false only when a save failed or
    // the editor did not answer.

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
    /// A window nobody typed in is left alone by Quit, over a real editor
    /// channel whose flush always reports: a new message, a reply whose quote
    /// the page serialises its own way, and a message opened from the Drafts
    /// folder, which a save would take over from the client that wrote it
    /// (<c>replaces</c>). Quit's flush reports the content unchanged, in
    /// either order of its answers, and no <c>draft.save</c> goes out.
    /// </summary>
    [Theory]
    [InlineData("new", true)]
    [InlineData("new", false)]
    [InlineData("reply", true)]
    [InlineData("reply", false)]
    [InlineData("drafts", true)]
    [InlineData("drafts", false)]
    public async Task SaveForQuitLeavesAnUntouchedWindowAlone(string window, bool changedFirst)
    {
        await using var h = await Harness.StartAsync(editor: true);
        var (loaded, page) = window switch
        {
            "reply" => (QuotedLoaded, QuotedSerialised),
            "drafts" => ("<p>quoted</p>", "<p>quoted</p>"),
            _ => ("", ""),
        };
        await h.Run(() =>
        {
            if (window == "reply")
            {
                h.Draft.SetOriginal("m1", null);
            }
            else if (window == "drafts")
            {
                h.Draft.SetOpened(null, 0, "m9", fromDrafts: true);
            }
            h.Form.Load(loaded);
            h.Form.Ready(page, changedFirst);
        });
        await h.IdleAsync();

        var quit = await h.StartQuitAsync();
        await h.IdleAsync();
        Assert.Equal(1, h.Form.Unanswered);
        await h.Run(() => h.Form.Answer(page, changedFirst));
        Assert.True(await h.SettledAsync(quit));
        await h.IdleAsync();
        Assert.Empty(h.Fake.Calls);
        Assert.False(h.Draft.Draft.Dirty);
        Assert.False(h.Draft.AutosaveArmed);
        Assert.True(h.Draft.CanCloseWithoutAsking);
        Assert.Empty(h.Form.Toasts);
    }

    /// <summary>
    /// Quit's flush on its own, without the baseline of EditorReady (a
    /// window that does not forward Ready): content the page reports
    /// unchanged since what the editor last knew is no edit, so a message
    /// opened from the Drafts folder is not taken over from its client on
    /// Quit. This is the review's reproduction.
    /// </summary>
    [Theory]
    [InlineData(true)]
    [InlineData(false)]
    public async Task SaveForQuitSeesUnchangedContentWithoutTheBaseline(bool changedFirst)
    {
        await using var h = await Harness.StartAsync(editor: true, forwardReady: false);
        await h.Run(() =>
        {
            h.Form.Load("<p>quoted</p>");
            h.Form.Ready();
            h.Draft.SetOpened(null, 0, "m9", fromDrafts: true);
        });
        Assert.Equal(0, h.Form.Unanswered);

        var quit = await h.StartQuitAsync();
        await h.IdleAsync();
        Assert.Equal(1, h.Form.Unanswered);
        await h.Run(() => h.Form.Answer("<p>quoted</p>", changedFirst));
        Assert.True(await h.SettledAsync(quit));
        Assert.Empty(h.Fake.Calls);
        Assert.False(h.Draft.Draft.Dirty);
        Assert.False(h.Draft.AutosaveArmed);
    }

    /// <summary>
    /// A saved window nobody typed in since: Quit's flush reports what was
    /// saved, which is no edit.
    /// </summary>
    [Theory]
    [InlineData(true)]
    [InlineData(false)]
    public async Task SaveForQuitLeavesASavedWindowAlone(bool changedFirst)
    {
        const string Reply = "<p>Thanks</p>" + QuotedSerialised;
        await using var h = await Harness.StartAsync(editor: true);
        await h.Run(() =>
        {
            h.Form.Load(QuotedLoaded);
            h.Form.Ready(QuotedSerialised, changedFirst);
            h.Form.Post(Reply);
            h.Draft.Save(SaveReason.Explicit);
            h.Form.Answer(Reply, changedFirst);
        });
        await h.IdleAsync();
        Assert.Single(h.Script.Saves);
        Assert.False(h.Draft.Draft.Dirty);

        var quit = await h.StartQuitAsync();
        await h.IdleAsync();
        Assert.Equal(1, h.Form.Unanswered);
        await h.Run(() => h.Form.Answer(Reply, changedFirst));
        Assert.True(await h.SettledAsync(quit));
        await h.IdleAsync();
        Assert.Single(h.Script.Saves);
        Assert.False(h.Draft.Draft.Dirty);
        Assert.False(h.Draft.AutosaveArmed);
    }

    /// <summary>
    /// Quit right after the editor became ready: the baseline's flush and
    /// Quit's are both outstanding when the page answers them, and neither
    /// takes the untouched quote for an edit. Before the editor is ready
    /// there is nothing unreported to fetch, and the flush answers at once.
    /// </summary>
    [Theory]
    [InlineData(true)]
    [InlineData(false)]
    public async Task SaveForQuitRightAfterTheEditorBecameReady(bool changedFirst)
    {
        await using var h = await Harness.StartAsync(editor: true);
        await h.Run(() => h.Form.Load(QuotedLoaded));
        Assert.True(await h.SaveForQuitAsync());
        Assert.Equal(0, h.Form.Unanswered);

        await h.Run(() => h.Form.Ready());
        var quit = await h.StartQuitAsync();
        await h.IdleAsync();
        Assert.Equal(2, h.Form.Unanswered);
        await h.Run(() =>
        {
            h.Form.Answer(QuotedSerialised, changedFirst);
            h.Form.Answer(QuotedSerialised, changedFirst);
        });
        Assert.True(await h.SettledAsync(quit));
        await h.IdleAsync();
        Assert.Empty(h.Fake.Calls);
        Assert.False(h.Draft.Draft.Dirty);
        Assert.False(h.Draft.AutosaveArmed);
    }

    /// <summary>
    /// An edit the editor has not reported yet (its changed is debounced) is
    /// fetched with a flush and saved, whichever of the flush's answers
    /// comes first: in a window saved before, and in one nobody had typed in
    /// until just now.
    /// </summary>
    [Theory]
    [InlineData(true, true)]
    [InlineData(false, true)]
    [InlineData(true, false)]
    [InlineData(false, false)]
    public async Task SaveForQuitSavesAnEditTheEditorHasNotReported(bool changedFirst, bool savedBefore)
    {
        await using var h = await Harness.StartAsync(editor: true);
        await h.Run(() =>
        {
            h.Form.Load("<p>a</p>");
            h.Form.Ready("<p>a</p>", changedFirst);
            if (savedBefore)
            {
                h.Form.Post("<p>a.</p>");
                h.Draft.Save(SaveReason.Explicit);
                h.Form.Answer("<p>a.</p>", changedFirst);
            }
        });
        await h.IdleAsync();
        Assert.Equal(savedBefore ? 1 : 0, h.Script.Saves.Count);
        Assert.False(h.Draft.Draft.Dirty);

        // "b" typed; the page has not posted it yet.
        var typed = savedBefore ? "<p>a.b</p>" : "<p>ab</p>";
        var quit = await h.StartQuitAsync();
        await h.IdleAsync();
        Assert.Equal(1, h.Form.Unanswered);
        await h.Run(() => h.Form.Answer(typed, changedFirst));
        await h.IdleAsync();
        Assert.True(h.Draft.Draft.Saving, "the unreported edit is being saved");
        Assert.Equal(1, h.Form.Unanswered);
        await h.Run(() => h.Form.Answer(typed, changedFirst));
        Assert.True(await h.SettledAsync(quit));
        await h.IdleAsync();
        Assert.Equal(savedBefore ? 2 : 1, h.Script.Saves.Count);
        Assert.Equal(typed, h.Script.Saves[^1].Draft.HtmlBody);
        Assert.False(h.Draft.Draft.Dirty);
        Assert.False(h.Draft.AutosaveArmed);
    }

    /// <summary>
    /// A keystroke after an autosave's flush, while its draft.save is on its
    /// way: Quit flushes all the same, the report is an edit, and it is saved
    /// after the save in flight.
    /// </summary>
    [Theory]
    [InlineData(true)]
    [InlineData(false)]
    public async Task SaveForQuitFetchesAnEditTypedWhileAnAutosaveIsInFlight(bool changedFirst)
    {
        await using var h = await Harness.StartAsync(editor: true);
        var gate = h.Script.HoldSaves();
        await h.Run(() =>
        {
            h.Form.Load("");
            h.Form.Ready("", changedFirst);
            h.Form.Post("<p>a</p>");
            h.Draft.Save(SaveReason.Autosave);
            h.Form.Answer("<p>a</p>", changedFirst);
        });
        await Eventually.Holds(() => h.Script.Saves.Count == 1);

        // "b" typed after that flush; the page has not posted it yet.
        var quit = await h.StartQuitAsync();
        await h.Run(() =>
        {
            Assert.True(h.Draft.Draft.Saving);
            Assert.False(h.Draft.Draft.Dirty);
            Assert.Equal(1, h.Form.Unanswered);
            h.Form.Answer("<p>ab</p>", changedFirst);
            Assert.True(h.Draft.Draft.Dirty, "the report after the save's flush is an edit");
        });
        gate.SetResult();
        await h.IdleAsync();
        Assert.Equal(1, h.Form.Unanswered);
        await h.Run(() => h.Form.Answer("<p>ab</p>", changedFirst));
        Assert.True(await h.SettledAsync(quit));
        await h.IdleAsync();
        Assert.Equal(["<p>a</p>", "<p>ab</p>"], h.Script.Saves.Select(s => s.Draft.HtmlBody));
        Assert.False(h.Draft.Draft.Dirty);
        Assert.False(h.Draft.Draft.Saving);
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
        var quit = await h.StartQuitAsync();
        gate.SetResult();
        Assert.True(await h.SettledAsync(quit));
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

    /// <summary>
    /// An editor that hangs never answers a flush: Quit gives up after
    /// <see cref="ComposeDraftController.QuitFlushTimeout"/> (on a save,
    /// that plus draft.save's own timeout) and says so, so that the app asks
    /// the window's question instead of waiting for ever.
    /// </summary>
    [Fact]
    public async Task SaveForQuitGivesUpOnAnEditorThatDoesNotAnswer()
    {
        await using var h = await Harness.StartAsync(editor: true);
        var questions = 0;
        h.Draft.SaveDraftQuestion = () =>
        {
            questions++;
            return Task.FromResult(DraftCloseAnswer.Discard);
        };
        await h.Run(() =>
        {
            h.Form.Load("<p>q</p>");
            h.Form.Ready("<p>q</p>", changedFirst: true);
        });

        // A clean window: Quit's own flush is not answered.
        var quit = await h.StartQuitAsync();
        await h.IdleAsync();
        h.Time.Advance(ComposeDraftController.QuitFlushTimeout - TimeSpan.FromMilliseconds(1));
        await h.IdleAsync();
        Assert.False(quit.IsCompleted);
        h.Time.Advance(TimeSpan.FromMilliseconds(1));
        Assert.False(await h.SettledAsync(quit));
        Assert.Contains((LogLevel.Warning, "saving for quit: editor flush did not finish in time"), h.Logger.Entries);

        // A dirty window: the save's flush is not answered either.
        await h.Run(() => h.Draft.MarkDirty());
        var dirty = await h.StartQuitAsync();
        await h.IdleAsync();
        Assert.True(h.Draft.Draft.Saving);
        h.Time.Advance(ComposeDraftController.QuitFlushTimeout + API.DraftSave.Timeout - TimeSpan.FromMilliseconds(1));
        await h.IdleAsync();
        Assert.False(dirty.IsCompleted);
        h.Time.Advance(TimeSpan.FromMilliseconds(1));
        Assert.False(await h.SettledAsync(dirty));
        Assert.Contains((LogLevel.Warning, "saving for quit: draft.save did not finish in time"), h.Logger.Entries);
        Assert.Empty(h.Fake.Calls);
        Assert.Empty(h.Form.Toasts);

        // The app asks now; Discard closes.
        Assert.True(await h.CloseRequestAsync());
        Assert.Equal(1, questions);
        Assert.True(h.Draft.Draft.Closed);
        Assert.Empty(h.Fake.Calls);
    }

    /// <summary>
    /// The discard question that cannot be shown (WinUI allows one
    /// <c>ContentDialog</c> at a time) discards nothing, and says why in the
    /// log.
    /// </summary>
    [Fact]
    public async Task AFailedDiscardQuestionDiscardsNothing()
    {
        await using var h = await Harness.StartAsync();
        h.Draft.ConfirmDiscard = (_, _, _) => throw new InvalidOperationException("Only a single ContentDialog can be open at any time.");
        await h.Run(() =>
        {
            h.Draft.MarkDirty();
            h.Draft.Discard();
        });
        await h.IdleAsync();
        Assert.Equal(0, h.Form.Closes);
        Assert.False(h.Draft.Draft.Discard);
        Assert.Empty(h.Fake.Calls);
        Assert.Equal([(LogLevel.Warning, "the discard confirmation failed; nothing was discarded")], h.Logger.Entries);
    }

    // The paste of Markdown (the editor's "paste"): draft.markdown's HTML
    // when the text reads as Markdown; null, the text as it is, when it does
    // not, when the daemon refuses it, when an older daemon does not know the
    // method, when there is no daemon, and for text too long to ask about,
    // which is never sent.
    [Fact]
    public async Task PasteMarkdownAnswersWithTheHtmlOrNull()
    {
        await using var h = await Harness.StartAsync();
        h.Fake.On(API.DraftMarkdown.Name, (Func<string, string>)(p => JsonCoding.Decode<DraftMarkdownParams>(p).Text switch
        {
            "# Plan" => JsonCoding.EncodeToString(new DraftMarkdownResult { Markdown = true, Html = "<h1>Plan</h1>" }),
            "**empty**" => JsonCoding.EncodeToString(new DraftMarkdownResult { Markdown = true, Html = "" }),
            "boom" => throw new RpcException(new RpcError { Code = ErrorCode.InvalidArgument, Message = "text" }),
            _ => JsonCoding.EncodeToString(new DraftMarkdownResult { Markdown = false }),
        }));
        Assert.Equal("<h1>Plan</h1>", await PasteAsync(h, "# Plan"));
        Assert.Null(await PasteAsync(h, "plain"));
        Assert.Null(await PasteAsync(h, "**empty**"));
        Assert.Null(await PasteAsync(h, "boom"));
        var calls = h.Fake.Calls.Count;
        Assert.Null(await PasteAsync(h, new string('a', API.Limits.MaxDraftBodyBytes + 1)));
        Assert.Equal(calls, h.Fake.Calls.Count);

        await using var older = await Harness.StartAsync();
        Assert.Null(await PasteAsync(older, "# Plan"));

        await using var none = await Harness.StartAsync(connect: false);
        Assert.Null(await PasteAsync(none, "# Plan"));
    }

    private static async Task<string?> PasteAsync(Harness h, string text)
    {
        var done = new TaskCompletionSource<string?>(TaskCreationOptions.RunContinuationsAsynchronously);
        await h.Run(() => h.Draft.PasteMarkdown(text, html => done.TrySetResult(html)));
        var html = await done.Task;
        await h.IdleAsync();
        return html;
    }

    // Helpers

    private static DraftAttachment Att(string id, bool inline = false, string? cid = null) =>
        new() { Id = id, Filename = id + ".bin", ContentType = "application/octet-stream", Size = 10, Inline = inline, ContentId = cid };

    // A reply's quote as the backend renders it, and as the page's
    // innerHTML serialises the same document.
    private const string QuotedLoaded = "<p>Alice wrote:</p><blockquote type=\"cite\"><p>don&#39;t</p></blockquote>";
    private const string QuotedSerialised = "<p>Alice wrote:</p><blockquote type=\"cite\"><p>don't</p></blockquote>";

    private static string Changed(long seq, string html)
    {
        var n = seq.ToString(CultureInfo.InvariantCulture);
        return "{\"type\":\"changed\",\"seq\":" + n + ",\"html\":\"" + JsonEncodedText.Encode(html).Value + "\",\"text\":\"t" + n + "\"}";
    }

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
    /// content and flushes are an EditorChannel's, whose Ready and Changed
    /// reach the controller as the window forwards them, and the test plays
    /// the page: its ready, its debounced changed, its answers to the
    /// flushes in the order they were asked. Without one a flush answers at
    /// once, as Swift's fake does.
    /// </summary>
    private sealed class FakeForm : IComposeForm
    {
        // The flushes the page has not answered yet, oldest first.
        private readonly List<long> unanswered = [];

        // The page's seq of its last changed in the current document.
        private long seq;

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

        /// <summary>The flushes the page has not answered yet.</summary>
        public int Unanswered => unanswered.Count;

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
                unanswered.Add(id);
            }
        }

        /// <summary>
        /// editor.Load: a new document with <paramref name="html"/>; the
        /// page's seq starts over and the old document's flushes are gone.
        /// </summary>
        public void Load(string html)
        {
            Editor!.Load(html);
            seq = 0;
            unanswered.Clear();
        }

        /// <summary>The page's ready; the controller's EditorReady asks for a flush.</summary>
        public void Ready() => Editor!.Receive("{\"type\":\"ready\"}");

        /// <summary>The page's ready, and its answer to EditorReady's flush with <paramref name="html"/>.</summary>
        public void Ready(string html, bool changedFirst)
        {
            Ready();
            Answer(html, changedFirst);
        }

        /// <summary>The page's debounced changed with <paramref name="html"/>.</summary>
        public void Post(string html) => Editor!.Receive(Changed(++seq, html));

        /// <summary>
        /// The page answers the oldest flush with <paramref name="html"/>:
        /// its changed and the script's result (the changed's seq), in the
        /// order given.
        /// </summary>
        public void Answer(string html, bool changedFirst)
        {
            var id = unanswered[0];
            unanswered.RemoveAt(0);
            var n = ++seq;
            var changed = Changed(n, html);
            var result = n.ToString(CultureInfo.InvariantCulture);
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

        public RecordingLogger<ComposeDraftController> Logger { get; } = new();

        public static async Task<Harness> StartAsync(bool connect = true, bool placeholder = false, bool editor = false, bool forwardReady = true)
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
                var draft = new ComposeDraftController(h.Client, h.Settings, () => placeholder, h.Registry, h.Time, h.Pending, h.Logger) { Form = h.Form };
                if (h.Form.Editor is { } channel)
                {
                    // The window forwards the editor's Changed (compose.go)
                    // and Ready (the Windows baseline of the echo).
                    channel.Changed += (_, _) => draft.EditorChanged();
                    if (forwardReady)
                    {
                        channel.Ready += (_, _) => draft.EditorReady();
                    }
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

        public async Task<bool> SaveForQuitAsync() => await SettledAsync(await StartQuitAsync());

        /// <summary>
        /// Starts SaveForQuitAsync on the UI thread and hands back the
        /// controller's own task, which completes on the UI thread: once the
        /// test is idle, whether it completed is exact.
        /// </summary>
        public Task<Task<bool>> StartQuitAsync() => Ui.RunAsync(() => Draft.SaveForQuitAsync());

        /// <summary>
        /// The answer of a SaveForQuitAsync once everything is idle. A Quit
        /// still waiting then waits for a flush the test does not answer
        /// (a save the window should not have started): a failure, not a
        /// hang.
        /// </summary>
        public async Task<bool> SettledAsync(Task<bool> quit)
        {
            await IdleAsync();
            Assert.True(quit.IsCompleted, "SaveForQuitAsync is still waiting, for a flush nobody answers");
            return await quit;
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
