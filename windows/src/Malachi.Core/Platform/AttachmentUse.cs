// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: what a file written out of a message is for, which decides
// the zone its Mark of the Web names (IMarkOfTheWeb; docs/windows-port.md
// §10). The quarantine of macos/Sources/MalachiMail/Attachments/
// AttachmentActions.swift (quarantine) is the same attribute either way.

namespace Malachi.Core.Platform;

/// <summary>What a file written out of a message is for.</summary>
public enum AttachmentUse
{
    /// <summary>
    /// Written into the open directory to be handed to its default
    /// application (attachments.go <c>openAttachment</c>). Never a program:
    /// the caller refuses those first (<see cref="IFileTypePolicy"/>).
    /// </summary>
    Open,

    /// <summary>
    /// Saved where the user asked (attachments.go <c>saveAttachment</c>,
    /// <c>saveInto</c>): the user's file, whatever it is.
    /// </summary>
    Save,
}
