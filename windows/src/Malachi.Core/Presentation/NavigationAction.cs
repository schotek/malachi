// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.3): what a web view does with a
// navigation or a new-window request (NavigationPolicy).

namespace Malachi.Core.Presentation;

/// <summary>The answer of <see cref="NavigationPolicy"/>.</summary>
public enum NavigationAction
{
    /// <summary>Cancel it; nothing else happens (logged as refused).</summary>
    Cancel,

    /// <summary>
    /// Cancel it, then ask the page which link was activated
    /// (<see cref="LinkProbe"/>) and hand that on as an
    /// <see cref="Model.ActivatedLink"/>.
    /// </summary>
    CancelAndProbe,

    /// <summary>Let it happen: the view's own pending document.</summary>
    Allow,
}
