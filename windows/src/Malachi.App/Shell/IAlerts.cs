// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Contracts.swift (Alerts,
// SaveDraftAnswer as Core's DraftCloseAnswer); GTK: widget/rpc.go
// ConfirmDestructive, compose/draft.go (the close question),
// widget/consent.go (Send Mail to Claude?, through ConfirmAsync),
// window/remote.go (Open This Link?), accountwizard/trust.go (Trust This
// Certificate?), and main.go's app.about. The dialogs go on the window
// that owns the action, or the main window when it is null (Windows has no
// application-modal alert: a hidden main window is shown first). Heading
// and body are plain text, often hostile input such as a subject.
// Controllers never create a dialog (docs/windows-port.md §7.5): they get
// these as hooks (ActionsController.Confirm, the draft controller's
// SaveDraftQuestion and ConfirmDiscard, the wizard's ConfirmTrust).

using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Controllers;
using Malachi.Core.Wizard;
using Microsoft.UI.Xaml;

namespace Malachi.App.Shell;

/// <summary>The confirmation dialogs of the app.</summary>
public interface IAlerts
{
    /// <summary>Cancel / <paramref name="confirmLabel"/> (the action); true when confirmed. Cancel is the default.</summary>
    Task<bool> ConfirmDestructiveAsync(Window? window, string heading, string body, string confirmLabel);

    /// <summary>
    /// <see cref="ConfirmDestructiveAsync"/> with a check box between the
    /// body and the buttons ("Also delete drafts and downloaded data").
    /// </summary>
    Task<(bool Confirmed, bool Extra)> ConfirmDestructiveExtraAsync(
        Window? window, string heading, string body, string confirmLabel, string extraLabel, bool extraDefault);

    /// <summary>
    /// A question without a destructive answer (macOS Alerts.confirm):
    /// <paramref name="confirmLabel"/> leads as the default (Return),
    /// <paramref name="declineLabel"/> takes Escape; true when confirmed. The
    /// Assistant's "Send Mail to Claude?" (Allow / Cancel) and "Restart
    /// Claude Desktop?" (Restart Claude Desktop / Later).
    /// </summary>
    Task<bool> ConfirmAsync(Window? window, string heading, string body, string confirmLabel, string declineLabel);

    /// <summary>
    /// A message with a single button, <paramref name="closeLabel"/> (the
    /// default and Escape): the bulk strip's "The sender could not be
    /// verified" when there is no alternative to offer.
    /// </summary>
    Task InformAsync(Window? window, string heading, string body, string closeLabel);

    /// <summary>"Save changes to this draft?": Save Draft (the default) / Discard / Cancel.</summary>
    Task<DraftCloseAnswer> SaveDraftQuestionAsync(Window? window);

    /// <summary>
    /// "Open This Link?" for a link whose text says one site and whose target
    /// is another, or, with <paramref name="text"/> "", for a link the daemon
    /// did not list (the destination alone, as on macOS); true opens it.
    /// </summary>
    Task<bool> OpenLinkQuestionAsync(Window? window, string text, string href);

    /// <summary>
    /// "Trust This Certificate?": Cancel / <paramref name="confirmLabel"/>,
    /// the certificate's details between the body and the buttons as
    /// selectable plain text, the fingerprints monospaced.
    /// </summary>
    Task<bool> ConfirmTrustCertificateAsync(
        Window? window, string heading, string body, IReadOnlyList<CertificateDetail> details, string confirmLabel);

    /// <summary>About Malachi Mail (app.about).</summary>
    Task ShowAboutAsync(Window? window);

    /// <summary>Whether one of <paramref name="window"/>'s dialogs is up or waiting.</summary>
    bool IsShowing(Window window);
}
