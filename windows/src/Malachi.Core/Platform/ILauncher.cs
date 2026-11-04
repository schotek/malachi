// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the NSWorkspace calls of macos/Sources/MalachiMail/Shared/
// OpenInBrowser.swift (openInBrowser), Actions/MessageActionsController.swift
// (launch) and Attachments/AttachmentActions.swift (open); GTK:
// ui/internal/widget/uri.go (LaunchURI) and ui/internal/window/
// attachments.go (launchFile). Implemented by
// Malachi.Platform.Windows.Launch.Launcher; Open With… is Windows-only, and
// so is the check of the Assistant's links (GTK hands them to GIO as they are).

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
    /// <c>mailto:</c> goes to the composer, never here. What the browser
    /// gets is <see cref="LinkTarget"/> of it.
    /// </summary>
    Task<bool> OpenLinkAsync(string url, nint owner, CancellationToken cancellationToken = default);

    /// <summary>
    /// The address <see cref="OpenLinkAsync"/> would hand the browser for
    /// <paramref name="url"/> (escaped, the host as DNS gets it, without
    /// userinfo), or null when it refuses <paramref name="url"/>: what the
    /// reader judges a link's text against and what a confirmation of the
    /// link shows, so the user judges exactly the address that is opened.
    /// </summary>
    string? LinkTarget(string? url);

    /// <summary>
    /// Hands a link of the Assistant (ui/internal/assistant Link and
    /// FileLink) to the Claude app that handles its scheme: only
    /// <c>claude://claude.ai/new?</c>, <c>claude://cowork/new?</c> and
    /// <c>claude-cli://open?</c> as the Assistant builds them (percent-encoded
    /// ASCII, never a space, quote or control character), at most
    /// <see cref="MaxAssistantLink"/> characters. The link of a file names
    /// the attachment: nothing logs it. False when the user dismissed a
    /// dialog of the system on the way.
    /// </summary>
    Task<bool> OpenAssistantLinkAsync(string link, nint owner, CancellationToken cancellationToken = default);

    /// <summary>
    /// The longest link <see cref="OpenAssistantLinkAsync"/> takes: under
    /// Windows' limit of a command line (32 767 characters), which the
    /// handler's command line with the link in it must fit (Windows only;
    /// Claude's own limits on the prompt are shorter anyway for ordinary text).
    /// </summary>
    public const int MaxAssistantLink = 32000;

    /// <summary>
    /// Opens a file written out of a message, and marked, with its default
    /// application (attachments.go <c>launchFile</c>); a file on a local
    /// drive only, never a program (<see cref="IFileTypePolicy"/>). Where no
    /// application is set, Windows asks for one; false when the user
    /// dismissed that. Its exceptions never name the path.
    /// </summary>
    Task<bool> OpenFileAsync(string path, nint owner, CancellationToken cancellationToken = default);

    /// <summary>
    /// Asks the user which application opens the file this once (Open
    /// With…), then opens it; the same files as <see cref="OpenFileAsync"/>.
    /// False when the user cancelled.
    /// </summary>
    Task<bool> OpenWithAsync(string path, nint owner, CancellationToken cancellationToken = default);
}
