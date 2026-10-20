// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/QuickReplyHostAdapter.swift
// (QuickReplyHostAdapter); GTK: ui/internal/window/quick_reply.go
// (quickReplyHost).
//
// IQuickReplyHost over the main window: the daemon's client for Send's
// calls, the window's dispatcher for their outcome, and what the window
// knows to reply as its compose window would — the attribution line, the
// download before a reply and the compose window itself, all through the
// window's message actions (MessageActionRouter, whose ActionsController
// does them for the compose window too). The texts are the compose window's
// ("Message queued for sending" or a comment's, the failed send's, as
// ComposeDraftController says them), so this file adds no msgid. A failure
// is logged by its type only (docs/windows-port.md §3.1: no mail content in
// logs; the daemon's message may name a recipient).

using System;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;

namespace Malachi.App.Reader.Conversation;

/// <summary>The quick-reply bar's host over the main window and its reader services.</summary>
internal sealed partial class QuickReplyHostAdapter : IQuickReplyHost
{
    private readonly ReaderServices services;
    private readonly Func<Window?> window;
    private readonly DispatcherQueue dispatcher;
    private readonly ILogger logger;

    /// <summary>
    /// The host over <paramref name="services"/>, whose compose windows and
    /// toasts go on <paramref name="window"/>; made on the UI thread.
    /// </summary>
    public QuickReplyHostAdapter(ReaderServices services, Func<Window?> window)
    {
        ArgumentNullException.ThrowIfNull(services);
        ArgumentNullException.ThrowIfNull(window);
        this.services = services;
        this.window = window;
        dispatcher = DispatcherQueue.GetForCurrentThread();
        logger = services.State.Logs.CreateLogger<QuickReplyHostAdapter>();
    }

    /// <inheritdoc/>
    public Task<TResult> CallAsync<TParams, TResult>(RpcMethod<TParams, TResult> method, TParams parameters, TimeSpan timeout) =>
        services.State.Client.CallAsync(method, parameters, timeout);

    /// <inheritdoc/>
    public void Post(Action action)
    {
        ArgumentNullException.ThrowIfNull(action);
        dispatcher.TryEnqueue(() => action());
    }

    /// <inheritdoc/>
    public string Attribution(QuickReplyMode mode, MessageId id) => services.Router.QuickReplyAttribution(mode.ComposeKind, id);

    /// <inheritdoc/>
    public void BeforeCreate(MessageId id, Action done) => services.Router.PrepareQuickReply(id, done);

    /// <inheritdoc/>
    public void OpenCompose(QuickReplyMode mode, MessageId id, string ownHtml, Action opened) =>
        services.Router.OpenCompose(mode.ComposeKind, id, ownHtml, window(), opened);

    /// <summary>The compose window's confirmation, on the main window (ComposeController.ReportSent).</summary>
    public void Sent(bool comment) =>
        services.ToastIn(window(), comment ? Jira.CommentQueued() : L10n.T("Message queued for sending"));

    /// <summary>The compose window's toast for a send that did not go through; the text stays in the bar.</summary>
    public void Failed(Exception failure)
    {
        ArgumentNullException.ThrowIfNull(failure);
        LogSendFailed(logger, failure.GetType().Name);
        services.ToastIn(window(), RpcErrorText.Text(L10n.T("Sending"), failure));
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "quick reply: sending failed: {Kind}")]
    private static partial void LogSendFailed(ILogger logger, string kind);
}
