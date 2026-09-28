// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/AttachmentChipView.swift
// (icon: the system's icon for the claimed or guessed type, 14 px); GTK:
// attachments.go buildChip (gio.ContentTypeGetSymbolicIcon of
// chipIconType). On Windows the chip shows the shell's icon for the
// extension, as Explorer does, looked up by the extension alone
// (ShellFileTypes: no file exists and no icon handler reads an attachment)
// and cached per extension; a name without a plain extension gets the
// generic attachment glyph. Call on the UI thread.

using System.Collections.Generic;
using System.IO;
using System.Runtime.InteropServices.WindowsRuntime;
using Malachi.App.Resources;
using Malachi.Core.Platform;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Media.Imaging;

namespace Malachi.App.Attachments;

/// <summary>The icons of the attachment chips.</summary>
public sealed class ChipIcons
{
    // Rendered at twice the chip's size, so a scaled display stays sharp.
    private const int Pixels = 32;

    private readonly IFileTypeInfo? types;
    private readonly Dictionary<string, ImageSource?> cache = [];

    /// <summary>Icons from <paramref name="types"/>; glyphs only without it.</summary>
    public ChipIcons(IFileTypeInfo? types)
    {
        this.types = types;
    }

    /// <summary>The chip's icon for a file named <paramref name="fileName"/>, at <paramref name="size"/> effective pixels.</summary>
    public FrameworkElement For(string? fileName, double size = Icons.Small)
    {
        var extension = PreviewPanel.IconExtensionOf(fileName);
        FrameworkElement icon = extension.Length > 0 && Source(extension) is { } source
            ? new Image { Source = source, Width = size, Height = size, Stretch = Stretch.Uniform }
            : Icons.Create("mail-attachment", size);
        // Decoration: Narrator reads the chip's name.
        AutomationProperties.SetAccessibilityView(icon, AccessibilityView.Raw);
        return icon;
    }

    private ImageSource? Source(string extension)
    {
        if (cache.TryGetValue(extension, out var known))
        {
            return known;
        }
        ImageSource? source = null;
        if (types?.Icon(extension, Pixels) is { } icon && icon.Width > 0 && icon.Height > 0
            && icon.Pixels.Length == icon.Width * icon.Height * 4)
        {
            var bitmap = new WriteableBitmap(icon.Width, icon.Height);
            using (var pixels = bitmap.PixelBuffer.AsStream())
            {
                pixels.Write(icon.Pixels.Span);
            }
            bitmap.Invalidate();
            source = bitmap;
        }
        cache[extension] = source;
        return source;
    }
}
