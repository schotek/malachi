// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: what IAttachmentServices.Save came to. MarkOfTheWeb tells
// apart Attachment Services that could not be used at all (the zone is
// written directly and the file may open, as where the class is missing)
// from a Save that ran and failed (the file may not have been scanned, so
// a file for opening is not opened).

namespace Malachi.Platform.Windows.Attachments;

/// <summary>
/// Whether <c>IAttachmentExecute</c> could be created and told about the
/// file (<paramref name="Ran"/>), and the HRESULT: of that failure when it
/// could not, of <c>Save</c> when it ran, 0 when <c>Save</c> succeeded.
/// </summary>
internal readonly record struct AttachmentSaveResult(bool Ran, int HResult)
{
    /// <summary>Save ran and succeeded.</summary>
    public static AttachmentSaveResult Saved => new(true, 0);

    /// <summary>Attachment Services could not be used: <paramref name="hResult"/>.</summary>
    public static AttachmentSaveResult Unavailable(int hResult) => new(false, hResult);

    /// <summary>Save ran and failed with <paramref name="hResult"/>.</summary>
    public static AttachmentSaveResult Failed(int hResult) => new(true, hResult);
}
