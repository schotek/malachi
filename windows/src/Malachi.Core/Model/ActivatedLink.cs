// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/RemoteBar.swift (ActivatedLink);
// GTK has no counterpart (WebKitGTK hands the attribute to remote.go
// openLink). On Windows the viewer reads the attribute with
// document.activeElement.getAttribute('href') through ExecuteScriptAsync,
// which can fail or come too late; the resolved URL is what
// NavigationStarting and NewWindowRequested report (docs/windows-port.md
// §6.3).

namespace Malachi.Core.Model;

/// <summary>
/// A link the viewer reported activated. <see cref="Raw"/> is the
/// <c>href</c> attribute as written in the sanitiser's output, which is the
/// string the daemon lists in <c>links[].href</c>; <see cref="Resolved"/> is
/// the absolute URL WebView2 made of it (Chromium's canonical form: scheme
/// and host lower-cased, an internationalised host in punycode, a trailing
/// slash added, characters escaped), so the two rarely compare equal.
/// <see cref="Raw"/> is null when the attribute could not be read, and only
/// the resolved URL is known; <see cref="LinkDecision.For(ActivatedLink, System.Collections.Generic.IReadOnlyList{Api.Link}, System.Func{string, string})"/>
/// then compares canonical forms.
/// </summary>
/// <param name="Raw">The attribute as written, when it could be read.</param>
/// <param name="Resolved">The URL the navigation goes to.</param>
public sealed record ActivatedLink(string? Raw, string Resolved)
{
    /// <summary>
    /// What is matched against the daemon's list and, when allowed, opened
    /// (Swift <c>href</c>): the attribute as written, or the resolved URL
    /// without it.
    /// </summary>
    public string Href => Raw ?? Resolved;
}
