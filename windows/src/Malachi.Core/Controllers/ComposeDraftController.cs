// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ComposeDraftController.swift
// (composeRichText, ComposeDraftController); GTK: ui/internal/compose/draft.go
// (richText, autosaveDelay, editorChanged, markDirty, refreshStatus, build,
// save, saveFailed, send, deleteDraft, discard, closeRequest, cleanup).
//
// The lifecycle of the draft behind a compose window, without the widgets:
// the window is reached through IComposeForm, the daemon through the
// client. Two things macOS keeps in its AppKit window live here, as GTK
// keeps them in draft.go: the flush echo (flushEcho, editorChanged; the
// window forwards the editor's Changed to EditorChanged, and the save's
// flush records what it reported), and the autosave timer on an injected
// TimeProvider. One more thing is a Windows addition (docs/windows-port.md
// §0): SaveForQuitAsync, which saves a dirty draft on Quit without asking
// and says whether that went through, so that the app asks only when it did
// not. It flushes the editor first, which GTK and macOS never do outside a
// save, so the echo needs two baselines they do without: what the page
// reports when it becomes ready (EditorReady; the page serialises the loaded
// body its own way), and, for Quit's flush, the content the editor had
// already reported, when the flush reports it unchanged.
//
// The window's part (phase E): it forwards the editor's Ready and Changed
// to EditorReady and EditorChanged, runs CloseRequestAsync on a close, and
// once it really closes (Cleanup has run) calls ComposeController.Remove
// with its handle, as GTK's cleanup calls Manager.remove.
//
// Every call is fired as GTK and Swift fire it, to its end even when the
// window closes meanwhile (GTK: "a closed window does not cancel a save in
// flight"): ControllerScope.PerformPastClose, whose outcome is dropped once
// Cleanup closed the scope (Swift's closed guard).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Html;
using Malachi.Core.I18n;
using Malachi.Core.Settings;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// compose/draft.go: autosave, <see cref="Build"/>, <see cref="Save"/>,
/// <see cref="SaveFailed"/>, <see cref="Send"/>, <see cref="Discard"/>,
/// <see cref="CloseRequestAsync"/> and <see cref="Cleanup"/> over an
/// <see cref="IComposeForm"/>. Created by its window on the UI thread and
/// UI-thread-affine (docs/windows-port.md §7.1).
/// </summary>
public sealed partial class ComposeDraftController : ObservableObject, IDisposable
{
    /// <summary>
    /// compose.richText: whether the editor's HTML is transmitted with the
    /// draft. The backend sanitises it in compose mode and derives the text
    /// alternative, so the formatting toolbar and inline images are on.
    /// </summary>
    public const bool RichText = true;

    /// <summary>
    /// compose.autosaveDelay: how long after the first unsaved edit a dirty
    /// draft is saved (a second edit does not restart the timer).
    /// </summary>
    public static readonly TimeSpan AutosaveDelay = TimeSpan.FromSeconds(30);

    /// <summary>
    /// How long <see cref="SaveForQuitAsync"/> waits for the editor to
    /// report its content, and for each save on top of <c>draft.save</c>'s
    /// own timeout: a renderer that hangs, rather than crashes, never answers
    /// a flush, and Quit must not wait for it for ever.
    /// </summary>
    public static readonly TimeSpan QuitFlushTimeout = TimeSpan.FromSeconds(5);

    private readonly RpcClient client;
    private readonly SettingsStore settings;
    private readonly Func<bool> placeholder;
    private readonly CidRegistry registry;
    private readonly TimeProvider time;
    private readonly ILogger logger;
    private readonly ControllerScope scope;

    // What the last save's flush reported (draftState.flushed).
    private readonly FlushEcho flushed = new();

    // Runs when the in-flight save finishes (pendingAfterSave).
    private readonly List<Action<Exception?>> pendingAfterSave = [];

    // The armed autosave timer; null when none is (Go autosave == 0).
    private CancellationTokenSource? autosave;

    /// <param name="client">The transport.</param>
    /// <param name="settings"><c>ConfirmDelete</c> decides whether Discard asks.</param>
    /// <param name="placeholder">
    /// Manager.Placeholder: whether the From row lists the placeholder
    /// identity (the status line says so).
    /// </param>
    /// <param name="registry">The <c>cid:</c> registry the inline pictures live in; <see cref="CidRegistry.Shared"/> by default.</param>
    /// <param name="time">The clock of the autosave and of "Draft saved"; the system's by default.</param>
    /// <param name="pending">Where the background work is counted (tests wait on it); a tracker of its own by default.</param>
    /// <param name="logger">Method names and errors only, never mail content.</param>
    public ComposeDraftController(
        RpcClient client,
        SettingsStore settings,
        Func<bool> placeholder,
        CidRegistry? registry = null,
        TimeProvider? time = null,
        PendingWork? pending = null,
        ILogger<ComposeDraftController>? logger = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(settings);
        ArgumentNullException.ThrowIfNull(placeholder);
        this.client = client;
        this.settings = settings;
        this.placeholder = placeholder;
        this.registry = registry ?? CidRegistry.Shared;
        this.time = time ?? TimeProvider.System;
        this.logger = logger ?? (ILogger)NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>
    /// Manager.OnSent: raised with a short message when the window queued a
    /// message (the window hands it on to <see cref="ComposeController.ReportSent"/>).
    /// </summary>
    public event EventHandler<string>? Sent;

    /// <summary>The state of the draft; replaced whole on every change.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanCloseWithoutAsking))]
    public partial DraftState Draft { get; private set; } = new();

    /// <summary>Whether the autosave timer is armed (Go <c>autosave != 0</c>).</summary>
    [ObservableProperty]
    public partial bool AutosaveArmed { get; private set; }

    /// <summary>The window; set by the window, which owns the controller.</summary>
    public IComposeForm? Form { get; set; }

    /// <summary>
    /// widget.ConfirmDestructive for Discard: heading, body, label, true when
    /// confirmed. The default confirms, for a window without dialogs.
    /// </summary>
    public Func<string, string, string, Task<bool>> ConfirmDiscard { get; set; } = static (_, _, _) => Task.FromResult(true);

    /// <summary>
    /// The "Save changes to this draft?" question. The default cancels, so a
    /// window without dialogs stays open.
    /// </summary>
    public Func<Task<DraftCloseAnswer>> SaveDraftQuestion { get; set; } = static () => Task.FromResult(DraftCloseAnswer.Cancel);

    /// <summary>closeRequest's first branch: the window may go without a question.</summary>
    public bool CanCloseWithoutAsking => Draft.Discard || (!Draft.Dirty && !Draft.Saving);

    /// <summary>
    /// The original a reply or forward refers to (compose.go <c>newWindow</c>:
    /// <c>Params.InReplyTo</c> / <c>Params.Forwarding</c>).
    /// </summary>
    public void SetOriginal(MessageId? inReplyTo, MessageId? forwarding)
    {
        scope.VerifyAccess();
        Draft = Draft with { InReplyTo = inReplyTo, Forwarding = forwarding };
    }

    /// <summary>
    /// The saved draft the window edits (compose.go <c>newWindow</c>:
    /// <c>Params.DraftID</c>, <c>Version</c>, <c>Replaces</c>): its id and
    /// version make the saves updates, and a draft opened from the Drafts
    /// folder is never deleted by closing the window.
    /// </summary>
    public void SetOpened(DraftId? draftId, int version, MessageId? replaces, bool fromDrafts)
    {
        scope.VerifyAccess();
        Draft = Draft with { DraftId = draftId, Version = version, Replaces = replaces, ExplicitSave = fromDrafts };
    }

    // Dirty state and status

    /// <summary>
    /// editorChanged: the editor's <c>Changed</c>. The one a save's flush
    /// produces reports what is being saved; only other content is an edit.
    /// Correct in either order of the flush's answers together with
    /// <see cref="EditorChannel"/>, which raises <c>Changed</c> only after
    /// the flush's done has recorded (docs/windows-port.md §6.5).
    /// </summary>
    public void EditorChanged()
    {
        scope.VerifyAccess();
        if (Draft.Closed || Form is not { } form || flushed.Echo(form.EditorHtml()))
        {
            return;
        }
        MarkDirty();
    }

    /// <summary>
    /// Windows addition, for <see cref="SaveForQuitAsync"/>: the editor's
    /// <c>Ready</c> (the window forwards it, as it forwards
    /// <c>Changed</c>). The page is asked for the document it was given, and
    /// what it reports is recorded as the flush echo: its serialisation of
    /// the loaded body need not match that body character for character
    /// (<c>&amp;#39;</c> comes back as an apostrophe), and Quit's flush must
    /// not take the page's first report of untouched content for an edit.
    /// GTK and macOS need no such baseline, since their pages report only
    /// after an input or for a save. An edit typed in the few milliseconds
    /// before this flush reaches the page would be taken for the baseline;
    /// the next edit marks the draft dirty with it all the same.
    /// </summary>
    public void EditorReady()
    {
        scope.VerifyAccess();
        if (Draft.Closed || Form is not { } form)
        {
            return;
        }
        form.FlushEditor(() =>
        {
            // Runs before the Changed of the same report (EditorChannel).
            if (!Draft.Closed)
            {
                flushed.Record(form.EditorHtml());
            }
        });
    }

    /// <summary>markDirty records an edit and arms the autosave timer.</summary>
    public void MarkDirty()
    {
        scope.VerifyAccess();
        if (Draft.Closed)
        {
            return; // a late report of a window already gone
        }
        Draft = Draft with { Dirty = true };
        RefreshStatus();
        if (autosave is not null)
        {
            return;
        }
        var timer = new CancellationTokenSource();
        var cancelled = timer.Token;
        autosave = timer;
        AutosaveArmed = true;
        scope.RunDetached(async lifetime =>
        {
            using var wait = CancellationTokenSource.CreateLinkedTokenSource(lifetime, cancelled);
            try
            {
                await Task.Delay(AutosaveDelay, time, wait.Token);
            }
            catch (OperationCanceledException)
            {
                return; // disarmed, or the window closed
            }
            if (cancelled.IsCancellationRequested || !ReferenceEquals(autosave, timer))
            {
                return;
            }
            autosave = null;
            AutosaveArmed = false;
            if (Draft.Dirty && !Draft.Saving)
            {
                Save(SaveReason.Autosave);
            }
        });
    }

    private void CancelAutosave()
    {
        autosave?.Cancel();
        autosave = null;
        AutosaveArmed = false;
    }

    /// <summary>The status line under the window.</summary>
    public void RefreshStatus()
    {
        scope.VerifyAccess();
        if (Form is not { } form)
        {
            return;
        }
        var d = Draft;
        if (d.Sending)
        {
            form.SetStatus(L10n.T("Sending…"));
        }
        else if (d.Saving)
        {
            form.SetStatus(L10n.T("Saving draft…"));
        }
        else if (d.Dirty)
        {
            form.SetStatus(L10n.T("Unsaved changes"));
        }
        else if (d.LastSaved is { } saved)
        {
            form.SetStatus(L10n.T("Draft saved %s", Format.FormatTime(saved, timeZone: time.LocalTimeZone)));
        }
        else if (placeholder())
        {
            form.SetStatus(L10n.T("Using placeholder account"));
        }
        else
        {
            form.SetStatus("");
        }
    }

    // Building and saving

    /// <summary>
    /// build assembles the wire draft from the rows and the editor's last
    /// content. Call after the editor's flush. Null without a form.
    /// </summary>
    public Api.Draft? Build()
    {
        scope.VerifyAccess();
        if (Form is not { } form)
        {
            return null;
        }
        var (to, cc, bcc, _) = form.Recipients();
        var d = new Api.Draft
        {
            Id = Draft.DraftId,
            AccountId = form.Account.Id,
            Version = Draft.Version,
            To = to,
            Cc = cc.Count == 0 ? null : cc,
            Bcc = bcc.Count == 0 ? null : bcc,
            Subject = form.Subject,
            TextBody = form.EditorText(),
            InReplyTo = Draft.InReplyTo,
            Forwarding = Draft.Forwarding,
            Replaces = Draft.Replaces,
        };
        if (RichText)
        {
            d = d with { HtmlBody = form.EditorHtml() };
        }
        // In draft.save params only the id of an attachment is read.
        DraftAttachment[] attachments =
        [
            .. form.Attachments.Select(a => new DraftAttachment { Id = a.Id, Filename = "", ContentType = "", Size = 0, Inline = false }),
        ];
        return d with { Attachments = attachments.Length == 0 ? null : attachments };
    }

    /// <summary>
    /// save persists the draft; <paramref name="done"/> (optional) runs with
    /// the failure, null for a success. A save already in flight queues
    /// <paramref name="done"/> behind it.
    /// </summary>
    public void Save(SaveReason reason, Action<Exception?>? done = null)
    {
        scope.VerifyAccess();
        if (done is not null)
        {
            pendingAfterSave.Add(done);
        }
        if (Draft.Saving)
        {
            return;
        }
        CancelAutosave();
        Draft = Draft with { Saving = true, Dirty = false }; // edits during the call set it again
        RefreshStatus();
        if (Form is not { } form)
        {
            return;
        }
        form.FlushEditor(() =>
        {
            // Runs before the Changed of the same report (EditorChannel).
            flushed.Record(form.EditorHtml());
            if (Draft.Closed || Build() is not { } wire)
            {
                return;
            }
            scope.PerformPastClose(client, API.DraftSave, new DraftSaveParams { Draft = wire }, outcome =>
            {
                if (!Draft.Closed)
                {
                    Saved(reason, outcome);
                }
            });
        });
    }

    private void Saved(SaveReason reason, Outcome<DraftSaveResult> outcome)
    {
        Draft = Draft with { Saving = false };
        Exception? failure = null;
        if (!outcome.TryGetValue(out var res, out var error))
        {
            failure = error!;
            Draft = Draft with { Dirty = true };
            SaveFailed(reason, failure);
        }
        else
        {
            Draft = Draft with
            {
                DraftId = res.DraftId,
                Version = res.Version,
                Replaces = null,
                ExplicitSave = Draft.ExplicitSave || reason == SaveReason.Explicit,
                LastSaved = time.GetUtcNow(),
                LastError = "",
            };
            if (Form is { } form)
            {
                var kept = res.Attachments ?? [];
                if (kept.Count != form.Attachments.Count)
                {
                    form.SetAttachments(kept);
                }
                var message = BlockedSummary.Text(res.Blocked);
                if (message.Length > 0)
                {
                    form.Toast(message);
                }
            }
        }
        RefreshStatus();
        if (Draft.Dirty && autosave is null && failure is null)
        {
            MarkDirty(); // edits arrived during the save
        }
        RunPending(failure);
    }

    /// <summary>
    /// saveFailed: a conflict, or a draft deleted meanwhile, starts over with
    /// a fresh draft (local wins); a Drafts message to take over that is gone
    /// is dropped from the next save; an autosave does not nag with the same
    /// failure every 30 s.
    /// </summary>
    public void SaveFailed(SaveReason reason, Exception error)
    {
        scope.VerifyAccess();
        ArgumentNullException.ThrowIfNull(error);
        if (error is RpcException e)
        {
            switch (e.Code.Value)
            {
                case ErrorCode.Conflict:
                    // Local wins: the next save creates a fresh draft with our text.
                    Draft = Draft with { DraftId = null, Version = 0, Replaces = null };
                    Form?.Toast(L10n.T("This draft was changed elsewhere; your text will be saved as a new draft"));
                    return;
                case ErrorCode.DraftNotFound:
                    // Deleted meanwhile (its copy went to the Trash): the text
                    // survives as a new draft, its attachments with it.
                    Draft = Draft with { DraftId = null, Version = 0, Replaces = null };
                    Form?.Toast(L10n.T("This draft was removed elsewhere; your text will be saved as a new draft"));
                    return;
                case ErrorCode.MessageNotFound when Draft.Replaces is not null:
                    // The Drafts message it was to take over is gone: save
                    // without it.
                    Draft = Draft with { Replaces = null };
                    MarkDirty();
                    return;
            }
        }
        var text = RpcErrorText.Text(L10n.T("Saving the draft"), error);
        if (reason == SaveReason.Autosave)
        {
            // Do not nag every 30 s with the same failure (e.g. no backend).
            if (string.Equals(text, Draft.LastError, StringComparison.Ordinal))
            {
                LogAutosaveFailedAgain(logger, error);
                return;
            }
            Draft = Draft with { LastError = text };
        }
        Form?.Toast(text);
        // Retry later.
        if (autosave is null)
        {
            MarkDirty();
        }
    }

    // Sending

    /// <summary>send validates, saves if needed and queues the message.</summary>
    public void Send()
    {
        scope.VerifyAccess();
        if (Draft.Sending || Form is not { } form)
        {
            return;
        }
        var (to, cc, bcc, ok) = form.Recipients();
        if (!ok)
        {
            form.Toast(L10n.T("Fix the highlighted recipients"));
            return;
        }
        if (to.Count + cc.Count + bcc.Count == 0)
        {
            form.Toast(L10n.T("Add at least one recipient"));
            return;
        }
        Draft = Draft with { Sending = true };
        form.SetSendEnabled(false);
        RefreshStatus();
        Save(SaveReason.Explicit, error =>
        {
            if (error is not null || Form is not { } current || Draft.DraftId is not { } draftId)
            {
                SendFailed();
                return;
            }
            var parameters = new MessageSendParams { AccountId = current.Account.Id, DraftId = draftId, Version = Draft.Version };
            scope.PerformPastClose(client, API.MessageSend, parameters, outcome =>
            {
                if (Draft.Closed)
                {
                    return;
                }
                if (!outcome.TryGetValue(out _, out var failure))
                {
                    if (failure is RpcException { Code.Value: ErrorCode.Conflict })
                    {
                        Draft = Draft with { DraftId = null, Version = 0, Dirty = true };
                    }
                    Form?.Toast(RpcErrorText.Text(L10n.T("Sending"), failure));
                    SendFailed();
                    return;
                }
                Draft = Draft with { Discard = true };
                Sent?.Invoke(this, L10n.T("Message queued for sending"));
                Form?.CloseWindow();
            });
        });
    }

    // send's fail: the button back, the status line with it.
    private void SendFailed()
    {
        Draft = Draft with { Sending = false };
        Form?.SetSendEnabled(true);
        RefreshStatus();
    }

    // Discarding and closing

    /// <summary>
    /// discard drops the draft (after confirmation when the setting is on). A
    /// draft never saved has no id, but may hold attachments the backend
    /// imported for it (the template's pictures and files); those are
    /// released rather than left for the sweep.
    /// </summary>
    public void Discard()
    {
        scope.VerifyAccess();
        if (Form is null)
        {
            return;
        }
        if (!settings.ConfirmDelete || (!Draft.Dirty && Draft.DraftId is null))
        {
            DiscardNow();
            return;
        }
        scope.Perform(
            _ => ConfirmDiscard(L10n.T("Discard this message?"), "", L10n.T("_Discard")),
            outcome =>
            {
                if (!outcome.TryGetValue(out var confirmed, out var error))
                {
                    // The dialog could not be shown (another one is open);
                    // nothing is discarded unasked.
                    LogConfirmationFailed(logger, error!);
                    return;
                }
                if (confirmed && !Draft.Closed)
                {
                    DiscardNow();
                }
            });
    }

    // deleteDraft deletes the stored draft (and with it its copy in the
    // Drafts folder); the window is closing, so a failure is only logged.
    private void DeleteDraft()
    {
        if (Form is not { } form || Draft.DraftId is not { } id)
        {
            return;
        }
        FireOnTheWayOut(API.DraftDelete, new DraftDeleteParams { AccountId = form.Account.Id, DraftId = id });
    }

    private void DiscardNow()
    {
        if (Form is not { } form)
        {
            return;
        }
        var accountId = form.Account.Id;
        if (Draft.DraftId is not null)
        {
            DeleteDraft();
        }
        else
        {
            foreach (var a in form.Attachments)
            {
                FireOnTheWayOut(API.AttachmentRemove, new AttachmentRemoveParams { AccountId = accountId, AttachmentId = a.Id });
            }
        }
        Draft = Draft with { Discard = true };
        form.CloseWindow();
    }

    /// <summary>
    /// closeRequest keeps the window open while there are unsaved edits and
    /// asks what to do with them (<see cref="SaveDraftQuestion"/>). True when
    /// the window may close now (<see cref="Cleanup"/> has run); false when it
    /// stays.
    /// </summary>
    public async Task<bool> CloseRequestAsync()
    {
        scope.VerifyAccess();
        if (CanCloseWithoutAsking)
        {
            Cleanup();
            return true;
        }
        switch (await SaveDraftQuestion())
        {
            case DraftCloseAnswer.Discard:
                Draft = Draft with { Discard = true };
                if (!Draft.ExplicitSave && Draft.DraftId is not null)
                {
                    DeleteDraft();
                }
                Cleanup();
                return true;
            case DraftCloseAnswer.Save:
                var error = await SaveAsync(SaveReason.Explicit);
                // On failure the toast is shown and the window stays.
                if (error is not null || Draft.Closed)
                {
                    return false;
                }
                Draft = Draft with { Discard = true };
                Cleanup();
                return true;
            default:
                return false;
        }
    }

    /// <summary>
    /// Windows addition (docs/windows-port.md §0, "dirty drafts saved on
    /// Quit"): saves what the window holds without asking, as the close
    /// question's Save Draft would, and says whether nothing unsaved is left.
    /// An edit the editor has not reported yet (its <c>changed</c> is
    /// debounced) is fetched with a flush first, also while a save is in
    /// flight (that save's flush may have come before the keystroke); what
    /// the flush reports unchanged is no edit. A save in flight is waited
    /// for, and edits that arrived meanwhile are saved after it. True when
    /// everything is saved, there was nothing to save, or the window is gone
    /// or being discarded; false when a save failed (its toast is shown) or
    /// the editor did not answer within <see cref="QuitFlushTimeout"/>: the
    /// app then asks that window's question (<see cref="CloseRequestAsync"/>),
    /// and only then. The window stays open either way; closing it is the
    /// app's next step.
    /// </summary>
    public async Task<bool> SaveForQuitAsync()
    {
        scope.VerifyAccess();
        if (Draft.Closed || Draft.Discard || Form is not { } form)
        {
            return true;
        }
        if (!Draft.Dirty)
        {
            // Content the editor reports unchanged since its last report (or
            // since the baseline of EditorReady) is recorded as this flush's
            // echo before the Changed of the same report arrives
            // (EditorChannel); new content is an edit and is saved below.
            // Recording every flush, as a save does, would lose exactly the
            // edit this flush is for.
            var before = form.EditorHtml();
            // The continuation runs after the turn that raised the Changed.
            var (reported, _) = await WithinAsync<bool>(
                answer => form.FlushEditor(() =>
                {
                    if (!Draft.Closed && string.Equals(form.EditorHtml(), before, StringComparison.Ordinal))
                    {
                        flushed.Record(before);
                    }
                    answer(true);
                }),
                QuitFlushTimeout);
            if (!reported)
            {
                LogQuitTimedOut(logger, "editor flush");
                return Draft.Closed || Draft.Discard;
            }
        }
        while (!Draft.Closed && !Draft.Discard && (Draft.Dirty || Draft.Saving))
        {
            var (saved, error) = await WithinAsync<Exception?>(answer => Save(SaveReason.Explicit, answer), QuitFlushTimeout + API.DraftSave.Timeout);
            if (!saved)
            {
                LogQuitTimedOut(logger, API.DraftSave.Name);
                return Draft.Closed || Draft.Discard;
            }
            if (error is not null)
            {
                return Draft.Closed;
            }
        }
        return true;
    }

    /// <summary>
    /// cleanup runs when the window really closes: late replies are dropped,
    /// the autosave is disarmed, the inline pictures forgotten. Idempotent. A
    /// save whose outcome is still awaited (the close question's) is answered
    /// with a cancellation so nobody waits for ever.
    /// </summary>
    public void Cleanup()
    {
        scope.VerifyAccess();
        if (Draft.Closed)
        {
            return;
        }
        Draft = Draft with { Closed = true };
        CancelAutosave();
        foreach (var a in Form?.Attachments ?? [])
        {
            if (a.Inline && a.ContentId is { } cid)
            {
                registry.Unregister(cid);
            }
        }
        scope.Close();
        RunPending(new OperationCanceledException("the compose window closed"));
    }

    /// <summary>The window is gone: <see cref="Cleanup"/>.</summary>
    public void Dispose() => Cleanup();

    // save with its outcome awaited (Swift's withCheckedContinuation).
    private Task<Exception?> SaveAsync(SaveReason reason)
    {
        var done = new TaskCompletionSource<Exception?>(TaskCreationOptions.RunContinuationsAsynchronously);
        Save(reason, failure => done.TrySetResult(failure));
        return done.Task;
    }

    // Starts work, which calls its answer once (on the UI thread), and
    // waits for that answer or for limit on the controller's clock,
    // whichever comes first: (true, the answer) or (false, default). What
    // the work started is left to finish whenever it does. One completion
    // source for both, whose continuation is posted to the UI thread when
    // it completes (Task.WaitAsync would add a hop through the thread pool
    // that nothing tracks).
    private async Task<(bool Answered, T Value)> WithinAsync<T>(Action<Action<T>> work, TimeSpan limit)
    {
        var done = new TaskCompletionSource<(bool, T)>(TaskCreationOptions.RunContinuationsAsynchronously);
        using var timer = time.CreateTimer(_ => done.TrySetResult((false, default!)), null, limit, Timeout.InfiniteTimeSpan);
        work(value => done.TrySetResult((true, value)));
        return await done.Task;
    }

    private void RunPending(Exception? failure)
    {
        var pending = pendingAfterSave.ToArray();
        pendingAfterSave.Clear();
        foreach (var f in pending)
        {
            f(failure);
        }
    }

    // A mutation fired while the window closes (Swift's detached Task): sent
    // whatever the window does next, its failure only logged.
    private void FireOnTheWayOut<TParams, TResult>(RpcMethod<TParams, TResult> method, TParams parameters) =>
        scope.PerformPastClose(async () =>
        {
            try
            {
                await client.CallAsync(method, parameters);
            }
#pragma warning disable CA1031 // The window is gone; there is nobody left to tell.
            catch (Exception e)
#pragma warning restore CA1031
            {
                LogCallFailed(logger, method.Name, e);
            }
            return true;
        });

    [LoggerMessage(Level = LogLevel.Debug, Message = "autosave failed again")]
    private static partial void LogAutosaveFailedAgain(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Debug, Message = "{Method} failed")]
    private static partial void LogCallFailed(ILogger logger, string method, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "the discard confirmation failed; nothing was discarded")]
    private static partial void LogConfirmationFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "saving for quit: {Step} did not finish in time")]
    private static partial void LogQuitTimedOut(ILogger logger, string step);
}
