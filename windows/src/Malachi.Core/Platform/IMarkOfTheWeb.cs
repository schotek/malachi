// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the Mark of the Web on every file written out of a message,
// the counterpart of the quarantine attribute of macos/Sources/MalachiMail/
// Attachments/AttachmentActions.swift (quarantine); implemented by
// Malachi.Platform.Windows.Attachments.MarkOfTheWeb (docs/windows-port.md
// §10, the deviation row of windows/README.md).

using System.Threading;
using System.Threading.Tasks;

namespace Malachi.Core.Platform;

/// <summary>
/// Marks a file written out of a message as coming from the internet, so
/// that SmartScreen, Office's Protected View and the default application
/// treat it like a download.
/// </summary>
public interface IMarkOfTheWeb
{
    /// <summary>
    /// Marks the file at <paramref name="path"/>, which the caller has just
    /// written, and reads the mark back. The file is judged by its own name,
    /// the last component of <paramref name="path"/> (what
    /// <see cref="OpenDir.Write"/> returned, or what the user typed for Save
    /// As), for the attachment policy and the scan alike. It may take a
    /// while (an antivirus scan) and never throws for what happened to the
    /// file; the outcome says it. An <see cref="System.ArgumentException"/>
    /// for a path that is not a file's own (never naming the path, which
    /// carries the attachment's name).
    /// </summary>
    Task<ZoneMark> MarkAsync(string path, AttachmentUse use, CancellationToken cancellationToken = default);
}
