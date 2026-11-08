// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/updates/updates.go (active, BannerFor, PromptFor,
// NotesFor, AboutLine, CheckNow, CanCheckNow, PrefsGroup, PrefsTitle,
// PrefsSubtitle, WizardNote, OpenableURL); Swift:
// macos/Sources/MalachiCore/Updates/ (written at the same time).
//
// The view logic of the update check (docs/plans/update-check.md): the
// banner in the main window, the question a security release asks once,
// the What's New dialog, the status line and Check Now of Preferences →
// General (and About), and the texts of the preference and the first-run
// note. The daemon checks and decides what is offered (update.*,
// docs/api.md §4.20); this only turns an UpdateStatus into texts and small
// view models, one to one with the Go package. Go's Translator is L10n here:
// every text goes through it with the GTK msgid.
//
// The flow every client follows (the Go package's comment):
// - at start and on every notify.updateChanged, call update.status with
//   its locale and keep the result (Controllers/UpdateStatusController);
// - show BannerFor(status, now) in the main window only;
// - when PromptFor(status) is not null, ask it once (download, then quit)
//   and call update.dismiss {version, "prompted"} whatever the answer;
// - Download opens Available.Url in the browser (OpenableUrl), What's New
//   shows NotesFor, Later and Skip call update.dismiss, a notice's Close
//   calls update.dismissNotice;
// - Preferences → General shows AboutLine and Check Now (update.check).
//
// The class is UpdateCheck, not Updates: a class named like its namespace
// would hide it (as Board in Boards, Cal in Calendars). Go's "(value, ok)"
// result of OpenableURL is a nullable string, as BulkMail.OpenableUrl's.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Compose;
using Malachi.Core.I18n;

namespace Malachi.Core.Updates;

/// <summary>ui/internal/updates: a namespace of functions, so that the Go names map 1:1.</summary>
public static partial class UpdateCheck
{
    /// <summary>The longest URL a browser is given (updates.OpenableURL).</summary>
    public const int MaxUrlBytes = 2048;

    // updates.downloadHosts: the hosts a package or page may be on
    // (docs/services.md §2.3); the daemon checked them already.
    private static readonly string[] DownloadHosts = ["github.com", "malachimail.app"];

    /// <summary>
    /// updates.BannerFor: the banner of <paramref name="s"/> at
    /// <paramref name="now"/>: an offered version that the user has not
    /// skipped or put off, else a notice not yet dismissed, else none. A
    /// security release is never skipped or put off.
    /// </summary>
    public static UpdateBanner BannerFor(UpdateStatus? s, DateTimeOffset now)
    {
        if (!Active(s))
        {
            return new UpdateBanner();
        }
        if (s.Available is { } a)
        {
            var url = OpenableUrl(a.Url) ?? "";
            if (a.Security)
            {
                var buttons = new List<BannerButton>();
                if (url.Length > 0)
                {
                    buttons.Add(new BannerButton(BannerAction.Download, L10n.T("Download")));
                }
                buttons.Add(new BannerButton(BannerAction.WhatsNew, L10n.T("What's New")));
                return new UpdateBanner
                {
                    Kind = BannerKind.Security,
                    // TRANSLATORS: %s is a version number such as 0.2.1
                    Title = L10n.T("Malachi Mail %s fixes a security problem. Please update.", a.Version),
                    Version = a.Version,
                    Url = url,
                    Buttons = buttons,
                };
            }
            var putOff = a.RemindAfter is { } remind && now < remind;
            if (!a.Skipped && !putOff)
            {
                var buttons = new List<BannerButton>();
                if (url.Length > 0)
                {
                    buttons.Add(new BannerButton(BannerAction.Download, L10n.T("Download")));
                }
                buttons.Add(new BannerButton(BannerAction.WhatsNew, L10n.T("What's New")));
                buttons.Add(new BannerButton(BannerAction.Later, L10n.T("Remind Me Later")));
                buttons.Add(new BannerButton(BannerAction.Skip, L10n.T("Skip This Version")));
                return new UpdateBanner
                {
                    Kind = BannerKind.Update,
                    // TRANSLATORS: %s is a version number such as 0.2.1
                    Title = L10n.T("Malachi Mail %s is available.", a.Version),
                    Version = a.Version,
                    Url = url,
                    Buttons = buttons,
                };
            }
        }
        if (s.Notice is { } n && n.Text.Trim().Length > 0)
        {
            var buttons = new List<BannerButton>();
            var page = OpenableUrl(n.Url);
            if (page is not null)
            {
                buttons.Add(new BannerButton(BannerAction.MoreInfo, L10n.T("More Information")));
            }
            buttons.Add(new BannerButton(BannerAction.Close, L10n.T("Close")));
            return new UpdateBanner
            {
                Kind = BannerKind.Notice,
                Title = n.Text,
                NoticeId = n.Id,
                Url = page ?? "",
                Buttons = buttons,
            };
        }
        return new UpdateBanner();
    }

    /// <summary>
    /// updates.PromptFor: the question to ask, a security release no client
    /// has asked about yet (<see cref="UpdateInfo.Prompt"/>) with a package to
    /// open; null: none.
    /// </summary>
    public static UpdatePrompt? PromptFor(UpdateStatus? s)
    {
        if (!Active(s) || s.Available is not { Security: true, Prompt: true } a)
        {
            return null;
        }
        if (OpenableUrl(a.Url) is not { } u)
        {
            return null;
        }
        return new UpdatePrompt
        {
            Version = a.Version,
            Url = u,
            Title = L10n.T("Security Update Available"),
            // TRANSLATORS: %s is a version number such as 0.2.1
            Body = L10n.T("Malachi Mail %s fixes a security problem. Download the installer and install it once Malachi Mail has quit.", a.Version),
            Download = L10n.T("Download"),
            Cancel = L10n.T("Cancel"),
            Quit = L10n.T("Quit Malachi Mail after the download starts"),
        };
    }

    /// <summary>
    /// updates.NotesFor: the What's New dialog of the offered version;
    /// <paramref name="date"/> formats the release date in the user's
    /// locale. Null: nothing is offered.
    /// </summary>
    public static UpdateNotes? NotesFor(UpdateStatus? s, Func<DateTimeOffset, string> date)
    {
        ArgumentNullException.ThrowIfNull(date);
        if (!Active(s) || s.Available is not { } a)
        {
            return null;
        }
        return new UpdateNotes
        {
            // TRANSLATORS: %s is a version number such as 0.2.1
            Title = L10n.T("What's New in Malachi Mail %s", a.Version),
            // TRANSLATORS: %s is a date
            Subtitle = L10n.T("Released %s", date(a.PublishedAt)),
            Body = a.Notes.Trim(),
            Blocks = NotesBlocks(a.Notes.Trim()),
            Download = L10n.T("Download"),
            MoreInfo = L10n.T("More Information"),
            Close = L10n.T("Close"),
            Url = OpenableUrl(a.Url) ?? "",
            InfoUrl = NewsUrl,
        };
    }

    /// <summary>
    /// updates.NewsURL: the page More Information opens, the project's news
    /// (rather than the release's infoUrl).
    /// </summary>
    public const string NewsUrl = "https://malachimail.app/news.html";

    /// <summary>updates.NotesBlocks: notes read as Markdown, links as plain text.</summary>
    public static IReadOnlyList<MarkdownBlock> NotesBlocks(string notes) =>
        [.. Assistant.Markdown(notes).Select(b => b with { Spans = [.. b.Spans.Select(s => s with { Link = "" })] })];

    /// <summary>
    /// updates.AboutLine: the status line (Preferences → General, About);
    /// <paramref name="date"/> formats the time of the last check.
    /// </summary>
    public static string AboutLine(UpdateStatus? s, Func<DateTimeOffset, string> date)
    {
        ArgumentNullException.ThrowIfNull(date);
        if (s is null)
        {
            return "";
        }
        switch (s.State.Value)
        {
            case UpdateState.Disabled:
                return L10n.T("Automatic update checks are off.");
            case UpdateState.Unsupported:
                return L10n.T("This build does not check for updates.");
            case UpdateState.Checking:
                return L10n.T("Checking for updates…");
            case UpdateState.Available:
                if (s.Available is { } a)
                {
                    // TRANSLATORS: %s is a version number such as 0.2.1
                    return L10n.T("Malachi Mail %s is available.", a.Version);
                }
                break;
            case UpdateState.Failed:
                return L10n.T("Could not check for updates.");
            case UpdateState.UpToDate:
                if (s.CheckedAt is { } at)
                {
                    // TRANSLATORS: %s is the date and time of the last check
                    return L10n.T("You are using the latest version (checked %s).", date(at));
                }
                return L10n.T("You are using the latest version.");
        }
        return L10n.T("Not checked for updates yet.");
    }

    /// <summary>updates.CheckNow: the label of the Check Now button.</summary>
    public static string CheckNow() => L10n.T("Check Now");

    /// <summary>updates.CanCheckNow: Check Now is enabled (the check is on, the build has a channel, no check is running).</summary>
    public static bool CanCheckNow(UpdateStatus? s) => Active(s) && s.State.Value != UpdateState.Checking;

    /// <summary>updates.PrefsGroup: the group of Preferences → General.</summary>
    public static string PrefsGroup() => L10n.T("Updates");

    /// <summary>updates.PrefsTitle: the switch.</summary>
    public static string PrefsTitle() => L10n.T("Check for Updates Automatically");

    /// <summary>updates.PrefsSubtitle: the switch's explanation.</summary>
    public static string PrefsSubtitle() =>
        L10n.T("Once a day, Malachi Mail asks its website which versions exist. It sends only its version number: nothing about you, your mail or your computer.");

    /// <summary>updates.WizardNote: the sentence of the first-run page (No Accounts).</summary>
    public static string WizardNote() =>
        L10n.T("Malachi Mail checks once a day whether a new version exists and sends only its version number. You can turn this off in Preferences.");

    /// <summary>
    /// updates.OpenableURL: <paramref name="raw"/> when it is an https URL on
    /// github.com or malachimail.app, exactly so (the daemon's rule, checked
    /// again before a browser opens it); null otherwise.
    /// </summary>
    public static string? OpenableUrl(string? raw)
    {
        if (string.IsNullOrEmpty(raw) || !raw.StartsWith("https://", StringComparison.Ordinal) || raw.AsSpan().IndexOfAny('#', '\\') >= 0)
        {
            return null;
        }
        foreach (var c in raw)
        {
            if (c <= 0x20 || c >= 0x7f)
            {
                return null;
            }
        }
        // Every character is ASCII now: its length is Go's len.
        if (raw.Length > MaxUrlBytes)
        {
            return null;
        }
        if (UrlSyntax.Parse(raw) is not { Scheme: "https" } u || u.Rest.HasUserinfo || !u.Rest.Opaque.IsEmpty)
        {
            return null;
        }
        foreach (var h in DownloadHosts)
        {
            if (u.Rest.Host == h)
            {
                return raw;
            }
        }
        return null;
    }

    // updates.active: the check is on and the build has a channel.
    private static bool Active([System.Diagnostics.CodeAnalysis.NotNullWhen(true)] UpdateStatus? s) =>
        s is not null && s.Enabled && s.State.Value != UpdateState.Disabled && s.State.Value != UpdateState.Unsupported;
}
