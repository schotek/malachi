// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: what MarkOfTheWeb asks of Attachment Services, behind a seam
// so that its decisions (the zone, the fallback, the verdicts) are tested
// without the machine's antivirus and policy.

namespace Malachi.Platform.Windows.Attachments;

/// <summary>The two calls MarkOfTheWeb makes of IAttachmentExecute.</summary>
internal interface IAttachmentServices
{
    /// <summary>
    /// Whether the attachment policy of the Restricted sites zone blocks a
    /// file of this name (<c>CheckPolicy</c> failing).
    /// </summary>
    bool PolicyBlocks(string fileName);

    /// <summary>
    /// <c>SetClientGuid</c>, <c>SetLocalPath</c>, <c>SetFileName</c>, the
    /// source if any, then <c>Save</c>: whether it came as far as
    /// <c>Save</c>, and the HRESULT.
    /// </summary>
    AttachmentSaveResult Save(string path, string fileName, string? source);
}
