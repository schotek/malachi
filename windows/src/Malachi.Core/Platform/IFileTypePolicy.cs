// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Attachments/AttachmentActions.swift
// (mustNotOpen: the name lists and executableByType, the system's own
// judgement); GTK: ui/internal/window/attachments.go (executableAttachment).
// Implemented by Malachi.Platform.Windows.Attachments.FileTypePolicy, which
// adds AssocIsDangerous to DangerousTypes.

namespace Malachi.Core.Platform;

/// <summary>
/// Which attachments are never handed to an application, only saved
/// (docs/security.md §4).
/// </summary>
public interface IFileTypePolicy
{
    /// <summary>
    /// Whether an attachment named <paramref name="fileName"/> that claims
    /// <paramref name="contentType"/> must go through Save As… rather than
    /// its default application. Asked about what the message lists before
    /// the fetch, and again about the name and type the daemon served.
    /// </summary>
    bool IsDangerous(string? fileName, string? contentType);
}
