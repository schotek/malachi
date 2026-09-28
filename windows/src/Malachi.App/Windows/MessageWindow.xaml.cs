// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Windows/MessageWindowController.swift
// (init, show, rendered, refreshStar, windowWillClose, the actions and
// their validation); GTK: ui/internal/window/message_window.go
// (newMessageWindow: the msg.* group, the star, the outbox banner's retry,
// the reply buttons, the single-key shortcuts; setSeen; show) and
// message_view.go openMessageWindow's close-request. One message in its
// own top-level window, 820×620, not owned by the main window (the user
// asked for independent windows): its commands act on its message through
// the MessageActionRouter and are enabled from the message's live flags;
// Escape and Ctrl+W close it (the window's CommandRouter, also with the
// viewer focused), as do Delete, A, J, U and S what they do in the main
// window. The title follows the subject once the full message is in; the
// body has the first focus, so the selectable subject is not what has it.
// A late reply after the close is dropped (the view's controller is
// closed); the registry forgets the window and closes it, with the windows
// of its attached messages, when the message leaves its folder.

using System;
using Malachi.App.Reader;
using Malachi.App.Shell;
using Malachi.Core;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;

namespace Malachi.App.MessageWindows;

/// <summary>A message in its own window (message_window.blp).</summary>
public sealed partial class MessageWindow : Window, IMessageWindowHandle
{
    private readonly ReaderServices services;
    private readonly MessageWindowKey key;
    private readonly IDisposable frame;
    private MessageSummary summary;
    private bool focused;

    /// <summary>The window of message <paramref name="s"/>; the registry shows it.</summary>
    public MessageWindow(ReaderServices services, MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(services);
        ArgumentNullException.ThrowIfNull(s);
        this.services = services;
        summary = s;
        key = new MessageWindowKey.Message(s.Id);
        InitializeComponent();
        SetTitle(L10n.T("Message"));
        frame = WindowFrame.Apply(this, WindowTitleBar, SolidBackground);
        Tracked = services.State.Windows.Track(this, WindowKind.Message, Root, ToastsHost);
        View = new MessageView(ReaderMode.Window, services, Tracked.Commands) { HostWindow = this };
        ViewHost.Content = View;
        WireCommands();
        View.Reader.Rendered += (_, r) => Rendered(r);
        Activated += (_, e) =>
        {
            if (!focused && e.WindowActivationState != WindowActivationState.Deactivated)
            {
                focused = true;
                View.FocusBody();
            }
        };
        Closed += (_, _) =>
        {
            services.Windows.Closed(key, this);
            // The only timer that would outlive the window, and the viewer.
            View.Close();
            frame.Dispose();
        };
    }

    /// <summary>The window as the shell tracks it (its commands and keys).</summary>
    public TrackedWindow Tracked { get; }

    /// <summary>The message view.</summary>
    public MessageView View { get; }

    /// <inheritdoc/>
    public ReaderController Reader => View.Reader;

    /// <summary>The message the window shows.</summary>
    public MessageId Id => summary.Id;

    /// <inheritdoc/>
    public void Present() => WindowPresenter.Present(this);

    /// <inheritdoc/>
    public void RefreshActions() => Tracked.Commands.Flags = services.Router.FlagsFor(View.Reader.Current ?? summary);

    // The msg.* group: the command row, its menu and the keys act on this
    // window's message; trash and junk ask over this window.
    private void WireCommands()
    {
        var c = Tracked.Commands;
        var router = services.Router;
        c.Reply.Handler = () => router.Reply(Id);
        c.ReplyAll.Handler = () => router.ReplyAll(Id);
        c.Forward.Handler = () => router.Forward(Id);
        c.Trash.Handler = () => router.Trash(Id, this);
        c.Junk.Handler = () => router.Junk(Id, this);
        c.Archive.Handler = () => router.Archive(Id);
        c.ToggleFlag.Handler = () => router.ToggleFlag(Id);
        c.MarkRead.Handler = () => router.MarkRead(Id);
        c.MarkUnread.Handler = () => router.MarkUnread(Id);
        c.LoadImages.Handler = () => router.LoadImages(Id);
        c.TrustSender.Handler = () => router.TrustSender(Id);
        RefreshActions();
    }

    // The title follows the full message's subject, the commands the flags
    // (MessageWindowController.rendered).
    private void Rendered(ReaderRender r)
    {
        summary = r.Summary;
        SetTitle(LoadedMessageText.SubjectText(r.Loaded?.Msg?.Summary.Subject ?? r.Summary.Subject));
        RefreshActions();
    }

    private void SetTitle(string subject)
    {
        // The subject is shown as plain text by both.
        Title = subject;
        WindowTitleBar.Title = subject;
    }
}
