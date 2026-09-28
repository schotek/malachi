// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// One step of a canary run (HostConfig.Steps).

namespace Malachi.App.Canary.Host;

/// <summary>One thing the host does to one view.</summary>
public sealed record HostStep
{
    /// <summary><c>viewer</c>, <c>editor</c>, <c>preview</c> or <c>control</c>.</summary>
    public required string View { get; init; }

    /// <summary>
    /// <c>load</c> (<see cref="Html"/> as the view's body), <c>show</c> (an
    /// attachment to the previewer: <see cref="Name"/>,
    /// <see cref="ContentType"/>, <see cref="Data"/>), <c>hover</c>,
    /// <c>press</c> (mouse down and leave), <c>click</c>, <c>middle</c>
    /// (a middle click) on <see cref="Target"/> or at
    /// <see cref="X"/>/<see cref="Y"/>, <c>crash</c> (the page's renderer,
    /// through the DevTools protocol), <c>loadcrash</c> (a load, and the
    /// crash once the document committed, while it still loads),
    /// <c>hang</c> (a host script that never ends, and pointer input it
    /// leaves unanswered), <c>await</c> (until the view records an event of
    /// the kind <see cref="Target"/> whose detail contains <see cref="Html"/>,
    /// at most <see cref="Ms"/>), or <c>wait</c>.
    /// </summary>
    public required string Op { get; init; }

    /// <summary>A label the events of this step and the ones after it carry.</summary>
    public string? Phase { get; init; }

    /// <summary>The body to load.</summary>
    public string? Html { get; init; }

    /// <summary>The id of the element to point at (its centre).</summary>
    public string? Target { get; init; }

    /// <summary>The point to act on, in CSS pixels of the view, without a target.</summary>
    public double X { get; init; }

    /// <summary>The point to act on, in CSS pixels of the view, without a target.</summary>
    public double Y { get; init; }

    /// <summary>How long to wait after the step, in milliseconds.</summary>
    public int Ms { get; init; }

    /// <summary>The attachment's name (<c>show</c>).</summary>
    public string? Name { get; init; }

    /// <summary>The attachment's claimed type (<c>show</c>).</summary>
    public string? ContentType { get; init; }

    /// <summary>The attachment's bytes, base64 (<c>show</c>).</summary>
    public string? Data { get; init; }
}
