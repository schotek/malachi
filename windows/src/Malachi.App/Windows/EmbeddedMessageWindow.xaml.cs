// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Windows/EmbeddedWindowController.swift
// (init, show, windowWillClose); GTK: ui/internal/window/embedded.go
// (newEmbeddedWindow, show, openEmbeddedWindow's close-request). An
// attached message, rendered read-only by the daemon (message.embedded)
// from the part's bytes and only when asked, in its own window: its subject
// as the title, the containing message's as the subtitle ("Attached to
// “%s”"), the view in its embedded mode. Its pictures arrive inlined, so
// the viewer needs no part server; its own attachments have no part numbers,
// so their chips only name them; Load Images renders the part again with
// remote images allowed for that one call (ReaderController). No actions:
// the message has no id of its own. Escape and Ctrl+W close it.

using System;
using Malachi.App.Reader;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Text;
using Microsoft.UI.Xaml;

namespace Malachi.App.MessageWindows;

/// <summary>An attached message in its own window (embedded_window.blp).</summary>
public sealed partial class EmbeddedMessageWindow : Window, IMessageWindowHandle
{
    private readonly IDisposable frame;
    private bool focused;

    /// <summary>
    /// The window of the attached message <paramref name="part"/> of
    /// <paramref name="containing"/>, showing <paramref name="result"/>; the
    /// registry shows it.
    /// </summary>
    public EmbeddedMessageWindow(ReaderServices services, MessageSummary containing, string part, MessageEmbeddedResult result)
    {
        ArgumentNullException.ThrowIfNull(services);
        ArgumentNullException.ThrowIfNull(containing);
        ArgumentNullException.ThrowIfNull(part);
        ArgumentNullException.ThrowIfNull(result);
        var key = new MessageWindowKey.Embedded(containing.Id, part);
        InitializeComponent();
        Title = L10n.T("Attached Message");
        WindowTitleBar.Title = Title;
        // The subject isolated inside the sentence (DisplayText).
        // TRANSLATORS: window subtitle; %s is the subject of the message this one was attached to.
        WindowTitleBar.Subtitle = L10n.T("Attached to “%s”", DisplayText.Isolate(LoadedMessageText.SubjectText(containing.Subject)));
        frame = WindowFrame.Apply(this, WindowTitleBar, SolidBackground);
        Tracked = services.State.Windows.Track(this, WindowKind.Embedded, Root, ToastsHost);
        View = new MessageView(ReaderMode.Embedded, services, commands: null) { HostWindow = this };
        ViewHost.Content = View;
        // The attached message's subject as the title (embedded.go show).
        View.Reader.Rendered += (_, r) =>
        {
            var subject = LoadedMessageText.SubjectText(r.Loaded?.Msg?.Summary.Subject ?? r.Summary.Subject);
            Title = subject;
            WindowTitleBar.Title = subject;
        };
        View.Reader.ShowEmbedded(containing, part, result);
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
            View.Close();
            frame.Dispose();
        };
    }

    /// <summary>The window as the shell tracks it.</summary>
    public TrackedWindow Tracked { get; }

    /// <summary>The message view.</summary>
    public MessageView View { get; }

    /// <inheritdoc/>
    public ReaderController Reader => View.Reader;

    /// <inheritdoc/>
    public void Present() => WindowPresenter.Present(this);

    /// <inheritdoc/>
    public void RefreshActions()
    {
        // No actions: the message has no id of its own.
    }
}
