// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Attachments/AttachmentPreview.swift
// (show: one shared panel, its content swapped) and AttachmentActions.swift
// (preview); GTK: attachments.go previewAttachment with
// ui/internal/preview (Sushi's one window). The decided replacement for
// Quick Look and Sushi (docs/windows-port.md §0, §6.6): a click on a chip
// fetches the part (AttachmentOpener.PreviewAsync; a program's bytes are
// not fetched, it gets its panel) and shows it in the one preview window,
// made on first use, reused while it is open, and gone when closed.
// Nothing changes on screen while the part is fetched; a failure is a
// toast where the chip was.

using System.Threading.Tasks;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;

namespace Malachi.App.Attachments;

/// <summary>The attachment previewer's window.</summary>
public sealed class AttachmentPreview
{
    private readonly AttachmentOpener opener;
    private readonly AppState state;
    private MessageWindows.PreviewWindow? window;

    /// <summary>A previewer over <paramref name="opener"/>, its windows tracked by <paramref name="state"/>.</summary>
    public AttachmentPreview(AttachmentOpener opener, AppState state)
    {
        this.opener = opener;
        this.state = state;
    }

    /// <summary>The window, while one is open (for tests of the UI).</summary>
    public MessageWindows.PreviewWindow? Window => window;

    /// <summary>
    /// Shows <paramref name="attachment"/> of <paramref name="message"/> in
    /// the preview window, opening it or swapping what it shows; the click
    /// was in <paramref name="from"/>.
    /// </summary>
    public async Task ShowAsync(Attachment attachment, MessageSummary message, Window? from)
    {
        if (state.IsStopping || await opener.PreviewAsync(attachment, message, from) is not { } request || state.IsStopping)
        {
            return;
        }
        if (window is null)
        {
            var w = new MessageWindows.PreviewWindow(state, opener);
            w.Closed += (_, _) =>
            {
                if (ReferenceEquals(window, w))
                {
                    window = null;
                }
            };
            window = w;
        }
        window.Show(request);
    }

    /// <summary>Closes the preview window, if open.</summary>
    public void Close() => window?.Close();
}
