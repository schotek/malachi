// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/Integrations.swift; GTK:
// ui/internal/integrations/integrations.go (ShowsInAccounts,
// ShowsInIntegrations, Split, Page, FullOrder, MailIcon, JiraIcon,
// CalDAVIcon, Service, Catalog, Editor, Row, RowFor, PageTexts, Texts).
//
// The view logic of the two account pages of the preferences: Accounts,
// which lists the mail accounts (kind imap, the empty kind, and graph),
// and Integrations, which lists every other account — the services beside
// the mail, today Jira (kind jira) and CalDAV calendars (kind caldav); a
// kind added later lands there by itself — and the catalogue of services
// that can be added. Both pages show rows of the same shape (Row): the
// switch, the editor and the sentence of the remove confirmation depend on
// the kind. The order of the accounts is one list owned by the daemon; a
// page that reorders its own rows sends the whole list (FullOrder), mail
// accounts first, so account.reorder never has to guess where the other
// page's accounts go.
//
// Windows: Go's package is this static class and its types are nested
// with Go's names (Integrations.Page, Integrations.Service,
// Integrations.Editor, Integrations.Row, Integrations.PageTexts; Go's
// constants PageAccounts, EditorJira, … are the enums' members Accounts,
// Jira, …). Go's Translator is L10n, as in the other ports: every text is
// the GTK msgid, so RowFor, Catalog and Texts take no translator.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Model;

/// <summary>The two account pages of the preferences (Go's package <c>integrations</c>).</summary>
public static class Integrations
{
    /// <summary>integrations.MailIcon: a mail account's icon when its provider has none (GTK name).</summary>
    public const string MailIcon = "mail-unread-symbolic";

    /// <summary>integrations.JiraIcon: a Jira account's (Adwaita has no ticket).</summary>
    public const string JiraIcon = "checkbox-checked-symbolic";

    /// <summary>integrations.CalDAVIcon: a CalDAV account's.</summary>
    public const string CalDAVIcon = "x-office-calendar-symbolic";

    // The brand, never translated.
    private const string JiraName = "Jira";

    /// <summary>integrations.Page: one of the two account pages.</summary>
    public enum Page
    {
        /// <summary>PageAccounts: the mail accounts.</summary>
        Accounts,

        /// <summary>PageIntegrations: every other account.</summary>
        Integrations,
    }

    /// <summary>integrations.Editor: what a row's Edit button opens.</summary>
    public enum Editor
    {
        /// <summary>EditorNone: no Edit button (a kind this client does not know).</summary>
        None,

        /// <summary>EditorMailWizard: the mail account wizard in its edit mode.</summary>
        MailWizard,

        /// <summary>
        /// EditorJira: a Jira account's settings (the token assistant when a
        /// sign-in route asks for the token).
        /// </summary>
        Jira,

        /// <summary>
        /// EditorCalendarPage: the preferences switch to the Calendar page,
        /// where a CalDAV account's server, password, certificate and
        /// calendars are.
        /// </summary>
        CalendarPage,
    }

    /// <summary>integrations.Service: one entry of the catalogue on the Integrations page.</summary>
    /// <param name="Kind">The kind of account it adds.</param>
    /// <param name="Name">The service's name; Jira is a brand, not translated.</param>
    /// <param name="Description">What it brings.</param>
    /// <param name="Icon">A GTK symbolic icon name (IconGlyphs maps it).</param>
    public sealed record Service(AccountKind Kind, string Name, string Description, string Icon);

    /// <summary>
    /// integrations.Row: what an account's row on either page shows besides
    /// its status: plain texts (nothing is markup), the icon, whether it has
    /// the Enabled switch (account.setEnabled pauses the sync; CalDAV has no
    /// pause), what its Edit button opens and is called, and the body of its
    /// remove confirmation.
    /// </summary>
    public sealed record Row
    {
        /// <summary>The account.</summary>
        public required AccountId Id { get; init; }

        /// <summary>Its kind, the empty one resolved.</summary>
        public required AccountKind Kind { get; init; }

        /// <summary>The name, else the Jira site's host, else the address.</summary>
        public required string Title { get; init; }

        /// <summary>The host of the Jira site or the CalDAV server, else the address.</summary>
        public required string Subtitle { get; init; }

        /// <summary>A GTK symbolic icon name.</summary>
        public required string Icon { get; init; }

        /// <summary>Whether the row has the Enabled switch.</summary>
        public required bool OffersSwitch { get; init; }

        /// <summary>What the Edit button opens; <see cref="Editor.None"/> hides it.</summary>
        public required Editor Editor { get; init; }

        /// <summary>The Edit button's tooltip; "" without one.</summary>
        public required string EditLabel { get; init; }

        /// <summary>The body of "Remove this account?".</summary>
        public required string RemoveBody { get; init; }
    }

    /// <summary>integrations.PageTexts: the fixed texts of the Integrations page.</summary>
    /// <param name="Title">The page's title in the preferences' navigation.</param>
    /// <param name="Description">Under the title.</param>
    /// <param name="ConnectedTitle">The group of the added services.</param>
    /// <param name="AvailableTitle">The group of the catalogue.</param>
    /// <param name="EmptyTitle">The connected group's placeholder row.</param>
    /// <param name="EmptySubtitle">Its subtitle.</param>
    /// <param name="Add">A catalogue row's button.</param>
    /// <param name="CalendarSettings">A CalDAV row's Edit tooltip.</param>
    public sealed record PageTexts(
        string Title,
        string Description,
        string ConnectedTitle,
        string AvailableTitle,
        string EmptyTitle,
        string EmptySubtitle,
        string Add,
        string CalendarSettings);

    /// <summary>
    /// integrations.ShowsInAccounts: an account the Accounts page lists, a
    /// mail account (kind imap, also the empty kind, or graph).
    /// </summary>
    public static bool ShowsInAccounts(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var kind = KindOf(a);
        return kind == AccountKind.Imap || kind == AccountKind.Graph;
    }

    /// <summary>
    /// integrations.ShowsInIntegrations: an account the Integrations page
    /// lists, every account that is not a mail account.
    /// </summary>
    public static bool ShowsInIntegrations(Account a) => !ShowsInAccounts(a);

    /// <summary>
    /// Whether <paramref name="page"/> lists <paramref name="a"/> (Windows:
    /// <see cref="ShowsInAccounts"/> or <see cref="ShowsInIntegrations"/> by
    /// page, the filter of AccountsPageController).
    /// </summary>
    public static bool ShowsIn(Page page, Account a) =>
        page == Page.Accounts ? ShowsInAccounts(a) : ShowsInIntegrations(a);

    /// <summary>
    /// integrations.Split: <paramref name="accounts"/> divided into the mail
    /// accounts and the integrations, each in the order of the input.
    /// </summary>
    public static (IReadOnlyList<Account> Mail, IReadOnlyList<Account> Integrations) Split(IEnumerable<Account> accounts)
    {
        ArgumentNullException.ThrowIfNull(accounts);
        var mail = new List<Account>();
        var other = new List<Account>();
        foreach (var a in accounts)
        {
            (ShowsInAccounts(a) ? mail : other).Add(a);
        }
        return (mail, other);
    }

    /// <summary>
    /// integrations.FullOrder: the order account.reorder gets when
    /// <paramref name="page"/> was reordered to <paramref name="order"/>: the
    /// mail accounts first, then the integrations. The reordered page's
    /// accounts follow <paramref name="order"/>; the other page's keep their
    /// order in <paramref name="all"/>. An id of <paramref name="order"/> that
    /// is not in <paramref name="all"/>, or that belongs to the other page, is
    /// ignored, a repeated one counts once, and an account of the page that
    /// <paramref name="order"/> omits follows the listed ones in its order in
    /// <paramref name="all"/>. The result names every account of
    /// <paramref name="all"/> exactly once.
    /// </summary>
    public static IReadOnlyList<AccountId> FullOrder(IEnumerable<Account> all, Page page, IEnumerable<AccountId>? order)
    {
        var (mail, other) = Split(all);
        var wanted = order is null ? new List<AccountId>() : new List<AccountId>(order);
        var output = new List<AccountId>(mail.Count + other.Count);
        Ordered(mail, page == Page.Accounts);
        Ordered(other, page == Page.Integrations);
        return output;

        void Ordered(IReadOnlyList<Account> group, bool reordered)
        {
            if (!reordered)
            {
                foreach (var a in group)
                {
                    output.Add(a.Id);
                }
                return;
            }
            var inGroup = new HashSet<AccountId>();
            foreach (var a in group)
            {
                inGroup.Add(a.Id);
            }
            var seen = new HashSet<AccountId>();
            foreach (var id in wanted)
            {
                if (inGroup.Contains(id) && seen.Add(id))
                {
                    output.Add(id);
                }
            }
            foreach (var a in group)
            {
                if (seen.Add(a.Id))
                {
                    output.Add(a.Id);
                }
            }
        }
    }

    /// <summary>integrations.Catalog: the services that can be added, in the order the page lists them.</summary>
    public static IReadOnlyList<Service> Catalog() =>
    [
        new Service(
            AccountKind.Jira,
            JiraName,
            // TRANSLATORS: Preferences → Integrations: what a Jira
            // account brings ("issues" as in Jira's own word).
            L10n.T("Issues read as mail, comments and status changes"),
            JiraIcon),
        new Service(
            AccountKind.CalDAV,
            // TRANSLATORS: Preferences → Integrations: the name of the
            // service in the list of services that can be added.
            L10n.T("CalDAV Calendar"),
            L10n.T("Nextcloud, iCloud, Fastmail and other CalDAV servers"),
            CalDAVIcon),
    ];

    /// <summary>
    /// integrations.RowFor: the row of account <paramref name="a"/>. Title:
    /// the account name as the user typed it (nothing trimmed), else the Jira
    /// site's host, else the address. Subtitle: the host of the Jira site or
    /// of the CalDAV server, else — and when the title shows the host
    /// already — the address.
    /// </summary>
    public static Row RowFor(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        var kind = KindOf(a);
        var title = TitleOf(a);
        var host = HostOf(a);
        var subtitle = host.Length > 0 && host != title ? host : a.Config.Email;
        string icon;
        Editor editor;
        string editLabel;
        string removeBody;
        var offersSwitch = true;
        switch (kind.Value)
        {
            case AccountKind.Imap:
            case AccountKind.Graph:
                (icon, editor, editLabel) = (MailIcon, Editor.MailWizard, L10n.T("Edit Account"));
                // TRANSLATORS: %s is the account's e-mail address.
                removeBody = L10n.T("%s will be removed from Malachi Mail. Mail on the server is not affected.", a.Config.Email);
                break;
            case AccountKind.Jira:
                (icon, editor, editLabel) = (JiraIcon, Editor.Jira, L10n.T("Edit Account"));
                // TRANSLATORS: %s is the account's name (or its site's host).
                removeBody = L10n.T("%s will be removed from Malachi Mail. Issues on the Jira site are not affected.", title);
                break;
            case AccountKind.CalDAV:
                (icon, editor, editLabel) = (CalDAVIcon, Editor.CalendarPage, CalendarSettings());
                offersSwitch = false;
                // TRANSLATORS: %s is the calendar account's name.
                removeBody = L10n.T("%s will be removed from Malachi Mail. Calendars on the server are not affected.", title);
                break;
            default:
                (icon, editor, editLabel) = (MailIcon, Editor.None, "");
                // TRANSLATORS: %s is the account's name.
                removeBody = L10n.T("%s will be removed from Malachi Mail.", title);
                break;
        }
        return new Row
        {
            Id = a.Id,
            Kind = kind,
            Title = title,
            Subtitle = subtitle,
            Icon = icon,
            OffersSwitch = offersSwitch,
            Editor = editor,
            EditLabel = editLabel,
            RemoveBody = removeBody,
        };
    }

    /// <summary>integrations.Texts: the Integrations page's texts.</summary>
    public static PageTexts Texts() => new(
        // TRANSLATORS: a page of the preferences: services beside the
        // mail, such as Jira and CalDAV calendars.
        Title: L10n.T("Integrations"),
        Description: L10n.T("Services beside your mail: issue trackers and calendars"),
        ConnectedTitle: L10n.T("Connected Services"),
        AvailableTitle: L10n.T("Available Services"),
        EmptyTitle: L10n.T("No integrations yet"),
        EmptySubtitle: L10n.T("Add one from the list below"),
        // TRANSLATORS: the button of a service in Preferences →
        // Integrations → Available Services.
        Add: L10n.T("Add…"),
        CalendarSettings: CalendarSettings());

    // calendarSettings: what a CalDAV row's Edit opens.
    private static string CalendarSettings() =>
        // TRANSLATORS: tooltip of a CalDAV account's Edit button in
        // Preferences → Integrations; it opens the Calendar page.
        L10n.T("Calendar Settings");

    // api.AccountConfig.Protocol: the kind, imap when absent or empty.
    private static AccountKind KindOf(Account a)
    {
        var kind = a.Config.ProtocolKind;
        return string.IsNullOrEmpty(kind.Value) ? new AccountKind(AccountKind.Imap) : kind;
    }

    // title: the name, the Jira site's host, the address.
    private static string TitleOf(Account a)
    {
        if (a.Config.Name.Length > 0)
        {
            return a.Config.Name;
        }
        var host = Jira.SiteHost(a.Config);
        return host.Length > 0 ? host : a.Config.Email;
    }

    // host: a Jira account's site, a CalDAV account's server; "" for
    // anything else.
    private static string HostOf(Account a)
    {
        var kind = KindOf(a);
        if (kind == AccountKind.Jira)
        {
            return Jira.SiteHost(a.Config);
        }
        if (kind != AccountKind.CalDAV || a.Config.CalDAV is not { } caldav || UrlSyntax.Parse(caldav.ServerUrl.Trim()) is not { } u)
        {
            return "";
        }
        return Jira.Clean(CodePoints.ToLower(UrlSyntax.SplitHostPort(u.Rest.Host).Host));
    }
}
