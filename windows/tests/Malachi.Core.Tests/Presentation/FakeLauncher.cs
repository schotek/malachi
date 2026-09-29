// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only test fixture: an ILauncher that records what it would hand
// to the shell (Malachi.Platform.Windows.Launch.Launcher does the real
// thing; its own tests check the checks, and run LinkOpener over it).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Platform;

namespace Malachi.Core.Tests.Presentation;

internal sealed class FakeLauncher : ILauncher
{
    /// <summary>What the browser was handed: <see cref="LinkTarget"/> of each opened link.</summary>
    public List<string> Links { get; } = [];

    public List<string> Files { get; } = [];

    /// <summary>The Assistant's links handed to the shell.</summary>
    public List<string> AssistantLinks { get; } = [];

    /// <summary>What a launch throws, when set.</summary>
    public Exception? Failure { get; set; }

    /// <summary>
    /// The launcher's rule for a link (Launcher.WebLinkTarget), which the
    /// decision and the question rely on, so it is the real one's: an
    /// absolute http or https address with a host and no control
    /// characters, escaped, the host as DNS gets it, without userinfo.
    /// </summary>
    public static string? Target(string? url)
    {
        if (string.IsNullOrEmpty(url) || url.Any(c => c < ' ' || c == '\x7F')
            || !(url.StartsWith("http://", StringComparison.OrdinalIgnoreCase) || url.StartsWith("https://", StringComparison.OrdinalIgnoreCase))
            || !Uri.TryCreate(url, UriKind.Absolute, out var uri)
            || (uri.Scheme != Uri.UriSchemeHttp && uri.Scheme != Uri.UriSchemeHttps) || uri.Host.Length == 0)
        {
            return null;
        }
        string target;
        try
        {
            target = new UriBuilder(uri) { Host = uri.IdnHost, UserName = "", Password = "" }.Uri.AbsoluteUri;
        }
        catch (Exception e) when (e is UriFormatException or ArgumentException)
        {
            return null;
        }
        return target.Any(c => c <= ' ' || c == '"' || c >= '\x7F') ? null : target;
    }

    public Task<bool> OpenUrlAsync(string url, nint owner, CancellationToken cancellationToken = default) => throw new NotSupportedException();

    public Task<bool> OpenAssistantLinkAsync(string link, nint owner, CancellationToken cancellationToken = default)
    {
        if (Failure is { } e)
        {
            return Task.FromException<bool>(e);
        }
        AssistantLinks.Add(link);
        return Task.FromResult(true);
    }

    public Task<bool> OpenLinkAsync(string url, nint owner, CancellationToken cancellationToken = default)
    {
        if (Failure is { } e)
        {
            return Task.FromException<bool>(e);
        }
        if (Target(url) is not { } target)
        {
            return Task.FromException<bool>(new ArgumentException("not an http or https address", nameof(url)));
        }
        Links.Add(target);
        return Task.FromResult(true);
    }

    public string? LinkTarget(string? url) => Target(url);

    public Task<bool> OpenFileAsync(string path, nint owner, CancellationToken cancellationToken = default)
    {
        if (Failure is { } e)
        {
            return Task.FromException<bool>(e);
        }
        Files.Add(path);
        return Task.FromResult(true);
    }

    public Task<bool> OpenWithAsync(string path, nint owner, CancellationToken cancellationToken = default) => throw new NotSupportedException();
}
