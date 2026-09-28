// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.1, §6.3, §6.5, §6.6; windows/README.md
// deviations): what a web view does when WebView2 reports a failed process
// (CoreWebView2.ProcessFailed).
//
// Neither reference reloads by itself. GTK only logs a terminated web
// process (htmlview/view.go, ConnectWebProcessTerminated); macOS marks the
// body so that the next load(body:) loads it again (MessageWebView.swift
// processTerminated, needsReload); the editor's window reloads its text when
// told (ComposeWindowController onCrashed). WebView2 also reports a renderer
// that hangs, and a dead browser process takes the control with it, so a
// Windows view recovers on its own, but once per document: the document
// that was on display when its renderer died, hung or took the browser with
// it is shown again once, and when the same document fails again the view
// gives up (drops it, raises Unavailable, and the reader shows the plain
// text, the previewer its panel). A body that reliably kills or hangs the
// renderer, a Chromium or PDFium bug (the previewer hands PDFium the
// attachment's bytes), would otherwise reload for as long as it is on
// display, burn CPU, write a local crash dump holding the mail each time
// (IsCustomCrashReportingEnabled), and give an exploit unlimited retries
// without the user doing anything.
//
// A hang is not acted on at its first report, which Chromium's hang monitor
// makes about 15 s after an input event went unanswered (measured by the
// canary, runtime 153), and repeats only for further input: a large
// legitimate message may take its time. After the first report the view
// asks the renderer (a host script): one that answers is not hung; one that
// has not answered within AnswerTimeout, or is reported again first, is,
// and its control is replaced, which is what ends a hung renderer.
//
// A document is known by a hash of the bytes it is served from, so the view
// keeps no content for this, and the same body loaded again, by the view's
// own recovery or by its caller (the compose window reloads its text after
// Crashed), is the same document: a caller that reloads what failed cannot
// start the loop either. Pure logic; the view (HardenedWebView) does what
// it says.

using System;
using System.Security.Cryptography;

namespace Malachi.Core.Presentation;

/// <summary>Decides how a web view recovers from a failed WebView2 process.</summary>
public sealed class RendererRecovery
{
    /// <summary>
    /// How many consecutive <see cref="RendererFailure.Unresponsive"/>
    /// reports, without an answer of the renderer in between, make a hang
    /// (<see cref="Unanswered"/> counts as one).
    /// </summary>
    public const int UnresponsiveReports = 2;

    /// <summary>
    /// How long the view waits for the renderer to answer its host script
    /// after a report of a hang before it calls <see cref="Unanswered"/>.
    /// </summary>
    public static readonly TimeSpan AnswerTimeout = TimeSpan.FromSeconds(5);

    // The document on display (its hash), null when there is none.
    private string? current;

    // The document that was shown again after a failure: it gets no second
    // automatic reload.
    private string? recovered;

    private int hangs;

    /// <summary>
    /// The view starts loading a document served from <paramref name="bytes"/>
    /// (for a page that only embeds a resource, that resource's bytes).
    /// </summary>
    public void Loading(ReadOnlySpan<byte> bytes)
    {
        current = Convert.ToHexString(SHA256.HashData(bytes));
        hangs = 0;
    }

    /// <summary>The view dropped its document without a new one.</summary>
    public void Dropped()
    {
        current = null;
        hangs = 0;
    }

    /// <summary>The renderer answered a host script: it is not hung.</summary>
    public void Responsive() => hangs = 0;

    /// <summary>
    /// The renderer has not answered the host script the view sent after a
    /// report of a hang within <see cref="AnswerTimeout"/>: the next report,
    /// unless that hang is no longer the current one (it answered, it was
    /// acted on, or another document started).
    /// </summary>
    public RecoveryAction Unanswered() =>
        hangs > 0 ? Failed(RendererFailure.Unresponsive) : RecoveryAction.None;

    /// <summary>What the view does about <paramref name="failure"/>.</summary>
    public RecoveryAction Failed(RendererFailure failure)
    {
        bool newControl;
        switch (failure)
        {
            case RendererFailure.RendererExited:
                newControl = false;
                break;
            case RendererFailure.BrowserExited:
                newControl = true;
                break;
            case RendererFailure.Unresponsive:
                if (++hangs < UnresponsiveReports)
                {
                    return RecoveryAction.None;
                }
                newControl = true;
                break;
            default:
                throw new ArgumentOutOfRangeException(nameof(failure), failure, null);
        }
        hangs = 0;
        var document = current;
        current = null;
        if (document is null)
        {
            return newControl ? RecoveryAction.Replace : RecoveryAction.None;
        }
        if (string.Equals(document, recovered, StringComparison.Ordinal))
        {
            return newControl ? RecoveryAction.ReplaceAndGiveUp : RecoveryAction.GiveUp;
        }
        recovered = document;
        return newControl ? RecoveryAction.ReplaceAndReload : RecoveryAction.Reload;
    }
}
