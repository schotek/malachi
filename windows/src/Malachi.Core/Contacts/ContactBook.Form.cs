// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Contacts/ContactBookForm.swift; GTK:
// ui/internal/contacts form.go (FieldKey, LabelledValue, Form, Form0,
// NewForm, EditForm, load, snapshot, Changed, Dirty, normal, normalRows,
// trimAddress, emptyAddress, Card, Rebase, Saved, MaxRows, Count, CanAdd,
// NextLabel, AddRow, RemoveRow, LabelChoices, AddRowText, RemoveRowText,
// AddBirthday, Placeholder, AddressPart, AddressPlaceholder, BookLabel,
// NameLabel, EmailLabel, Problem, Validate, GraphMaxEmails, graphProblems,
// ProblemOf, RefusedText, ValidEmail, ValidURL, SplitName).
//
// The contact form (W4), edited on the detail's card and saved to the server
// with Done. The form keeps the card it was opened from (Original) and
// knows which fields the user changed (Changed), so that Done sends a card
// the daemon turns into a patch, a contactConflict reloads the server's
// version under the user's changes (Rebase), and closing an unchanged form
// asks nothing (Dirty). A Microsoft book (Kind graph) has Graph's limits,
// checked before anything is sent; a field the daemon still refuses is
// named by its error (ProblemOf) and marked with RefusedText.
//
// Windows: Go's *Form and Swift's struct are a class here with Clone, as
// the conventions have it (docs/windows-port.md §3.1); the controller keeps
// the open one and the view edits it through
// ContactsDetailController.UpdateForm. Problem.Index is null where Go has
// -1. A street of several lines keeps its line breaks (only its ends are
// trimmed). ProblemOf reads the daemon's data.field
// (ContactFieldData, docs/api.md §4.19) first and Go's message path only
// when the data is absent.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using System.Text.Json;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Compose;
using Malachi.Core.I18n;
using Malachi.Core.Text;

namespace Malachi.Core.Contacts;

public static partial class ContactBook
{
    /// <summary>contacts.GraphMaxEmails: the e-mail addresses a Microsoft contact holds.</summary>
    public const int GraphMaxEmails = 3;

    /// <summary>contacts.FieldKey: a part of the form for dirty tracking, rebase and validation problems.</summary>
    public enum FieldKey
    {
        /// <summary>The given name.</summary>
        Given,

        /// <summary>The family name.</summary>
        Family,

        /// <summary>The middle name.</summary>
        Middle,

        /// <summary>A title before the name.</summary>
        Prefix,

        /// <summary>A title after the name.</summary>
        Suffix,

        /// <summary>The nickname.</summary>
        Nickname,

        /// <summary>The company.</summary>
        Org,

        /// <summary>The job title.</summary>
        Title,

        /// <summary>The e-mail addresses.</summary>
        Emails,

        /// <summary>The phone numbers.</summary>
        Phones,

        /// <summary>The postal addresses.</summary>
        Addresses,

        /// <summary>The websites.</summary>
        Urls,

        /// <summary>The birthday.</summary>
        Birthday,

        /// <summary>The note.</summary>
        Note,
    }

    /// <summary>contacts.AddressPart: a part of a postal address, in the form's order.</summary>
    public enum AddressPart
    {
        /// <summary>The street (several lines).</summary>
        Street,

        /// <summary>The postal code.</summary>
        Postcode,

        /// <summary>The city.</summary>
        City,

        /// <summary>The region.</summary>
        Region,

        /// <summary>The country.</summary>
        Country,
    }

    /// <summary>contacts.LabelledValue: an e-mail address, phone or website row.</summary>
    /// <param name="Label">The row's label.</param>
    /// <param name="Value">What the user typed.</param>
    public sealed record LabelledValue(ContactLabel Label = default, string Value = "");

    /// <summary>contacts.Problem: a reason Done cannot save: the part, the row (null for a part without rows or a whole list) and the sentence.</summary>
    /// <param name="Key">The part.</param>
    /// <param name="Index">The row; null none.</param>
    /// <param name="Text">The sentence.</param>
    public sealed record Problem(FieldKey Key, int? Index, string Text);

    /// <summary>contacts.Form: the state of the contact form.</summary>
    public sealed class Form
    {
        private Snapshot baseline;

        private Form(AddressBookId? book, bool isNew)
        {
            Book = book;
            IsNew = isNew;
            baseline = Take();
        }

        /// <summary>Where a new contact is created (ignored for an update).</summary>
        public AddressBookId? Book { get; set; }

        /// <summary>The given name.</summary>
        public string Given { get; set; } = "";

        /// <summary>The family name.</summary>
        public string Family { get; set; } = "";

        /// <summary>The middle name.</summary>
        public string Middle { get; set; } = "";

        /// <summary>A title before the name.</summary>
        public string Prefix { get; set; } = "";

        /// <summary>A title after the name.</summary>
        public string Suffix { get; set; } = "";

        /// <summary>The nickname.</summary>
        public string Nickname { get; set; } = "";

        /// <summary>The company.</summary>
        public string Org { get; set; } = "";

        /// <summary>The job title.</summary>
        public string Title { get; set; } = "";

        /// <summary>The e-mail address rows.</summary>
        public List<LabelledValue> Emails { get; set; } = [];

        /// <summary>The phone rows.</summary>
        public List<LabelledValue> Phones { get; set; } = [];

        /// <summary>The postal address rows (a street keeps its line breaks).</summary>
        public List<ContactAddress> Addresses { get; set; } = [];

        /// <summary>The website rows.</summary>
        public List<LabelledValue> Urls { get; set; } = [];

        /// <summary>The birthday; null none.</summary>
        public ContactBirthday? Birthday { get; set; }

        /// <summary>The note.</summary>
        public string Note { get; set; } = "";

        /// <summary>The card the form was opened from (empty for a new contact); its display name stays when no name part changed.</summary>
        public ContactCard Original { get; private set; } = new();

        /// <summary>Done creates (<c>contact.create</c> in <see cref="Book"/>).</summary>
        public bool IsNew { get; private set; }

        /// <summary>The kind of <see cref="Book"/> (the controller sets it before <see cref="Validate"/>): a Microsoft book has Graph's limits.</summary>
        public AddressBookKind Kind { get; set; }

        /// <summary>contacts.NewForm: the form of a new contact in <paramref name="book"/>.</summary>
        public static Form New(AddressBookId? book) => new(book, isNew: true);

        /// <summary>contacts.EditForm: the form of an existing card.</summary>
        public static Form Edit(ContactCard card)
        {
            ArgumentNullException.ThrowIfNull(card);
            var f = new Form(card.BookId, isNew: false) { Original = card };
            f.Load(card);
            f.baseline = f.Take();
            return f;
        }

        /// <summary>A copy that changes apart from this one (a snapshot).</summary>
        public Form Clone()
        {
            var f = (Form)MemberwiseClone();
            f.Emails = [.. Emails];
            f.Phones = [.. Phones];
            f.Addresses = [.. Addresses];
            f.Urls = [.. Urls];
            return f;
        }

        /// <summary>contacts.Form.Changed: the parts the user changed since the form opened (or was rebased), compared after trimming, empty rows left out.</summary>
        public IReadOnlyList<FieldKey> Changed
        {
            get
            {
                var cur = Normal(Take());
                var old = Normal(baseline);
                var output = new List<FieldKey>();
                for (var i = 0; i < 8; i++)
                {
                    if (cur.Strings[i] != old.Strings[i])
                    {
                        output.Add((FieldKey)i);
                    }
                }
                if (!cur.Emails.SequenceEqual(old.Emails))
                {
                    output.Add(FieldKey.Emails);
                }
                if (!cur.Phones.SequenceEqual(old.Phones))
                {
                    output.Add(FieldKey.Phones);
                }
                if (!cur.Addresses.SequenceEqual(old.Addresses))
                {
                    output.Add(FieldKey.Addresses);
                }
                if (!cur.Urls.SequenceEqual(old.Urls))
                {
                    output.Add(FieldKey.Urls);
                }
                if (cur.Birthday != old.Birthday)
                {
                    output.Add(FieldKey.Birthday);
                }
                if (cur.Note != old.Note)
                {
                    output.Add(FieldKey.Note);
                }
                return output;
            }
        }

        /// <summary>contacts.Form.Dirty: a change (closing the form asks; Done of an unchanged existing contact sends nothing).</summary>
        public bool Dirty => Changed.Count > 0;

        /// <summary>
        /// contacts.Form.Card: the card Done sends: trimmed, empty rows left
        /// out; the display name composed from the name parts when one of
        /// them changed or the original had none, else the original's.
        /// </summary>
        public ContactCard Card
        {
            get
            {
                var s = Normal(Take());
                var c = new ContactCard
                {
                    Id = Original.Id,
                    BookId = IsNew ? Book ?? new AddressBookId("") : Original.BookId,
                    Revision = Original.Revision,
                    Name = new ContactName
                    {
                        Given = s.Strings[(int)FieldKey.Given],
                        Family = s.Strings[(int)FieldKey.Family],
                        Middle = s.Strings[(int)FieldKey.Middle],
                        Prefix = s.Strings[(int)FieldKey.Prefix],
                        Suffix = s.Strings[(int)FieldKey.Suffix],
                    },
                    Nickname = s.Strings[(int)FieldKey.Nickname],
                    Org = s.Strings[(int)FieldKey.Org],
                    Title = s.Strings[(int)FieldKey.Title],
                    Emails = [.. s.Emails.Select(r => new ContactEmail { Label = r.Label, Address = r.Value })],
                    Phones = [.. s.Phones.Select(r => new ContactPhone { Label = r.Label, Number = r.Value })],
                    Addresses = s.Addresses,
                    Urls = [.. s.Urls.Select(r => new ContactUrl { Label = r.Label, Url = r.Value })],
                    Birthday = s.Birthday,
                    Note = s.Note,
                };
                var nameChanged = Changed.Any(k => k <= FieldKey.Suffix);
                if (!IsNew && !nameChanged && TrimSpace(Original.DisplayName).Length > 0)
                {
                    // Unchanged: the original's name as it was, so the patch
                    // leaves it alone.
                    return c with { Name = Original.Name, DisplayName = Original.DisplayName };
                }
                var display = ComposeName(c.Name);
                if (display.Length == 0)
                {
                    display = c.Org ?? "";
                }
                if (display.Length == 0 && c.Emails.Count > 0)
                {
                    display = c.Emails[0].Address;
                }
                return c with { DisplayName = display };
            }
        }

        /// <summary>
        /// contacts.Form.Rebase: a contactConflict's Reload. The form shows
        /// <paramref name="server"/>, and the parts the user changed keep the
        /// user's values (a list as a whole); afterwards the server's card is
        /// the original, so Done updates it with its revision.
        /// </summary>
        public void Rebase(ContactCard server)
        {
            ArgumentNullException.ThrowIfNull(server);
            var changed = Changed;
            var mine = Clone();
            Original = server;
            Load(server);
            baseline = Take();
            foreach (var k in changed)
            {
                switch (k)
                {
                    case FieldKey.Given: Given = mine.Given; break;
                    case FieldKey.Family: Family = mine.Family; break;
                    case FieldKey.Middle: Middle = mine.Middle; break;
                    case FieldKey.Prefix: Prefix = mine.Prefix; break;
                    case FieldKey.Suffix: Suffix = mine.Suffix; break;
                    case FieldKey.Nickname: Nickname = mine.Nickname; break;
                    case FieldKey.Org: Org = mine.Org; break;
                    case FieldKey.Title: Title = mine.Title; break;
                    case FieldKey.Emails: Emails = mine.Emails; break;
                    case FieldKey.Phones: Phones = mine.Phones; break;
                    case FieldKey.Addresses: Addresses = mine.Addresses; break;
                    case FieldKey.Urls: Urls = mine.Urls; break;
                    case FieldKey.Birthday: Birthday = mine.Birthday; break;
                    case FieldKey.Note: Note = mine.Note; break;
                }
            }
        }

        /// <summary>
        /// contacts.Form.Saved: the answer of Done: the card as sent with its
        /// new id (null keeps it) and revision becomes the original, the form
        /// unchanged.
        /// </summary>
        public void Saved(ContactId? id, string revision)
        {
            var c = Card;
            if (id is { Value.Length: > 0 } newId)
            {
                c = c with { Id = newId };
            }
            Original = c with { Revision = revision ?? "" };
            IsNew = false;
            baseline = Take();
        }

        /// <summary>contacts.Form.Count: the number of rows of a kind.</summary>
        public int Count(FieldKind k) => k switch
        {
            FieldKind.Email => Emails.Count,
            FieldKind.Phone => Phones.Count,
            FieldKind.Address => Addresses.Count,
            _ => Urls.Count,
        };

        /// <summary>contacts.Form.CanAdd: another row of the kind fits.</summary>
        public bool CanAdd(FieldKind k) => Count(k) < MaxRows(k);

        /// <summary>contacts.Form.NextLabel: the label of a new row: the first of <see cref="Labels"/> no row of the kind has, else other.</summary>
        public ContactLabel NextLabel(FieldKind k)
        {
            IEnumerable<ContactLabel> used = k switch
            {
                FieldKind.Email => Emails.Select(r => r.Label),
                FieldKind.Phone => Phones.Select(r => r.Label),
                FieldKind.Address => Addresses.Select(r => r.Label),
                _ => Urls.Select(r => r.Label),
            };
            var set = used.ToHashSet();
            foreach (var l in Labels(k))
            {
                if (!set.Contains(l))
                {
                    return l;
                }
            }
            return ContactLabel.Other;
        }

        /// <summary>contacts.Form.AddRow: appends an empty row of the kind (<see cref="NextLabel"/>) and returns its index; null at the limit.</summary>
        public int? AddRow(FieldKind k)
        {
            if (!CanAdd(k))
            {
                return null;
            }
            var l = NextLabel(k);
            switch (k)
            {
                case FieldKind.Email: Emails.Add(new LabelledValue(l)); break;
                case FieldKind.Phone: Phones.Add(new LabelledValue(l)); break;
                case FieldKind.Address: Addresses.Add(new ContactAddress { Label = l }); break;
                default: Urls.Add(new LabelledValue(l)); break;
            }
            return Count(k) - 1;
        }

        /// <summary>contacts.Form.RemoveRow: an index out of range does nothing.</summary>
        public void RemoveRow(FieldKind k, int i)
        {
            if (i < 0 || i >= Count(k))
            {
                return;
            }
            switch (k)
            {
                case FieldKind.Email: Emails.RemoveAt(i); break;
                case FieldKind.Phone: Phones.RemoveAt(i); break;
                case FieldKind.Address: Addresses.RemoveAt(i); break;
                default: Urls.RemoveAt(i); break;
            }
        }

        /// <summary>
        /// contacts.Form.Validate: the form's problems, none when Done may
        /// save: a name, a company or an address at all; valid e-mail
        /// addresses; websites with no scheme or http/https; a real birthday;
        /// the API's limits; a Microsoft book's limits.
        /// </summary>
        public IReadOnlyList<Problem> Validate()
        {
            var c = Card;
            var output = new List<Problem>();
            if (ComposeName(c.Name).Length == 0 && string.IsNullOrEmpty(c.Org) && c.Emails.Count == 0 && string.IsNullOrEmpty(c.Nickname))
            {
                output.Add(new Problem(FieldKey.Given, null, L10n.T("Enter a name, a company or an email address.")));
            }
            var tooLong = L10n.T("This text is too long.");
            string?[] names = [c.Name.Given, c.Name.Family, c.Name.Middle, c.Name.Prefix, c.Name.Suffix, c.Nickname];
            for (var i = 0; i < names.Length; i++)
            {
                if (Bytes(names[i]) > API.Limits.MaxContactNameBytes)
                {
                    output.Add(new Problem((FieldKey)i, null, tooLong));
                }
            }
            if (Bytes(c.Org) > API.Limits.MaxContactFieldBytes)
            {
                output.Add(new Problem(FieldKey.Org, null, tooLong));
            }
            if (Bytes(c.Title) > API.Limits.MaxContactFieldBytes)
            {
                output.Add(new Problem(FieldKey.Title, null, tooLong));
            }
            for (var i = 0; i < Emails.Count; i++)
            {
                var r = Emails[i];
                var v = TrimSpace(r.Value);
                if (v.Length == 0)
                {
                    continue;
                }
                if (!ValidEmail(v))
                {
                    // TRANSLATORS: %s is what the user typed as an e-mail address.
                    output.Add(new Problem(FieldKey.Emails, i, L10n.T("%s is not a valid email address.", Clean(v, 80))));
                }
                else if (Bytes(v) > API.Limits.MaxContactFieldBytes || Bytes(r.Label.Value) > API.Limits.MaxContactLabelBytes)
                {
                    output.Add(new Problem(FieldKey.Emails, i, tooLong));
                }
            }
            for (var i = 0; i < Phones.Count; i++)
            {
                if (Bytes(TrimSpace(Phones[i].Value)) > API.Limits.MaxContactFieldBytes || Bytes(Phones[i].Label.Value) > API.Limits.MaxContactLabelBytes)
                {
                    output.Add(new Problem(FieldKey.Phones, i, tooLong));
                }
            }
            for (var i = 0; i < Addresses.Count; i++)
            {
                var a = TrimAddress(Addresses[i]);
                if (new[] { a.Street, a.City, a.Region, a.Postcode, a.Country }.Any(s => Bytes(s) > API.Limits.MaxContactFieldBytes))
                {
                    output.Add(new Problem(FieldKey.Addresses, i, tooLong));
                }
            }
            for (var i = 0; i < Urls.Count; i++)
            {
                var v = TrimSpace(Urls[i].Value);
                if (v.Length == 0)
                {
                    continue;
                }
                if (!ValidUrl(v))
                {
                    output.Add(new Problem(FieldKey.Urls, i, L10n.T("A website must be an address starting with http:// or https://.")));
                }
                else if (Bytes(v) > API.Limits.MaxContactFieldBytes)
                {
                    output.Add(new Problem(FieldKey.Urls, i, tooLong));
                }
            }
            if (Birthday is { } b && !ValidBirthday(b))
            {
                output.Add(new Problem(FieldKey.Birthday, null, L10n.T("Enter a valid date.")));
            }
            if (Bytes(c.Note) > API.Limits.MaxContactNoteBytes)
            {
                output.Add(new Problem(FieldKey.Note, null, tooLong));
            }
            if (Kind.Value == AddressBookKind.Graph)
            {
                output.AddRange(GraphProblems());
            }
            return output;
        }

        // contacts.graphProblems: the rows a Microsoft contact cannot hold (at
        // most GraphMaxEmails e-mail addresses, one postal address each of
        // work, home and other, any other label being other, one website);
        // Index is the row of the form, empty rows not counted.
        private List<Problem> GraphProblems()
        {
            var output = new List<Problem>();
            var n = 0;
            for (var i = 0; i < Emails.Count; i++)
            {
                if (TrimSpace(Emails[i].Value).Length == 0)
                {
                    continue;
                }
                if (++n == GraphMaxEmails + 1)
                {
                    output.Add(new Problem(FieldKey.Emails, i, L10n.N(
                        // TRANSLATORS: Microsoft 365 is a brand.
                        "A Microsoft 365 contact has at most %d email address.",
                        "A Microsoft 365 contact has at most %d email addresses.", GraphMaxEmails, GraphMaxEmails)));
                }
            }
            var slots = new HashSet<string>(StringComparer.Ordinal);
            for (var i = 0; i < Addresses.Count; i++)
            {
                var a = Addresses[i];
                if (EmptyAddress(TrimAddress(a)))
                {
                    continue;
                }
                var slot = a.Label.Value is ContactLabel.Work or ContactLabel.Home ? a.Label.Value : ContactLabel.Other;
                if (!slots.Add(slot))
                {
                    output.Add(new Problem(FieldKey.Addresses, i, L10n.T("A Microsoft 365 contact has one work, one home and one other address.")));
                }
            }
            n = 0;
            for (var i = 0; i < Urls.Count; i++)
            {
                if (TrimSpace(Urls[i].Value).Length == 0)
                {
                    continue;
                }
                if (++n == 2)
                {
                    output.Add(new Problem(FieldKey.Urls, i, L10n.T("A Microsoft 365 contact has only one website.")));
                }
            }
            return output;
        }

        private void Load(ContactCard c)
        {
            Given = c.Name.Given ?? "";
            Family = c.Name.Family ?? "";
            Middle = c.Name.Middle ?? "";
            Prefix = c.Name.Prefix ?? "";
            Suffix = c.Name.Suffix ?? "";
            Nickname = c.Nickname ?? "";
            Org = c.Org ?? "";
            Title = c.Title ?? "";
            Note = c.Note ?? "";
            Emails = [.. c.Emails.Select(e => new LabelledValue(e.Label, e.Address))];
            Phones = [.. c.Phones.Select(p => new LabelledValue(p.Label, p.Number))];
            Addresses = [.. c.Addresses];
            Urls = [.. c.Urls.Select(u => new LabelledValue(u.Label, u.Url))];
            Birthday = c.Birthday;
            if (Given.Length == 0 && Family.Length == 0 && Middle.Length == 0 && Prefix.Length == 0 && Suffix.Length == 0
                && c.DisplayName.Length > 0)
            {
                // A card with only a display name (a server's FN): the form
                // shows it split, so that it can be edited.
                (Given, Family) = SplitName(c.DisplayName);
            }
        }

        private Snapshot Take() => new(
            [Given, Family, Middle, Prefix, Suffix, Nickname, Org, Title],
            [.. Emails], [.. Phones], [.. Addresses], [.. Urls], Birthday, Note);

        // contacts.normal: a snapshot as it would be saved: strings trimmed,
        // empty rows dropped.
        private static Snapshot Normal(Snapshot s) => new(
            [.. s.Strings.Select(TrimSpace)],
            NormalRows(s.Emails),
            NormalRows(s.Phones),
            [.. s.Addresses.Select(TrimAddress).Where(a => !EmptyAddress(a))],
            NormalRows(s.Urls),
            s.Birthday,
            TrimSpace(s.Note));

        private static List<LabelledValue> NormalRows(List<LabelledValue> rows) =>
            [.. rows.Select(r => r with { Value = TrimSpace(r.Value) }).Where(r => r.Value.Length > 0)];

        // contacts.Form0: the comparable snapshot of a form's fields.
        private sealed record Snapshot(
            string[] Strings, List<LabelledValue> Emails, List<LabelledValue> Phones, List<ContactAddress> Addresses,
            List<LabelledValue> Urls, ContactBirthday? Birthday, string Note);
    }

    // contacts.trimAddress: each part trimmed at its ends (a street's inner
    // line breaks stay).
    private static ContactAddress TrimAddress(ContactAddress a) => a with
    {
        Street = TrimSpace(a.Street),
        City = TrimSpace(a.City),
        Region = TrimSpace(a.Region),
        Postcode = TrimSpace(a.Postcode),
        Country = TrimSpace(a.Country),
    };

    private static bool EmptyAddress(ContactAddress a) =>
        string.IsNullOrEmpty(a.Street) && string.IsNullOrEmpty(a.City) && string.IsNullOrEmpty(a.Region)
        && string.IsNullOrEmpty(a.Postcode) && string.IsNullOrEmpty(a.Country);

    private static int Bytes(string? s) => Encoding.UTF8.GetByteCount(s ?? "");

    /// <summary>contacts.MaxRows: the limit of rows of a kind (the API's).</summary>
    public static int MaxRows(FieldKind k) => k switch
    {
        FieldKind.Email => API.Limits.MaxContactEmails,
        FieldKind.Phone => API.Limits.MaxContactPhones,
        FieldKind.Address => API.Limits.MaxContactAddresses,
        _ => API.Limits.MaxContactURLs,
    };

    /// <summary>
    /// contacts.LabelChoices: the picker's choices for a row with label
    /// <paramref name="current"/>: <see cref="Labels"/>, no label, and
    /// <paramref name="current"/> when it is the server's own.
    /// </summary>
    public static IReadOnlyList<ContactLabel> LabelChoices(FieldKind k, ContactLabel current)
    {
        var output = new List<ContactLabel>(Labels(k)) { ContactLabel.None };
        if (!current.IsNone && !IsKnownLabel(k, current))
        {
            output.Add(current);
        }
        return output;
    }

    /// <summary>contacts.AddRowText: the button under the rows of a kind.</summary>
    public static string AddRowText(FieldKind k) => k switch
    {
        // TRANSLATORS: the contact form's button that adds an e-mail address row.
        FieldKind.Email => L10n.T("Add Email"),
        FieldKind.Phone => L10n.T("Add Phone"),
        FieldKind.Address => L10n.T("Add Address"),
        _ => L10n.T("Add Website"),
    };

    /// <summary>contacts.RemoveRowText: a row's remove button (tooltip and accessible name).</summary>
    public static string RemoveRowText => L10n.T("Remove");

    /// <summary>contacts.AddBirthday.</summary>
    public static string AddBirthday => L10n.T("Add Birthday");

    /// <summary>contacts.Placeholder: a form field's placeholder ("" for the addresses, which have their parts').</summary>
    public static string Placeholder(FieldKey k) => k switch
    {
        // TRANSLATORS: placeholders of the contact form's fields.
        FieldKey.Given => L10n.T("First Name"),
        FieldKey.Family => L10n.T("Last Name"),
        FieldKey.Middle => L10n.T("Middle Name"),
        // TRANSLATORS: a title before a name, such as "Ing." or "Dr.".
        FieldKey.Prefix => L10n.T("Prefix"),
        // TRANSLATORS: a title after a name, such as "Ph.D." or "Jr.".
        FieldKey.Suffix => L10n.T("Suffix"),
        FieldKey.Nickname => L10n.T("Nickname"),
        FieldKey.Org => L10n.T("Company"),
        FieldKey.Title => L10n.T("Job Title"),
        FieldKey.Emails => L10n.T("Email"),
        FieldKey.Phones => L10n.T("Phone"),
        FieldKey.Urls => L10n.T("Website"),
        FieldKey.Birthday => L10n.T("Birthday"),
        FieldKey.Note => L10n.T("Note"),
        _ => "",
    };

    /// <summary>contacts.AddressPlaceholder.</summary>
    public static string AddressPlaceholder(AddressPart p) => p switch
    {
        AddressPart.Street => L10n.T("Street"),
        AddressPart.Postcode => L10n.T("Postal Code"),
        AddressPart.City => L10n.T("City"),
        // TRANSLATORS: a part of a postal address: state, province or region.
        AddressPart.Region => L10n.T("Region"),
        _ => L10n.T("Country"),
    };

    /// <summary>contacts.BookLabel: the choice of book of a new contact.</summary>
    public static string BookLabel =>
        // TRANSLATORS: the label of the choice where a new contact is saved.
        L10n.T("Address Book");

    /// <summary>contacts.NameLabel: Add to Contacts' name.</summary>
    public static string NameLabel =>
        // TRANSLATORS: the label of a new contact's name in Add to Contacts.
        L10n.T("Name");

    /// <summary>contacts.EmailLabel: Add to Contacts' address.</summary>
    public static string EmailLabel => Placeholder(FieldKey.Emails);

    /// <summary>contacts.RefusedText: the sentence over the field the server refused.</summary>
    public static string RefusedText => L10n.T("The server did not accept the marked detail. Correct it and try again.");

    /// <summary>
    /// contacts.ProblemOf: the field of a card the daemon refused, from its
    /// invalidArgument: the path of <c>data.field</c>
    /// (<see cref="ContactFieldData"/>, relative to the card:
    /// "emails[2].address", "name.given", "emails"), else the path the
    /// message starts with ("card.emails[2].address: …"). The problem marks
    /// that field (null index for a whole list or a field without rows) with
    /// <see cref="RefusedText"/>; null when the error is another or names no
    /// field the form has (the caller then says <see cref="ContactsText.ErrorText"/>).
    /// </summary>
    public static Problem? ProblemOf(Exception? error) => ProblemOf(RpcErrorText.DaemonError(error));

    /// <summary><see cref="ProblemOf(Exception?)"/> of the daemon's error object.</summary>
    public static Problem? ProblemOf(RpcError? e)
    {
        if (e is null || e.Code != ErrorCode.InvalidArgument)
        {
            return null;
        }
        string path;
        if (FieldOf(e) is { Length: > 0 } field)
        {
            path = field.StartsWith("card.", StringComparison.Ordinal) ? field["card.".Length..] : field;
        }
        else
        {
            var colon = e.Message.IndexOf(':', StringComparison.Ordinal);
            var head0 = colon < 0 ? "" : TrimSpace(e.Message[..colon]);
            if (colon < 0 || !head0.StartsWith("card.", StringComparison.Ordinal))
            {
                return null;
            }
            path = head0["card.".Length..];
        }
        var dot = path.IndexOf('.', StringComparison.Ordinal);
        var head = dot < 0 ? path : path[..dot];
        int? index = null;
        var bracket = head.IndexOf('[', StringComparison.Ordinal);
        if (bracket >= 0)
        {
            var rest = head[(bracket + 1)..];
            if (!rest.EndsWith(']') || !int.TryParse(rest[..^1], NumberStyles.None, CultureInfo.InvariantCulture, out var n))
            {
                return null;
            }
            head = head[..bracket];
            index = n;
        }
        FieldKey? key = head switch
        {
            "emails" => FieldKey.Emails,
            "phones" => FieldKey.Phones,
            "addresses" => FieldKey.Addresses,
            "urls" => FieldKey.Urls,
            "nickname" => FieldKey.Nickname,
            "org" => FieldKey.Org,
            "title" => FieldKey.Title,
            "note" => FieldKey.Note,
            "birthday" => FieldKey.Birthday,
            "displayName" => FieldKey.Given,
            "name" => (dot < 0 ? "" : path[(dot + 1)..]) switch
            {
                "given" => FieldKey.Given,
                "family" => FieldKey.Family,
                "middle" => FieldKey.Middle,
                "prefix" => FieldKey.Prefix,
                "suffix" => FieldKey.Suffix,
                _ => null,
            },
            _ => null,
        };
        return key is { } k ? new Problem(k, index, RefusedText) : null;
    }

    // The field of a ContactFieldData; null when the data has another shape.
    private static string? FieldOf(RpcError e)
    {
        if (e.Data is not { ValueKind: JsonValueKind.Object } data)
        {
            return null;
        }
        try
        {
            return data.Deserialize(ApiJsonContext.Wire.ContactFieldData)?.Field;
        }
        catch (JsonException)
        {
            return null;
        }
    }

    /// <summary>contacts.ValidEmail: a bare e-mail address (no name, no angle brackets) as the recipients' fields read one, with a dot in the domain.</summary>
    public static bool ValidEmail(string? s)
    {
        if (string.IsNullOrEmpty(s))
        {
            return false;
        }
        foreach (var r in s.EnumerateRunes())
        {
            if (IsGoSpace(r.Value) || Rune.GetUnicodeCategory(r) == UnicodeCategory.Control || r.Value is '<' or '>' or ',' or ';' or '"')
            {
                return false;
            }
        }
        var at = s.LastIndexOf('@');
        return Cal.IsBareAddress(s) && at >= 0 && s[(at + 1)..].Contains('.', StringComparison.Ordinal);
    }

    /// <summary>contacts.ValidURL: a website the form may save: http or https with a host, or no scheme where <see cref="OpenUrl"/> reads it as https.</summary>
    public static bool ValidUrl(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (s.Contains("://", StringComparison.Ordinal))
        {
            return UrlSyntax.Parse(s) is { } p && p.Scheme is "http" or "https" && p.Rest.Host.Length > 0;
        }
        return OpenUrl(s).Length > 0;
    }

    /// <summary>contacts.SplitName: the last word is the family name ("Petr Svoboda" → "Petr", "Svoboda"); one word is a given name.</summary>
    public static (string Given, string Family) SplitName(string? name)
    {
        var words = Fields(Clean(name, API.Limits.MaxContactNameBytes));
        return words.Count switch
        {
            0 => ("", ""),
            1 => (words[0], ""),
            _ => (string.Join(' ', words.Take(words.Count - 1)), words[^1]),
        };
    }
}
