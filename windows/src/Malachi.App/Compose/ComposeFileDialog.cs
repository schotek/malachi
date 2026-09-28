// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the file dialogs of ui/internal/compose/compose.go (attachFiles:
// gtk.FileDialog OpenMultiple titled "Attach Files"; insertImage: Open
// titled "Insert Image" with the filter "Images" of *.png, *.jpg, *.jpeg,
// *.gif and *.webp) and of macos/Sources/MalachiMail/Compose/
// ComposeAttachments.swift (the NSOpenPanels). Windows-only file: the
// shell's IFileOpenDialog, owned by the compose window, which the WinRT
// FileOpenPicker cannot do as GTK does (it has no title and no named
// filter). It runs on an STA thread of its own, as the Open With dialog of
// Malachi.Platform.Windows does, so the UI thread goes on while it is up.
// Items are not forced to the file system (no FOS_FORCEFILESYSTEM): an item
// without a file-system path (a phone, a virtual folder) comes back as null,
// which the compose window refuses with GTK's "Only local … can be …".

using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Threading;
using System.Threading.Tasks;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.UI.Shell;
using Windows.Win32.UI.Shell.Common;

namespace Malachi.App.Compose;

/// <summary>The open dialogs of a compose window.</summary>
internal static class ComposeFileDialog
{
    // HRESULT_FROM_WIN32(ERROR_CANCELLED): the user closed the dialog.
    private const int Cancelled = unchecked((int)0x800704C7);

    /// <summary>
    /// Shows an open dialog over <paramref name="owner"/> titled
    /// <paramref name="title"/>; with <paramref name="filter"/> (a name and
    /// its patterns, <c>*.png;*.jpg</c>) only those files are listed. The
    /// chosen items' paths, null for an item without one; null when the
    /// dialog was cancelled.
    /// </summary>
    public static Task<IReadOnlyList<string?>?> OpenAsync(nint owner, string title, bool multiple, (string Name, string Patterns)? filter = null)
    {
        ArgumentNullException.ThrowIfNull(title);
        var completion = new TaskCompletionSource<IReadOnlyList<string?>?>(TaskCreationOptions.RunContinuationsAsynchronously);
        var thread = new Thread(() =>
        {
            try
            {
                completion.TrySetResult(Open((HWND)owner, title, multiple, filter));
            }
            catch (Exception e)
            {
                completion.TrySetException(e);
            }
        })
        {
            IsBackground = true,
            Name = "Malachi file dialog (STA)",
        };
        thread.SetApartmentState(ApartmentState.STA);
        thread.Start();
        return completion.Task;
    }

    private static unsafe List<string?>? Open(HWND owner, string title, bool multiple, (string Name, string Patterns)? filter)
    {
        var dialog = (IFileOpenDialog)new FileOpenDialog();
        try
        {
            dialog.GetOptions(out var options);
            options |= FILEOPENDIALOGOPTIONS.FOS_FILEMUSTEXIST | FILEOPENDIALOGOPTIONS.FOS_PATHMUSTEXIST
                | FILEOPENDIALOGOPTIONS.FOS_NOCHANGEDIR | FILEOPENDIALOGOPTIONS.FOS_DONTADDTORECENT;
            if (multiple)
            {
                options |= FILEOPENDIALOGOPTIONS.FOS_ALLOWMULTISELECT;
            }
            dialog.SetOptions(options);
            dialog.SetTitle(title);
            if (filter is { } f)
            {
                fixed (char* name = f.Name)
                fixed (char* patterns = f.Patterns)
                {
                    var spec = new COMDLG_FILTERSPEC { pszName = name, pszSpec = patterns };
                    dialog.SetFileTypes(1, &spec);
                }
            }
            try
            {
                dialog.Show(owner);
            }
            catch (COMException e) when (e.HResult == Cancelled)
            {
                return null;
            }
            dialog.GetResults(out var items);
            try
            {
                items.GetCount(out var count);
                var paths = new List<string?>((int)count);
                for (uint i = 0; i < count; i++)
                {
                    items.GetItemAt(i, out var item);
                    try
                    {
                        paths.Add(PathOf(item));
                    }
                    finally
                    {
                        Marshal.FinalReleaseComObject(item);
                    }
                }
                return paths;
            }
            finally
            {
                Marshal.FinalReleaseComObject(items);
            }
        }
        finally
        {
            Marshal.FinalReleaseComObject(dialog);
        }
    }

    // The item's file-system path; null when it has none.
    private static unsafe string? PathOf(IShellItem item)
    {
        PWSTR path = default;
        try
        {
            item.GetDisplayName(SIGDN.SIGDN_FILESYSPATH, &path);
            return path.Value is null ? null : new string(path.Value);
        }
        catch (COMException)
        {
            return null;
        }
        catch (ArgumentException)
        {
            return null;
        }
        finally
        {
            if (path.Value is not null)
            {
                PInvoke.CoTaskMemFree(path.Value);
            }
        }
    }
}
