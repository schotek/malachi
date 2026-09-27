// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the menu of the notification-area icon (docs/windows-port.md
// §10; measured in APP-SPIKES §4.2: Open in bold, New Message, Check for New
// Mail, a separator, Quit). The labels are GTK's where GTK has the action
// in a menu or a button (window.blp primary_menu "_New Message", the
// refresh button's "Check for New Mail"); GTK has no Open or Quit item, so
// those two are Windows-only strings. A GTK mnemonic (_N) becomes the Win32
// menu's access key (&N).

using System.Collections.Generic;
using System.Text;
using Malachi.Core.I18n;

namespace Malachi.Platform.Windows.Tray;

/// <summary>The entries of the notification-area icon's menu.</summary>
public static class TrayMenu
{
    /// <summary>The menu in the current language, <see cref="TrayCommand.Open"/> as its default.</summary>
    public static IReadOnlyList<TrayMenuItem> Items() =>
    [
        new(TrayCommand.Open, "&Open Malachi Mail", IsDefault: true), // Windows-only string
        new(TrayCommand.NewMessage, FromGtkLabel(L10n.T("_New Message"))),
        new(TrayCommand.CheckForNewMail, FromGtkLabel(L10n.T("Check for New Mail"))),
        TrayMenuItem.Separator,
        new(TrayCommand.Quit, "&Quit"), // Windows-only string
    ];

    /// <summary>
    /// A GTK label with a mnemonic as a Win32 menu label: the first
    /// <c>_x</c> becomes <c>&amp;x</c>, later ones lose their underscore,
    /// <c>__</c> is an underscore and <c>&amp;</c> is doubled, so that a
    /// translation's ampersand shows as itself.
    /// </summary>
    public static string FromGtkLabel(string label)
    {
        System.ArgumentNullException.ThrowIfNull(label);
        var sb = new StringBuilder(label.Length + 1);
        var mnemonic = false;
        for (var i = 0; i < label.Length; i++)
        {
            var c = label[i];
            if (c == '&')
            {
                sb.Append("&&");
            }
            else if (c == '_' && i + 1 < label.Length)
            {
                var next = label[i + 1];
                if (next == '_')
                {
                    sb.Append('_');
                    i++;
                }
                else if (!mnemonic && next != '&')
                {
                    mnemonic = true;
                    sb.Append('&');
                }
            }
            else
            {
                sb.Append(c);
            }
        }
        return sb.ToString();
    }
}
