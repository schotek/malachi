// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/maildate/groups.go and
// macos/Sources/MalachiCore/Model/MailDateGroups.swift; header presentation
// from ui/data/ui/mail_date_header.blp and MailDateHeaderView.swift.

using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.I18n;
using Malachi.Core.Model;

namespace Malachi.Core.Presentation;

/// <summary>A native WinUI collection group; its header is never a message item.</summary>
public sealed class MailDateSectionRow : ObservableObject
{
    private bool collapsed;

    /// <summary>Creates the header and its observable child collection.</summary>
    public MailDateSectionRow(MailDateGroup group) => Group = group;

    /// <summary>Stable identity.</summary>
    public MailDateGroup Group { get; }

    /// <summary>Localized heading.</summary>
    public string Title => Group.Title;

    /// <summary>Visible messages; empty while collapsed, with the header retained.</summary>
    public ObservableCollection<MessageRow> Rows { get; } = [];

    /// <summary>Disclosure icon.</summary>
    public string Icon => collapsed ? "pan-end-symbolic" : "pan-down-symbolic";

    /// <summary>Accessible disclosure state.</summary>
    public string StateText => collapsed ? L10n.T("Collapsed") : L10n.T("Expanded");

    /// <summary>Updates disclosure presentation.</summary>
    public void SetCollapsed(bool value)
    {
        if (SetProperty(ref collapsed, value, nameof(StateText)))
        {
            OnPropertyChanged(nameof(Icon));
        }
    }
}
