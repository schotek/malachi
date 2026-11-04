// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Contacts/ContactBookDetail.swift and
// ContactBook.swift's Strftime; GTK: ui/internal/contacts detail.go
// (FieldKind, Labels, IsKnownLabel, NoLabel, LabelText, phoneLabel,
// DetailLabel, RowKind, DetailRow, Detail, Actions, BirthdayText,
// ValidBirthday, AddressLine, OpenURL, Title, SummaryTitle, ComposeName,
// Initials, firstLetter, DetailOf, ShowMailQuery, CopyText) and text.go
// (Strftime).
//
// The contact's detail (W1): the heading (initials, name, "Title ·
// Company", the source line, Edit or Read Only), the actions (Write Email,
// Show Mail, Copy) and the rows of label and value in a fixed order:
// e-mail addresses, phones, postal addresses, birthday, websites, nickname,
// note. Everything is plain text: a website is opened only when it is http
// or https (OpenUrl, through the client's "Open This Link?"), a phone
// number only copied.
//
// Windows: Go's DetailOf is Detail (Swift's detail(_:format:)); OpenURL is
// OpenUrl. A street of several lines keeps its line breaks in the card; the
// detail writes each line as a part of the address line.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Compose;
using Malachi.Core.I18n;

namespace Malachi.Core.Contacts;

public static partial class ContactBook
{
    /// <summary>
    /// contacts.Strftime: <paramref name="t"/> written with a strftime format
    /// (a msgid already translated) in UTC, the zone the birthdays are built
    /// in. The application passes <see cref="CultureStrftime"/>, the tests
    /// <see cref="EnglishStrftime"/>.
    /// </summary>
    public delegate string Strftime(DateTimeOffset t, string format);

    /// <summary>calendar.EnglishStrftime in UTC: the writer of the tests and the samples.</summary>
    public static Strftime EnglishStrftime { get; } = (t, f) => Cal.EnglishStrftime(t, f, TimeZoneInfo.Utc);

    /// <summary>The user's regional format in UTC (Swift <c>strftime(locale:)</c>).</summary>
    public static Strftime CultureStrftime(CultureInfo culture)
    {
        var w = Cal.CultureStrftime(culture);
        return (t, f) => w(t, f, TimeZoneInfo.Utc);
    }

    /// <summary>contacts.FieldKind: a kind of labelled field of a card.</summary>
    public enum FieldKind
    {
        /// <summary>E-mail addresses.</summary>
        Email,

        /// <summary>Phone numbers.</summary>
        Phone,

        /// <summary>Postal addresses.</summary>
        Address,

        /// <summary>Websites.</summary>
        Url,
    }

    /// <summary>contacts.RowKind: what a detail row shows, in the rows' order.</summary>
    public enum RowKind
    {
        /// <summary>An e-mail address.</summary>
        Email,

        /// <summary>A phone number.</summary>
        Phone,

        /// <summary>A postal address.</summary>
        Address,

        /// <summary>The birthday.</summary>
        Birthday,

        /// <summary>A website.</summary>
        Url,

        /// <summary>The nickname.</summary>
        Nickname,

        /// <summary>The note.</summary>
        Note,
    }

    private static readonly ContactLabel[] PhoneLabels =
        [ContactLabel.Mobile, ContactLabel.Work, ContactLabel.Home, ContactLabel.Main, ContactLabel.Fax, ContactLabel.Pager, ContactLabel.Other];

    private static readonly ContactLabel[] OtherLabels = [ContactLabel.Work, ContactLabel.Home, ContactLabel.Other];

    /// <summary>contacts.Labels: a kind's label choices in the form's picker, in their order; no label is offered after them.</summary>
    public static IReadOnlyList<ContactLabel> Labels(FieldKind k) => k == FieldKind.Phone ? PhoneLabels : OtherLabels;

    /// <summary>contacts.IsKnownLabel: a label of <see cref="Labels"/>; any other non-empty label is the server's own.</summary>
    public static bool IsKnownLabel(FieldKind k, ContactLabel l) => Labels(k).Contains(l);

    /// <summary>contacts.NoLabel: the picker's choice for no label.</summary>
    public static string NoLabel =>
        // TRANSLATORS: the contact form's label choice for an e-mail address, phone or address without a label.
        L10n.T("No Label");

    /// <summary>contacts.LabelText: a label in the form's picker: a known label translated by kind, the server's own cleaned, none <see cref="NoLabel"/>.</summary>
    public static string LabelText(FieldKind k, ContactLabel l)
    {
        if (l.IsNone)
        {
            return NoLabel;
        }
        if (!IsKnownLabel(k, l))
        {
            return Clean(l.Value, API.Limits.MaxContactLabelBytes);
        }
        return k switch
        {
            FieldKind.Email => l.Value switch
            {
                // TRANSLATORS: the label of a work e-mail address in the contact form (lower case, like the other labels).
                ContactLabel.Work => L10n.C("email label", "work"),
                // TRANSLATORS: the label of a personal e-mail address.
                ContactLabel.Home => L10n.C("email label", "home"),
                _ => L10n.C("email label", "other"),
            },
            FieldKind.Phone => PhoneLabel(l),
            FieldKind.Address => l.Value switch
            {
                // TRANSLATORS: the label of a postal address at work.
                ContactLabel.Work => L10n.C("address label", "work"),
                ContactLabel.Home => L10n.C("address label", "home"),
                _ => L10n.C("address label", "other"),
            },
            _ => l.Value switch
            {
                // TRANSLATORS: the label of a work website in the contact form.
                ContactLabel.Work => L10n.C("url label", "work"),
                ContactLabel.Home => L10n.C("url label", "home"),
                _ => L10n.C("url label", "other"),
            },
        };
    }

    private static string PhoneLabel(ContactLabel l) => l.Value switch
    {
        // TRANSLATORS: the label of a mobile phone number.
        ContactLabel.Mobile => L10n.C("phone label", "mobile"),
        ContactLabel.Work => L10n.C("phone label", "work"),
        ContactLabel.Home => L10n.C("phone label", "home"),
        // TRANSLATORS: the label of a company's main phone number.
        ContactLabel.Main => L10n.C("phone label", "main"),
        ContactLabel.Fax => L10n.C("phone label", "fax"),
        ContactLabel.Pager => L10n.C("phone label", "pager"),
        _ => L10n.C("phone label", "other"),
    };

    /// <summary>contacts.DetailLabel: a labelled row's label in the detail ("work email", "mobile", "website"); the server's own label as it is.</summary>
    public static string DetailLabel(FieldKind k, ContactLabel l)
    {
        if (!l.IsNone && !IsKnownLabel(k, l))
        {
            return Clean(l.Value, API.Limits.MaxContactLabelBytes);
        }
        switch (k)
        {
            case FieldKind.Email:
                return l.Value switch
                {
                    // TRANSLATORS: a row label of the contact detail (lower case).
                    ContactLabel.Work => L10n.T("work email"),
                    ContactLabel.Home => L10n.T("home email"),
                    ContactLabel.Other => L10n.T("other email"),
                    // TRANSLATORS: a row label of the contact detail: an e-mail address without a label.
                    _ => L10n.T("email"),
                };
            case FieldKind.Phone:
                // TRANSLATORS: a row label of the contact detail: a phone number without a label.
                return l.IsNone ? L10n.T("phone") : PhoneLabel(l);
            case FieldKind.Address:
                // TRANSLATORS: a row label of the contact detail: a postal address without a label.
                return l.IsNone ? L10n.T("address") : LabelText(k, l);
            default:
                return l.Value switch
                {
                    ContactLabel.Work => L10n.T("work website"),
                    ContactLabel.Home => L10n.T("home website"),
                    // TRANSLATORS: a row label of the contact detail: a web address.
                    _ => L10n.T("website"),
                };
        }
    }

    /// <summary>contacts.DetailRow: one row of the detail, plain text.</summary>
    /// <param name="Kind">What the row shows.</param>
    /// <param name="Label">The row's label.</param>
    /// <param name="Value">The value.</param>
    /// <param name="Address">An e-mail row's address (Write Email to it).</param>
    /// <param name="Url">A website row's address to open (<see cref="OpenUrl"/>); "" when it cannot be opened.</param>
    public sealed record DetailRow(RowKind Kind, string Label, string Value, string Address = "", string Url = "");

    /// <summary>contacts.Actions: the detail's buttons.</summary>
    public sealed record Actions
    {
        /// <summary>The contact has an address.</summary>
        public bool WriteEmail { get; init; }

        /// <summary>With several addresses, the choices (a menu); else empty.</summary>
        public IReadOnlyList<DetailRow> Emails { get; init; } = [];

        /// <summary>The search of Show Mail ("" none: no address).</summary>
        public string ShowMail { get; init; } = "";

        /// <summary>The book can be changed.</summary>
        public bool Edit { get; init; }

        /// <summary>The contact can be deleted.</summary>
        public bool Delete { get; init; }
    }

    /// <summary>contacts.Detail: the detail of a contact.</summary>
    public sealed record ContactDetail
    {
        /// <summary>The name.</summary>
        public string Title { get; init; } = "";

        /// <summary>"Job · Company", "" none.</summary>
        public string Subtitle { get; init; } = "";

        /// <summary>The avatar's letters.</summary>
        public string Initials { get; init; } = "";

        /// <summary>The rows.</summary>
        public IReadOnlyList<DetailRow> Rows { get; init; } = [];

        /// <summary>The Read Only capsule instead of Edit.</summary>
        public bool ReadOnly { get; init; }

        /// <summary>The buttons.</summary>
        public Actions Actions { get; init; } = new();
    }

    /// <summary>contacts.BirthdayText: "14 March 1980", without a year "14 March"; "" for an invalid one.</summary>
    public static string BirthdayText(ContactBirthday b, Strftime format)
    {
        ArgumentNullException.ThrowIfNull(b);
        ArgumentNullException.ThrowIfNull(format);
        if (!ValidBirthday(b))
        {
            return "";
        }
        if (b.Year == 0)
        {
            // TRANSLATORS: strftime format of a birthday without a year, e.g. "14 March".
            // xgettext:no-c-format
            return format(Noon(2000, b.Month, b.Day), L10n.T("%-d %B"));
        }
        // TRANSLATORS: strftime format of a birthday with its year, e.g. "14 March 1980".
        // xgettext:no-c-format
        return format(Noon(b.Year, b.Month, b.Day), L10n.T("%-d %B %Y"));
    }

    // Noon UTC of a proleptic Gregorian date (Go's time.Date(…, 12, 0, 0, 0, time.UTC)).
    private static DateTimeOffset Noon(int year, int month, int day) => new(year, month, day, 12, 0, 0, TimeSpan.Zero);

    /// <summary>contacts.ValidBirthday: a real date (year 0 = unknown; 29 February without a year is valid).</summary>
    public static bool ValidBirthday(ContactBirthday b)
    {
        ArgumentNullException.ThrowIfNull(b);
        if (b.Month is < 1 or > 12 || b.Day < 1 || b.Year is < 0 or > 9999)
        {
            return false;
        }
        var y = b.Year == 0 ? 2000 : b.Year;
        return b.Day <= DateTime.DaysInMonth(y, b.Month);
    }

    /// <summary>
    /// contacts.AddressLine: a postal address on one line: "Dlouhá 12, 110 00
    /// Praha 1, Czechia" (street lines, postcode and city, region, country).
    /// </summary>
    public static string AddressLine(ContactAddress a)
    {
        ArgumentNullException.ThrowIfNull(a);
        const int field = API.Limits.MaxContactFieldBytes;
        var parts = new List<string>();
        foreach (var l in (a.Street ?? "").Split('\n'))
        {
            var c = Clean(l, field);
            if (c.Length > 0)
            {
                parts.Add(c);
            }
        }
        var city = TrimSpace(Clean(a.Postcode, field) + " " + Clean(a.City, field));
        foreach (var p in new[] { city, Clean(a.Region, field), Clean(a.Country, field) })
        {
            if (p.Length > 0)
            {
                parts.Add(p);
            }
        }
        return string.Join(", ", parts);
    }

    /// <summary>
    /// contacts.OpenURL: the address a website row opens: an http or https
    /// URL with a host and no user, a bare host ("example.org/x") as https;
    /// "" anything else.
    /// </summary>
    public static string OpenUrl(string? raw)
    {
        var s = TrimSpace(Clean(raw, API.Limits.MaxContactFieldBytes));
        if (s.Length == 0 || s.Contains(' ', StringComparison.Ordinal) || s.Contains('\t', StringComparison.Ordinal))
        {
            return "";
        }
        if (!s.Contains("://", StringComparison.Ordinal))
        {
            if (s.Contains(':', StringComparison.Ordinal) || s.StartsWith('/') || !s.Contains('.', StringComparison.Ordinal))
            {
                return "";
            }
            s = "https://" + s;
        }
        if (UrlSyntax.Parse(s) is not { } p || p.Scheme is not ("http" or "https") || p.Rest.Host.Length == 0 || p.Rest.HasUserinfo)
        {
            return "";
        }
        // url.String: the scheme as parsed (lower case), the rest as written.
        return p.Scheme + s[s.IndexOf(':', StringComparison.Ordinal)..];
    }

    /// <summary>contacts.Title: a contact's name in a list or heading: the display name, else the structured name, the company, the first address, <see cref="ContactsText.NoName"/>.</summary>
    public static string Title(ContactCard c)
    {
        ArgumentNullException.ThrowIfNull(c);
        var n = Clean(c.DisplayName, API.Limits.MaxContactNameBytes);
        if (n.Length > 0)
        {
            return n;
        }
        var composed = ComposeName(c.Name);
        if (composed.Length > 0)
        {
            return composed;
        }
        var o = Clean(c.Org, API.Limits.MaxContactFieldBytes);
        if (o.Length > 0)
        {
            return o;
        }
        foreach (var e in c.Emails)
        {
            var a = Clean(e.Address, API.Limits.MaxContactFieldBytes);
            if (a.Length > 0)
            {
                return a;
            }
        }
        return ContactsText.NoName;
    }

    /// <summary>contacts.SummaryTitle: a list row's name: the display name, the company, the address, <see cref="ContactsText.NoName"/>.</summary>
    public static string SummaryTitle(ContactSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        foreach (var v in new[] { s.DisplayName, s.Org, s.Email })
        {
            var c = Clean(v, API.Limits.MaxContactFieldBytes);
            if (c.Length > 0)
            {
                return c;
            }
        }
        return ContactsText.NoName;
    }

    /// <summary>contacts.ComposeName: prefix, given, middle, family, suffix.</summary>
    public static string ComposeName(ContactName n)
    {
        ArgumentNullException.ThrowIfNull(n);
        return string.Join(' ', new[] { n.Prefix, n.Given, n.Middle, n.Family, n.Suffix }
            .Select(p => Clean(p, API.Limits.MaxContactNameBytes))
            .Where(p => p.Length > 0));
    }

    /// <summary>
    /// contacts.Initials: the avatar's letters: the first letters of the first
    /// and the last word of <paramref name="name"/> (one for one word), upper
    /// case; else the first letter of the address; "" none.
    /// </summary>
    public static string Initials(string name, string address)
    {
        var words = new List<string>();
        foreach (var w in Fields(Clean(name, API.Limits.MaxContactNameBytes)))
        {
            if (FirstLetter(w) is { } r)
            {
                words.Add(Upper(r));
            }
        }
        return words.Count switch
        {
            0 => FirstLetter(address) is { } r ? Upper(r) : "",
            1 => words[0],
            _ => words[0] + words[^1],
        };
    }

    /// <summary>contacts.DetailOf: the detail of a card; <paramref name="format"/> writes the birthday.</summary>
    public static ContactDetail Detail(ContactCard card, Strftime format)
    {
        ArgumentNullException.ThrowIfNull(card);
        ArgumentNullException.ThrowIfNull(format);
        const int field = API.Limits.MaxContactFieldBytes;
        var title = Title(card);
        var jobTitle = Clean(card.Title, field);
        var org = Clean(card.Org, field);
        var subtitle = jobTitle.Length > 0 && org.Length > 0 ? ContactsText.TwoParts(jobTitle, org) : jobTitle.Length > 0 ? jobTitle : org;
        if (subtitle == title)
        {
            subtitle = "";
        }
        var rows = new List<DetailRow>();
        var emails = new List<DetailRow>();
        var first = "";
        foreach (var e in card.Emails)
        {
            var a = Clean(e.Address, field);
            if (a.Length == 0)
            {
                continue;
            }
            if (first.Length == 0)
            {
                first = a;
            }
            var r = new DetailRow(RowKind.Email, DetailLabel(FieldKind.Email, e.Label), a, Address: a);
            rows.Add(r);
            emails.Add(r);
        }
        var initials = title == ContactsText.NoName ? Initials("", first) : Initials(title, first);
        foreach (var p in card.Phones)
        {
            var n = Clean(p.Number, field);
            if (n.Length > 0)
            {
                rows.Add(new DetailRow(RowKind.Phone, DetailLabel(FieldKind.Phone, p.Label), n));
            }
        }
        foreach (var a in card.Addresses)
        {
            var l = AddressLine(a);
            if (l.Length > 0)
            {
                rows.Add(new DetailRow(RowKind.Address, DetailLabel(FieldKind.Address, a.Label), l));
            }
        }
        if (card.Birthday is { } b && BirthdayText(b, format) is { Length: > 0 } t)
        {
            // TRANSLATORS: a row label of the contact detail.
            rows.Add(new DetailRow(RowKind.Birthday, L10n.T("birthday"), t));
        }
        foreach (var u in card.Urls)
        {
            var v = Clean(u.Url, field);
            if (v.Length > 0)
            {
                rows.Add(new DetailRow(RowKind.Url, DetailLabel(FieldKind.Url, u.Label), v, Url: OpenUrl(v)));
            }
        }
        var nick = Clean(card.Nickname, API.Limits.MaxContactNameBytes);
        if (nick.Length > 0)
        {
            // TRANSLATORS: a row label of the contact detail.
            rows.Add(new DetailRow(RowKind.Nickname, L10n.T("nickname"), nick));
        }
        var note = CleanBlock(card.Note, API.Limits.MaxContactNoteBytes);
        if (note.Length > 0)
        {
            // TRANSLATORS: a row label of the contact detail.
            rows.Add(new DetailRow(RowKind.Note, L10n.T("note"), note));
        }
        return new ContactDetail
        {
            Title = title,
            Subtitle = subtitle,
            Initials = initials,
            Rows = rows,
            ReadOnly = card.ReadOnly,
            Actions = new Actions
            {
                WriteEmail = first.Length > 0,
                Emails = emails.Count < 2 ? [] : emails,
                ShowMail = first.Length > 0 ? ShowMailQuery(first) : "",
                Edit = !card.ReadOnly,
                Delete = !card.ReadOnly,
            },
        };
    }

    /// <summary>
    /// contacts.ShowMailQuery: the search of Show Mail for an address, in the
    /// syntax of search.query: from:address, the value in straight quotes
    /// when it has a space; the quote characters the parser reads („ “ ” ")
    /// dropped and control characters as spaces. "" for an empty address.
    /// </summary>
    public static string ShowMailQuery(string address)
    {
        var mapped = new StringBuilder();
        foreach (var r in (address ?? "").EnumerateRunes())
        {
            if (r.Value is '"' or 0x201E or 0x201C or 0x201D)
            {
                continue;
            }
            mapped.Append(Rune.GetUnicodeCategory(r) == UnicodeCategory.Control ? " " : r.ToString());
        }
        var v = string.Join(' ', Fields(mapped.ToString()));
        if (v.Length == 0)
        {
            return "";
        }
        return v.Contains(' ', StringComparison.Ordinal) ? "from:\"" + v + "\"" : "from:" + v;
    }

    /// <summary>
    /// contacts.CopyText: Copy, the contact as plain text, one line per row:
    /// the name, "Job · Company", then "label: value". Never vCard syntax: it
    /// is for pasting into a message.
    /// </summary>
    public static string CopyText(ContactCard card, Strftime format)
    {
        var d = Detail(card, format);
        var lines = new List<string> { d.Title };
        if (d.Subtitle.Length > 0)
        {
            lines.Add(d.Subtitle);
        }
        lines.AddRange(d.Rows.Select(r => r.Label + ": " + r.Value));
        return string.Join('\n', lines);
    }
}
