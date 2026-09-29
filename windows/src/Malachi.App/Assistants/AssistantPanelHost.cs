// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Assistant/AssistantPanelHost.swift;
// GTK: ui/internal/window/assistant_panel.go (newAssistantPanel's
// controller and hooks, close, syncShown, reveal, messageContext,
// selectionContext, followSelection, resolve, run, runAbout,
// summarizeUnread, askAttachment, askConsent). The assistant panel's
// conversation (Core's AssistantPanelController) between the main window
// and the panel's view: the list's selection becomes the panel's context
// (one message, or a conversation row's folder members newest first, only
// its newest message until the members are known; never an Outbox
// message), the Assistant menus' actions run in it (the panel unfolds,
// the main window comes forward), the first question asks for consent on
// the main window, and Open Draft opens a draft only after draft.list has
// it. A link of an answer is opened only after "Open This Link?" named its
// destination, as a link the daemon did not list.

using System;
using System.Linq;
using Malachi.App.Reader;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Malachi.Core.Daemon;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Malachi.Core.Presentation;
using Malachi.Platform.Windows.Files;
using Microsoft.Extensions.Logging;

namespace Malachi.App.Assistants;

/// <summary>The assistant panel of the main window and its conversation.</summary>
internal sealed class AssistantPanelHost : IDisposable
{
    private readonly MainWindow window;
    private readonly ListController list;

    /// <summary>
    /// The panel's conversation over <paramref name="state"/>, rendered by
    /// <paramref name="view"/>, following <paramref name="integration"/>'s
    /// list; links go through <paramref name="reader"/>'s LinkOpener.
    /// </summary>
    public AssistantPanelHost(AppState state, MainWindow window, Integration integration, ReaderServices reader, AssistantPanel view)
    {
        this.window = window;
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
        var texts = Core.Assistants.Assistant.PanelTexts();
        Controller.Consent = () => state.Alerts.ConfirmAsync(window, texts.ConsentHeading, texts.ConsentBody, texts.Allow, L10n.T("_Cancel"));
        Controller.ResolveContext = Resolve;
        Controller.OpenDraft = r => integration.Actions.OpenSavedDraft(new AccountId(r.AccountId), new DraftId(r.DraftId));
        view.Attach(Controller, href => _ = reader.Links.OpenAsync(new ActivatedLink(href, href), [], window));
    }

    /// <summary>The conversation.</summary>
    public AssistantPanelController Controller { get; }

    /// <summary>Ends a running Claude Code for good (the application quits).</summary>
    public void Dispose() => Controller.Close();

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

    // Completes a folded conversation's members (newest first) when the
    // list still shows it selected; otherwise the context stays what it was.
    private void Resolve(AssistantPanelController.Context c, Action<AssistantSelection> done)
    {
        if (list.SelectedRow is not { Thread: true } row || row.Message.AccountId.Value != c.Selection.AccountId
            || c.Selection.MessageIds.Count == 0 || c.Selection.MessageIds[0] != row.Message.Id.Value)
        {
            done(c.Selection);
            return;
        }
        list.SelectedIds((r, ids) =>
            done(new AssistantSelection(r.Message.AccountId.Value, ids.Reverse().Select(i => i.Value).ToList())));
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
