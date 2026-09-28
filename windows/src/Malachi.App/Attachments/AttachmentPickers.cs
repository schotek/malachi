// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the panels of macos/Sources/MalachiMail/Attachments/
// AttachmentActions.swift (saveAs: NSSavePanel with the attachment's name,
// saveAll: NSOpenPanel choosing a folder); GTK: attachments.go
// saveAttachment (gtk.FileDialog Save, title "Save Attachment", the initial
// name) and saveAllAttachments (SelectFolder, "Save Attachments"). The
// Windows App SDK's pickers, owned by the window where the click was (their
// WindowId), work in an unpackaged app and hand back paths. The save
// dialog asks before overwriting, as GTK's and macOS's do; its file type is
// the attachment's own extension, so a name the user types keeps it.

using System;
using System.Threading.Tasks;
using Malachi.Core.Presentation;
using Malachi.Platform.Windows.Files;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Microsoft.Windows.Storage.Pickers;

namespace Malachi.App.Attachments;

/// <summary>The save and folder pickers of the attachment actions.</summary>
public sealed partial class AttachmentPickers : IAttachmentPickers
{
    private readonly Func<Window?> fallbackWindow;
    private readonly ILogger logger;

    /// <param name="fallbackWindow">The window a picker goes on when the click had none (the main window).</param>
    /// <param name="logger">Failures, by kind.</param>
    public AttachmentPickers(Func<Window?> fallbackWindow, ILogger logger)
    {
        this.fallbackWindow = fallbackWindow;
        this.logger = logger;
    }

    /// <inheritdoc/>
    public async Task<string?> PickSaveFileAsync(object? window, string title, string suggestedName)
    {
        if (Owner(window) is not { } owner)
        {
            return null;
        }
        try
        {
            var picker = new FileSavePicker(owner.AppWindow.Id)
            {
                Title = title,
                SuggestedFileName = suggestedName,
                SuggestedStartLocation = PickerLocationId.Downloads,
            };
            var extension = PreviewPanel.IconExtensionOf(suggestedName);
            if (extension.Length > 0)
            {
                var type = new ShellFileTypes().TypeName(extension) ?? extension;
                picker.FileTypeChoices.Add(type, [extension]);
                picker.DefaultFileExtension = extension;
            }
            var result = await picker.PickSaveFileAsync();
            return string.IsNullOrEmpty(result?.Path) ? null : result.Path;
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            LogPickerFailed(logger, e.GetType().Name, e.HResult);
            return null;
        }
    }

    /// <inheritdoc/>
    public async Task<string?> PickFolderAsync(object? window, string title)
    {
        if (Owner(window) is not { } owner)
        {
            return null;
        }
        try
        {
            var picker = new FolderPicker(owner.AppWindow.Id)
            {
                Title = title,
                SuggestedStartLocation = PickerLocationId.Downloads,
            };
            var result = await picker.PickSingleFolderAsync();
            return string.IsNullOrEmpty(result?.Path) ? null : result.Path;
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            LogPickerFailed(logger, e.GetType().Name, e.HResult);
            return null;
        }
    }

    private Window? Owner(object? window) => window as Window ?? fallbackWindow();

    [LoggerMessage(Level = LogLevel.Warning, Message = "a file picker failed: {Kind} 0x{HResult:X8}")]
    private static partial void LogPickerFailed(ILogger logger, string kind, int hResult);
}
