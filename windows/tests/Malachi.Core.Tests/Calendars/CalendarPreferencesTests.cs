// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarPreferencesTests.swift
// (preferenceTexts, statusLine); GTK: ui/internal/calendar/preferences_test.go
// (TestPreferenceTexts, TestPreferencesLayout).

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;
using P = Malachi.Core.Calendars.Cal.Prefs;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarPreferencesTests
{
    private static Calendar Record(bool isDefault = false, string access = CalendarAccess.Owner, string kind = "", Address? owner = null) => new()
    {
        Id = new CalendarId("c"),
        AccountId = new AccountId("a"),
        RemoteId = "c",
        Name = "",
        Color = "",
        Visible = true,
        Access = new CalendarAccess(access),
        Kind = new CalendarKind(kind),
        Owner = owner,
        IsDefault = isDefault,
    };

    [Theory]
    [InlineData(WeekStart.Locale, "System")]
    [InlineData(WeekStart.Monday, "Monday")]
    [InlineData(WeekStart.Sunday, "Sunday")]
    [InlineData(WeekStart.Saturday, "Saturday")]
    [InlineData("tuesday", "System")]
    public void WeekStartTitles(string w, string want)
    {
        Assert.Equal(want, P.WeekStartTitle(new WeekStart(w), F.Env(F.Utc)));
    }

    public static TheoryData<string[], string, string, string> HoursCases => new()
    {
        { ["MO", "TU", "WE", "TH", "FR"], "08:00", "17:00", "Every weekday · 08:00–17:00" },
        { ["WE", "MO", "MO", "2TU"], "09:30", "12:00", "Monday and Wednesday · 09:30–12:00" },
        { ["SU"], "x", "12:00", "Sunday" },
        { [], "", "", "" },
    };

    [Theory]
    [MemberData(nameof(HoursCases))]
    public void WorkingHoursTexts(string[] days, string start, string end, string want)
    {
        Assert.Equal(want, P.WorkingHoursText(new WorkingHours { Days = days, Start = start, End = end }, F.Env(F.Utc)));
    }

    [Theory]
    [InlineData(30, "1 month")]
    [InlineData(90, "3 months")]
    [InlineData(180, "6 months")]
    [InlineData(365, "1 year")]
    [InlineData(730, "2 years")]
    [InlineData(7, "7 days")]
    public void KeepTitles(int days, string want)
    {
        Assert.Equal(want, P.KeepTitle(days));
    }

    public static TheoryData<bool, string, string?, string?, string> NoteCases => new()
    {
        { true, CalendarAccess.Owner, null, null, "default" },
        { false, CalendarAccess.Read, null, null, "read only" },
        { false, CalendarAccess.Write, "Petr", "p@example.org", "shared · Petr" },
        { true, CalendarAccess.Read, null, "p@example.org", "default · shared · p@example.org" },
        { false, CalendarAccess.Owner, null, null, "" },
    };

    [Theory]
    [MemberData(nameof(NoteCases))]
    public void CalendarRowNotes(bool isDefault, string access, string? ownerName, string? ownerAddress, string want)
    {
        var owner = ownerAddress is null ? null : new Address { Name = ownerName, Email = ownerAddress };
        Assert.Equal(want, P.CalendarRowNote(Record(isDefault, access, owner: owner)));
    }

    [Theory]
    [InlineData(CalendarAccess.Owner, CalendarKind.Events, true)]
    [InlineData(CalendarAccess.Write, "", true)]
    [InlineData(CalendarAccess.Read, "", false)]
    [InlineData(CalendarAccess.Owner, CalendarKind.Birthdays, false)]
    [InlineData(CalendarAccess.Owner, CalendarKind.Holidays, false)]
    public void Writable(string access, string kind, bool want)
    {
        Assert.Equal(want, P.Writable(Record(access: access, kind: kind)));
    }

    [Fact]
    public void PreferenceTexts()
    {
        Assert.Equal("Replace Password…", P.ReplacePassword);
        Assert.Equal("Replace the Password?", P.ReplacePasswordTitle);
        Assert.Equal("Reload Event", Cal.Text.ReloadEvent);
        Assert.Equal("Unanswered", Cal.Text.UnansweredRow);
        Assert.Equal("Invitations", Cal.UnansweredHeading);
        Assert.Equal("Invitations", P.InvitationsGroup);
        Assert.Equal("Archive", P.AfterAnswerTitle(AfterAnswer.Archive));
        Assert.Equal("Keep in Inbox", P.AfterAnswerTitle(null));
        Assert.Equal("Keep in Inbox", P.AfterAnswerTitle(new AfterAnswer("")));
        Assert.Equal("Calendar", P.TabTitle);
        Assert.Equal("None", P.NoMailAccount);
        Assert.Equal([WeekStart.Locale, WeekStart.Monday, WeekStart.Sunday, WeekStart.Saturday], P.WeekStarts.Select(w => w.Value));
        Assert.Equal([30, 90, 180, 365, 730], P.KeepChoices);
        Assert.Equal([AfterAnswer.Keep, AfterAnswer.Archive], P.AfterAnswers.Select(a => a.Value));
        // api.DefaultCalendarPreferences.
        var d = Cal.DefaultPreferences;
        Assert.Equal(new WeekStart(WeekStart.Locale), d.WeekStart);
        Assert.Equal(60, d.DefaultDurationMinutes);
        Assert.Equal(15, d.DefaultAlertMinutes);
        Assert.False(d.ShowDeclined);
        Assert.Equal(["MO", "TU", "WE", "TH", "FR"], d.WorkingHours.Days);
        Assert.Equal("08:00", d.WorkingHours.Start);
        Assert.Equal("17:00", d.WorkingHours.End);
    }

    [Fact]
    public void PreferencesLayout()
    {
        var s = Cal.SampleData(F.UtcAt(2026, 10, 14, 12, 0), F.Utc);
        var imap = new Account
        {
            Id = new AccountId("acc_imap"),
            Config = new AccountConfig { Name = "", Email = "", Kind = AccountKind.Imap },
            Enabled = true,
            State = new SyncState { AccountId = new AccountId("acc_imap"), Status = SyncStatus.Idle },
            Capabilities = API.MailCapabilities,
        };
        var got = P.PreferencesLayout([imap, .. s.Accounts]);
        Assert.Equal(5, got.Count);
        Assert.Equal(Cal.PrefsSectionKind.Display, got[0].Kind);
        Assert.Equal(Cal.PrefsSectionKind.Add, got[4].Kind);
        Assert.Equal(5, got[0].Rows.Count);
        var (work, nextcloud) = (got[1], got[3]);
        Assert.Equal(Cal.SampleWork, work.AccountId);
        Assert.Equal(Cal.PrefsRow.Sync, work.Rows[0]);
        Assert.DoesNotContain(Cal.PrefsRow.Remove, work.Rows);
        Assert.Contains(Cal.PrefsRow.AfterAnswer, work.Rows);
        Assert.Equal(new AccountKind(AccountKind.CalDAV), nextcloud.AccountKind);
        Assert.Equal(Cal.PrefsRow.Server, nextcloud.Rows[0]);
        Assert.DoesNotContain(Cal.PrefsRow.Sync, nextcloud.Rows);
        Assert.Equal(Cal.PrefsRow.Remove, nextcloud.Rows[^1]);
        Assert.Contains(Cal.PrefsRow.ReplacePassword, nextcloud.Rows);
        Assert.DoesNotContain(Cal.PrefsRow.Card, nextcloud.Rows);
        var empty = P.PreferencesLayout([imap]);
        Assert.Equal(3, empty.Count);
        Assert.Equal(Cal.PrefsSectionKind.Empty, empty[1].Kind);
        Assert.Equal(Cal.PrefsSectionKind.Add, empty[2].Kind);
        Assert.False(P.ShowsInAccountsTab(s.Accounts[2]));
        Assert.True(P.ShowsInAccountsTab(s.Accounts[0]));
        Assert.True(P.ShowsInAccountsTab(imap));
        Assert.Equal("Display", P.DisplayGroup);
        Assert.Equal("Remove Account", P.RemoveAccount);
        Assert.Equal("No calendars yet.", P.NoCalendarsTitle);
    }

    // Swift's statusLine: the status line, errors and the general texts.
    [Fact]
    public void StatusLine()
    {
        var now = F.At(2026, 10, 14, 12);
        CalendarSyncState St(string s, double? agoSeconds) => new()
        {
            AccountId = new AccountId("a"),
            Status = new CalendarSyncStatus(s),
            Progress = -1,
            LastSync = agoSeconds is { } a ? now.AddSeconds(-a) : null,
        };
        var z = F.Prague;
        Assert.Equal("", Cal.Text.StatusLine([], now, z));
        Assert.Equal("Calendars synced 10 minutes ago", Cal.Text.StatusLine([St(CalendarSyncStatus.Idle, 120), St(CalendarSyncStatus.Idle, 600)], now, z));
        Assert.Equal("Syncing calendars…", Cal.Text.StatusLine([St(CalendarSyncStatus.Idle, 60), St(CalendarSyncStatus.Syncing, null)], now, z));
        Assert.Equal("Calendars are offline", Cal.Text.StatusLine([St(CalendarSyncStatus.Offline, null), St(CalendarSyncStatus.Offline, 60)], now, z));
        Assert.Equal(
            "Calendar access needs a new sign-in",
            Cal.Text.StatusLine([St(CalendarSyncStatus.Syncing, null), St(CalendarSyncStatus.ConsentRequired, null)], now, z));
        Assert.Equal("1 unanswered invitation", Cal.Text.UnansweredCount(1));
        Assert.Equal("3 unanswered invitations", Cal.Text.UnansweredCount(3));
        Assert.Equal("The event no longer exists.", Cal.Text.ErrorText(ErrorCode.EventGone));
        Assert.Null(Cal.Text.ErrorText(ErrorCode.AuthFailed));
        Assert.Equal(Cal.Text.ConsentLine, Cal.Text.ErrorText(ErrorCode.CalendarConsentRequired));
        Assert.Equal("", Cal.Text.EmptyBody(Cal.EmptyKind.Loading));
        Assert.Equal("No Calendars", Cal.Text.EmptyTitle(Cal.EmptyKind.NoAccounts));
        Assert.Equal("Agenda", Cal.Text.ViewTitle(Cal.View.Agenda));
        Assert.Equal("Calendar", Cal.Text.ModeName);
        Assert.Equal("Unanswered", Cal.Text.UnansweredRow);
        Assert.Equal("Invitations", Cal.UnansweredHeading);
    }
}
