// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The onOpenURL recorder of the Swift controller suites
// (WizardControllerTests, JiraWizardControllerTests): the ILauncher a
// controller is given, which notes the addresses and may fail.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Platform;

namespace Malachi.Core.Tests.Controllers;

/// <summary>Swift's onOpenURL: the addresses handed to the browser; fails with <see cref="Failure"/> when set.</summary>
internal sealed class RecordingLauncher : ILauncher
{
    private readonly Lock gate = new();
    private readonly List<string> opened = [];

    public Exception? Failure { get; set; }

    public IReadOnlyList<string> Opened
    {
        get
        {
            lock (gate)
            {
                return [.. opened];
            }
        }
    }

    public Task<bool> OpenUrlAsync(string url, nint owner, CancellationToken cancellationToken = default)
    {
        lock (gate)
        {
            opened.Add(url);
        }
        return Failure is { } failure ? Task.FromException<bool>(failure) : Task.FromResult(true);
    }

    public Task<bool> OpenLinkAsync(string url, nint owner, CancellationToken cancellationToken = default) => throw new NotSupportedException();

    public string? LinkTarget(string? url) => throw new NotSupportedException();

    public Task<bool> OpenAssistantLinkAsync(string link, nint owner, CancellationToken cancellationToken = default) => throw new NotSupportedException();

    public Task<bool> OpenFileAsync(string path, nint owner, CancellationToken cancellationToken = default) => throw new NotSupportedException();

    public Task<bool> OpenWithAsync(string path, nint owner, CancellationToken cancellationToken = default) => throw new NotSupportedException();
}
