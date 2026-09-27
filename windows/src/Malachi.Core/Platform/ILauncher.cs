// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the NSWorkspace calls of macos/Sources/MalachiMail/Shared/
// OpenInBrowser.swift (openInBrowser), Actions/MessageActionsController.swift
// (launch) and Attachments/AttachmentActions.swift (open); GTK:
// ui/internal/widget/uri.go (LaunchURI) and ui/internal/window/
// attachments.go (launchFile). Implemented by
// Malachi.Platform.Windows.Launch.Launcher; Open With… is Windows-only.

using System.Threading;
using System.Threading.Tasks;

namespace Malachi.Core.Platform;

/// <summary>
/// Hands addresses and files to the applications Windows has for them. Each
/// method refuses what it is not for (an <see cref="System.ArgumentException"/>)
/// before anything is launched; <paramref name="owner"/> is the window a
/// dialog of the system belongs to, 0 for none.
/// </summary>
public interface ILauncher
{
    /// <summary>
    /// Opens a sign-in page in the browser: only an absolute https address
    /// with a host and no user information (signin.BrowserURL). False when
    /// the user dismissed a dialog of the system on the way.
    /// </summary>
    Task<bool> OpenUrlAsync(string url, nint owner, CancellationToken cancellationToken = default);

    /// <summary>
    /// Opens a link of a message that the reader decided to open (remote.go
    /// <c>launchURI</c> after <c>openLink</c>): an http or https address;
    /// <c>mailto:</c> goes to the composer, never here.
    /// </summary>
    Task<bool> OpenLinkAsync(string url, nint owner, CancellationToken cancellationToken = default);

    /// <summary>
    /// Opens a file written out of a message, and marked, with its default
    /// application (attachments.go <c>launchFile</c>); a local file only,
    /// never a program (<see cref="IFileTypePolicy"/>). Where no application
    /// is set, Windows asks for one; false when the user dismissed that.
    /// </summary>
    Task<bool> OpenFileAsync(string path, nint owner, CancellationToken cancellationToken = default);

    /// <summary>
    /// Asks the user which application opens the file this once (Open
    /// With…), then opens it; the same files as <see cref="OpenFileAsync"/>.
    /// False when the user cancelled.
    /// </summary>
    Task<bool> OpenWithAsync(string path, nint owner, CancellationToken cancellationToken = default);
}
