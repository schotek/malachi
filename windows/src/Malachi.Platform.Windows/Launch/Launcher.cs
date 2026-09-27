// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the NSWorkspace calls of macos/Sources/MalachiMail/Shared/
// OpenInBrowser.swift (openInBrowser: isBrowserURL first),
// Actions/MessageActionsController.swift (launch) and
// Attachments/AttachmentActions.swift (open); GTK: ui/internal/widget/
// uri.go (LaunchURI), ui/internal/signin/signin.go (BrowserURL),
// ui/internal/window/attachments.go (launchFile). Windows' own handler is
// ShellExecuteEx (through Process.Start, on an STA thread, with
// SEE_MASK_NOASYNC and the zone checks on), and SHOpenWithDialog for Open
// With…, which neither GTK nor macOS has.

using System;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Platform;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.UI.Shell;

namespace Malachi.Platform.Windows.Launch;

/// <summary>
/// Hands addresses to the browser and files to their applications, each
/// only after it passed the checks of its kind; nothing that fails them
/// reaches the shell.
/// </summary>
public sealed class Launcher : ILauncher
{
    private static readonly int Cancelled = unchecked((int)0x800704C7); // HRESULT_FROM_WIN32(ERROR_CANCELLED)

    private readonly IFileTypePolicy fileTypes;
    private readonly Func<string, nint, bool, bool> shell;
    private readonly Func<string, nint, bool> openWith;

    /// <summary>
    /// A launcher that refuses every file <paramref name="fileTypes"/> calls
    /// dangerous.
    /// </summary>
    public Launcher(IFileTypePolicy fileTypes)
        : this(fileTypes, ShellExecute, OpenWithDialog)
    {
    }

    /// <summary>
    /// A launcher over other shell calls: <paramref name="shell"/> takes
    /// the target, the owner window and whether the shell may show its own
    /// dialogs, and says false when the user dismissed one;
    /// <paramref name="openWith"/> takes the file and the owner window.
    /// </summary>
    internal Launcher(IFileTypePolicy fileTypes, Func<string, nint, bool, bool> shell, Func<string, nint, bool> openWith)
    {
        ArgumentNullException.ThrowIfNull(fileTypes);
        this.fileTypes = fileTypes;
        this.shell = shell;
        this.openWith = openWith;
    }

    /// <inheritdoc/>
    public Task<bool> OpenUrlAsync(string url, nint owner, CancellationToken cancellationToken = default)
    {
        // The address carries a sign-in's state: it stays out of the message.
        var target = BrowserTarget(url) ?? throw new ArgumentException("not an https address", nameof(url));
        return StaThread.RunAsync(() => shell(target, owner, false), cancellationToken);
    }

    /// <inheritdoc/>
    public Task<bool> OpenLinkAsync(string url, nint owner, CancellationToken cancellationToken = default)
    {
        var target = LinkTarget(url) ?? throw new ArgumentException("not an http or https address", nameof(url));
        return StaThread.RunAsync(() => shell(target, owner, false), cancellationToken);
    }

    /// <inheritdoc/>
    public Task<bool> OpenFileAsync(string path, nint owner, CancellationToken cancellationToken = default)
    {
        var file = CheckFile(path);
        // The shell's own dialogs stay on: the security prompt of a marked
        // file, and the choice of an application where none is set.
        return StaThread.RunAsync(() => shell(file, owner, true), cancellationToken);
    }

    /// <inheritdoc/>
    public Task<bool> OpenWithAsync(string path, nint owner, CancellationToken cancellationToken = default)
    {
        var file = CheckFile(path);
        return StaThread.RunAsync(() => openWith(file, owner), cancellationToken);
    }

    /// <summary>
    /// Whether <see cref="OpenUrlAsync"/> takes <paramref name="url"/>
    /// (signin.BrowserURL, isBrowserURL): an absolute https address with a
    /// host and no user information, and no control characters (Go's
    /// parser refuses them). On Windows also no backslash, which browsers
    /// and .NET read as a slash where Go does not. What reaches the browser
    /// is the escaped address, so no space or quote splits its command line.
    /// </summary>
    public static bool IsBrowserUrl(string? url) => BrowserTarget(url) is not null;

    /// <summary>
    /// Whether <see cref="OpenLinkAsync"/> takes <paramref name="url"/>:
    /// an absolute http or https address with a host (htmlview.AllowedLink
    /// without <c>mailto:</c>), and no control characters.
    /// </summary>
    public static bool IsWebLink(string? url) => LinkTarget(url) is not null;

    // The address the browser gets for a sign-in page, or null.
    private static string? BrowserTarget(string? url)
    {
        if (string.IsNullOrEmpty(url) || url.Any(c => c < ' ' || c == '\x7F' || c == '\\'))
        {
            return null;
        }
        const string Prefix = "https://";
        if (!url.StartsWith(Prefix, StringComparison.OrdinalIgnoreCase))
        {
            return null; // another scheme, or an opaque one ("https:host")
        }
        var authority = url[Prefix.Length..];
        var end = authority.IndexOfAny(['/', '?', '#']);
        if (end >= 0)
        {
            authority = authority[..end];
        }
        if (authority.Length == 0 || authority.Contains('@', StringComparison.Ordinal))
        {
            return null; // no host, or user information (even empty)
        }
        if (!Uri.TryCreate(url, UriKind.Absolute, out var uri)
            || uri.Scheme != Uri.UriSchemeHttps || uri.Host.Length == 0 || uri.UserInfo.Length != 0)
        {
            return null;
        }
        return Escaped(uri);
    }

    // The address the browser gets for a link of a message, or null.
    private static string? LinkTarget(string? url)
    {
        if (string.IsNullOrEmpty(url) || url.Any(c => c < ' ' || c == '\x7F'))
        {
            return null;
        }
        if (!url.StartsWith("http://", StringComparison.OrdinalIgnoreCase)
            && !url.StartsWith("https://", StringComparison.OrdinalIgnoreCase))
        {
            return null;
        }
        if (!Uri.TryCreate(url, UriKind.Absolute, out var uri)
            || (uri.Scheme != Uri.UriSchemeHttp && uri.Scheme != Uri.UriSchemeHttps) || uri.Host.Length == 0)
        {
            return null;
        }
        return Escaped(uri);
    }

    // The address as .NET escapes it, which is what reaches the browser's
    // command line: never a space or a quote to split it at.
    private static string? Escaped(Uri uri)
    {
        var s = uri.AbsoluteUri;
        return s.Any(c => c <= ' ' || c == '"' || c == '\x7F') ? null : s;
    }

    // A file of this machine that the policy lets be opened: fully
    // qualified on a drive (never a share, whose server would learn the
    // user's credentials, nor a device), a file's own content (not an
    // alternate data stream), there, not a directory, not a link, and not a
    // program.
    private string CheckFile(string path)
    {
        ArgumentException.ThrowIfNullOrEmpty(path);
        if (!Path.IsPathFullyQualified(path) || path.StartsWith(@"\\", StringComparison.Ordinal))
        {
            throw new ArgumentException("not a file on a drive of this machine", nameof(path));
        }
        var full = Path.GetFullPath(path);
        if (full.IndexOf(':', 2) >= 0)
        {
            throw new ArgumentException("an alternate data stream, not a file", nameof(path));
        }
        var attributes = File.GetAttributes(full);
        if ((attributes & FileAttributes.ReparsePoint) != 0)
        {
            throw new ArgumentException("a link, not a file", nameof(path));
        }
        if ((attributes & FileAttributes.Directory) != 0)
        {
            throw new ArgumentException("a directory, not a file", nameof(path));
        }
        if (fileTypes.IsDangerous(Path.GetFileName(full), null))
        {
            throw new ArgumentException("programs and scripts are never opened", nameof(path));
        }
        return full;
    }

    // ShellExecuteEx on the calling STA thread (Process.Start does nothing
    // else with UseShellExecute): the default verb, SEE_MASK_NOASYNC, the
    // zone checks on, the shell's dialogs only when asked for.
    private static bool ShellExecute(string target, nint owner, bool dialogs)
    {
        var info = new ProcessStartInfo
        {
            FileName = target,
            UseShellExecute = true,
            ErrorDialog = dialogs,
            ErrorDialogParentHandle = owner,
        };
        try
        {
            using var process = Process.Start(info);
            return true;
        }
        catch (Win32Exception e) when (e.NativeErrorCode == (int)WIN32_ERROR.ERROR_CANCELLED)
        {
            return false;
        }
    }

    // The system's Open With dialog: the file opens in what the user picks,
    // this once; the choice does not become the default.
    private static unsafe bool OpenWithDialog(string path, nint owner)
    {
        fixed (char* file = path)
        {
            var info = new OPENASINFO
            {
                pcszFile = file,
                pcszClass = default,
                oaifInFlags = OPEN_AS_INFO_FLAGS.OAIF_EXEC | OPEN_AS_INFO_FLAGS.OAIF_HIDE_REGISTRATION,
            };
            var result = PInvoke.SHOpenWithDialog((HWND)owner, info);
            if (result.Value == Cancelled)
            {
                return false;
            }
            result.ThrowOnFailure();
            return true;
        }
    }
}
