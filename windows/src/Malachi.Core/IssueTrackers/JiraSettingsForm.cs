// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraSettings.swift
// (Jira.SettingsForm, apply, settingsProblem); GTK:
// ui/internal/jira/settings.go (SettingsForm, SettingsForm.Apply,
// SettingsForm.SettingsProblem).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.IssueTrackers;

/// <summary>
/// jira.SettingsForm: the edited copy of what the settings page changes
/// (<see cref="Jira.NewSettingsForm"/>). Its equality compares the lists by
/// reference, as every record's does.
/// </summary>
public sealed record JiraSettingsForm
{
    /// <summary>The account's name.</summary>
    public string Name { get; init; } = "";

    /// <summary>The chosen spaces.</summary>
    public IReadOnlyList<SpaceRef> Spaces { get; init; } = [];

    /// <summary>The offline window as stored (0 is the default) until the user picks one of the choices.</summary>
    public int OfflineDays { get; init; }

    /// <summary>Only the issues involving the user.</summary>
    public bool OnlyMine { get; init; }

    /// <summary>"Show Status and Assignee Changes": not HideEvents.</summary>
    public bool ShowEvents { get; init; } = true;

    /// <summary>The views switched off.</summary>
    public IReadOnlyList<VirtualFolder> DisabledFolders { get; init; } = [];

    /// <summary>The statuses that count as closed; empty is those of the category done.</summary>
    public IReadOnlyList<StatusRef> ClosedStatuses { get; init; } = [];

    /// <summary>The mode, never "".</summary>
    public NotificationMailMode NotificationMail { get; init; } = NotificationMailMode.Sync;

    /// <summary>JiraConfig.NotificationSenders.</summary>
    public IReadOnlyList<string> NotificationSenders { get; init; } = [];

    /// <summary>JiraConfig.BotNames.</summary>
    public IReadOnlyList<string> BotNames { get; init; } = [];

    /// <summary>JiraConfig.MetadataFilters.</summary>
    public IReadOnlyList<string> MetadataFilters { get; init; } = [];

    /// <summary>JiraConfig.AuthorPrefixes.</summary>
    public IReadOnlyList<string> AuthorPrefixes { get; init; } = [];

    /// <summary>
    /// jira.SettingsForm.Apply: the configuration of account.update:
    /// <paramref name="cfg"/>, the account as it is stored, with what the
    /// form holds, normalised (texts trimmed, repetitions and empty entries
    /// dropped, an empty list null, the default mode left out). The
    /// connection (site, deployment, login) and everything the page does not
    /// edit stay as they are. A configuration of another kind comes back
    /// unchanged. Go's zero values are null here, as omitempty leaves them
    /// off the wire.
    /// </summary>
    public AccountConfig Apply(AccountConfig cfg)
    {
        ArgumentNullException.ThrowIfNull(cfg);
        if (cfg.Jira is not { } jc)
        {
            return cfg;
        }
        var name = Jira.Truncate(Name.Trim(), Jira.MaxNameBytes);
        if (name.Length == 0)
        {
            name = Jira.SiteHost(cfg);
            if (name.Length == 0)
            {
                name = "Jira";
            }
        }
        var seen = new HashSet<string>(StringComparer.Ordinal);
        var spaces = Spaces.Where(s => s.Id.Length > 0 && seen.Add(s.Id)).ToArray();
        var days = Math.Min(Math.Max(OfflineDays, 0), API.Limits.MaxJiraOfflineDays);
        seen.Clear();
        var closed = new List<StatusRef>();
        foreach (var r in ClosedStatuses)
        {
            var id = r.Id.Trim();
            if (id.Length == 0 || !seen.Add(id))
            {
                continue;
            }
            var statusName = (r.Name ?? "").Trim();
            closed.Add(new StatusRef { Id = id, Name = statusName.Length == 0 ? null : statusName });
        }
        var disabled = Jira.VirtualFolders.Where(v => !Jira.FolderShown(DisabledFolders, v)).ToArray();
        var mode = Jira.NotificationModes[Jira.IndexOfNotificationMode(NotificationMail)];
        return cfg with
        {
            Name = name,
            Jira = jc with
            {
                Spaces = spaces,
                OfflineDays = days == 0 ? null : days,
                OnlyMine = OnlyMine ? true : null,
                HideEvents = ShowEvents ? null : true,
                DisabledFolders = disabled.Length == 0 ? null : disabled,
                ClosedStatuses = closed.Count == 0 ? null : closed,
                NotificationMail = mode == NotificationMailMode.Sync ? null : (NotificationMailMode?)mode,
                NotificationSenders = NullIfEmpty(Jira.NormaliseList(JiraListKind.Senders, NotificationSenders)),
                BotNames = NullIfEmpty(Jira.NormaliseList(JiraListKind.BotNames, BotNames)),
                MetadataFilters = NullIfEmpty(Jira.NormaliseList(JiraListKind.MetadataFilters, MetadataFilters)),
                AuthorPrefixes = NullIfEmpty(Jira.NormaliseList(JiraListKind.AuthorPrefixes, AuthorPrefixes)),
            },
        };
    }

    /// <summary>jira.SettingsForm.SettingsProblem: why the form cannot be saved; "" when it can.</summary>
    public string SettingsProblem()
    {
        var p = Jira.SpacesProblem(Spaces.Count);
        return p.Length > 0 ? p : Jira.StatusesProblem(ClosedStatuses);
    }

    private static IReadOnlyList<string>? NullIfEmpty(IReadOnlyList<string> list) => list.Count == 0 ? null : list;
}
