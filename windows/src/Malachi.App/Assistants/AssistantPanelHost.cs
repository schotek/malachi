// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Assistant/AssistantPanelHost.swift;
// GTK: ui/internal/window/assistant_panel.go (newAssistantPanel's
// controller and hooks, close, syncShown, reveal, messageContext,
// selectionContext, followSelection, resolve, run, runAbout,
// summarizeUnread, askAttachment, askConsent) and selection.go
// (severalContext). The assistant panel's conversation (Core's
// AssistantPanelController) between the main window and the panel's view:
// the list's selection becomes the panel's context (one message, or a
// conversation row's folder members newest first, only its newest message
// until the members are known, or every message of several selected rows
// newest first; never an Outbox message), the Assistant menus' actions run
// in it (the panel unfolds,
// the main window comes forward), the first question asks for consent on
// the main window, and Open Draft opens a draft only after draft.list has
// it. A link of an answer is opened only after "Open This Link?" named its
// destination, as a link the daemon did not list; Get Claude Code…, a page
// the application itself names, opens as the sign-in pages do. The
// calendar's quick actions show while some account has the calendar
// capability (followed as the sidebar's accounts change), and Show in
// Calendar of an answer is the main window's Day view of the event's day
// with the event's detail (docs/calendar.md §5.9).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Preferences;
using Malachi.App.Reader;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Daemon;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Malachi.Core.Presentation;
using Malachi.Core.Selections;
using Malachi.Platform.Windows.Files;
using Microsoft.Extensions.Logging;

namespace Malachi.App.Assistants;

/// <summary>The assistant panel of the main window and its conversation.</summary>
internal sealed class AssistantPanelHost : IDisposable
{
    private readonly MainWindow window;
    private readonly ListController list;
    private readonly AppState state;
    private readonly AssistantPanel view;
    private readonly MailboxController mailbox;
    private readonly Malachi.Core.Boards.BoardObserverToken calendarToken;

    /// <summary>
    /// The panel's conversation over <paramref name="state"/>, rendered by
    /// <paramref name="view"/>, following <paramref name="integration"/>'s
    /// list; links go through <paramref name="reader"/>'s LinkOpener.
    /// </summary>
    public AssistantPanelHost(AppState state, MainWindow window, Integration integration, ReaderServices reader, AssistantPanel view)
    {
        this.window = window;
        this.state = state;
        this.view = view;
        mailbox = integration.Mailbox;
        list = integration.List;
        Controller = new AssistantPanelController(
            state.Settings,
            state.ClaudeCode,
            state.Paths.McpBridge,
            state.Paths.Socket,
            state.Paths.AssistantDir,
            ProcessEnvironment.Copy(ProcessEnvironment.Current()),
            logger: state.Logs.CreateLogger<AssistantPanelController>(),
            directories: new PrivateDirectory(),
            processLogger: state.Logs.CreateLogger<ClaudeCodeProcess>());
        Controller.Provider = state.InAppProvider;
        // Every conversation carries the user's profile, every account's note.
        Controller.Profile = () => state.CurrentProfile;
        Controller.Consent = () => state.AskAssistantConsentAsync(window);
        Controller.ReconnectProvider = ReconnectChatGptAsync;
        state.InAppProviderChanged += OnProviderChanged;
        Controller.ResolveContext = Resolve;
        Controller.OpenDraft = r => integration.Actions.OpenSavedDraft(new AccountId(r.AccountId), new DraftId(r.DraftId));
        // Show in Calendar of an answer: the main window's Day view of the
        // event's day with the event's detail.
        Controller.OpenInCalendar = r => window.ShowInCalendar(
            r.EventId.Length == 0 ? default(CalendarEventId?) : new CalendarEventId(r.EventId), r.Recurrence, r.Day(AppState.CalendarEnv().Zone));
        // The calendar's quick actions while some account has a calendar.
        // The calendar's quick actions while some account has a calendar: the
        // mailbox's accounts, or the window's calendar source's (the samples
        // of MALACHI_CALENDAR_SAMPLES have no account in the daemon).
        var calendar = state.CalendarSourceFor(window);
        view.HasCalendar = () =>
            list.Mailbox.Model.Accounts.Any(a => a.Can(Capability.Calendar)) || Cal.ShowsTodayStrip(calendar.Snapshot.Accounts);
        mailbox.EntriesChanged += OnEntriesChanged;
        calendarToken = calendar.Observe(change =>
        {
            if (change is Cal.SourceChange.SnapshotChanged)
            {
                view.UpdateQuickActions();
            }
        });
        view.AttachProfile(
            state.Profile,
            text => ProfileDialogs.EditProposalAsync(state.Alerts, window, text, state.Profile.Limits.Proposal),
            () => PreferencesWindow.Show(state)?.ShowAi());
        view.Attach(
            Controller,
            href => _ = reader.Links.OpenAsync(new ActivatedLink(href, href), [], window),
            url => _ = state.OpenUrlAsync(url));
    }

    /// <summary>The conversation.</summary>
    public AssistantPanelController Controller { get; }

    /// <summary>Ends a running Claude Code for good (the application quits).</summary>
    public void Dispose()
    {
        state.InAppProviderChanged -= OnProviderChanged;
        mailbox.EntriesChanged -= OnEntriesChanged;
        calendarToken.Cancel();
        view.DetachProfile();
        Controller.Close();
    }

    // Reconnect to ChatGPT on an error line: the connection's own sign-in
    // (the preferences' button); the board's triage looks at it again once
    // it worked.
    private async System.Threading.Tasks.Task<bool> ReconnectChatGptAsync()
    {
        try
        {
            await state.ChatGpt.SignInAsync();
        }
        catch (Exception e) when (e is OperationCanceledException or Malachi.Core.ChatGPT.ChatGptAuthException)
        {
            return false;
        }
        state.BoardTriage.RecheckSignIn();
        return true;
    }

    private void OnProviderChanged(object? sender, EventArgs e) => Controller.Provider = state.InAppProvider;

    // The accounts may have changed: the calendar's quick actions follow.
    private void OnEntriesChanged(object? sender, EventArgs e) => view.UpdateQuickActions();

    // The context

    /// <summary>The list's selection changed: the panel follows it (or compares it with what the conversation is about).</summary>
    public void FollowSelection() => Controller.SetContext(SelectionContext());

    // One message as the panel's context.
    private static AssistantPanelController.Context MessageContext(MessageSummary s) =>
        new(new AssistantSelection(s.AccountId.Value, [s.Id.Value]), 1, false, s.Subject, s.ThreadId?.Value ?? "");

    // The list's selection as the panel's context; null for none or an
    // Outbox message.
    private AssistantPanelController.Context? SelectionContext()
    {
        var rows = list.SelectedRows;
        if (rows.Count > 1)
        {
            return SeveralContext(rows);
        }
        if (list.SelectedRow is not { } row || list.Mailbox.Model.InOutbox(row.Message))
        {
            return null;
        }
        if (!row.Thread)
        {
            return MessageContext(row.Message);
        }
        // The conversation's subject is its newest member's without Re: and
        // Fwd:, as its row shows it.
        var subject = row.Summary?.Subject is { Length: > 0 } s ? s : row.Message.Subject;
        var thread = row.Summary?.Id.Value is { Length: > 0 } t ? t : row.Key.Thread?.Value ?? "";
        var account = row.Message.AccountId.Value;
        if (list.Mailbox.Model.RowIds(row) is { } ids)
        {
            var members = ids.Reverse().Select(i => i.Value).ToList();
            return new(new AssistantSelection(account, members), members.Count, false, subject, thread);
        }
        // A folded conversation whose members are not known yet: its newest
        // message stands for it until they are asked for.
        return new(new AssistantSelection(account, [row.Message.Id.Value]), Math.Max(row.Summary?.MessageCount ?? 0, 2), true, subject, thread);
    }

    // selection.go severalContext: several selected rows (one account) as
    // the panel's context: every message they stand for, newest first; a
    // conversation whose members are not known yet stands by its newest
    // message until they are asked for (partial). Null when one is queued.
    private AssistantPanelController.Context? SeveralContext(IReadOnlyList<ListRow> rows)
    {
        var model = list.Mailbox.Model;
        var ids = new List<MessageId>();
        var partial = false;
        var count = 0;
        foreach (var r in rows)
        {
            if (model.InOutbox(r.Message))
            {
                return null;
            }
            if (model.RowIds(r) is { } got)
            {
                ids.AddRange(got);
                count += got.Count;
            }
            else
            {
                ids.Add(r.Message.Id);
                count += Math.Max(r.Summary?.MessageCount ?? 1, 1);
                partial = true;
            }
        }
        var newest = Selection.NewestFirst(model, ids).Select(i => i.Value).ToList();
        return new(new AssistantSelection(rows[0].Message.AccountId.Value, newest), count, partial, Selection.SelectedText(rows.Count), "");
    }

    // Completes a folded conversation's members (newest first) when the
    // list still shows it selected; otherwise the context stays what it was.
    // Several rows: every member of their conversations, once known, while
    // the selection is of the context's account.
    private void Resolve(AssistantPanelController.Context c, Action<AssistantSelection> done)
    {
        if (list.SelectedRows.Count > 1)
        {
            list.SelectedIds((rows, ids) =>
            {
                if (rows[0].Message.AccountId.Value != c.Selection.AccountId)
                {
                    done(c.Selection);
                    return;
                }
                var newest = Selection.NewestFirst(list.Mailbox.Model, ids).Select(i => i.Value).ToList();
                done(new AssistantSelection(c.Selection.AccountId, newest));
            });
            return;
        }
        if (list.SelectedRow is not { Thread: true } row || row.Message.AccountId.Value != c.Selection.AccountId
            || c.Selection.MessageIds.Count == 0 || c.Selection.MessageIds[0] != row.Message.Id.Value)
        {
            done(c.Selection);
            return;
        }
        list.SelectedIds((rows, ids) =>
            done(new AssistantSelection(rows[0].Message.AccountId.Value, ids.Reverse().Select(i => i.Value).ToList())));
    }

    // Running from the menus

    /// <summary>A message action of the Assistant menu on the list's selection.</summary>
    public void Run(AssistantAction action)
    {
        if (SelectionContext() is not { } c)
        {
            return;
        }
        window.RevealAssistant();
        Controller.RunOn(action, c);
    }

    /// <summary>
    /// A message action from a message window: the main window comes forward
    /// and the action runs on the window's message; the list's selection stays.
    /// </summary>
    public void RunAbout(AssistantAction action, MessageSummary s)
    {
        if (list.Mailbox.Model.InOutbox(s))
        {
            return;
        }
        window.RevealAssistant();
        Controller.RunOn(action, MessageContext(s));
    }

    /// <summary>Summarize Unread in This Folder for folder <paramref name="k"/>.</summary>
    public void SummarizeUnread(FolderKey k)
    {
        window.RevealAssistant();
        Controller.SummarizeUnread(k.Account.Value, k.Folder.Value);
    }

    /// <summary>An attachment's question: the panel waits for the user's words.</summary>
    public void AskAttachment(MessageSummary s, Attachment a)
    {
        window.RevealAssistant();
        Controller.AskAttachment(s.AccountId.Value, s.Id.Value, a.PartId, s.Subject, s.ThreadId?.Value ?? "");
    }
}
