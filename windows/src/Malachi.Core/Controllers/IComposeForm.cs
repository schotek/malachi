// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ComposeDraftController.swift
// (the ComposeForm protocol); GTK: the compose.Window widgets draft.go
// reaches (account, recipients, subject, editor, attachments, setStatus,
// toast, the compose.send action, Close).

using System;
using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Controllers;

/// <summary>
/// What the draft controller reads from and writes to the compose window
/// (the rows, the editor, the chips, the status line, the toasts). Every
/// string it hands over is plain text. UI-thread-affine.
/// </summary>
public interface IComposeForm
{
    /// <summary>
    /// The selected From identity (compose.go <c>account</c>): the
    /// placeholder account while the backend lists none, never null.
    /// </summary>
    Account Account { get; }

    /// <summary>The subject row.</summary>
    string Subject { get; }

    /// <summary>The attachments the window lists, in order.</summary>
    IReadOnlyList<DraftAttachment> Attachments { get; }

    /// <summary>
    /// The three recipient rows parsed (compose.go <c>recipients</c>);
    /// <c>Ok</c> is false when any token is invalid.
    /// </summary>
    (IReadOnlyList<Address> To, IReadOnlyList<Address> Cc, IReadOnlyList<Address> Bcc, bool Ok) Recipients();

    /// <summary>editor.HTML: the editor's last reported content (<see cref="Html.EditorChannel.Html"/>).</summary>
    string EditorHtml();

    /// <summary>editor.Text: the editor's last reported text (<see cref="Html.EditorChannel.Text"/>).</summary>
    string EditorText();

    /// <summary>
    /// editor.Flush (<see cref="Html.EditorChannel.BeginFlush"/>):
    /// <paramref name="done"/> runs once the editor reported its current
    /// content, always, on the UI thread; the editor's <c>Changed</c> of the
    /// same report comes after it.
    /// </summary>
    void FlushEditor(Action done);

    /// <summary>
    /// compose.go <c>setAttachments</c>: replaces the list and the chips with
    /// what the backend kept.
    /// </summary>
    void SetAttachments(IReadOnlyList<DraftAttachment> attachments);

    /// <summary>The status line under the window.</summary>
    void SetStatus(string text);

    /// <summary>Shows a transient message over the window.</summary>
    void Toast(string text);

    /// <summary>The Send button and command (<c>compose.send</c>'s enabled state).</summary>
    void SetSendEnabled(bool enabled);

    /// <summary>Closes the window without asking (the controller decided).</summary>
    void CloseWindow();
}
