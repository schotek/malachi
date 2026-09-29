// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Assistant/AssistantActions.swift
// (ask, summarizeUnread, ask(about:), open, failed) and of
// AssistantController.canAsk(about:); GTK: ui/internal/window/assistant.go
// (askAssistant, askAssistantAbout, handOff, canSummarizeUnread,
// summarizeUnread, openAssistantLink, assistantFailed,
// askAboutAttachment). What the Assistant menus and an attachment's "Ask
// the Assistant…" do: build the prompt for what they act on, the link for
// the chosen Claude app (the assistant-target setting; the application's
// AssistantController says whether it can, and nothing goes to the other
// app instead), and hand the link to Windows. Claude opens with the prompt
// prefilled and sends nothing. A prompt carries only the API's opaque ids;
// a file goes over as a path, written as Open writes it (the open
// directory, the Mark of the Web). With In App chosen nothing leaves the
// application: the actions run in the assistant panel, which unfolds.
//
// Windows differences: the link goes through ILauncher.OpenAssistantLinkAsync,
// which takes only the Assistant's three forms; its failure is the toast of
// a link that could not be opened, as on macOS.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Reader;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;

namespace Malachi.App.Assistants;

/// <summary>The Assistant menus' and the attachment chips' actions.</summary>
internal sealed partial class AssistantActions
{
    private readonly AppState state;
    private readonly ListController list;
    private readonly ReaderServices reader;
    private readonly ILogger logger;

    /// <summary>The actions over the application's state, the list's selection and the reader's services.</summary>
    public AssistantActions(AppState state, ListController list, ReaderServices reader)
    {
        this.state = state;
        this.list = list;
        this.reader = reader;
        logger = state.Logs.CreateLogger<AssistantActions>();
    }

    /// <summary>The assistant panel of the main window; the Integration sets it.</summary>
    public AssistantPanelHost? Panel { get; set; }

    private AssistantController Assistant => state.Assistant;

    private MailModel Model => list.Mailbox.Model;

    // Messages

    /// <summary>
    /// A message action on the list's selection: a conversation row's folder
    /// members, newest first (the list hands them oldest first, fetched first
    /// when not known yet), or the one message. Never an Outbox message: it
    /// is not on the server yet.
    /// </summary>
    public void Ask(AssistantAction action, Window? window)
    {
        var (target, ok) = Assistant.Pick(needsBridge: true);
        if (target == AssistantTarget.App)
        {
            if (ok)
            {
                Panel?.Run(action);
            }
            return;
        }
        list.SelectedIds((row, ids) =>
        {
            if (Model.InOutbox(row.Message))
            {
                return;
            }
            IEnumerable<MessageId> newestFirst = row.Thread ? ids.Reverse() : ids;
            HandOff(action, row.Message.AccountId, newestFirst.ToList(), window);
        });
    }

    /// <summary>A message action on one message (a message window).</summary>
    public void AskAbout(AssistantAction action, MessageSummary s, Window? window)
    {
        if (Model.InOutbox(s))
        {
            return;
        }
        var (target, ok) = Assistant.Pick(needsBridge: true);
        if (target == AssistantTarget.App)
        {
            if (ok)
            {
                Panel?.RunAbout(action, s);
            }
            return;
        }
        HandOff(action, s.AccountId, [s.Id], window);
    }

    private void HandOff(AssistantAction action, AccountId account, IReadOnlyList<MessageId> ids, Window? window)
    {
        var (target, ok) = Assistant.Pick(needsBridge: true);
        if (!ok)
        {
            return;
        }
        string prompt;
        try
        {
            prompt = Core.Assistants.Assistant.Prompt(target, action, new AssistantSelection(account.Value, ids.Select(i => i.Value).ToList()));
        }
        catch (AssistantException e)
        {
            Failed(e, window);
            return;
        }
        Open(Core.Assistants.Assistant.Link(target, prompt), window);
    }

    // The folder

    /// <summary>
    /// Whether Summarize Unread in This Folder can run: a folder is selected
    /// in the sidebar, it is not an Outbox (whose messages are not on the
    /// server), and no search replaces it.
    /// </summary>
    public bool CanSummarizeUnread =>
        Model.Selected is { } k && !list.SearchActive && Model.FolderRole(k) != FolderRole.Outbox;

    /// <summary>Summarize Unread in This Folder, for the folder selected in the sidebar.</summary>
    public void SummarizeUnread(Window? window)
    {
        if (!CanSummarizeUnread || Model.Selected is not { } k)
        {
            return;
        }
        var (target, ok) = Assistant.Pick(needsBridge: true);
        if (!ok)
        {
            return;
        }
        if (target == AssistantTarget.App)
        {
            Panel?.SummarizeUnread(k);
            return;
        }
        string prompt;
        try
        {
            prompt = Core.Assistants.Assistant.UnreadPrompt(k.Account.Value, k.Folder.Value);
        }
        catch (AssistantException e)
        {
            Failed(e, window);
            return;
        }
        Open(Core.Assistants.Assistant.Link(target, prompt), window);
    }

    // Files

    /// <summary>
    /// An attachment chip's "Ask the Assistant…". In the panel it waits for
    /// the user's question, and Claude Code reads the part through the
    /// bridge. Otherwise the part is written as Open writes it (downloaded
    /// first when <paramref name="remote"/>), then handed to the chosen
    /// Claude app, which needs its handler but not the bridge (Claude reads
    /// the file, not the mail). Failures of the fetch and the write have had
    /// their toasts.
    /// </summary>
    public async void AskAboutAttachment(Attachment a, MessageSummary s, bool remote, Window? window)
    {
        Assistant.RefreshHandlers();
        if (!Assistant.CanAskFile(a.ContentType))
        {
            return;
        }
        if (Assistant.Settings.AssistantTarget == AssistantTarget.App)
        {
            Panel?.AskAttachment(s, a);
            return;
        }
        if (await reader.Attachments.WriteForHandOffAsync(a, s, remote, window) is not { } path)
        {
            return;
        }
        // Asked again: the download may have taken a while.
        var (target, ok) = Assistant.Pick(needsBridge: false);
        if (!ok)
        {
            return;
        }
        string link;
        try
        {
            link = Core.Assistants.Assistant.FileLink(target, path, Core.Assistants.Assistant.FilePrompt(target));
        }
        catch (AssistantException e)
        {
            Failed(e, window);
            return;
        }
        Open(link, window);
    }

    // Opening

    // Hands link to the app that handles its scheme; a failure is a toast.
    // The link of a file names the attachment, so it is never logged.
    private async void Open(string link, Window? window)
    {
        try
        {
            await state.Launcher.OpenAssistantLinkAsync(link, ReaderServices.Owner(window));
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            LogOpenFailed(logger, e.GetType().Name, e.HResult);
            // TRANSLATORS: %s is a technical error message.
            reader.ToastIn(window, L10n.T("The link could not be opened: %s", e.Message));
        }
    }

    // A prompt or a link that could not be built: ids missing, a prompt too
    // long even for one message, a path that is not clean.
    private void Failed(AssistantException e, Window? window)
    {
        LogFailed(logger, e.Kind.ToString());
        // TRANSLATORS: %s is a technical error message.
        reader.ToastIn(window, L10n.T("The link could not be opened: %s", e.Message));
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "opening an assistant link failed: {Kind} 0x{HResult:X8}")]
    private static partial void LogOpenFailed(ILogger logger, string kind, int hResult);

    [LoggerMessage(Level = LogLevel.Warning, Message = "assistant: {Kind}")]
    private static partial void LogFailed(ILogger logger, string kind);
}
