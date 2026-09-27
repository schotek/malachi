// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ComposeController.swift (the
// ComposeWindowHandle protocol and its default edits/present); GTK: the
// compose.Window methods manager.go calls (setAccounts, toast, the draft
// comparison of FindDraft, Present). SaveForQuitAsync is the Windows
// addition of docs/windows-port.md §0 (dirty drafts saved on Quit).

using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Api;

namespace Malachi.Core.Controllers;

/// <summary>
/// What the compose manager pushes to an open compose window (the refreshed
/// account list, a toast) and what it asks of one (whether it edits a
/// draft, to come to the front, to save for Quit). UI-thread-affine. The
/// window hands itself to <see cref="ComposeController.Remove"/> once it
/// really closes, after its draft controller's
/// <see cref="ComposeDraftController.Cleanup"/> (GTK's cleanup calls
/// Manager.remove); otherwise <see cref="ComposeController.FindDraft"/> and
/// <see cref="ComposeController.SaveForQuitAsync"/> keep visiting it.
/// </summary>
public interface IComposeWindowHandle
{
    /// <summary>compose.go <c>setAccounts</c>: the From row lists <paramref name="accounts"/>.</summary>
    void SetAccounts(IReadOnlyList<Account> accounts, bool placeholder);

    /// <summary>Shows a transient message over the window.</summary>
    void Toast(string text);

    /// <summary>
    /// Whether the window edits <paramref name="draft"/>: the same saved
    /// draft, or the same Drafts message taken over (manager.go
    /// <c>FindDraft</c>). None by default.
    /// </summary>
    bool Edits(Draft draft) => false;

    /// <summary>Brings the window to the front. Nothing by default.</summary>
    void Present()
    {
    }

    /// <summary>
    /// The window's <see cref="ComposeDraftController.SaveForQuitAsync"/>:
    /// true when nothing unsaved is left. True by default (nothing to save).
    /// </summary>
    Task<bool> SaveForQuitAsync() => Task.FromResult(true);

    /// <summary>
    /// Quit could not save this window's draft
    /// (<see cref="ComposeController.SaveForQuitAsync"/> returned it): asks
    /// the window's close question (its draft controller's
    /// <see cref="ComposeDraftController.CloseRequestAsync"/>) and closes the
    /// window when the answer lets it. True when the window closed; false
    /// when the user kept it, which abandons the Quit
    /// (Malachi.Core.Presentation.QuitSequence). True by default (nothing
    /// to ask).
    /// </summary>
    Task<bool> CloseForQuitAsync() => Task.FromResult(true);
}
