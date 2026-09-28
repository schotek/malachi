// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of MessageViewController.swift (BodyPage); GTK: the pages of
// window.blp's body_stack ("text", "loading", "html").

namespace Malachi.Core.Presentation;

/// <summary>What the body area shows (window.blp <c>body_stack</c>).</summary>
public enum ReaderBodyPage
{
    /// <summary>The plain text, an error or a state sentence.</summary>
    Text,

    /// <summary>The spinner, once the body took longer than <see cref="ReaderController.SpinnerDelay"/>.</summary>
    Loading,

    /// <summary>The sanitised HTML in the viewer.</summary>
    Html,
}
