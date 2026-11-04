// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first, the flow of ui/internal/updates/updates.go's package
// comment (docs/plans/update-check.md, docs/api.md §4.20): the update status
// once for the application, one UpdateStatusController read when the
// connection comes (again after a reconnect) and on every
// notify.updateChanged, in the language the client's own texts are in. The
// main window shows its banner and asks the security question
// (MainWindow.Updates.cs), Preferences → General its switch, line and Check
// Now (GeneralPage.Updates.cs), About its line (AlertService.About).

using System;
using System.Collections.Generic;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Text;
using Malachi.Core.Updates;
using Microsoft.Extensions.Logging;

namespace Malachi.App.Shell;

/// <summary>The update check.</summary>
public sealed partial class AppState
{
    private readonly List<IDisposable> updateTokens = [];

    /// <summary>The update status, one for the application.</summary>
    public UpdateStatusController Updates { get; private set; } = null!;

    /// <summary>The status line of Preferences → General and About (updates.AboutLine), the dates in the user's format.</summary>
    public string UpdateLine => UpdateCheck.AboutLine(Updates.Status, t => Format.FormatDateTime(t));

    private void InitializeUpdates()
    {
        // The daemon's texts (the notes, a notice) in the catalogue's language.
        Updates = new UpdateStatusController(Client, () => L10n.Catalogue.Language, Logs.CreateLogger<UpdateStatusController>());
        updateTokens.Add(Notifications.AddConnectionState(s =>
        {
            if (s is ConnectionState.Connected or ConnectionState.InfoFailed)
            {
                Updates.Refresh();
            }
        }));
        updateTokens.Add(Notifications.AddUpdateChanged(Updates.Refresh));
        if (Notifications.ConnectionState is ConnectionState.Connected)
        {
            Updates.Refresh();
        }
    }

    private void CloseUpdates()
    {
        foreach (var t in updateTokens)
        {
            t.Dispose();
        }
        updateTokens.Clear();
        Updates.Dispose();
    }
}
