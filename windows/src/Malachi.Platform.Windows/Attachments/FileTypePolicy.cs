// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Attachments/AttachmentActions.swift
// (mustNotOpen, executableByType); GTK: ui/internal/window/attachments.go
// (executableAttachment). Where macOS asks the type system whether a name
// or type conforms to an executable, Windows asks AssocIsDangerous, which
// knows the built-in list of the shell and every type registered as
// always unsafe (FTA_AlwaysUnsafe). Measured on Windows 11 26100, it names
// exactly what the attachment policy of the Restricted sites zone blocks
// (CheckPolicy), all of it in DangerousTypes already; both miss .rdp,
// .search-ms, .searchconnector-ms, .settingcontent-ms, .appinstaller,
// .msix, .xll, .jar, .py and .sh, and also .one, .onepkg, .contact, .wab
// and Access's formats since 2007 (.accdb and the rest, measured with
// Office installed, while the older .mdb, .mde, .mda, .ade and .adp are
// named), all of which DangerousTypes lists.

using System;
using Malachi.Core.Platform;
using Windows.Win32;

namespace Malachi.Platform.Windows.Attachments;

/// <summary>
/// Whether an attachment is never handed to an application:
/// <see cref="DangerousTypes"/>, or what the shell calls dangerous
/// (<c>AssocIsDangerous</c>) for the extension of the name as given or as
/// written to disk, which covers what software installed later registers
/// as unsafe.
/// </summary>
public sealed class FileTypePolicy : IFileTypePolicy
{
    private readonly Func<string, bool> shellSaysDangerous;

    /// <summary>The lists and the shell of this machine.</summary>
    public FileTypePolicy()
        : this(IsDangerousToTheShell)
    {
    }

    internal FileTypePolicy(Func<string, bool> shellSaysDangerous)
    {
        this.shellSaysDangerous = shellSaysDangerous;
    }

    /// <inheritdoc/>
    public bool IsDangerous(string? fileName, string? contentType)
    {
        if (DangerousTypes.IsDangerous(fileName, contentType))
        {
            return true;
        }
        foreach (var extension in DangerousTypes.CandidateExtensions(fileName))
        {
            if (shellSaysDangerous(extension))
            {
                return true;
            }
        }
        return false;
    }

    /// <summary>
    /// <c>AssocIsDangerous</c> for an extension, with or without its dot.
    /// </summary>
    public static bool IsDangerousToTheShell(string? extension)
    {
        var ext = extension ?? "";
        if (ext.StartsWith('.'))
        {
            ext = ext[1..];
        }
        return ext.Length > 0 && PInvoke.AssocIsDangerous("." + ext);
    }
}
