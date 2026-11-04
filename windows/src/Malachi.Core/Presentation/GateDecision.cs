// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.2): what a web view answers one
// request with. The counterpart of the macOS content rule list (block
// everything, then allow the application's own scheme) and of the scheme
// handlers behind it (PartSchemeHandler.swift, CIDSchemeHandler.swift).

using Malachi.Core.Html;

namespace Malachi.Core.Presentation;

/// <summary>The answer <see cref="RequestGate.Decide"/> gives a request.</summary>
public abstract record GateDecision
{
    private GateDecision()
    {
    }

    /// <summary>The view's current document, served this once.</summary>
    /// <param name="Generation">The document's generation.</param>
    public sealed record Document(long Generation) : GateDecision;

    /// <summary>The current document's embedded resource, served this once.</summary>
    /// <param name="Generation">The document's generation.</param>
    public sealed record Content(long Generation) : GateDecision;

    /// <summary>
    /// A <c>malachi-cid:</c> picture of the viewer (PartSchemeHandler): fetched
    /// through <c>message.part</c> and served only when it is a picture
    /// (<see cref="PartPath.IsImageType"/>) and the view still shows
    /// <paramref name="Generation"/>.
    /// </summary>
    /// <param name="Reference">The part the URL names, checked by <see cref="PartPath.ParsePartPath"/>.</param>
    /// <param name="Generation">The view's generation when the request arrived.</param>
    public sealed record Part(PartReference Reference, long Generation) : GateDecision;

    /// <summary>
    /// A <c>cid:</c> picture of the editor (CIDSchemeHandler): served only for
    /// an id the window registered (<see cref="CidRegistry"/>), through
    /// <see cref="CidRegistry.CheckInline"/>, while the view still shows
    /// <paramref name="Generation"/>.
    /// </summary>
    /// <param name="Id">The id the URL names (<see cref="CidRegistry.IdOf"/>), matched exactly.</param>
    /// <param name="Generation">The view's generation when the request arrived.</param>
    public sealed record InlineImage(string Id, long Generation) : GateDecision;

    /// <summary>Nothing: an error response with <see cref="Status"/> and no body.</summary>
    public sealed record Refused : GateDecision
    {
        private Refused(int status)
        {
            Status = status;
        }

        /// <summary>403 Forbidden: anything that is not the view's own.</summary>
        public static Refused Forbidden { get; } = new(403);

        /// <summary>404 Not Found: a picture of the view's own scheme that is not there (or no longer).</summary>
        public static Refused NotFound { get; } = new(404);

        /// <summary>403 or 404.</summary>
        public int Status { get; }

        /// <summary>The reason phrase of the status line.</summary>
        public string ReasonPhrase => Status == 404 ? "Not Found" : "Forbidden";
    }
}
