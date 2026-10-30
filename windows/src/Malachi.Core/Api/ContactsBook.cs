// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/ContactsAPI.swift; Go:
// backend/pkg/api/contacts_book.go (all of it); contract: docs/api.md §2,
// §4.19; design: docs/plans/address-book.md.
//
// Address books and contacts of connected services. The daemon keeps a
// cache of the server's address books (Microsoft Graph contact folders,
// CardDAV collections) and a virtual read-only book of recently used
// addresses; writes go to the server first. Every string of these types
// that comes from a server or a vCard (names, organisations, labels, notes,
// book names) is untrusted display text: cleaned by the daemon, cleaned
// again by ContactBook (Malachi.Core.Contacts) and shown as plain text only
// (TextBlock.Text).
//
// Windows: Go's ContactURL is ContactUrl. The members Go leaves out when
// empty (omitempty) are nullable here and read "" as absent, as elsewhere in
// the mirror; ContactLabel's none is its default value, which a record
// leaves out (WhenWritingDefault), and ContactBirthday.Year 0 likewise. The
// methods are in API (§4.19), the limits in API.Limits, the error data
// readers on ContactsOptions (Controllers).

using System;
using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>api.AddressBookID: an address book (<c>b_…</c>), opaque; <see cref="Recent"/> is the virtual book.</summary>
[JsonConverter(typeof(StringWireValueConverter<AddressBookId>))]
public readonly record struct AddressBookId(string Value) : IOpaqueId<AddressBookId>
{
    /// <summary>api.AddressBookRecent: the virtual, read-only book of recently used addresses.</summary>
    public static readonly AddressBookId Recent = new("recent");

    /// <summary>api.AddressBookAll: in <c>contact.list</c>, every visible book.</summary>
    public static readonly AddressBookId All = new("all");

    /// <summary>The identifier of a wire string.</summary>
    public static implicit operator AddressBookId(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.ContactID: a contact (<c>p_…</c>, or <c>r_…</c> of the recent book, valid for <c>contact.get</c> only), opaque.</summary>
[JsonConverter(typeof(StringWireValueConverter<ContactId>))]
public readonly record struct ContactId(string Value) : IOpaqueId<ContactId>
{
    /// <summary>The identifier of a wire string.</summary>
    public static implicit operator ContactId(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.AddressBookKind: where an address book comes from; an unknown value is a read-only book of an unknown source.</summary>
[JsonConverter(typeof(StringWireValueConverter<AddressBookKind>))]
public readonly record struct AddressBookKind(string Value) : IWireEnumeration<AddressBookKind>
{
    /// <summary>A Microsoft Graph contact folder.</summary>
    public const string Graph = "graph";

    /// <summary>A CardDAV collection of a caldav account.</summary>
    public const string CardDAV = "carddav";

    /// <summary>The virtual book of recently used addresses.</summary>
    public const string Recent = "recent";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator AddressBookKind(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.ContactLabel: the label of an e-mail address, phone number, postal
/// address or website. Open: a value outside the constants is the server's
/// own label, untrusted display text. None ("") is the default value, so
/// <c>new ContactLabel("")</c> equals <c>default</c> and a record leaves it
/// out (Go's omitempty).
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<ContactLabel>))]
public readonly record struct ContactLabel : IWireEnumeration<ContactLabel>
{
    /// <summary>Home.</summary>
    public const string Home = "home";

    /// <summary>Work.</summary>
    public const string Work = "work";

    /// <summary>Other.</summary>
    public const string Other = "other";

    /// <summary>A mobile phone.</summary>
    public const string Mobile = "mobile";

    /// <summary>A fax.</summary>
    public const string Fax = "fax";

    /// <summary>A pager.</summary>
    public const string Pager = "pager";

    /// <summary>A company's main number.</summary>
    public const string Main = "main";

    // Null for none, so that "" and default are one value.
    private readonly string? value;

    /// <summary>The label of a wire string; "" or null is none.</summary>
    public ContactLabel(string? value) => this.value = string.IsNullOrEmpty(value) ? null : value;

    /// <summary>No label.</summary>
    public static ContactLabel None => default;

    /// <summary>The wire string; "" for none.</summary>
    public string Value => value ?? "";

    /// <summary>Whether this is no label.</summary>
    public bool IsNone => value is null;

    /// <summary>The value of a wire string.</summary>
    public static implicit operator ContactLabel(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value;
}

/// <summary>api.ContactSearchScope: which address books <c>contact.search</c> reads (the setting contacts-suggest-scope).</summary>
[JsonConverter(typeof(StringWireValueConverter<ContactSearchScope>))]
public readonly record struct ContactSearchScope(string Value) : IWireEnumeration<ContactSearchScope>
{
    /// <summary>The books of the sending account and the recent book (the default when absent).</summary>
    public const string Account = "account";

    /// <summary>Every visible book of every enabled account.</summary>
    public const string All = "all";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator ContactSearchScope(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

// ----------------------------------------------------------------------
// Accounts
// ----------------------------------------------------------------------

/// <summary>
/// api.ContactsConfig: the contacts part of a graph account
/// (<see cref="GraphConfig.Contacts"/>); a caldav account has
/// <see cref="CalDAVConfig.Contacts"/> instead.
/// </summary>
public sealed record ContactsConfig
{
    /// <summary>Synchronise the account's contact folders; default false.</summary>
    [JsonPropertyName("sync")]
    public bool Sync { get; init; }
}

/// <summary>api.CardDAVAddressBook: an address book collection of a CardDAV server (<c>account.listCalDAVCalendars</c>).</summary>
public sealed record CardDAVAddressBook
{
    /// <summary>The collection's URL.</summary>
    [JsonPropertyName("href")]
    public required string Href { get; init; }

    /// <summary>Untrusted display text.</summary>
    [JsonPropertyName("name")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Name { get; init => field = value ?? ""; } = "";

    /// <summary>The user cannot write to it; false is written as absent.</summary>
    [JsonPropertyName("readOnly")]
    public bool? ReadOnly { get; init => field = value == true ? true : null; }

    /// <summary>Whether <see cref="ReadOnly"/> is set.</summary>
    [JsonIgnore]
    public bool IsReadOnly => ReadOnly == true;
}

// ----------------------------------------------------------------------
// Error data
// ----------------------------------------------------------------------

/// <summary>
/// api.ContactsConsentRequiredData: the <c>data</c> of
/// <see cref="ErrorCode.ContactsConsentRequired"/>. <see cref="SignIn"/>:
/// <c>account.oauthStart</c> with <c>contacts</c> can fix it; false for GNOME
/// Online Accounts.
/// </summary>
public sealed record ContactsConsentRequiredData
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>A sign-in of the daemon can grant the scope.</summary>
    [JsonPropertyName("signIn")]
    public bool SignIn { get; init; }
}

/// <summary>api.ContactConflictData: the <c>data</c> of <see cref="ErrorCode.ContactConflict"/>, the server's current version.</summary>
public sealed record ContactConflictData
{
    /// <summary>The contact as <c>contact.get</c> returns it.</summary>
    [JsonPropertyName("server")]
    public required ContactCard Server { get; init; }
}

// ----------------------------------------------------------------------
// Common types
// ----------------------------------------------------------------------

/// <summary>api.AddressBook: one address book. Its name is untrusted display text.</summary>
public sealed record AddressBook
{
    /// <summary>The book.</summary>
    [JsonPropertyName("id")]
    public required AddressBookId Id { get; init; }

    /// <summary>Its account; null for the recent book.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init => field = value is { Value.Length: > 0 } ? value : null; }

    /// <summary>Untrusted display text; always written.</summary>
    [JsonPropertyName("name")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Name { get; init => field = value ?? ""; } = "";

    /// <summary>Where the book comes from.</summary>
    [JsonPropertyName("kind")]
    public required AddressBookKind Kind { get; init; }

    /// <summary>The user cannot change it (the recent book, a server's read-only collection).</summary>
    [JsonPropertyName("readOnly")]
    public bool ReadOnly { get; init; }

    /// <summary>The book can only be searched, not listed whole (no kind of stage 1 sets it); false is written as absent.</summary>
    [JsonPropertyName("searchOnly")]
    public bool? SearchOnly { get; init => field = value == true ? true : null; }

    /// <summary>Whether <see cref="SearchOnly"/> is set.</summary>
    [JsonIgnore]
    public bool IsSearchOnly => SearchOnly == true;

    /// <summary>The user's choice (<c>addressBook.set</c>); hidden books are neither listed by <c>all</c> nor searched.</summary>
    [JsonPropertyName("visible")]
    public bool Visible { get; init; }

    /// <summary>Its number of contacts.</summary>
    [JsonPropertyName("count")]
    public int Count { get; init; }

    /// <summary>The end of the last synchronisation; null never (the recent book).</summary>
    [JsonPropertyName("lastSyncAt")]
    public DateTimeOffset? LastSyncAt { get; init; }
}

/// <summary>api.ContactSummary: a row of the contact list.</summary>
public sealed record ContactSummary
{
    /// <summary>The contact.</summary>
    [JsonPropertyName("id")]
    public required ContactId Id { get; init; }

    /// <summary>Its book.</summary>
    [JsonPropertyName("bookId")]
    public AddressBookId BookId { get; init => field = value.Value is null ? new AddressBookId("") : value; } = new("");

    /// <summary>Untrusted display text; always written.</summary>
    [JsonPropertyName("displayName")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string DisplayName { get; init => field = value ?? ""; } = "";

    /// <summary>Orders the list (family name first when known), compared by the client's collation.</summary>
    [JsonPropertyName("sortKey")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string SortKey { get; init => field = value ?? ""; } = "";

    /// <summary>The index letter of <see cref="SortKey"/>, upper case, or "#".</summary>
    [JsonPropertyName("section")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Section { get; init => field = value ?? ""; } = "";

    /// <summary>The company; null none.</summary>
    [JsonPropertyName("org")]
    public string? Org { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The first address; null none.</summary>
    [JsonPropertyName("email")]
    public string? Email { get; init => field = string.IsNullOrEmpty(value) ? null : value; }
}

/// <summary>api.ContactName: the structured name (vCard N); null parts are none.</summary>
public sealed record ContactName
{
    /// <summary>The given name.</summary>
    [JsonPropertyName("given")]
    public string? Given { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The family name.</summary>
    [JsonPropertyName("family")]
    public string? Family { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The middle name.</summary>
    [JsonPropertyName("middle")]
    public string? Middle { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>A title before the name.</summary>
    [JsonPropertyName("prefix")]
    public string? Prefix { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>A title after the name.</summary>
    [JsonPropertyName("suffix")]
    public string? Suffix { get; init => field = string.IsNullOrEmpty(value) ? null : value; }
}

/// <summary>api.ContactEmail: an e-mail address with its label.</summary>
public sealed record ContactEmail
{
    /// <summary>The label; none is left out.</summary>
    [JsonPropertyName("label")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingDefault)]
    public ContactLabel Label { get; init; }

    /// <summary>The address.</summary>
    [JsonPropertyName("address")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Address { get; init => field = value ?? ""; } = "";
}

/// <summary>api.ContactPhone: a phone number with its label.</summary>
public sealed record ContactPhone
{
    /// <summary>The label; none is left out.</summary>
    [JsonPropertyName("label")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingDefault)]
    public ContactLabel Label { get; init; }

    /// <summary>The number as the server has it.</summary>
    [JsonPropertyName("number")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Number { get; init => field = value ?? ""; } = "";
}

/// <summary>api.ContactAddress: a postal address with its label; null parts are none.</summary>
public sealed record ContactAddress
{
    /// <summary>The label; none is left out.</summary>
    [JsonPropertyName("label")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingDefault)]
    public ContactLabel Label { get; init; }

    /// <summary>The street lines, separated by line breaks.</summary>
    [JsonPropertyName("street")]
    public string? Street { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The city.</summary>
    [JsonPropertyName("city")]
    public string? City { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The state, province or region.</summary>
    [JsonPropertyName("region")]
    public string? Region { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The postal code.</summary>
    [JsonPropertyName("postcode")]
    public string? Postcode { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The country.</summary>
    [JsonPropertyName("country")]
    public string? Country { get; init => field = string.IsNullOrEmpty(value) ? null : value; }
}

/// <summary>
/// api.ContactURL: a web address with its label; clients open only http and
/// https, through their "Open this link?".
/// </summary>
public sealed record ContactUrl
{
    /// <summary>The label; none is left out.</summary>
    [JsonPropertyName("label")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingDefault)]
    public ContactLabel Label { get; init; }

    /// <summary>The address as the server has it.</summary>
    [JsonPropertyName("url")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Url { get; init => field = value ?? ""; } = "";
}

/// <summary>api.ContactBirthday: <see cref="Year"/> 0 is unknown (left out).</summary>
public sealed record ContactBirthday
{
    /// <summary>The year; 0 unknown.</summary>
    [JsonPropertyName("year")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingDefault)]
    public int Year { get; init; }

    /// <summary>The month, 1–12.</summary>
    [JsonPropertyName("month")]
    public required int Month { get; init; }

    /// <summary>The day of the month.</summary>
    [JsonPropertyName("day")]
    public required int Day { get; init; }
}

/// <summary>
/// api.ContactCard: a whole contact. As input (<c>contact.create</c>,
/// <c>contact.update</c>) <see cref="Id"/>, <see cref="BookId"/>,
/// <see cref="ReadOnly"/> and <see cref="Revision"/> are ignored. The lists
/// are always written.
/// </summary>
public sealed record ContactCard
{
    /// <summary>The contact; "" for a new one.</summary>
    [JsonPropertyName("id")]
    public ContactId Id { get; init => field = value.Value is null ? new ContactId("") : value; } = new("");

    /// <summary>Its book; "" for a new one.</summary>
    [JsonPropertyName("bookId")]
    public AddressBookId BookId { get; init => field = value.Value is null ? new AddressBookId("") : value; } = new("");

    /// <summary>The user cannot change it.</summary>
    [JsonPropertyName("readOnly")]
    public bool ReadOnly { get; init; }

    /// <summary>The server's version (CardDAV ETag, Graph changeKey), opaque; <c>contact.update</c> and <c>contact.delete</c> send it back.</summary>
    [JsonPropertyName("revision")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Revision { get; init => field = value ?? ""; } = "";

    /// <summary>The structured name; always written.</summary>
    [JsonPropertyName("name")]
    public ContactName Name { get; init => field = value ?? new(); } = new();

    /// <summary>The name as shown; always written.</summary>
    [JsonPropertyName("displayName")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string DisplayName { get; init => field = value ?? ""; } = "";

    /// <summary>The nickname; null none.</summary>
    [JsonPropertyName("nickname")]
    public string? Nickname { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The company; null none.</summary>
    [JsonPropertyName("org")]
    public string? Org { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The job title; null none.</summary>
    [JsonPropertyName("title")]
    public string? Title { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The e-mail addresses.</summary>
    [JsonPropertyName("emails")]
    [JsonConverter(typeof(NullAsEmptyListConverter<ContactEmail>))]
    public IReadOnlyList<ContactEmail> Emails { get; init => field = value ?? []; } = [];

    /// <summary>The phone numbers.</summary>
    [JsonPropertyName("phones")]
    [JsonConverter(typeof(NullAsEmptyListConverter<ContactPhone>))]
    public IReadOnlyList<ContactPhone> Phones { get; init => field = value ?? []; } = [];

    /// <summary>The postal addresses.</summary>
    [JsonPropertyName("addresses")]
    [JsonConverter(typeof(NullAsEmptyListConverter<ContactAddress>))]
    public IReadOnlyList<ContactAddress> Addresses { get; init => field = value ?? []; } = [];

    /// <summary>The websites.</summary>
    [JsonPropertyName("urls")]
    [JsonConverter(typeof(NullAsEmptyListConverter<ContactUrl>))]
    public IReadOnlyList<ContactUrl> Urls { get; init => field = value ?? []; } = [];

    /// <summary>The birthday; null none.</summary>
    [JsonPropertyName("birthday")]
    public ContactBirthday? Birthday { get; init; }

    /// <summary>A plain-text note; null none.</summary>
    [JsonPropertyName("note")]
    public string? Note { get; init => field = string.IsNullOrEmpty(value) ? null : value; }
}

// ----------------------------------------------------------------------
// addressBook.* and contact.* (docs/api.md §4.19)
// ----------------------------------------------------------------------

/// <summary>api.AddressBookListParams: <see cref="AccountId"/> null lists every account and the recent book.</summary>
public sealed record AddressBookListParams
{
    /// <summary>One account's books only (then a missing consent is <see cref="ErrorCode.ContactsConsentRequired"/>).</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }
}

/// <summary>api.AddressBookListResult.</summary>
public sealed record AddressBookListResult
{
    /// <summary>The books, hidden ones too; never null.</summary>
    [JsonPropertyName("books")]
    [JsonConverter(typeof(NullAsEmptyListConverter<AddressBook>))]
    public IReadOnlyList<AddressBook> Books { get; init => field = value ?? []; } = [];
}

/// <summary>api.AddressBookSetParams: shows or hides a book.</summary>
public sealed record AddressBookSetParams
{
    /// <summary>The book.</summary>
    [JsonPropertyName("bookId")]
    public required AddressBookId BookId { get; init; }

    /// <summary>The user's choice.</summary>
    [JsonPropertyName("visible")]
    public required bool Visible { get; init; }
}

/// <summary>api.AddressBookSetResult.</summary>
public sealed record AddressBookSetResult
{
    /// <summary>The book as it is now.</summary>
    [JsonPropertyName("book")]
    public required AddressBook Book { get; init; }
}

/// <summary>
/// api.ContactListParams: <see cref="Query"/> filters by name, organisation
/// and address (at most <see cref="API.Limits.MaxContactQueryBytes"/>);
/// <see cref="Limit"/> null is <see cref="API.Limits.DefaultContactListLimit"/>,
/// clamped to <see cref="API.Limits.MaxContactListLimit"/>.
/// </summary>
public sealed record ContactListParams
{
    /// <summary>A book or <see cref="AddressBookId.All"/>.</summary>
    [JsonPropertyName("bookId")]
    public required AddressBookId BookId { get; init; }

    /// <summary>The search; null lists everything.</summary>
    [JsonPropertyName("query")]
    public string? Query { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The first row; null is 0.</summary>
    [JsonPropertyName("offset")]
    public int? Offset { get; init => field = value is 0 ? null : value; }

    /// <summary>How many rows; null is the default.</summary>
    [JsonPropertyName("limit")]
    public int? Limit { get; init => field = value is 0 ? null : value; }
}

/// <summary>api.ContactListResult: the rows by sort key, and how many match.</summary>
public sealed record ContactListResult
{
    /// <summary>The page; never null.</summary>
    [JsonPropertyName("contacts")]
    [JsonConverter(typeof(NullAsEmptyListConverter<ContactSummary>))]
    public IReadOnlyList<ContactSummary> Contacts { get; init => field = value ?? []; } = [];

    /// <summary>The rows of the book and query.</summary>
    [JsonPropertyName("total")]
    public int Total { get; init; }
}

/// <summary>api.ContactGetParams.</summary>
public sealed record ContactGetParams
{
    /// <summary>The contact.</summary>
    [JsonPropertyName("contactId")]
    public required ContactId ContactId { get; init; }
}

/// <summary>api.ContactGetResult.</summary>
public sealed record ContactGetResult
{
    /// <summary>The whole contact.</summary>
    [JsonPropertyName("contact")]
    public required ContactCard Contact { get; init; }
}

/// <summary>api.ContactLookupParams: at most <see cref="API.Limits.MaxContactLookupAddresses"/> addresses.</summary>
public sealed record ContactLookupParams
{
    /// <summary>The addresses.</summary>
    [JsonPropertyName("addresses")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> Addresses { get; init => field = value ?? []; } = [];
}

/// <summary>
/// api.ContactLookupResult: each address with a contact in a visible book
/// (normalised, lower case) to that contact; unknown addresses are absent.
/// </summary>
public sealed record ContactLookupResult
{
    /// <summary>The known addresses; never null.</summary>
    [JsonPropertyName("known")]
    public IReadOnlyDictionary<string, ContactId> Known { get; init => field = value ?? new Dictionary<string, ContactId>(StringComparer.Ordinal); }
        = new Dictionary<string, ContactId>(StringComparer.Ordinal);
}

/// <summary>api.ContactCreateParams.</summary>
public sealed record ContactCreateParams
{
    /// <summary>The book to create in.</summary>
    [JsonPropertyName("bookId")]
    public required AddressBookId BookId { get; init; }

    /// <summary>The new contact.</summary>
    [JsonPropertyName("card")]
    public required ContactCard Card { get; init; }
}

/// <summary>api.ContactCreateResult.</summary>
public sealed record ContactCreateResult
{
    /// <summary>The new contact.</summary>
    [JsonPropertyName("contactId")]
    public required ContactId ContactId { get; init; }

    /// <summary>Its revision.</summary>
    [JsonPropertyName("revision")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Revision { get; init => field = value ?? ""; } = "";
}

/// <summary>api.ContactUpdateParams: <see cref="Revision"/> is that of the last <c>contact.get</c> (else contactConflict).</summary>
public sealed record ContactUpdateParams
{
    /// <summary>The contact.</summary>
    [JsonPropertyName("contactId")]
    public required ContactId ContactId { get; init; }

    /// <summary>The revision the edit started from.</summary>
    [JsonPropertyName("revision")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Revision { get; init => field = value ?? ""; } = "";

    /// <summary>The whole contact as edited.</summary>
    [JsonPropertyName("card")]
    public required ContactCard Card { get; init; }
}

/// <summary>api.ContactUpdateResult.</summary>
public sealed record ContactUpdateResult
{
    /// <summary>The new revision.</summary>
    [JsonPropertyName("revision")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Revision { get; init => field = value ?? ""; } = "";
}

/// <summary>api.ContactDeleteParams; deleting a deleted contact answers <c>{}</c>.</summary>
public sealed record ContactDeleteParams
{
    /// <summary>The contact.</summary>
    [JsonPropertyName("contactId")]
    public required ContactId ContactId { get; init; }

    /// <summary>The revision of the last <c>contact.get</c>.</summary>
    [JsonPropertyName("revision")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Revision { get; init => field = value ?? ""; } = "";
}

/// <summary>
/// api.ContactsChangedNotification (<c>notify.contactsChanged</c>): the
/// contacts of these books changed (a pass, a write, a book added or
/// removed, <c>addressBook.set</c>). <see cref="AccountId"/> null for the
/// recent book; no <see cref="BookIds"/> is any book of the account; absent
/// params are any book.
/// </summary>
public sealed record ContactsChangedNotification
{
    /// <summary>The account; null the recent book (or any).</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init => field = value is { Value.Length: > 0 } ? value : null; }

    /// <summary>The books; empty any book of the account.</summary>
    [JsonPropertyName("bookIds")]
    [JsonConverter(typeof(NullAsEmptyListConverter<AddressBookId>))]
    public IReadOnlyList<AddressBookId> BookIds { get; init => field = value ?? []; } = [];
}

/// <summary>
/// api.ContactFieldData: the <c>data</c> of an invalidArgument from
/// <c>contact.create</c> and <c>contact.update</c> that names the refused
/// field of the card as a path relative to it ("emails[2].address",
/// "name.given", "addresses[0]", "emails" for the whole list). Clients mark
/// that field; the message is not for display.
/// </summary>
public sealed record ContactFieldData
{
    /// <summary>The path of the refused field.</summary>
    [JsonPropertyName("field")]
    [JsonConverter(typeof(NullAsEmptyStringConverter))]
    public string Field { get; init => field = value ?? ""; } = "";
}
