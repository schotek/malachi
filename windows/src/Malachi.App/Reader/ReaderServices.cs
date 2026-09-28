// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: what every message view needs of the application, the
// counterpart of what macos/Sources/MalachiMail/Windows/MessageWindows.swift
// hands its views (the state, the cache, the delegate, the embedded-window
// opener) and of the *Window GTK's messageView keeps (fetchPart, openLink,
// compose, toasts). Made once by ReaderHub.

using System;
using Malachi.App.Attachments;
using Malachi.App.Shell;
using Malachi.Core.Controllers;
using Malachi.Core.Platform;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;

namespace Malachi.App.Reader;

/// <summary>The application's services, as the message views use them.</summary>
public sealed class ReaderServices
{
    /// <summary>The application.</summary>
    public required AppState State { get; init; }

    /// <summary>The loaded messages.</summary>
    public required MessageCache Cache { get; init; }

    /// <summary>The per-message actions.</summary>
    public required MessageActionRouter Router { get; init; }

    /// <summary>What a link activated in a message does.</summary>
    public required LinkOpener Links { get; init; }

    /// <summary>Opening and saving attachments.</summary>
    public required AttachmentOpener Attachments { get; init; }

    /// <summary>The attachment previewer.</summary>
    public required AttachmentPreview Preview { get; init; }

    /// <summary>The message windows and the fan-out to every view.</summary>
    public required MessageWindowRegistry Windows { get; init; }

    /// <summary>What the platform would run (a chip's Open).</summary>
    public required IFileTypePolicy FileTypes { get; init; }

    /// <summary>The icons of the attachment chips.</summary>
    public required ChipIcons Icons { get; init; }

    /// <summary>
    /// Shows <paramref name="text"/> in the toast overlay of
    /// <paramref name="window"/> when it has one, otherwise wherever the
    /// application's toasts go (macOS <c>windowToast</c>).
    /// </summary>
    public void ToastIn(object? window, string text)
    {
        if (window is Window w && State.Windows.Find(w)?.Toasts is { } own)
        {
            own.Show(text);
            return;
        }
        State.Toasts.Show(text);
    }

    /// <summary>The native handle of <paramref name="window"/> for the shell's dialogs, 0 for none.</summary>
    public static nint Owner(object? window) => window is Window w ? WindowPresenter.Handle(w) : 0;
}
