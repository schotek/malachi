// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.6): the shell's name and icon for a
// type of file, for the previewer's panel (IFileTypeInfo). Asked by the
// extension alone: the name through the type's association
// (AssocQueryString, ASSOCSTR_FRIENDLYDOCNAME), the icon through
// SHGetFileInfo with SHGFI_USEFILEATTRIBUTES, which touches no file, and the
// system image list, which also knows the icons of packaged apps (a type
// opened by the Store's Notepad, say). No file exists, so no icon handler
// reads an attachment. The icon comes at 256 pixels from the jumbo list; a
// type that has only a 48-pixel icon comes back in the corner of that
// square, and then the 48-pixel list answers instead. It is drawn with
// DrawIconEx into a 32-bit top-down DIB, which gives premultiplied BGRA,
// what WriteableBitmap takes. Call on the UI thread (the image lists are
// COM objects of the shell).

using System;
using System.Runtime.InteropServices;
using Malachi.Core.Platform;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.Graphics.Gdi;
using Windows.Win32.UI.Controls;
using Windows.Win32.UI.Shell;
using Windows.Win32.UI.WindowsAndMessaging;

namespace Malachi.Platform.Windows.Files;

/// <summary>The shell's type names and icons, by extension.</summary>
public sealed class ShellFileTypes : IFileTypeInfo
{
    private const int ExtraLarge = 48;
    private const int MaxIconSize = 256;

    /// <inheritdoc/>
    public string? TypeName(string extension)
    {
        ArgumentNullException.ThrowIfNull(extension);
        var name = Query(extension, ASSOCSTR.ASSOCSTR_FRIENDLYDOCNAME);
        return string.IsNullOrWhiteSpace(name) ? null : name.Trim();
    }

    /// <inheritdoc/>
    public FileIcon? Icon(string extension, int size)
    {
        ArgumentNullException.ThrowIfNull(extension);
        ArgumentOutOfRangeException.ThrowIfNegativeOrZero(size);
        if (!Environment.Is64BitProcess || IconIndex(extension) is not { } index)
        {
            return null;
        }
        if (size > ExtraLarge && Render(PInvoke.SHIL_JUMBO, index, Math.Min(size, MaxIconSize)) is { } jumbo
            && !OnlyInCorner(jumbo, ExtraLarge))
        {
            return jumbo;
        }
        return Render(PInvoke.SHIL_EXTRALARGE, index, Math.Min(size, ExtraLarge));
    }

    // The type's icon in the system image list, for a name that is only
    // the extension and no file.
    private static unsafe int? IconIndex(string extension)
    {
        var info = default(ShellFileInfoInterop.FileInfo);
        var result = ShellFileInfoInterop.SHGetFileInfo(
            "x" + extension,
            ShellFileInfoInterop.FileAttributeNormal,
            ref info,
            (uint)sizeof(ShellFileInfoInterop.FileInfo),
            ShellFileInfoInterop.SysIconIndex | ShellFileInfoInterop.UseFileAttributes);
        return result == 0 ? null : info.IconIndex;
    }

    private static string? Query(string extension, ASSOCSTR what)
    {
        const ASSOCF flags = ASSOCF.ASSOCF_INIT_IGNOREUNKNOWN | ASSOCF.ASSOCF_NOTRUNCATE;
        var key = extension.Length == 0 ? "." : extension;
        uint length = 0;
        var hr = PInvoke.AssocQueryString(flags, what, key, null, [], ref length);
        if (hr.Failed || length == 0 || length > 32768)
        {
            return null;
        }
        var buffer = new char[length];
        hr = PInvoke.AssocQueryString(flags, what, key, null, buffer, ref length);
        if (hr.Failed)
        {
            return null;
        }
        var end = Array.IndexOf(buffer, '\0');
        return new string(buffer, 0, end < 0 ? buffer.Length : end);
    }

    // The icon at index of one of the system image lists, drawn at size.
    private static unsafe FileIcon? Render(uint list, int index, int size)
    {
        IImageList images;
        try
        {
            var iid = typeof(IImageList).GUID;
            PInvoke.SHGetImageList((int)list, &iid, out var obj).ThrowOnFailure();
            if (obj is not IImageList l)
            {
                return null;
            }
            images = l;
        }
        catch (COMException)
        {
            return null;
        }
        HICON icon = default;
        try
        {
            images.GetIcon(index, (uint)IMAGE_LIST_DRAW_STYLE.ILD_TRANSPARENT, &icon);
        }
        catch (COMException)
        {
            return null;
        }
        if (icon.IsNull)
        {
            return null;
        }
        try
        {
            return Draw(icon, size);
        }
        finally
        {
            PInvoke.DestroyIcon(icon);
        }
    }

    private static unsafe FileIcon? Draw(HICON icon, int size)
    {
        var dc = PInvoke.CreateCompatibleDC(HDC.Null);
        if (dc.IsNull)
        {
            return null;
        }
        try
        {
            var header = new BITMAPINFO
            {
                bmiHeader = new BITMAPINFOHEADER
                {
                    biSize = (uint)sizeof(BITMAPINFOHEADER),
                    biWidth = size,
                    biHeight = -size,
                    biPlanes = 1,
                    biBitCount = 32,
                    biCompression = 0,
                },
            };
            void* bits;
            var bitmap = PInvoke.CreateDIBSection(dc, &header, DIB_USAGE.DIB_RGB_COLORS, &bits, HANDLE.Null, 0);
            if (bitmap.IsNull)
            {
                return null;
            }
            try
            {
                var old = PInvoke.SelectObject(dc, (HGDIOBJ)bitmap.Value);
                try
                {
                    var length = size * size * 4;
                    new Span<byte>(bits, length).Clear();
                    if (!PInvoke.DrawIconEx(dc, 0, 0, icon, size, size, 0, HBRUSH.Null, DI_FLAGS.DI_NORMAL))
                    {
                        return null;
                    }
                    return new FileIcon(size, size, new ReadOnlySpan<byte>(bits, length).ToArray());
                }
                finally
                {
                    PInvoke.SelectObject(dc, old);
                }
            }
            finally
            {
                PInvoke.DeleteObject((HGDIOBJ)bitmap.Value);
            }
        }
        finally
        {
            PInvoke.DeleteDC(dc);
        }
    }

    // Whether everything visible of the icon lies in its top-left corner ×
    // corner: a small icon the jumbo list did not scale.
    internal static bool OnlyInCorner(FileIcon icon, int corner)
    {
        var pixels = icon.Pixels.Span;
        for (var y = 0; y < icon.Height; y++)
        {
            for (var x = 0; x < icon.Width; x++)
            {
                if ((x >= corner || y >= corner) && pixels[(((y * icon.Width) + x) * 4) + 3] != 0)
                {
                    return false;
                }
            }
        }
        return true;
    }
}
