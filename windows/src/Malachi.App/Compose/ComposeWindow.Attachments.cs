// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/ComposeAttachments.swift
// (attachFiles, insertImage); GTK: ui/internal/compose/compose.go
// (attachFiles, insertImage, the chips' remove). The compose window's side
// of its attachments: the pickers (ComposeFileDialog, owned by this window),
// the chips, and the draft controller's part (an attachment added or
// removed is an edit; a failed import puts the status line back). What is
// imported, refused, registered for cid: and removed is Core's
// ComposeAttachmentsController.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.I18n;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Windows.ApplicationModel.DataTransfer;
using Windows.Storage;

namespace Malachi.App.Compose;

/// <summary>The attachments of a compose window.</summary>
public sealed partial class ComposeWindow
{
    // compose.go insertImage's filter: "Images", *.png, *.jpg, *.jpeg, *.gif,
    // *.webp (the pattern list is the shell's syntax, not text).
    private const string ImagePatterns = "*.png;*.jpg;*.jpeg;*.gif;*.webp";

    private void WireAttachments()
    {
        attachments.Changed += (_, _) => Chips.Show(attachments.Chips);
        attachments.Edited += (_, _) => draft.MarkDirty();
        attachments.StatusChanged += (_, text) => SetStatus(text);
        attachments.ToastRequested += (_, text) => Toast(text);
        attachments.ImportFailed += (_, _) => draft.RefreshStatus();
        Chips.RemoveRequested += (_, id) => attachments.Remove(id);
    }

    private void OnAttachClick(object sender, RoutedEventArgs e) => _ = AttachFilesAsync();

    // editor.OnDropFiles through XAML: WinUI's WebView2 hands an OLE drop of
    // files to its host rather than to the page (measured), so the editor's
    // slot takes it, as GTK's DropTarget on the editor takes file lists.
    // What the page takes itself arrives as ComposeWebView.FilesDropped.
    private void OnEditorDragOver(object sender, DragEventArgs e)
    {
        if (e.DataView.Contains(StandardDataFormats.StorageItems))
        {
            e.AcceptedOperation = DataPackageOperation.Copy;
            e.Handled = true;
        }
    }

    private void OnEditorDrop(object sender, DragEventArgs e)
    {
        if (!e.DataView.Contains(StandardDataFormats.StorageItems))
        {
            return;
        }
        e.Handled = true;
        _ = AttachDroppedAsync(e.DataView, e.GetDeferral());
    }

    private async Task AttachDroppedAsync(DataPackageView data, DragOperationDeferral deferral)
    {
        IReadOnlyList<IStorageItem> items;
        try
        {
            items = await data.GetStorageItemsAsync();
        }
        catch (Exception e) when (e is System.Runtime.InteropServices.COMException or UnauthorizedAccessException)
        {
            LogDropFailed(logger, e);
            return;
        }
        finally
        {
            deferral.Complete();
        }
        if (closing)
        {
            return;
        }
        // An item the shell cannot name by a path (a phone, a virtual
        // folder) has none: "Only local files can be attached".
        attachments.AttachFiles(items.Select(i => string.IsNullOrEmpty(i.Path) ? null : i.Path));
    }

    private void OnInsertImageClick(object sender, RoutedEventArgs e) => _ = InsertImageAsync();

    // compose.attach: files to attach, as many as chosen.
    private async Task AttachFilesAsync()
    {
        var picked = await PickAsync(L10n.T("Attach Files"), multiple: true, filter: null);
        if (picked is null || closing)
        {
            return; // cancelled
        }
        attachments.AttachFiles(picked);
    }

    // compose.insert-image: one picture, imported inline and inserted at the
    // caret as cid:<contentId>.
    private async Task InsertImageAsync()
    {
        var picked = await PickAsync(L10n.T("Insert Image"), multiple: false, filter: (L10n.T("Images"), ImagePatterns));
        if (picked is not { Count: > 0 } || closing)
        {
            return;
        }
        attachments.InsertImage(picked[0], url =>
        {
            editor.Exec("insertImage", url);
            FocusEditor();
        });
    }

    private async Task<IReadOnlyList<string?>?> PickAsync(string title, bool multiple, (string Name, string Patterns)? filter)
    {
        try
        {
            return await ComposeFileDialog.OpenAsync(Handle, title, multiple, filter);
        }
        catch (Exception e) when (e is System.Runtime.InteropServices.COMException or InvalidCastException or UnauthorizedAccessException)
        {
            LogPickerFailed(logger, e);
            return null;
        }
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "the files dropped on a compose window could not be read")]
    private static partial void LogDropFailed(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Warning, Message = "the file dialog of a compose window failed")]
    private static partial void LogPickerFailed(ILogger logger, Exception error);
}
