// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.1): the WebView2 process failures a
// view recovers from (CoreWebView2ProcessFailedKind, the ones that concern
// the view's own page); see RendererRecovery.

namespace Malachi.Core.Presentation;

/// <summary>A failure of the processes behind a web view.</summary>
public enum RendererFailure
{
    /// <summary>The page's renderer ended (RenderProcessExited); the control lives on.</summary>
    RendererExited,

    /// <summary>The page's renderer does not answer (RenderProcessUnresponsive, repeated while it lasts).</summary>
    Unresponsive,

    /// <summary>The browser process ended (BrowserProcessExited): the control and its environment are gone.</summary>
    BrowserExited,
}
