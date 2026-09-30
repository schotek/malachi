// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraSettings.swift; GTK:
// ui/internal/jira/settings.go (SuggestedBotName, SuggestedMetadataFilter,
// minBotNameRunes, SettingsTexts, SettingsSite, SettingsSpaceRows,
// SetSpaceSelected, VirtualFolders, FolderShown, SetFolderShown,
// NotificationModes, NotificationModeLabels, IndexOfNotificationMode,
// NotificationHint, SendersEditable, DefaultSenders, statusCategories,
// StatusCategoryTitle, knownCategory, DefaultClosedStatuses, StatusGroups,
// SetStatusSelected, StatusesProblem, NormaliseEntry, entryKey,
// NormaliseList, validSender, validHostName, CheckEntry, Suggestions,
// NewSettingsForm, Changed, compared, keys; PatternError is
// Jira.Pattern.cs).
//
// The settings of a Jira account are one page: the site (read only, with
// the button that replaces the token), the spaces, the synchronisation, the
// folders with the statuses that count as closed, what a notification
// e-mail of the site does, and how comments posted by bots are cleaned. The
// page opens with account.listSpaces (the stored token), which lists the
// spaces and the statuses to choose from; when that fails the page still
// edits what is stored. Save is account.update with empty credentials,
// which keeps the token. JiraSettingsForm holds the edited copy; Apply turns
// it into the configuration to save. The daemon validates everything again:
// what is checked here is immediate feedback.
//
// Windows: Go's nil lists are empty lists here (Apply writes null for an
// empty one), sort.Strings and Go's string order are CodePoints.Comparer,
// strings.ToLower CodePoints.ToLower, net/mail's address check
// Fields.ValidateEmail, and Changed compares the two normalised
// configurations by their JSON, since records compare lists by reference
// (Go's reflect.DeepEqual, Swift's ==).

using System;
using System.Buffers;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Wizard;

namespace Malachi.Core.IssueTrackers;

public static partial class Jira
{
    /// <summary>
    /// jira.SuggestedBotName: what the page offers to add with one click
    /// while the list lacks it: the integration that mirrors comments
    /// between two Jira sites. Data, not translated.
    /// </summary>
    public const string SuggestedBotName = "Issue Sync – Synchronization for Jira";

    /// <summary>
    /// jira.SuggestedMetadataFilter: the technical line that integration
    /// puts under the comment's header. Data, not translated.
    /// </summary>
    public const string SuggestedMetadataFilter = "^Remote comment create date:.*$";

    // jira.minBotNameRunes: the shortest bot name the page accepts: the
    // daemon looks for a name of three characters or more inside an
    // author's name, and takes a shorter one only when it is the whole name.
    private const int MinBotNameRunes = 3;

    /// <summary>jira.SettingsTexts: the fixed texts, translated.</summary>
    public static JiraSettingsStrings SettingsTexts() => new()
    {
        // TRANSLATORS: title of the window with the settings of a Jira account; "Jira" is a product name.
        Title = L10n.T("Jira Account"),
        SiteTitle = L10n.T("Jira Site"),
        SiteAddress = L10n.T("Site Address"),
        AccountName = L10n.T("Account Name"),
        // TRANSLATORS: label of the user a Jira account signs in as.
        SignedInAs = L10n.T("Signed In As"),
        // TRANSLATORS: button that asks for a new API token of a Jira account.
        ReplaceToken = L10n.T("Replace Token…"),
        SpacesTitle = L10n.C("jira", "Spaces"),
        SpacesDescription = L10n.T("Choose the spaces whose issues appear as folders."),
        NoSpaces = L10n.T("No spaces are visible to this account"),
        SyncTitle = L10n.T("Synchronisation"),
        KeepOffline = L10n.T("Keep Issues Offline For"),
        KeepOfflineSubtitle = L10n.T("Older issues stay on the site and are not shown"),
        OnlyMine = L10n.T("Only Issues Involving Me"),
        OnlyMineSubtitle = L10n.T("Assigned to you, reported by you or watched by you"),
        // TRANSLATORS: switch; the changes are listed among the comments of an issue.
        ShowEvents = L10n.T("Show Status and Assignee Changes"),
        FoldersTitle = L10n.T("Folders"),
        ClosedStatuses = L10n.T("Closed Statuses"),
        // TRANSLATORS: "Open" is the folder of a Jira account with the issues that are not closed yet.
        ClosedStatusesSubtitle = L10n.T("Issues in these statuses are left out of Open"),
        // TRANSLATORS: the e-mails Jira sends about changes of issues.
        NotificationTitle = L10n.T("Notification E-mails"),
        NotificationMode = L10n.T("When a Jira Notification Arrives"),
        // TRANSLATORS: the addresses notification e-mails of Jira come from.
        Senders = L10n.T("Senders"),
        // TRANSLATORS: "@example.org" is an example, keep it as it is.
        SendersSubtitle = L10n.T("An address, or a domain such as @example.org"),
        BotsTitle = L10n.T("Comments Posted by Bots"),
        // TRANSLATORS: the Jira accounts of integrations that post comments for other people.
        BotNames = L10n.T("Bot Accounts"),
        BotNamesSubtitle = L10n.T("Their comments are shown under the person they name"),
        // TRANSLATORS: lines of a comment that are not shown.
        HiddenLines = L10n.T("Hidden Lines"),
        HiddenLinesSubtitle = L10n.T("Lines matching these patterns are removed from comments"),
        // TRANSLATORS: words a bot puts in front of a person's name, such as a company name.
        NamePrefixes = L10n.T("Name Prefixes"),
        NamePrefixesSubtitle = L10n.T("Removed from the start of authors' names"),
        // TRANSLATORS: button that adds what was typed to a list.
        Add = L10n.T("Add"),
        Remove = L10n.T("Remove"),
        Loading = L10n.T("Loading the spaces"),
        Saving = L10n.T("Saving the account"),
    };

    /// <summary>
    /// jira.SettingsSite: the site of an account; <paramref name="user"/> is
    /// the one account.listSpaces signed in as, null while it is not known.
    /// </summary>
    public static JiraSiteInfo SettingsSite(AccountConfig cfg, SiteUser? user)
    {
        ArgumentNullException.ThrowIfNull(cfg);
        var jc = cfg.Jira;
        var deployment = jc?.Deployment ?? default;
        var address = Clean(jc?.Login);
        if (address.Length == 0)
        {
            address = Clean(cfg.Email);
        }
        var name = "";
        if (user is not null)
        {
            name = Clean(user.Name);
            if (Clean(user.Email) is { Length: > 0 } email)
            {
                address = email;
            }
        }
        return new JiraSiteInfo
        {
            Address = Clean(jc?.SiteUrl),
            Deployment = DeploymentName(deployment),
            TokenLabel = CredentialFields(deployment).TokenLabel,
            User = name.Length == 0 ? address : name,
            UserDetail = name.Length == 0 || address == name ? "" : address,
        };
    }

    /// <summary>
    /// jira.SettingsSpaceRows: the spaces the page chooses from: the spaces
    /// of account.listSpaces in the daemon's order, then the stored ones the
    /// listing lacks (a space the token no longer sees, or every stored
    /// space when the listing failed), so that nothing stored is dropped
    /// unseen.
    /// </summary>
    public static IReadOnlyList<JiraSpaceRow> SettingsSpaceRows(IReadOnlyList<SpaceRef>? stored, IReadOnlyList<Space>? listed)
    {
        var rows = SpaceRows(listed).ToList();
        var seen = new HashSet<string>((listed ?? []).Select(s => s.Id), StringComparer.Ordinal);
        foreach (var r in stored ?? [])
        {
            if (!seen.Add(r.Id))
            {
                continue;
            }
            rows.Add(new JiraSpaceRow { Id = r.Id, Title = SpaceTitle(new Space { Id = r.Id, Key = r.Key, Name = r.Name ?? "" }) });
        }
        return rows;
    }

    /// <summary>
    /// jira.SetSpaceSelected: the chosen spaces after the check box of the
    /// space <paramref name="id"/> changed: <paramref name="selected"/> are
    /// the chosen ones so far, <paramref name="stored"/> and
    /// <paramref name="listed"/> what SettingsSpaceRows shows. The result is
    /// in the order of the rows, with the key and name of the listing where
    /// it has the space.
    /// </summary>
    public static IReadOnlyList<SpaceRef> SetSpaceSelected(
        IReadOnlyList<SpaceRef> selected, IReadOnlyList<SpaceRef>? stored, IReadOnlyList<Space>? listed, string id, bool on)
    {
        ArgumentNullException.ThrowIfNull(selected);
        var chosen = new Dictionary<string, bool>(StringComparer.Ordinal);
        foreach (var r in selected)
        {
            chosen[r.Id] = true;
        }
        chosen[id] = on;
        var output = new List<SpaceRef>();
        var done = new HashSet<string>(StringComparer.Ordinal);
        void Add(SpaceRef r)
        {
            if (chosen.GetValueOrDefault(r.Id) && !done.Contains(r.Id))
            {
                output.Add(r);
            }
            done.Add(r.Id);
        }
        foreach (var s in listed ?? [])
        {
            Add(new SpaceRef { Id = s.Id, Key = s.Key, Name = s.Name.Length == 0 ? null : s.Name });
        }
        foreach (var r in stored ?? [])
        {
            Add(r);
        }
        return output;
    }

    /// <summary>jira.VirtualFolders: the fixed views the page has a switch for, in the order of the sidebar.</summary>
    public static IReadOnlyList<VirtualFolder> VirtualFolders { get; } = [VirtualFolder.AssignedToMe, VirtualFolder.Watching, VirtualFolder.Open];

    /// <summary>jira.FolderShown: whether the view <paramref name="v"/> is shown: it is not among the disabled ones.</summary>
    public static bool FolderShown(IReadOnlyList<VirtualFolder>? disabled, VirtualFolder v) => !(disabled ?? []).Contains(v);

    /// <summary>
    /// jira.SetFolderShown: JiraConfig.DisabledFolders after the switch of the
    /// view <paramref name="v"/> changed: the disabled views in the order of
    /// <see cref="VirtualFolders"/>, each once; empty when every view is shown.
    /// </summary>
    public static IReadOnlyList<VirtualFolder> SetFolderShown(IReadOnlyList<VirtualFolder>? disabled, VirtualFolder v, bool shown) =>
        [.. VirtualFolders.Where(known => known == v ? !shown : !FolderShown(disabled, known))];

    /// <summary>jira.NotificationModes: the choices of what a notification e-mail does, in the order shown.</summary>
    public static IReadOnlyList<NotificationMailMode> NotificationModes { get; } =
        [NotificationMailMode.Sync, NotificationMailMode.Hide, NotificationMailMode.Ignore];

    /// <summary>jira.NotificationModeLabels: the labels of <see cref="NotificationModes"/>, in order.</summary>
    public static IReadOnlyList<string> NotificationModeLabels() =>
    [
        // TRANSLATORS: what a notification e-mail of Jira does: the issue it names is synchronised.
        L10n.T("Check the Issue at Once"),
        // TRANSLATORS: what a notification e-mail of Jira does: the issue is synchronised and the e-mail is not listed.
        L10n.T("Check the Issue and Hide the E-mail"),
        // TRANSLATORS: what a notification e-mail of Jira does: nothing, it is an e-mail like any other.
        L10n.T("Do Nothing"),
    ];

    /// <summary>
    /// jira.IndexOfNotificationMode: the position in
    /// <see cref="NotificationModes"/> shown for JiraConfig.NotificationMail;
    /// the empty mode and one this client does not know are the default,
    /// the first.
    /// </summary>
    public static int IndexOfNotificationMode(NotificationMailMode? m)
    {
        for (var i = 0; i < NotificationModes.Count; i++)
        {
            if (NotificationModes[i] == m)
            {
                return i;
            }
        }
        return 0;
    }

    /// <summary>jira.NotificationHint: the text under the mode: what hiding means; "" for the other modes.</summary>
    public static string NotificationHint(NotificationMailMode m) =>
        m == NotificationMailMode.Hide ? L10n.T("Hidden e-mails stay in your mailbox and come back when you turn this off") : "";

    /// <summary>jira.SendersEditable: whether the senders matter in mode <paramref name="m"/>: not when notification e-mails are left alone.</summary>
    public static bool SendersEditable(NotificationMailMode m) => m != NotificationMailMode.Ignore;

    /// <summary>
    /// jira.DefaultSenders: what an empty list of senders stands for, the
    /// placeholder of its field: every address of the site's host for Jira
    /// Cloud ("@acme.atlassian.net"), "" for Data Center, which has no
    /// default.
    /// </summary>
    public static string DefaultSenders(AccountConfig cfg)
    {
        ArgumentNullException.ThrowIfNull(cfg);
        if (cfg.Jira is null || cfg.Jira.Deployment != JiraDeployment.Cloud)
        {
            return "";
        }
        var host = SiteHost(cfg);
        return host.Length > 0 ? "@" + host : "";
    }

    // jira.statusCategories: the groups of the picker, in order; the last one
    // takes the statuses of any other category.
    private static readonly IssueStatusCategory[] StatusCategories =
        [IssueStatusCategory.Todo, IssueStatusCategory.InProgress, IssueStatusCategory.Done, ""];

    /// <summary>jira.StatusCategoryTitle: the title of a group of the picker.</summary>
    public static string StatusCategoryTitle(IssueStatusCategory c) => c.Value switch
    {
        // TRANSLATORS: a category of issue statuses, as Jira calls it.
        IssueStatusCategory.Todo => L10n.C("status category", "To Do"),
        // TRANSLATORS: a category of issue statuses, as Jira calls it.
        IssueStatusCategory.InProgress => L10n.C("status category", "In Progress"),
        // TRANSLATORS: a category of issue statuses, as Jira calls it.
        IssueStatusCategory.Done => L10n.C("status category", "Done"),
        // TRANSLATORS: the issue statuses that belong to no category.
        _ => L10n.C("status category", "Other"),
    };

    // jira.knownCategory: c when the picker has a group for it, "" otherwise.
    private static IssueStatusCategory KnownCategory(IssueStatusCategory c) =>
        c.Value is IssueStatusCategory.Todo or IssueStatusCategory.InProgress or IssueStatusCategory.Done ? c : "";

    /// <summary>jira.DefaultClosedStatuses: what an empty JiraConfig.ClosedStatuses stands for: the statuses of the category done.</summary>
    public static IReadOnlyList<StatusRef> DefaultClosedStatuses(IReadOnlyList<IssueStatus>? statuses)
    {
        var output = new List<StatusRef>();
        var seen = new HashSet<string>(StringComparer.Ordinal);
        foreach (var s in statuses ?? [])
        {
            if (s.Category != IssueStatusCategory.Done || s.Id.Length == 0 || !seen.Add(s.Id))
            {
                continue;
            }
            output.Add(new StatusRef { Id = s.Id, Name = s.Name.Length == 0 ? null : s.Name });
        }
        return output;
    }

    /// <summary>
    /// jira.StatusGroups: the picker of the closed statuses: the statuses of
    /// the site (account.listSpaces) by category, one choice per name, and in
    /// the last group the stored ones the site does not list (all of them
    /// when the listing failed). <paramref name="closed"/> is
    /// JiraConfig.ClosedStatuses; while it is empty the statuses of the
    /// category done are the selected ones. A group without statuses is left
    /// out.
    /// </summary>
    public static IReadOnlyList<JiraStatusGroup> StatusGroups(IReadOnlyList<IssueStatus>? statuses, IReadOnlyList<StatusRef>? closed)
    {
        var selected = new HashSet<string>((closed ?? []).Select(r => r.Id), StringComparer.Ordinal);
        var byDefault = (closed ?? []).Count == 0;
        var names = StatusCategories.Select(_ => new List<string>()).ToArray();
        var ids = StatusCategories.Select(_ => new List<List<string>>()).ToArray();
        var on = StatusCategories.Select(_ => new List<bool>()).ToArray();
        var index = new Dictionary<string, (int Group, int Choice)>(StringComparer.Ordinal);
        var listed = new HashSet<string>(StringComparer.Ordinal);
        void Add(IssueStatusCategory category, string id, string? name, bool selectedNow)
        {
            if (id.Length == 0 || !listed.Add(id))
            {
                return;
            }
            name = Clean(name);
            if (name.Length == 0)
            {
                name = Clean(id);
            }
            var g = 0;
            for (var i = 0; i < StatusCategories.Length; i++)
            {
                if (StatusCategories[i] == category)
                {
                    g = i;
                }
            }
            var key = category.Value + (char)0 + name;
            if (!index.TryGetValue(key, out var at))
            {
                at = (g, names[g].Count);
                index[key] = at;
                names[g].Add(name);
                ids[g].Add([]);
                on[g].Add(false);
            }
            ids[at.Group][at.Choice].Add(id);
            on[at.Group][at.Choice] = on[at.Group][at.Choice] || selectedNow;
        }
        foreach (var s in statuses ?? [])
        {
            Add(KnownCategory(s.Category), s.Id, s.Name, byDefault ? s.Category == IssueStatusCategory.Done : selected.Contains(s.Id));
        }
        foreach (var r in closed ?? [])
        {
            Add("", r.Id, r.Name, true);
        }
        var groups = new List<JiraStatusGroup>();
        for (var g = 0; g < StatusCategories.Length; g++)
        {
            if (names[g].Count == 0)
            {
                continue;
            }
            var c = StatusCategories[g];
            groups.Add(new JiraStatusGroup
            {
                Category = c,
                Title = StatusCategoryTitle(c),
                Style = StyleOf(c),
                Choices = [.. names[g].Select((n, k) => new JiraStatusChoice { Name = n, Ids = ids[g][k], Selected = on[g][k] })],
            });
        }
        return groups;
    }

    /// <summary>
    /// jira.SetStatusSelected: JiraConfig.ClosedStatuses after the check box
    /// of <paramref name="choice"/> changed. Ticking stores every status of
    /// the name. The result is empty, the default, when it names exactly the
    /// statuses of the category done, and also when nothing is left: an
    /// empty list cannot say "no status is closed", so the default comes
    /// back.
    /// </summary>
    public static IReadOnlyList<StatusRef> SetStatusSelected(
        IReadOnlyList<IssueStatus>? statuses, IReadOnlyList<StatusRef>? closed, JiraStatusChoice choice, bool on)
    {
        ArgumentNullException.ThrowIfNull(choice);
        var current = closed is { Count: > 0 } ? closed : DefaultClosedStatuses(statuses);
        var touched = new HashSet<string>(choice.Ids, StringComparer.Ordinal);
        var output = new List<StatusRef>();
        var have = new HashSet<string>(StringComparer.Ordinal);
        foreach (var r in current)
        {
            if (r.Id.Length == 0 || have.Contains(r.Id) || (touched.Contains(r.Id) && !on))
            {
                continue;
            }
            have.Add(r.Id);
            output.Add(r);
        }
        if (on)
        {
            var names = new Dictionary<string, string>(StringComparer.Ordinal);
            foreach (var s in statuses ?? [])
            {
                names[s.Id] = s.Name;
            }
            foreach (var id in choice.Ids)
            {
                if (id.Length == 0 || !have.Add(id))
                {
                    continue;
                }
                var name = names.TryGetValue(id, out var n) ? n : choice.Name;
                output.Add(new StatusRef { Id = id, Name = name.Length == 0 ? null : name });
            }
        }
        var def = DefaultClosedStatuses(statuses);
        if (def.Count > 0 && def.Count == output.Count && def.All(r => have.Contains(r.Id)))
        {
            return [];
        }
        return output;
    }

    /// <summary>jira.StatusesProblem: why the chosen closed statuses cannot be saved; "" when they can.</summary>
    public static string StatusesProblem(IReadOnlyList<StatusRef>? closed) =>
        (closed ?? []).Count > API.Limits.MaxJiraStatuses
            ? L10n.N("Select at most %d status", "Select at most %d statuses", API.Limits.MaxJiraStatuses, API.Limits.MaxJiraStatuses)
            : "";

    /// <summary>jira.NormaliseEntry: an entry as it is stored: without the spaces around it; a sender in lower case.</summary>
    public static string NormaliseEntry(JiraListKind kind, string raw)
    {
        ArgumentNullException.ThrowIfNull(raw);
        var s = raw.Trim();
        return kind == JiraListKind.Senders ? CodePoints.ToLower(s) : s;
    }

    // jira.entryKey: what two entries of a list are the same by: a pattern
    // and a sender as they are, a name prefix ignoring case, and a bot name
    // the way the daemon compares names (visible text, lower case, every
    // dash a hyphen).
    private static string EntryKey(JiraListKind kind, string entry)
    {
        switch (kind)
        {
            case JiraListKind.BotNames:
                var b = new StringBuilder();
                foreach (var r in CodePoints.ToLower(Clean(entry)).EnumerateRunes())
                {
                    b.Append(r.Value is 0x2010 or 0x2011 or 0x2012 or 0x2013 or 0x2014 or 0x2015 or 0x2212 or 0xFE58 or 0xFE63 or 0xFF0D ? "-" : r.ToString());
                }
                return b.ToString();
            case JiraListKind.AuthorPrefixes:
                return CodePoints.ToLower(Clean(entry));
            default:
                return entry;
        }
    }

    /// <summary>
    /// jira.NormaliseList: a list as it is stored: every entry normalised,
    /// empty ones and repetitions left out; empty when nothing is left.
    /// </summary>
    public static IReadOnlyList<string> NormaliseList(JiraListKind kind, IReadOnlyList<string>? list)
    {
        var output = new List<string>();
        var seen = new HashSet<string>(StringComparer.Ordinal);
        foreach (var raw in list ?? [])
        {
            var entry = NormaliseEntry(kind, raw);
            if (entry.Length > 0 && seen.Add(EntryKey(kind, entry)))
            {
                output.Add(entry);
            }
        }
        return output;
    }

    // jira.validSender: an entry of the senders: a bare address, or "@" and a
    // host name.
    private static bool ValidSender(string s) =>
        s.StartsWith('@') ? ValidHostName(s[1..]) : Fields.ValidateEmail(s) == s;

    // jira.validHostName: a DNS name: labels of letters, digits and inner
    // hyphens, at most 63 bytes each and 253 together.
    private static bool ValidHostName(string h)
    {
        var bytes = Encoding.UTF8.GetBytes(h);
        if (bytes.Length == 0 || bytes.Length > 253)
        {
            return false;
        }
        ReadOnlySpan<byte> span = bytes;
        if (span[^1] == (byte)'.')
        {
            span = span[..^1];
        }
        foreach (var range in span.Split((byte)'.'))
        {
            var label = span[range];
            if (label.IsEmpty || label.Length > 63 || label[0] == (byte)'-' || label[^1] == (byte)'-')
            {
                return false;
            }
            foreach (var c in label)
            {
                if (!(c is (>= (byte)'a' and <= (byte)'z') or (>= (byte)'A' and <= (byte)'Z') or (>= (byte)'0' and <= (byte)'9') or (byte)'-'))
                {
                    return false;
                }
            }
        }
        return true;
    }

    /// <summary>
    /// jira.CheckEntry: checks what the user typed to add to a list that
    /// holds <paramref name="have"/>. Entry is what to add, normalised; it
    /// is "" when there is nothing to add: the field is empty (Problem is ""
    /// too) or the entry cannot be added, and Problem says why under the
    /// field.
    /// </summary>
    public static (string Entry, string Problem) CheckEntry(JiraListKind kind, string raw, IReadOnlyList<string>? have)
    {
        var s = NormaliseEntry(kind, raw);
        if (s.Length == 0)
        {
            return ("", "");
        }
        if (Encoding.UTF8.GetByteCount(s) > API.Limits.MaxJiraPatternBytes)
        {
            return ("", L10n.T("This entry is too long"));
        }
        if (HasControlOrInvalid(s))
        {
            return ("", L10n.T("This entry contains control characters"));
        }
        switch (kind)
        {
            case JiraListKind.BotNames:
                if (Clean(s).EnumerateRunes().Count() < MinBotNameRunes)
                {
                    return ("", L10n.T("A bot name needs at least 3 characters"));
                }
                break;
            case JiraListKind.MetadataFilters:
                if (PatternError(s) is { Length: > 0 } reason)
                {
                    // TRANSLATORS: %s says what is wrong with a regular expression, in English ("missing closing )").
                    return ("", L10n.T("This pattern is not valid: %s", reason));
                }
                break;
            case JiraListKind.AuthorPrefixes:
                if (Clean(s).Length == 0)
                {
                    return ("", L10n.T("This entry contains control characters"));
                }
                break;
            case JiraListKind.Senders:
                if (!ValidSender(s))
                {
                    return ("", L10n.T("Enter an address, or a domain such as @example.org"));
                }
                break;
            default:
                break;
        }
        var key = EntryKey(kind, s);
        var list = have ?? [];
        if (list.Any(other => EntryKey(kind, NormaliseEntry(kind, other)) == key))
        {
            return ("", L10n.T("This entry is already in the list"));
        }
        if (list.Count >= API.Limits.MaxJiraListEntries)
        {
            return ("", L10n.N("The list holds at most %d entry", "The list holds at most %d entries", API.Limits.MaxJiraListEntries, API.Limits.MaxJiraListEntries));
        }
        return (s, "");
    }

    // !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0:
    // a lone surrogate is Go's invalid UTF-8.
    private static bool HasControlOrInvalid(string s)
    {
        for (var i = 0; i < s.Length;)
        {
            if (Rune.DecodeFromUtf16(s.AsSpan(i), out var r, out var used) != OperationStatus.Done || Assistant.IsControl(r.Value))
            {
                return true;
            }
            i += used;
        }
        return false;
    }

    /// <summary>
    /// jira.Suggestions: the entries offered for a list that holds
    /// <paramref name="have"/>: the suggested bot name and the suggested
    /// pattern while their lists lack them and have room.
    /// </summary>
    public static IReadOnlyList<JiraSuggestion> Suggestions(JiraListKind kind, IReadOnlyList<string>? have)
    {
        var value = kind switch
        {
            JiraListKind.BotNames => SuggestedBotName,
            JiraListKind.MetadataFilters => SuggestedMetadataFilter,
            _ => null,
        };
        var list = have ?? [];
        if (value is null || list.Count >= API.Limits.MaxJiraListEntries)
        {
            return [];
        }
        var key = EntryKey(kind, value);
        if (list.Any(other => EntryKey(kind, NormaliseEntry(kind, other)) == key))
        {
            return [];
        }
        // TRANSLATORS: button that adds a suggested entry to a list; %s is the entry, such as the name of a bot.
        return [new JiraSuggestion(value, L10n.T("Add %s", value))];
    }

    /// <summary>jira.NewSettingsForm: the form of an account as it is stored.</summary>
    public static JiraSettingsForm NewSettingsForm(AccountConfig cfg)
    {
        ArgumentNullException.ThrowIfNull(cfg);
        var f = new JiraSettingsForm { Name = cfg.Name };
        if (cfg.Jira is not { } jc)
        {
            return f;
        }
        return f with
        {
            Spaces = [.. jc.Spaces],
            OfflineDays = jc.OfflineDays ?? 0,
            OnlyMine = jc.OnlyMine == true,
            ShowEvents = jc.HideEvents != true,
            DisabledFolders = [.. jc.DisabledFolders ?? []],
            ClosedStatuses = [.. jc.ClosedStatuses ?? []],
            NotificationMail = NotificationModes[IndexOfNotificationMode(jc.NotificationMail)],
            NotificationSenders = [.. jc.NotificationSenders ?? []],
            BotNames = [.. jc.BotNames ?? []],
            MetadataFilters = [.. jc.MetadataFilters ?? []],
            AuthorPrefixes = [.. jc.AuthorPrefixes ?? []],
        };
    }

    /// <summary>
    /// jira.Changed: whether <paramref name="updated"/> differs from
    /// <paramref name="old"/> in what the daemon acts on: the page saves only
    /// then. Two configurations that say the same in different words are
    /// equal: the default written out or left out (the offline window, the
    /// mode), lists that differ in spaces, repetitions or, where the order
    /// means nothing, in order, and the names of spaces and statuses, which
    /// are for display.
    /// </summary>
    public static bool Changed(AccountConfig old, AccountConfig updated) =>
        !string.Equals(JsonCoding.EncodeToString(Compared(old)), JsonCoding.EncodeToString(Compared(updated)), StringComparison.Ordinal);

    // jira.compared: cfg in the form Changed compares; what Go's zero values
    // are is null here.
    private static AccountConfig Compared(AccountConfig cfg)
    {
        ArgumentNullException.ThrowIfNull(cfg);
        cfg = cfg with
        {
            Name = cfg.Name.Trim(),
            Kind = cfg.ProtocolKind,
            DisplayName = string.IsNullOrEmpty(cfg.DisplayName) ? null : cfg.DisplayName,
            SyncIntervalSeconds = (cfg.SyncIntervalSeconds ?? 0) == 0 ? null : cfg.SyncIntervalSeconds,
        };
        if (cfg.Jira is not { } jc)
        {
            return cfg;
        }
        var seen = new HashSet<string>(StringComparer.Ordinal);
        var spaces = jc.Spaces.Where(r => seen.Add(r.Id)).Select(r => new SpaceRef { Id = r.Id, Key = r.Key }).OrderBy(r => r.Id, CodePoints.Comparer).ToArray();
        seen.Clear();
        var statuses = (jc.ClosedStatuses ?? []).Select(r => r.Id.Trim()).Where(id => id.Length > 0 && seen.Add(id))
            .Order(CodePoints.Comparer).Select(id => new StatusRef { Id = id }).ToArray();
        var disabled = (jc.DisabledFolders ?? []).Distinct().OrderBy(v => v.Value, CodePoints.Comparer).ToArray();
        var days = jc.OfflineDays ?? 0;
        return cfg with
        {
            Jira = jc with
            {
                CloudId = string.IsNullOrEmpty(jc.CloudId) ? null : jc.CloudId,
                Login = string.IsNullOrEmpty(jc.Login) ? null : jc.Login,
                Spaces = spaces,
                ClosedStatuses = statuses,
                DisabledFolders = disabled,
                OfflineDays = days <= 0 ? API.Limits.DefaultJiraOfflineDays : days,
                OnlyMine = jc.OnlyMine == true ? true : null,
                HideEvents = jc.HideEvents == true ? true : null,
                NotificationMail = string.IsNullOrEmpty(jc.NotificationMail?.Value) ? NotificationMailMode.Sync : jc.NotificationMail,
                NotificationSenders = Keys(JiraListKind.Senders, jc.NotificationSenders, anyOrder: true),
                BotNames = Keys(JiraListKind.BotNames, jc.BotNames, anyOrder: true),
                MetadataFilters = Keys(JiraListKind.MetadataFilters, jc.MetadataFilters, anyOrder: true),
                // The prefixes are stripped in their order.
                AuthorPrefixes = Keys(JiraListKind.AuthorPrefixes, jc.AuthorPrefixes, anyOrder: false),
            },
        };
    }

    // jira.keys: what the entries of a list are compared by (entryKey), each
    // once, in Go's string order when the order of the list means nothing.
    private static string[] Keys(JiraListKind kind, IReadOnlyList<string>? list, bool anyOrder)
    {
        var output = NormaliseList(kind, list).Select(e => EntryKey(kind, e));
        return anyOrder ? [.. output.Order(CodePoints.Comparer)] : [.. output];
    }
}
