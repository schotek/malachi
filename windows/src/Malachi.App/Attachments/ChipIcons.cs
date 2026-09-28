// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/AttachmentChipView.swift
// (icon: the system's icon for the claimed or guessed type, 14 px); GTK:
// attachments.go buildChip (gio.ContentTypeGetSymbolicIcon of
// chipIconType). On Windows the chip shows the shell's icon for the
// extension, as Explorer does, looked up by the extension alone
// (ShellFileTypes: no file exists and no icon handler reads an attachment)
// and remembered per extension; a name without a plain extension gets the
// generic attachment glyph. The lookup is synchronous (the shell's image
// lists want the UI thread), so one set of chips looks up a limited number
// of extensions it has not seen and gives the rest the glyph (Core's
// IconLookups): a message listing hundreds of parts with as many
// extensions does not stall its first render. Call on the UI thread.

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

    private readonly IconLookups<ImageSource> lookups;

    /// <summary>Icons from <paramref name="types"/>; glyphs only without it.</summary>
    public ChipIcons(IFileTypeInfo? types)
    {
        lookups = new IconLookups<ImageSource>(extension => Source(types, extension));
    }

    /// <summary>The lookups of one set of chips, which <see cref="For"/> draws on.</summary>
    public IconLookups<ImageSource>.Batch StartBatch() => lookups.StartBatch();

    /// <summary>The chip's icon for a file named <paramref name="fileName"/>, at <paramref name="size"/> effective pixels.</summary>
    public static FrameworkElement For(IconLookups<ImageSource>.Batch batch, string? fileName, double size = Icons.Small)
    {
        FrameworkElement icon = batch.For(PreviewPanel.IconExtensionOf(fileName)) is { } source
            ? new Image { Source = source, Width = size, Height = size, Stretch = Stretch.Uniform }
            : Icons.Create("mail-attachment", size);
        // Decoration: Narrator reads the chip's name.
        AutomationProperties.SetAccessibilityView(icon, AccessibilityView.Raw);
        return icon;
    }

    private static WriteableBitmap? Source(IFileTypeInfo? types, string extension)
    {
        if (types?.Icon(extension, Pixels) is not { } icon || icon.Width <= 0 || icon.Height <= 0
            || icon.Pixels.Length != icon.Width * icon.Height * 4)
        {
            return null;
        }
        var bitmap = new WriteableBitmap(icon.Width, icon.Height);
        using (var pixels = bitmap.PixelBuffer.AsStream())
        {
            pixels.Write(icon.Pixels.Span);
        }
        bitmap.Invalidate();
        return bitmap;
    }
}
