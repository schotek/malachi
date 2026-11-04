// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the Integration extension of
// macos/Sources/MalachiMail/App/Integration+Contacts.swift (wireContacts:
// showMail); GTK: ui/internal/window/contacts_detail.go (showMailSearch).
// Show Mail of the Contacts window (A14): the main window in Mail with the
// search from:address over every account, as search.query has it; the
// box shows the query and the list follows it, as typed.

using Malachi.Core.Boards;
using Malachi.Core.Settings;

namespace Malachi.App;

/// <summary>The contacts' way into the main window.</summary>
public sealed partial class MainWindow
{
    /// <summary>
    /// Show Mail: Mail with the search <paramref name="query"/> over every
    /// account (macOS <c>setSearchScope(.all)</c>, which stays the user's
    /// scope afterwards, as there).
    /// </summary>
    public void ShowMailSearch(string query)
    {
        if (query.Length == 0 || list is null)
        {
            return;
        }
        if (Mode != Board.Mode.Mail)
        {
            SetMode(Board.Mode.Mail);
        }
        list.SetSearchScope(SearchScope.All);
        BeginSearch();
        SearchBox.Text = query;
        list.SearchFieldChanged(query);
    }
}
