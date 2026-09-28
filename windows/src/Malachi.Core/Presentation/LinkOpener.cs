// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Actions/MessageActionsController.swift
// (openLink, openUnlistedLinkQuestion's use, openMailto, launch); GTK:
// ui/internal/window/remote.go (openLink, launchURI) and
// ui/internal/widget/uri.go (LaunchErrorText). What a link the user
// activated in a message does, minus the dialog and the shell: nothing for
// a scheme the reader does not follow, the composer for mailto:, the
// browser for a listed link whose text does not pretend to lead
// elsewhere, and a question first for a masked link and, as on macOS and
// unlike GTK (docs/windows-port.md §6.4, windows/README.md), for a link the
// daemon did not list. The address the browser would get
// (ILauncher.LinkTarget: escaped, the host as DNS gets it, no userinfo) is
// what the decision judges a listed link's text against, what the
// question names, and what is opened. The links are server data; nothing
// here logs them.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Platform;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Presentation;

/// <summary>Opens, confirms or composes a link activated in a message.</summary>
public sealed partial class LinkOpener
{
    private readonly ILauncher launcher;
    private readonly ILogger logger;

    /// <param name="launcher">Hands the link to the browser.</param>
    /// <param name="logger">Kinds only, never a link.</param>
    public LinkOpener(ILauncher launcher, ILogger<LinkOpener>? logger = null)
    {
        ArgumentNullException.ThrowIfNull(launcher);
        this.launcher = launcher;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
    }

    /// <summary>Opens a compose window for a <c>mailto:</c> link (the compose hook).</summary>
    public Action<ComposeParams>? Compose { get; set; }

    /// <summary>
    /// "Open This Link?" over the window: the decision (its
    /// <see cref="LinkDecision.Confirm.Text"/> is "" for an unlisted link)
    /// and the destination to name; true opens it. Without a hook a link
    /// that must be confirmed is not opened.
    /// </summary>
    public Func<object?, LinkDecision.Confirm, string, Task<bool>>? Confirm { get; set; }

    /// <summary>A toast in the window the link was activated in.</summary>
    public Action<object?, string>? Toast { get; set; }

    /// <summary>The native handle of a window, for the shell's dialogs; 0 for none.</summary>
    public Func<object?, nint>? Owner { get; set; }

    /// <summary>
    /// Handles <paramref name="link"/>, activated in a body whose links the
    /// daemon listed as <paramref name="links"/>, in <paramref name="window"/>
    /// (remote.go <c>openLink</c>).
    /// </summary>
    public async Task OpenAsync(ActivatedLink link, IReadOnlyList<Link> links, object? window)
    {
        ArgumentNullException.ThrowIfNull(link);
        ArgumentNullException.ThrowIfNull(links);
        switch (LinkDecision.For(link, links, launcher.LinkTarget))
        {
            case LinkDecision.Mailto m:
                OpenMailto(m.Href);
                return;
            case LinkDecision.Open o:
                // The decision judged launcher.LinkTarget of this href, which
                // is what the launcher hands the browser.
                await LaunchAsync(o.Href, window);
                return;
            case LinkDecision.Confirm c:
                // The address the browser would get; a link it would refuse
                // is not asked about.
                if (launcher.LinkTarget(c.Href) is not { } destination)
                {
                    LogRefused(logger);
                    return;
                }
                if (Confirm is not { } confirm)
                {
                    LogNoConfirmation(logger);
                    return;
                }
                if (await confirm(window, c, destination))
                {
                    await LaunchAsync(c.Href, window);
                }
                return;
            default:
                // Not http(s) or mailto: nothing happens.
                return;
        }
    }

    // A mailto: link as a new message (compose.ParseMailto).
    private void OpenMailto(string href)
    {
        ComposeParams parameters;
        try
        {
            parameters = Mailto.ParseMailto(href);
        }
        catch (MailtoException e)
        {
            LogMailtoRefused(logger, e.Error);
            return;
        }
        Compose?.Invoke(parameters);
    }

    // remote.go launchURI: the desktop's handler; a failure is a toast.
    private async Task LaunchAsync(string href, object? window)
    {
        try
        {
            await launcher.OpenLinkAsync(href, Owner?.Invoke(window) ?? 0);
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            // The exception may name the address: its type only.
            LogLaunchFailed(logger, e.GetType().Name);
            // TRANSLATORS: %s is a technical error message.
            Toast?.Invoke(window, L10n.T("The link could not be opened: %s", e.Message));
        }
    }

    [LoggerMessage(Level = LogLevel.Debug, Message = "mailto link refused: {Reason}")]
    private static partial void LogMailtoRefused(ILogger logger, MailtoError reason);

    [LoggerMessage(Level = LogLevel.Information, Message = "a link the browser would refuse was not offered")]
    private static partial void LogRefused(ILogger logger);

    [LoggerMessage(Level = LogLevel.Error, Message = "no confirmation hook is installed; the link was not opened")]
    private static partial void LogNoConfirmation(ILogger logger);

    [LoggerMessage(Level = LogLevel.Warning, Message = "open link failed: {Kind}")]
    private static partial void LogLaunchFailed(ILogger logger, string kind);
}
