// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.6), the window around the
// previewer: GTK hands a chip's file to Sushi (ui/internal/preview,
// attachments.go previewAttachment), macOS to the Quick Look panel
// (Attachments/AttachmentPreview.swift); both are one window whose content
// is swapped, titled with the file, with a way to open it. This is that
// window: the PreviewWebView in the app's environment (its own hardened
// view: script off, no network, nothing written to disk), titled with the
// attachment's name, with Open (disabled for a program, which only gets its
// panel) and Save As…, which act on the attachment through the
// AttachmentOpener as the chip's menu does. Escape and Ctrl+W close it (the
// window's CommandRouter), and closing ends its view; the next click on a
// chip makes a new one.

using System;
using Malachi.App.Shell;
using Malachi.App.WebViews;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Platform.Windows.Attachments;
using Malachi.Platform.Windows.Files;
using Microsoft.UI.Xaml;

namespace Malachi.App.MessageWindows;

/// <summary>The attachment previewer's window.</summary>
public sealed partial class PreviewWindow : Window
{
    // A little smaller than a message window: a picture or a page.
    private const int DefaultWidth = 800;
    private const int DefaultHeight = 600;

    private readonly AttachmentOpener opener;
    private readonly IDisposable frame;
    private PreviewRequest? shown;

    /// <summary>An empty previewer; <see cref="Show"/> fills and presents it.</summary>
    public PreviewWindow(AppState state, AttachmentOpener opener)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(opener);
        this.opener = opener;
        InitializeComponent();
        frame = WindowFrame.Apply(this, WindowTitleBar, SolidBackground, DefaultWidth, DefaultHeight);
        Tracked = state.Windows.Track(this, WindowKind.Other, Root, ToastsHost);
        View = new PreviewWebView
        {
            FileTypes = new ShellFileTypes(),
            TypePolicy = new FileTypePolicy(),
        };
        PreviewArea.Children.Insert(0, View);
        Closed += (_, _) =>
        {
            View.Close();
            frame.Dispose();
        };
    }

    /// <summary>The window as the shell tracks it.</summary>
    public TrackedWindow Tracked { get; }

    /// <summary>The previewer inside.</summary>
    public PreviewWebView View { get; }

    /// <summary>What the window shows now.</summary>
    public PreviewRequest? Shown => shown;

    /// <summary>Shows <paramref name="request"/> in place of what was shown, and brings the window up.</summary>
    public void Show(PreviewRequest request)
    {
        ArgumentNullException.ThrowIfNull(request);
        shown = request;
        // The name is server data: plain text in both places.
        var name = AttachmentChips.ChipName(request.Attachment);
        Title = name;
        WindowTitleBar.Title = name;
        OpenButton.IsEnabled = request.CanOpen;
        if (request.Data is { } data)
        {
            View.Show(request.Attachment, request.ContentType, data);
        }
        else
        {
            View.ShowPanel(request.Attachment, request.Size);
        }
        WindowPresenter.Present(this);
    }

    private void OnOpen(object sender, RoutedEventArgs e)
    {
        if (shown is { } r && r.CanOpen)
        {
            // Fetched for the preview already: the part the daemon served,
            // downloaded again only should the daemon have let go of it.
            _ = opener.OpenAsync(r.Attachment, r.Message, remote: false, this);
        }
    }

    private void OnSave(object sender, RoutedEventArgs e)
    {
        if (shown is { } r)
        {
            _ = opener.SaveAsAsync(r.Attachment, r.Message, remote: false, this);
        }
    }
}
