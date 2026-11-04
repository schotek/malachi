// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.3): what LinkProbe.Script reports
// about the page's focused element; the counterpart of the {raw, resolved}
// message of the macOS viewer script (MessageWebView.swift viewerScript).

namespace Malachi.Core.Presentation;

/// <summary>The focused element of the page after an activation.</summary>
/// <param name="Tag">Its tag name in lower case ("a", "button", "body", …).</param>
/// <param name="Raw">The <c>href</c> attribute as written of the link it is in; null outside a link.</param>
/// <param name="Resolved">The absolute URL that link resolves to; null outside a link.</param>
public sealed record LinkProbeResult(string Tag, string? Raw, string? Resolved);
