// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ComposeDraftBoardOwnerTests.swift
// (every test); GTK: ui/internal/compose/draft_test.go and pane_test.go (the
// OwnerBoard cases). ComposeDraftController with DraftOwner.Board (a board
// case's suggested reply edited inline): the board keeps the draft, so
// nothing here deletes it on cleanup or after a late save, closing never
// asks, FinishAsync saves what was typed and says whether anything was lost,
// a draft deleted elsewhere is reported (OnLost) and never recreated, a
// conflict keeps our text in the same draft (draft.get for the version), and
// Discard goes through DiscardStored. The window owner's behaviour is covered
// by DraftStateTests; a few contrasts are repeated here. After Swift's tests,
// two the Windows window needs: over a real EditorChannel, whose Changed the
// window forwards to EditorChanged (Swift's pane drops a flush's changed),
// SettleAsync's flush is no edit of its own, in either order of its answers.
//
// Swift shortens the autosave and sleeps; here the controller's clock is a
// FakeTimeProvider that is never advanced unless a test says so, and the
// test waits until everything is idle. A save or a send Swift slows down
// with a delay is held by a gate the test opens.

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
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

public sealed class ComposeDraftBoardOwnerTests
{
    private const string BoardDraft = "d_board";

    private static readonly RpcError StorageError = new() { Code = ErrorCode.StorageError, Message = "disk" };
    private static readonly RpcError Gone = new() { Code = ErrorCode.DraftNotFound, Message = "gone" };
    private static readonly RpcError Conflict = new() { Code = ErrorCode.Conflict, Message = "version" };

    [Fact]
    public async Task TheOwnerDefaultsToTheWindow()
    {
        await using var h = await Harness.StartAsync(owner: DraftOwner.Window);
        Assert.Equal(DraftOwner.Window, h.Draft.Owner);
        await using var b = await Harness.StartAsync();
        Assert.Equal(DraftOwner.Board, b.Draft.Owner);
        // Without the parameter, the window's.
        var plain = await h.Ui.RunAsync(() => new ComposeDraftController(h.Client, h.Settings, () => false, new CidRegistry(), h.Time, h.Pending));
        Assert.Equal(DraftOwner.Window, plain.Owner);
        await h.Run(plain.Dispose);
    }

    [Theory]
    [InlineData(false)]
    [InlineData(true)]
    public async Task ClosingNeverAsksAndNeverDeletes(bool comment)
    {
        await using var h = await Harness.StartAsync(comment: comment);
        await h.Run(() => h.Draft.MarkDirty());
        Assert.True(h.Draft.CanCloseWithoutAsking, "dirty, yet the board keeps it");
        Assert.True(await h.Ui.InvokeAsync(() => h.Draft.CloseRequestAsync()));
        Assert.Equal(0, h.Questions);
        Assert.True(h.Draft.Draft.Closed);
        await h.IdleAsync();
        Assert.Empty(h.Script.Deletes);
    }

    [Fact]
    public async Task TheWindowStillDeletesAClosedComment()
    {
        // The contrast: a comment window's copy goes with the window.
        await using var h = await Harness.StartAsync(owner: DraftOwner.Window, comment: true);
        await h.Run(() => h.Draft.Cleanup());
        await h.IdleAsync();
        Assert.Equal(BoardDraft, Assert.Single(h.Script.Deletes).DraftId.Value);
    }

    [Theory]
    [InlineData(false)]
    [InlineData(true)]
    public async Task ALateSaveNeverDeletes(bool comment)
    {
        await using var h = await Harness.StartAsync(comment: comment);
        var gate = h.Script.HoldSaves();
        await h.Run(() =>
        {
            h.Draft.MarkDirty();
            h.Draft.Save(SaveReason.Autosave);
        });
        await Eventually.Holds(() => h.Script.Saves.Count == 1);
        await h.Run(() => h.Draft.Cleanup());
        // The save answers after the cleanup.
        gate.SetResult();
        await h.IdleAsync();
        Assert.Empty(h.Script.Deletes);
    }

    [Fact]
    public async Task FinishSavesWhatWasTyped()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() =>
        {
            h.Form.Type("Thanks, Ann. Tuesday works.");
            h.Draft.MarkDirty();
        });
        Assert.True(await h.FinishAsync());
        var save = Assert.Single(h.Script.Saves).Draft;
        Assert.Equal((BoardDraft, 3), (save.Id?.Value, save.Version));
        Assert.Equal("Thanks, Ann. Tuesday works.", save.TextBody);
        Assert.True(h.Draft.Draft.Closed);
        Assert.False(h.Draft.AutosaveArmed);
        Assert.True(h.Form.Flushes >= 1);
        Assert.Empty(h.Script.Deletes);
        // Finished: a second call changes nothing.
        Assert.True(await h.FinishAsync());
        Assert.Single(h.Script.Saves);
    }

    [Fact]
    public async Task FinishWithNothingTypedSavesNothing()
    {
        await using var h = await Harness.StartAsync();
        Assert.True(await h.FinishAsync());
        Assert.Empty(h.Script.Saves);
        Assert.True(h.Draft.Draft.Closed);
    }

    [Fact]
    public async Task FinishWaitsForASaveUnderWayAndSavesLaterEdits()
    {
        await using var h = await Harness.StartAsync();
        var gate = h.Script.HoldSaves();
        await h.Run(() =>
        {
            h.Draft.MarkDirty();
            h.Draft.Save(SaveReason.Autosave);
        });
        await Eventually.Holds(() => h.Script.Saves.Count == 1);
        // Typed while the save runs.
        await h.Run(() =>
        {
            h.Form.Type("Second thought");
            h.Draft.MarkDirty();
        });
        var finish = await h.Ui.RunAsync(() => h.Draft.FinishAsync());
        // The daemon holds its answer: only the UI thread can be idle.
        await h.Ui.DrainAsync();
        Assert.False(finish.IsCompleted, "waits for the save under way");
        gate.SetResult();
        Assert.True(await h.SettledAsync(finish));
        Assert.Equal(2, h.Script.Saves.Count);
        Assert.Equal("Second thought", h.Script.Saves[^1].Draft.TextBody);
        Assert.True(h.Script.Saves[^1].Draft.Version == 4, "the version the first save returned");
    }

    [Fact]
    public async Task FinishSeesAnEditReportedOnlyByTheFlush()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() => h.Draft.MarkDirty());
        Assert.Null(await h.SaveNowAsync(SaveReason.Autosave));
        // The editor reports this content with the flush, too late to mark
        // the draft dirty.
        h.Form.Type("Changed just before leaving");
        Assert.False(h.Draft.Draft.Dirty);
        Assert.True(await h.FinishAsync());
        Assert.Equal(2, h.Script.Saves.Count);
        Assert.Equal("Changed just before leaving", h.Script.Saves[^1].Draft.TextBody);
    }

    [Fact]
    public async Task FinishSeesAnEditReportedOnlyByTheFlushBeforeAnySave()
    {
        // The user typed and left within the debounce: nothing was reported,
        // nothing saved yet in this pane's life.
        await using var h = await Harness.StartAsync();
        h.Form.Rendering = "<p>Thanks, Ann.</p>\n";
        await h.Run(() => h.Draft.EditorReady());
        h.Form.Unreported = "Thanks, Tuesday works";
        Assert.False(h.Draft.Draft.Dirty);
        Assert.True(await h.FinishAsync());
        var save = Assert.Single(h.Script.Saves).Draft;
        Assert.Equal("Thanks, Tuesday works", save.TextBody);
        Assert.Equal(BoardDraft, save.Id?.Value);
    }

    [Fact]
    public async Task SettleSavesAndLeavesTheControllerOpen()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() => h.Draft.EditorReady());
        h.Form.Unreported = "Kept open";
        Assert.True(await h.SettleAsync());
        Assert.Single(h.Script.Saves);
        Assert.False(h.Draft.Draft.Closed);
        Assert.False(h.Draft.Draft.Dirty);
        // Still usable: a later edit is saved by the next settle.
        await h.Run(() =>
        {
            h.Form.Type("And more");
            h.Draft.MarkDirty();
        });
        Assert.True(await h.SettleAsync());
        Assert.Equal("And more", h.Script.Saves[^1].Draft.TextBody);
        Assert.False(h.Draft.Draft.Closed);
    }

    // The daemon takes any draft.save of a linked suggestion as the user's
    // edit: nothing is saved unless the user changed something.
    [Fact]
    public async Task AnUntouchedPaneSavesNothing()
    {
        await using var h = await Harness.StartAsync();
        // The editor writes the draft differently from how it was given.
        h.Form.Rendering = "<p>Thanks, Ann.</p>\n";
        await h.Run(() => h.Draft.EditorReady());
        Assert.True(await h.SettleAsync());
        Assert.True(await h.SettleAsync());
        Assert.True(await h.FinishAsync());
        Assert.Empty(h.Script.Saves);
        Assert.True(h.Form.Flushes >= 3);
    }

    [Theory]
    [InlineData(false)]
    [InlineData(true)]
    public async Task TheEditorsNormalisationBeforeItIsKnownIsNoEdit(bool ready)
    {
        // Left before the page was up, or the host never said ready: the
        // flush reports the editor's own writing, which is not compared.
        await using var h = await Harness.StartAsync();
        h.Form.IsReady = ready;
        h.Form.Rendering = "<p>Thanks, Ann.</p>\n";
        Assert.True(await h.FinishAsync());
        Assert.Empty(h.Script.Saves);
    }

    [Fact]
    public async Task AnEditSavesExactlyOnce()
    {
        await using var h = await Harness.StartAsync();
        h.Form.Rendering = "<p>Thanks, Ann.</p>\n";
        await h.Run(() =>
        {
            h.Draft.EditorReady();
            // Reported by the bridge as the user typed.
            h.Form.Type("Tuesday works");
            h.Draft.MarkDirty();
        });
        // And a little more, reported only by the flush.
        h.Form.Unreported = "Tuesday works for me";
        Assert.True(await h.SettleAsync());
        Assert.True(await h.FinishAsync());
        Assert.Equal("Tuesday works for me", Assert.Single(h.Script.Saves).Draft.TextBody);
    }

    [Fact]
    public async Task SettleWaitsForASendUnderWay()
    {
        await using var h = await Harness.StartAsync();
        var gate = h.Script.HoldSends();
        await h.Run(() => h.Draft.Send());
        await Eventually.Holds(() => h.Script.Sends.Count == 1);
        Assert.True(h.Draft.Draft.Sending);
        var settle = await h.Ui.RunAsync(() => h.Draft.SettleAsync());
        // The daemon holds its answer: only the UI thread can be idle.
        await h.Ui.DrainAsync();
        Assert.False(settle.IsCompleted, "waits for the send to answer");
        gate.SetResult();
        Assert.True(await h.SettledAsync(settle));
        // The outcome arrived before settle went on.
        Assert.Equal(["Message queued for sending"], h.Sent);
        Assert.Equal(1, h.Form.Closes);
        Assert.Single(h.Script.Saves);
    }

    [Fact]
    public async Task FinishWhileSendingDeliversAFailure()
    {
        await using var h = await Harness.StartAsync();
        var gate = h.Script.HoldSends();
        h.Script.SendError = StorageError;
        await h.Run(() => h.Draft.Send());
        await Eventually.Holds(() => h.Script.Sends.Count == 1);
        // The pane is retired while the send is on its way.
        var finish = await h.Ui.RunAsync(() => h.Draft.FinishAsync());
        gate.SetResult();
        Assert.True(await h.SettledAsync(finish));
        Assert.Equal(1, h.SendFailures);
        Assert.Empty(h.Sent);
        Assert.Contains(h.Form.Toasts, t => t.StartsWith("Sending", StringComparison.Ordinal));
        Assert.True(h.Form.SendEnabled[^1]);
    }

    [Fact]
    public async Task AFailedSendIsReported()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SendError = StorageError;
        await h.Run(() => h.Draft.Send());
        await h.IdleAsync();
        Assert.Equal(1, h.SendFailures);
        Assert.False(h.Draft.Draft.Sending);
        Assert.False(h.Draft.Draft.Closed);
        // A failed save before the send counts too.
        h.Script.SendError = null;
        h.Script.SaveErrors.Enqueue(StorageError);
        await h.Run(() =>
        {
            h.Draft.MarkDirty();
            h.Draft.Send();
        });
        await h.IdleAsync();
        Assert.Equal(2, h.SendFailures);
        // Lost is not a failed send.
        h.Script.SendError = Gone;
        await h.Run(() => h.Draft.Send());
        await h.IdleAsync();
        Assert.Equal(1, h.LostCount);
        Assert.Equal(2, h.SendFailures);
    }

    [Fact]
    public async Task AFailedFinishKeepsTheTextAndTheController()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SaveErrors.Enqueue(StorageError);
        await h.Run(() => h.Draft.MarkDirty());
        Assert.False(await h.FinishAsync());
        Assert.False(h.Draft.Draft.Closed);
        Assert.True(h.Draft.Draft.Dirty);
        Assert.True(h.Draft.AutosaveArmed);
        Assert.Single(h.Form.Toasts);
        Assert.Empty(h.Script.Deletes);
        // Called again (the host kept the form), it saves.
        Assert.True(await h.FinishAsync());
        Assert.True(h.Draft.Draft.Closed);
        Assert.Equal(2, h.Script.Saves.Count);
        Assert.All(h.Script.Saves, s => Assert.Equal(BoardDraft, s.Draft.Id?.Value));
    }

    [Fact]
    public async Task ADraftDeletedElsewhereIsLostNotRecreated()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SaveErrors.Enqueue(Gone);
        await h.Run(() => h.Draft.MarkDirty());
        Assert.NotNull(await h.SaveNowAsync(SaveReason.Autosave));
        Assert.Equal(1, h.LostCount);
        Assert.True(h.Draft.Lost);
        Assert.True(h.Draft.Draft.Closed);
        Assert.False(h.Draft.AutosaveArmed);
        Assert.True(h.Form.Toasts.Count == 0, "the host says it, once");
        // Nothing more is saved: no new draft without a case.
        await h.Run(() =>
        {
            h.Draft.MarkDirty();
            h.Draft.Save(SaveReason.Explicit);
        });
        await h.AutosaveAsync();
        Assert.Single(h.Script.Saves);
        Assert.All(h.Script.Saves, s => Assert.Equal(BoardDraft, s.Draft.Id?.Value));
        Assert.False(await h.FinishAsync());
        Assert.Equal(1, h.LostCount);
    }

    [Fact]
    public async Task TheWindowStillStartsANewDraftWhenItsDraftWent()
    {
        await using var h = await Harness.StartAsync(owner: DraftOwner.Window);
        h.Script.SaveErrors.Enqueue(Gone);
        await h.Run(() => h.Draft.MarkDirty());
        _ = await h.SaveNowAsync(SaveReason.Autosave);
        Assert.Null(h.Draft.Draft.DraftId);
        Assert.Equal(0, h.LostCount);
        Assert.Equal(["This draft was removed elsewhere; your text will be saved as a new draft"], h.Form.Toasts);
    }

    [Fact]
    public async Task AConflictKeepsOurTextInTheSameDraft()
    {
        await using var h = await Harness.StartAsync();
        // Saved elsewhere meanwhile: the stored draft is at version 7.
        h.Script.Stored = 7;
        h.Script.SaveErrors.Enqueue(Conflict);
        await h.Run(() =>
        {
            h.Form.Type("Ours");
            h.Draft.MarkDirty();
        });
        Assert.NotNull(await h.SaveNowAsync(SaveReason.Autosave));
        await h.IdleAsync();
        var get = Assert.Single(h.Script.Gets);
        Assert.Equal(("a", BoardDraft), (get.AccountId.Value, get.DraftId.Value));
        Assert.Equal(7, h.Draft.Draft.Version);
        Assert.Equal(BoardDraft, h.Draft.Draft.DraftId?.Value);
        Assert.True(h.Draft.Draft.Dirty);
        Assert.Empty(h.Form.Toasts);
        // The next save goes over it with our text.
        Assert.True(await h.FinishAsync());
        Assert.Equal(2, h.Script.Saves.Count);
        var last = h.Script.Saves[^1].Draft;
        Assert.Equal((BoardDraft, 7, "Ours"), (last.Id?.Value, last.Version, last.TextBody));
    }

    [Fact]
    public async Task FinishRidesOutAConflict()
    {
        await using var h = await Harness.StartAsync();
        h.Script.Stored = 9;
        h.Script.SaveErrors.Enqueue(Conflict);
        await h.Run(() => h.Draft.MarkDirty());
        Assert.True(await h.FinishAsync());
        Assert.Equal(2, h.Script.Saves.Count);
        Assert.Equal(9, h.Script.Saves[^1].Draft.Version);
        Assert.Single(h.Script.Gets);
    }

    [Fact]
    public async Task AConflictOnADraftThatWentIsLost()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SaveErrors.Enqueue(Conflict);
        h.Script.GetError = Gone;
        await h.Run(() => h.Draft.MarkDirty());
        _ = await h.SaveNowAsync(SaveReason.Autosave);
        await h.IdleAsync();
        Assert.Equal(1, h.LostCount);
        Assert.True(h.Draft.Draft.Closed);
        Assert.Single(h.Script.Saves);
    }

    [Fact]
    public async Task DiscardGoesThroughTheHost()
    {
        await using var h = await Harness.StartAsync();
        var stored = new List<(AccountId, DraftId)>();
        h.Draft.DiscardStored = (account, id) =>
        {
            stored.Add((account, id));
            return Task.CompletedTask;
        };
        await h.Run(() =>
        {
            h.Draft.MarkDirty();
            h.Draft.Discard();
        });
        await h.IdleAsync();
        Assert.Equal(1, h.Form.Closes);
        var (account, draft) = Assert.Single(stored);
        Assert.Equal(("a", BoardDraft), (account.Value, draft.Value));
        Assert.Empty(h.Script.Deletes);
        Assert.True(h.Draft.Draft.Discard);
    }

    [Fact]
    public async Task DiscardWithoutAHostDeletesTheDraft()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() => h.Draft.Discard());
        await h.IdleAsync();
        Assert.Equal(1, h.Form.Closes);
        var delete = Assert.Single(h.Script.Deletes);
        Assert.Equal(("a", BoardDraft), (delete.AccountId.Value, delete.DraftId.Value));
    }

    [Fact]
    public async Task AFailedDiscardKeepsTheForm()
    {
        await using var h = await Harness.StartAsync();
        h.Draft.DiscardStored = (_, _) => throw new RpcException(StorageError);
        await h.Run(() => h.Draft.Discard());
        await h.IdleAsync();
        Assert.Equal(0, h.Form.Closes);
        Assert.False(h.Draft.Draft.Closed);
        Assert.StartsWith("Discarding the draft", Assert.Single(h.Form.Toasts), StringComparison.Ordinal);
        // A draft deleted already is what Discard wanted.
        h.Draft.DiscardStored = (_, _) => Task.FromException(new RpcException(Gone));
        await h.Run(() => h.Draft.Discard());
        await h.IdleAsync();
        Assert.Equal(1, h.Form.Closes);
        Assert.Single(h.Form.Toasts);
    }

    [Fact]
    public async Task AbandonForgetsWithoutSavingOrDeleting()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() =>
        {
            h.Form.Type("Not kept");
            h.Draft.MarkDirty();
            h.Draft.Abandon();
            h.Draft.Abandon();
        });
        Assert.True(h.Draft.Draft.Closed);
        Assert.False(h.Draft.AutosaveArmed);
        await h.AutosaveAsync();
        Assert.Empty(h.Script.Saves);
        Assert.Empty(h.Script.Deletes);
        Assert.Equal(0, h.LostCount);
    }

    [Fact]
    public async Task SendingQueuesAndEnds()
    {
        await using var h = await Harness.StartAsync();
        await h.Run(() => h.Draft.Send());
        await h.IdleAsync();
        Assert.Equal(1, h.Form.Closes);
        var send = Assert.Single(h.Script.Sends);
        Assert.Equal(("a", BoardDraft, 4), (send.AccountId.Value, send.DraftId.Value, send.Version));
        Assert.Equal(["Message queued for sending"], h.Sent);
        Assert.True(await h.FinishAsync(), "sent: nothing to save");
        Assert.Empty(h.Script.Deletes);
    }

    [Fact]
    public async Task SendingADraftThatWentIsLost()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SendError = Gone;
        await h.Run(() => h.Draft.Send());
        await h.IdleAsync();
        Assert.Equal(1, h.LostCount);
        Assert.Equal(0, h.Form.Closes);
        Assert.Empty(h.Sent);
        Assert.Single(h.Script.Saves);
    }

    [Fact]
    public async Task ASendConflictKeepsTheDraft()
    {
        await using var h = await Harness.StartAsync();
        h.Script.SendError = Conflict;
        await h.Run(() => h.Draft.Send());
        await h.IdleAsync();
        Assert.Single(h.Script.Gets);
        Assert.Equal(BoardDraft, h.Draft.Draft.DraftId?.Value);
        Assert.True(h.Form.SendEnabled[^1]);
        Assert.Equal(0, h.LostCount);
    }

    [Fact]
    public async Task RegisterFeedsAnInlineFormTheAccounts()
    {
        using var ui = new TestUIContext();
        var pending = new PendingWork();
        await using var fake = new FakeDaemon();
        IReadOnlyList<Account> accounts = [MailModelTests.TestAccount("a", email: "me@example.org")];
        fake.On(API.AccountList.Name, _ => JsonCoding.EncodeToString(new AccountListResult { Accounts = accounts }));
        await fake.StartAsync();
        using var client = new RpcClient(fake.Path, PortableKeyFilePolicy.Instance);
        await client.ConnectAsync(TestContext.Current.CancellationToken);
        var settings = new SettingsStore(new InMemorySettingsBackend(), null);
        var c = await ui.RunAsync(() => new ComposeController(client, settings, pending));
        var pane = new Handle();
        await ui.RunAsync(() =>
        {
            c.Register(pane);
            c.Register(pane);
            Assert.Single(c.OpenWindows);
        });
        await Quiescence.IdleAsync(ui, pending, fake);
        Assert.Equal(["a"], Assert.Single(pane.Lists).Select(a => a.Id.Value));
        // Registered once the list is known: it gets it at once.
        var second = new Handle();
        await ui.RunAsync(() =>
        {
            c.Register(second);
            Assert.Single(second.Lists);
            c.Remove(pane);
            c.Remove(second);
            Assert.Empty(c.OpenWindows);
            c.Dispose();
        });
        Assert.Equal(1, fake.Calls.Count(m => m == API.AccountList.Name));
    }

    // Windows: the window forwards the editor's Changed to EditorChanged
    // (Swift's pane drops a flush's changed). SettleAsync's flush records its
    // report as the echo: content unchanged since EditorReady's baseline is
    // no edit, and no draft.save goes out, in either order of its answers.
    [Theory]
    [InlineData(true)]
    [InlineData(false)]
    public async Task AnUntouchedPaneOverTheEditorSavesNothing(bool changedFirst)
    {
        const string Loaded = "<p>Thanks, Ann.</p>";
        const string Rendered = "<p>Thanks, Ann.</p>\n";
        await using var h = await Harness.StartAsync(editor: true);
        await h.Run(() =>
        {
            h.Form.Load(Loaded);
            h.Form.Ready(Rendered, changedFirst);
        });
        Assert.False(h.Draft.Draft.Dirty);

        var settle = await h.Ui.RunAsync(() => h.Draft.SettleAsync());
        await h.IdleAsync();
        Assert.Equal(1, h.Form.Unanswered);
        await h.Run(() => h.Form.Answer(Rendered, changedFirst));
        Assert.True(await h.SettledAsync(settle));
        Assert.Empty(h.Script.Saves);
        Assert.False(h.Draft.Draft.Dirty);
        Assert.False(h.Draft.AutosaveArmed);
    }

    // An edit the editor reports only with SettleAsync's flush is saved
    // once, and its Changed arms nothing afterwards.
    [Theory]
    [InlineData(true)]
    [InlineData(false)]
    public async Task AnEditReportedByTheSettleFlushIsSavedOnce(bool changedFirst)
    {
        const string Rendered = "<p>Thanks, Ann.</p>";
        const string Typed = "<p>Thanks, Ann. Tuesday works.</p>";
        await using var h = await Harness.StartAsync(editor: true);
        await h.Run(() =>
        {
            h.Form.Load(Rendered);
            h.Form.Ready(Rendered, changedFirst);
        });

        var settle = await h.Ui.RunAsync(() => h.Draft.SettleAsync());
        await h.IdleAsync();
        await h.Run(() => h.Form.Answer(Typed, changedFirst));
        await h.IdleAsync();
        Assert.True(h.Draft.Draft.Saving, "the edit is being saved");
        // The save's own flush.
        await h.Run(() => h.Form.Answer(Typed, changedFirst));
        Assert.True(await h.SettledAsync(settle));
        Assert.Equal(Typed, Assert.Single(h.Script.Saves).Draft.HtmlBody);
        Assert.False(h.Draft.Draft.Dirty);
        Assert.False(h.Draft.AutosaveArmed);
        // A late debounced report of the same content is no edit either.
        await h.Run(() => h.Form.Post(Typed));
        Assert.False(h.Draft.Draft.Dirty);
    }

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
        private readonly List<DraftGetParams> gets = [];
        private TaskCompletionSource? heldSaves;
        private TaskCompletionSource? heldSends;

        /// <summary>Errors for the next saves, in order; a save past them succeeds.</summary>
        public Queue<RpcError> SaveErrors { get; } = new();

        public RpcError? SendError { get; set; }

        public RpcError? GetError { get; set; }

        /// <summary>The version the stored draft has (draft.get answers it; a save succeeds with one more).</summary>
        public int Stored { get; set; } = 3;

        public IReadOnlyList<DraftSaveParams> Saves => Locked(saves);

        public IReadOnlyList<MessageSendParams> Sends => Locked(sends);

        public IReadOnlyList<DraftDeleteParams> Deletes => Locked(deletes);

        public IReadOnlyList<DraftGetParams> Gets => Locked(gets);

        /// <summary>Holds every draft.save answer until the returned gate opens (Swift's saveDelay).</summary>
        public TaskCompletionSource HoldSaves()
        {
            var g = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            lock (gate)
            {
                heldSaves = g;
            }
            return g;
        }

        /// <summary>Holds every message.send answer until the returned gate opens (Swift's sendDelay).</summary>
        public TaskCompletionSource HoldSends()
        {
            var g = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            lock (gate)
            {
                heldSends = g;
            }
            return g;
        }

        public async Task<string> Save(string p)
        {
            var parameters = JsonCoding.Decode<DraftSaveParams>(p);
            Task wait;
            lock (gate)
            {
                saves.Add(parameters);
                wait = heldSaves?.Task ?? Task.CompletedTask;
            }
            await wait;
            int version;
            lock (gate)
            {
                if (SaveErrors.Count > 0)
                {
                    throw new RpcException(SaveErrors.Dequeue());
                }
                version = ++Stored;
            }
            return JsonCoding.EncodeToString(new DraftSaveResult
            {
                DraftId = parameters.Draft.Id ?? new DraftId("d_new" + Saves.Count.ToString(CultureInfo.InvariantCulture)),
                Version = version,
                TextBody = parameters.Draft.TextBody,
            });
        }

        public string Get(string p)
        {
            var parameters = JsonCoding.Decode<DraftGetParams>(p);
            lock (gate)
            {
                gets.Add(parameters);
                if (GetError is { } e)
                {
                    throw new RpcException(e);
                }
                return JsonCoding.EncodeToString(new DraftGetResult
                {
                    Draft = new Malachi.Core.Api.Draft { Id = parameters.DraftId, AccountId = parameters.AccountId, Version = Stored, Local = true },
                });
            }
        }

        public async Task<string> Send(string p)
        {
            Task wait;
            lock (gate)
            {
                sends.Add(JsonCoding.Decode<MessageSendParams>(p));
                wait = heldSends?.Task ?? Task.CompletedTask;
            }
            await wait;
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

        private IReadOnlyList<T> Locked<T>(List<T> list)
        {
            lock (gate)
            {
                return [.. list];
            }
        }
    }

    /// <summary>
    /// The inline editor as the controller sees it: a reply with one
    /// recipient, or a comment on an issue. Without an editor (Swift's fake)
    /// a flush answers at once, applying <see cref="Rendering"/> and
    /// <see cref="Unreported"/>; with one, its content and flushes are an
    /// EditorChannel's whose Ready and Changed reach the controller as the
    /// window forwards them, and the test plays the page.
    /// </summary>
    private sealed class FakeForm : IComposeForm
    {
        private readonly List<long> unanswered = [];
        private long seq;
        private string html = "<p>Thanks, Ann.</p>";
        private string text = "Thanks, Ann.";

        public FakeForm(bool editor)
        {
            Editor = editor ? new EditorChannel() : null;
        }

        public EditorChannel? Editor { get; }

        public Account Account { get; } = MailModelTests.TestAccount("a", email: "me@example.invalid");

        public bool IsComment { get; set; }

        public CommentVisibility CommentVisibility { get; set; } = CommentVisibility.Public;

        public string Subject => "Re: Offer";

        public IReadOnlyList<DraftAttachment> Attachments { get; private set; } = [];

        public List<string> Statuses { get; } = [];

        public List<string> Toasts { get; } = [];

        public List<bool> SendEnabled { get; } = [];

        public int Closes { get; private set; }

        public int Flushes { get; private set; }

        /// <summary>The flushes the page has not answered yet.</summary>
        public int Unanswered => unanswered.Count;

        /// <summary>Typed, but not reported yet (the bridge's debounce): the next flush reports it.</summary>
        public string? Unreported { get; set; }

        /// <summary>The page is up (ready); before, a flush reports nothing.</summary>
        public bool IsReady { get; set; } = true;

        /// <summary>
        /// How the editor writes the HTML it was given (it normalises it):
        /// the first flush once ready reports this. Null: as given.
        /// </summary>
        public string? Rendering { get; set; }

        public (IReadOnlyList<Address> To, IReadOnlyList<Address> Cc, IReadOnlyList<Address> Bcc, bool Ok) Recipients() =>
            ([new Address { Name = "Ann", Email = "ann@example.org" }], [], [], true);

        public string EditorHtml() => Editor?.Html ?? html;

        public string EditorText() => Editor?.Text ?? text;

        public void FlushEditor(Action done)
        {
            Flushes++;
            if (Editor is { } channel)
            {
                if (channel.BeginFlush(done) is { } id)
                {
                    unanswered.Add(id);
                }
                return;
            }
            if (!IsReady)
            {
                done();
                return;
            }
            if (Rendering is { } r)
            {
                Rendering = null;
                html = r;
            }
            if (Unreported is { } s)
            {
                Unreported = null;
                Type(s);
            }
            done();
        }

        public void Type(string s)
        {
            html = "<p>" + s + "</p>";
            text = s;
        }

        /// <summary>editor.Load: a new document; the page's seq starts over.</summary>
        public void Load(string body)
        {
            Editor!.Load(body);
            seq = 0;
            unanswered.Clear();
        }

        /// <summary>The page's ready, and its answer to EditorReady's flush with <paramref name="reported"/>.</summary>
        public void Ready(string reported, bool changedFirst)
        {
            Editor!.Receive("{\"type\":\"ready\"}");
            Answer(reported, changedFirst);
        }

        /// <summary>The page's debounced changed with <paramref name="reported"/>.</summary>
        public void Post(string reported) => Editor!.Receive(Changed(++seq, reported));

        /// <summary>The page answers the oldest flush with <paramref name="reported"/>, its two answers in the order given.</summary>
        public void Answer(string reported, bool changedFirst)
        {
            var id = unanswered[0];
            unanswered.RemoveAt(0);
            var n = ++seq;
            var changed = Changed(n, reported);
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

        public void SetAttachments(IReadOnlyList<DraftAttachment> attachments) => Attachments = attachments;

        public void SetStatus(string text) => Statuses.Add(text);

        public void Toast(string text) => Toasts.Add(text);

        public void SetSendEnabled(bool enabled) => SendEnabled.Add(enabled);

        public void CloseWindow() => Closes++;
    }

    /// <summary>A form registered with the compose controller.</summary>
    private sealed class Handle : IComposeWindowHandle
    {
        public List<IReadOnlyList<Account>> Lists { get; } = [];

        public void SetAccounts(IReadOnlyList<Account> accounts, bool placeholder) => Lists.Add(accounts);

        public void Toast(string text)
        {
        }
    }

    /// <summary>A fake daemon, a connected client, the form and the board's draft controller on a test UI thread.</summary>
    private sealed class Harness : IAsyncDisposable
    {
        private Harness(bool editor)
        {
            Form = new FakeForm(editor);
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeTimeProvider Time { get; } = new(new DateTimeOffset(2026, 10, 2, 10, 0, 0, TimeSpan.Zero));

        public FakeDaemon Fake { get; } = new();

        public Script Script { get; } = new();

        public RpcClient Client { get; private set; } = null!;

        public SettingsStore Settings { get; } = new(new InMemorySettingsBackend(), null);

        public FakeForm Form { get; }

        public ComposeDraftController Draft { get; private set; } = null!;

        public int LostCount { get; private set; }

        public List<string> Sent { get; } = [];

        public int SendFailures { get; private set; }

        public int Questions { get; private set; }

        public static async Task<Harness> StartAsync(DraftOwner owner = DraftOwner.Board, bool comment = false, bool editor = false)
        {
            var h = new Harness(editor);
            h.Fake.On(API.DraftSave.Name, h.Script.Save);
            h.Fake.On(API.DraftGet.Name, h.Script.Get);
            h.Fake.On(API.MessageSend.Name, h.Script.Send);
            h.Fake.On(API.DraftDelete.Name, h.Script.Delete);
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            h.Form.IsComment = comment;
            h.Draft = await h.Ui.RunAsync(() =>
            {
                var draft = new ComposeDraftController(h.Client, h.Settings, () => false, new CidRegistry(), h.Time, h.Pending, owner: owner)
                {
                    Form = h.Form,
                };
                // As the inline pane does with the draft draft.get returned.
                if (comment)
                {
                    var issue = new IssueInfo
                    {
                        Key = "WEB-7",
                        Url = "https://acme.atlassian.net/browse/WEB-7",
                        Summary = "Logo",
                        Status = "To Do",
                        StatusCategory = IssueStatusCategory.Todo,
                    };
                    draft.SetOriginal("w1", null, new DraftComment { Issue = issue, Visibility = CommentVisibility.Public });
                }
                else
                {
                    draft.SetOriginal("m_2", null);
                }
                draft.SetOpened(BoardDraft, 3, null, fromDrafts: true);
                draft.OnLost = () => h.LostCount++;
                draft.Sent += (_, text) => h.Sent.Add(text);
                draft.OnSendFailed = () => h.SendFailures++;
                draft.ConfirmDiscard = static (_, _, _) => Task.FromResult(true);
                draft.SaveDraftQuestion = () =>
                {
                    h.Questions++;
                    return Task.FromResult(DraftCloseAnswer.Discard);
                };
                if (h.Form.Editor is { } channel)
                {
                    // The window forwards the editor's Changed and Ready.
                    channel.Changed += (_, _) => draft.EditorChanged();
                    channel.Ready += (_, _) => draft.EditorReady();
                }
                return draft;
            });
            return h;
        }

        public Task Run(Action action) => Ui.RunAsync(action);

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake);

        /// <summary>The autosave's 30 s pass: a timer armed by now fires, and everything it started settles.</summary>
        public async Task AutosaveAsync()
        {
            await IdleAsync();
            Time.Advance(ComposeDraftController.AutosaveDelay);
            await IdleAsync();
        }

        /// <summary>One save, its outcome awaited.</summary>
        public async Task<Exception?> SaveNowAsync(SaveReason reason)
        {
            var done = new TaskCompletionSource<Exception?>(TaskCreationOptions.RunContinuationsAsynchronously);
            await Run(() => Draft.Save(reason, e => done.TrySetResult(e)));
            var error = await done.Task;
            await IdleAsync();
            return error;
        }

        public async Task<bool> FinishAsync() => await SettledAsync(await Ui.RunAsync(() => Draft.FinishAsync()));

        public async Task<bool> SettleAsync() => await SettledAsync(await Ui.RunAsync(() => Draft.SettleAsync()));

        /// <summary>
        /// The answer of a controller task once everything is idle: one still
        /// waiting then waits for something nobody answers, a failure rather
        /// than a hang.
        /// </summary>
        public async Task<bool> SettledAsync(Task<bool> task)
        {
            await IdleAsync();
            Assert.True(task.IsCompleted, "the controller is still waiting, for something nobody answers");
            return await task;
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
