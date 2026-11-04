// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The XAML side of L10n (docs/windows-port.md §9): the Blueprints' _("…")
// and C_("context", "…") as a markup extension, {l:T Msgid='Search Mail'}
// and {l:T Msgid='Folder', Context='search scope'}, with xmlns:l bound to
// using:Malachi.App.Localization. The msgid is the GTK msgid verbatim, and
// the strings check (Malachi.Conventions.Tests) finds every one in
// po/malachi.pot. WinUI markup extensions take named properties only, so
// the msgid is named too; quote it with ' ' when it holds a comma, a brace
// or leading and trailing spaces. A GTK mnemonic ("_Add Account…") stays in
// the text: set it through MnemonicLabel.Text, which turns the marker into
// the control's access key. Windows-only strings are not translated and
// never come through here.

using Malachi.Core.I18n;
using Microsoft.UI.Xaml.Markup;

namespace Malachi.App.Localization;

/// <summary>A translated GTK msgid in XAML.</summary>
[MarkupExtensionReturnType(ReturnType = typeof(string))]
public sealed partial class T : MarkupExtension
{
    /// <summary>The GTK msgid, verbatim.</summary>
    public string Msgid { get; set; } = "";

    /// <summary>The msgctxt of a C_() msgid; none for _().</summary>
    public string? Context { get; set; }

    /// <inheritdoc/>
    protected override object ProvideValue() =>
        string.IsNullOrEmpty(Context) ? L10n.T(Msgid) : L10n.C(Context, Msgid);
}
