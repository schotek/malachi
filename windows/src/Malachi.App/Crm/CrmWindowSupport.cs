// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/CRM/CRMBarView.swift (CRMWindowSupport,
// CRMBarView.open) and CRMToolbarItem.swift (the button's actions); GTK:
// window/crm_actions.go of docs/eway.md §2.6 (written in T7). What every
// window does with its CrmController: the dialog as a ContentDialog on the
// window (queued with the window's other dialogs), the removal's question
// there too (Remove from eWay-CRM?, Cancel the default), the toasts in the
// window, the saved item opened in the browser without a question; the
// button follows the controller and the application's CRM state.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Crm;
using Malachi.Core.I18n;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;

namespace Malachi.App.Crm;

/// <summary>The eWay-CRM actions of a window.</summary>
internal static class CrmWindowSupport
{
    /// <summary>
    /// The CrmController of <paramref name="window"/> acting on
    /// <paramref name="subjects"/> (the shown message of each selected row,
    /// or the window's message); <paramref name="conversations"/> are those
    /// a conversation's row stands for. Closed with the window.
    /// </summary>
    public static CrmController Make(
        AppState state,
        Window window,
        IToasts toasts,
        Func<IReadOnlyList<MessageSummary>> subjects,
        Func<IReadOnlySet<MessageId>>? conversations = null)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(window);
        ArgumentNullException.ThrowIfNull(toasts);
        var c = new CrmController(state.Client, state.Crm, state.Launcher, state.Logs.CreateLogger<CrmController>())
        {
            Owner = WindowPresenter.Handle(window),
            Subjects = subjects,
        };
        if (conversations is not null)
        {
            c.Conversations = conversations;
        }
        c.PresentRequested += (_, session) => _ = PresentAsync(state, window, session);
        c.Confirm = q => state.Alerts.ConfirmDestructiveAsync(window, q.Heading, q.Body, q.Confirm);
        c.ToastRequested += (_, text) => toasts.Show(text);
        window.Closed += (_, _) => c.Close();
        return c;
    }

    /// <summary>
    /// The button of a command row follows <paramref name="c"/>: its state now
    /// and whenever the application's CRM state changes; its clicks and menu
    /// run the controller. Returns the redraw, for the window's own changes
    /// (the selection, a loaded message).
    /// </summary>
    public static Action Bind(CrmToolbarButton button, CrmController c, AppState state, Window window)
    {
        ArgumentNullException.ThrowIfNull(button);
        ArgumentNullException.ThrowIfNull(c);
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(window);
        void Refresh()
        {
            if (!c.IsClosed)
            {
                button.Show(c.Button);
            }
        }
        button.SaveClicked += (_, _) => c.Save();
        button.MenuChosen += (_, id) => Run(c, id);
        EventHandler changed = (_, _) => Refresh();
        state.Crm.Changed += changed;
        window.Closed += (_, _) => state.Crm.Changed -= changed;
        Refresh();
        return Refresh;
    }

    /// <summary>An item of the saved message's menu (the button's, the context menu's).</summary>
    public static void Run(CrmController c, CrmMenuId id)
    {
        ArgumentNullException.ThrowIfNull(c);
        switch (id)
        {
            case CrmMenuId.Open:
                c.Open();
                break;
            case CrmMenuId.EditLinks:
                c.EditLinks();
                break;
            case CrmMenuId.Remove:
                c.Remove();
                break;
        }
    }

    /// <summary>
    /// Opens a URL the daemon built (a bar's Open in eWay-CRM) in the browser
    /// without the "Open This Link?" question (docs/eway.md D15): https only
    /// (CrmRules.OpenUrl); a failure is a toast in <paramref name="window"/>.
    /// </summary>
    public static void OpenUrl(AppState state, Window? window, string url)
    {
        ArgumentNullException.ThrowIfNull(state);
        var u = CrmRules.OpenUrl(url);
        if (u.Length > 0)
        {
            _ = OpenAsync(state, window, u);
        }
    }

    private static async Task OpenAsync(AppState state, Window? window, string url)
    {
        try
        {
            await state.Launcher.OpenUrlAsync(url, window is null ? 0 : WindowPresenter.Handle(window));
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            // TRANSLATORS: %s is a technical error message.
            var text = L10n.T("The link could not be opened: %s", e.Message);
            if (window is not null && state.Windows.Find(window)?.Toasts is { } own)
            {
                own.Show(text);
            }
            else
            {
                state.Toasts.Show(text);
            }
        }
    }

    // The dialog of a session on its window; a dialog that never showed,
    // or closed any other way than Save, cancels the session.
    private static async Task PresentAsync(AppState state, Window window, CrmDialogSession session)
    {
        try
        {
            await state.Alerts.ShowDialogAsync(window, () => new CrmSaveDialog(session));
        }
        finally
        {
            session.Cancel();
        }
    }
}
