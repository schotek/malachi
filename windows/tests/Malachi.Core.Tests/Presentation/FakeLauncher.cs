// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only test fixture: an ILauncher that records what it would hand
// to the shell (Malachi.Platform.Windows.Launch.Launcher does the real
// thing; its own tests check the checks).

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Platform;

namespace Malachi.Core.Tests.Presentation;

internal sealed class FakeLauncher : ILauncher
{
    public List<string> Links { get; } = [];

    public List<string> Files { get; } = [];

    /// <summary>What a launch throws, when set.</summary>
    public Exception? Failure { get; set; }

    public Task<bool> OpenUrlAsync(string url, nint owner, CancellationToken cancellationToken = default) => throw new NotSupportedException();

    public Task<bool> OpenLinkAsync(string url, nint owner, CancellationToken cancellationToken = default)
    {
        if (Failure is { } e)
        {
            return Task.FromException<bool>(e);
        }
        Links.Add(url);
        return Task.FromResult(true);
    }

    /// <summary>The launcher's own rule, simplified: http(s) only, the host lower-cased.</summary>
    public string? LinkTarget(string? url) =>
        url is not null && Uri.TryCreate(url, UriKind.Absolute, out var u) && (u.Scheme == Uri.UriSchemeHttp || u.Scheme == Uri.UriSchemeHttps)
            ? u.AbsoluteUri
            : null;

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
