// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.6): what the previewer shows for an
// attachment it cannot render (PreviewKind.None), as Sushi shows a file it
// has no viewer for: the file's icon, its name, its size and its type. The
// name is the chip's (attachments.go chipName), plain text; the size is the
// bytes message.part returned when they are known, the listed size
// otherwise (Format.FormatSize, as the chips); the type is what Windows
// calls files of that extension, else the claimed media type
// (attachments.go chipIconType). The icon is Windows' for the extension,
// looked up by the extension alone, so no handler ever sees the file.

using System;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Malachi.Core.Text;

namespace Malachi.Core.Presentation;

/// <summary>The previewer's panel for an attachment it does not render.</summary>
/// <param name="Name">The attachment's name (<see cref="AttachmentChips.ChipName"/>).</param>
/// <param name="SizeText">Its size (<see cref="Format.FormatSize"/>).</param>
/// <param name="TypeText">What kind of file it is.</param>
/// <param name="IconExtension">The extension to ask Windows for an icon by, with its dot; "" for the generic file icon.</param>
public sealed record PreviewPanel(string Name, string SizeText, string TypeText, string IconExtension)
{
    private const int MaxIconExtension = 16;

    /// <summary>
    /// The panel of <paramref name="attachment"/>; <paramref name="size"/> is
    /// the length of the bytes fetched (null before they are), and
    /// <paramref name="typeName"/> what Windows calls files of
    /// <see cref="IconExtensionOf"/> (null or empty when it has no name).
    /// </summary>
    public static PreviewPanel For(Attachment attachment, long? size, string? typeName)
    {
        ArgumentNullException.ThrowIfNull(attachment);
        var type = string.IsNullOrWhiteSpace(typeName) ? AttachmentChips.ChipIconType(attachment) : typeName.Trim();
        return new PreviewPanel(
            AttachmentChips.ChipName(attachment),
            Format.FormatSize(size ?? attachment.Size),
            type,
            IconExtensionOf(attachment.Filename));
    }

    /// <summary>
    /// The extension of <paramref name="fileName"/> as Windows would store it
    /// (<see cref="WindowsFileNames.Sanitize(string)"/>), with its dot, when it
    /// is a short run of ASCII letters and digits; "" otherwise, which asks
    /// for the generic icon: nothing of a hostile name reaches the shell's
    /// lookup but a plain extension.
    /// </summary>
    public static string IconExtensionOf(string? fileName)
    {
        var name = WindowsFileNames.Sanitize(fileName ?? "");
        var dot = name.LastIndexOf('.');
        if (dot < 0 || dot == name.Length - 1 || name.Length - dot - 1 > MaxIconExtension)
        {
            return "";
        }
        var extension = name[(dot + 1)..];
        foreach (var c in extension)
        {
            if (!char.IsAsciiLetterOrDigit(c))
            {
                return "";
            }
        }
        return "." + extension.ToLowerInvariant();
    }
}
