// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/plans/toolbar-customization.md W.3, T7): the
// features the conditional items of the toolbars need, from the
// application's state: eWay-CRM with an eWay-CRM account, ✦ while the
// Assistant is shown (assistant-menu and the registration in Claude),
// Triage while the board's triage can run (the In App target), and never
// Print, which Windows does not have yet. The bars hide such an item
// themselves (its control's own visibility); the editor marks its row and
// More Actions leaves out Save to eWay-CRM… without it.

using Malachi.App.Shell;
using Malachi.Core.Controllers;
using Malachi.Core.Toolbars;

namespace Malachi.App.Toolbars;

/// <summary>The features of the toolbars' conditional items.</summary>
internal static class ToolbarFeatures
{
    /// <summary>What <paramref name="state"/> has now.</summary>
    public static Toolbar.Features Of(AppState state)
    {
        var have = new System.Collections.Generic.List<Toolbar.Feature>();
        if (state.Crm.Account is not null)
        {
            have.Add(Toolbar.Feature.Crm);
        }
        var shown = state.Assistant?.Shown ?? false;
        if (shown)
        {
            have.Add(Toolbar.Feature.Assistant);
        }
        if (BoardTriageController.TriageAvailable(shown, state.Settings.AssistantTarget))
        {
            have.Add(Toolbar.Feature.Triage);
        }
        return Toolbar.Features.With([.. have]);
    }
}
