// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the panels of macos/Sources/MalachiMail/Attachments/
// AttachmentActions.swift (saveAs: NSSavePanel, saveAll: NSOpenPanel for
// a folder); GTK: ui/internal/window/attachments.go (saveAttachment,
// saveAllAttachments: gtk.FileDialog Save and SelectFolder). The app
// implements it with the Windows App SDK's pickers, owned by the window.

using System.Threading.Tasks;

namespace Malachi.Core.Presentation;

/// <summary>Where the user wants attachments saved.</summary>
public interface IAttachmentPickers
{
    /// <summary>
    /// Asks for a file to save to, over <paramref name="window"/>, offering
    /// <paramref name="suggestedName"/>; the dialog confirms an overwrite
    /// itself. Null when the user dismissed it.
    /// </summary>
    Task<string?> PickSaveFileAsync(object? window, string title, string suggestedName);

    /// <summary>Asks for a folder, over <paramref name="window"/>; null when the user dismissed it.</summary>
    Task<string?> PickFolderAsync(object? window, string title);
}
