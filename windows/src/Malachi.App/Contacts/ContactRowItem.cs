// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the rows of macos/Sources/MalachiMail/Contacts/ContactsListViewController.swift
// (Item.contact, Item.header); GTK: contacts_window.go renderList (a row of
// the avatar, the name and the company or address; a heading per section).
// What the Contacts window's list shows: ContactBook.Row's contacts as items
// (the name, the company or the address, Core's text, already cleaned) in
// groups by ContactBook.Row's headings, which the ListView's grouping keeps
// out of the selection and of the arrow keys; while searching the rows are
// flat (no headings). The avatar shows the contact's photo (PhotoRef,
// docs/plans/address-book.md F9) when the summary reports one.

using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Controllers;

namespace Malachi.App.Contacts;

/// <summary>A contact's row of the Contacts window's list.</summary>
public sealed class ContactRowItem
{
    /// <summary>The row of <paramref name="row"/> (a contact's row of ContactBook.Rows).</summary>
    public ContactRowItem(ContactBook.Row row)
    {
        Contact = row.Contact!;
        Title = row.Title;
        Detail = row.Detail;
    }

    /// <summary>The contact.</summary>
    public ContactSummary Contact { get; }

    /// <summary>Its id.</summary>
    public ContactId Id => Contact.Id;

    /// <summary>The name.</summary>
    public string Title { get; }

    /// <summary>The contact with its photo state, for the avatar's photo.</summary>
    public ContactsPhotosController.ContactRef? PhotoRef => new ContactsPhotosController.ContactRef(Contact.Id, Contact.BookId, Contact.Photo);

    /// <summary>The company, else the address ("" none).</summary>
    public string Detail { get; }

    /// <summary>The screen reader's name of the row ("Jana Nováková · Stavby Nová s.r.o.").</summary>
    public string Spoken => Detail.Length == 0 ? Title : ContactsText.TwoParts(Title, Detail);
}

/// <summary>A letter's section of the list.</summary>
public sealed class ContactsListGroup : List<ContactRowItem>
{
    /// <summary>The section <paramref name="key"/> ("A", "#").</summary>
    public ContactsListGroup(string key) => Key = key;

    /// <summary>The heading.</summary>
    public string Key { get; }
}
