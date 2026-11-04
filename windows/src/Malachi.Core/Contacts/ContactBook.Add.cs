// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Contacts/ContactBookAdd.swift; GTK:
// ui/internal/contacts add.go (BookChoice, BookChoices, DefaultBook,
// AddFromChip, NewAddFromChip, CanAdd, Form, ChipItem, ChipItemFor,
// ChipItemText).
//
// Add to Contacts… of an address chip (W4, A12): a popover with the name
// and the address of the header prefilled — the name selected and
// SenderNameWarning under it, because the sender wrote it — and the book to
// save in. Nothing is saved before Add; More Details… opens the whole form
// with the same values. Show Contact is offered only for an address
// contact.lookup knows exactly, never by name.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Contacts;

public static partial class ContactBook
{
    /// <summary>contacts.ChipItem: the address chip's contacts item.</summary>
    public enum ChipItem
    {
        /// <summary>Nothing (no book can be written and none knows the address, or the lookup has not answered).</summary>
        None,

        /// <summary>Show Contact (<c>contact.lookup</c> knows the address).</summary>
        Show,

        /// <summary>Add to Contacts… (a writable book exists).</summary>
        Add,
    }

    /// <summary>contacts.BookChoice: a book in a choice of books.</summary>
    /// <param name="Book">The book.</param>
    /// <param name="Text">"Contacts (jana@firma.cz)" (<see cref="BookChoiceText"/>).</param>
    public sealed record BookChoice(AddressBookId Book, string Text);

    /// <summary>contacts.BookChoices: the books a new contact can be created in (<see cref="Writable"/>), in the order of <paramref name="books"/>, each with its account.</summary>
    public static IReadOnlyList<BookChoice> BookChoices(IReadOnlyList<AddressBook> books, IReadOnlyList<Account> accounts)
    {
        ArgumentNullException.ThrowIfNull(books);
        ArgumentNullException.ThrowIfNull(accounts);
        var names = new Dictionary<AccountId, string>();
        foreach (var a in accounts)
        {
            names[a.Id] = AccountName(a);
        }
        return [.. books.Where(Writable).Select(b => new BookChoice(
            b.Id, BookChoiceText(BookName(b), b.AccountId is { } id && names.TryGetValue(id, out var n) ? n : "")))];
    }

    /// <summary>
    /// contacts.DefaultBook: the book a new contact goes to:
    /// <paramref name="selected"/> when it is writable (New Contact in a
    /// book), else the first visible writable book of
    /// <paramref name="account"/> (the message's account for Add to
    /// Contacts…), else the first visible writable book, else the first
    /// writable one; null none.
    /// </summary>
    public static AddressBookId? DefaultBook(IReadOnlyList<AddressBook> books, AddressBookId? selected, AccountId? account)
    {
        ArgumentNullException.ThrowIfNull(books);
        if (selected is { } s && books.FirstOrDefault(b => b.Id == s && Writable(b)) is { } chosen)
        {
            return chosen.Id;
        }
        AddressBookId? Pick(Func<AddressBook, bool> ok) => books.FirstOrDefault(b => Writable(b) && ok(b))?.Id;
        if (account is { Value.Length: > 0 } acc && Pick(b => b.Visible && b.AccountId == acc) is { } own)
        {
            return own;
        }
        return Pick(b => b.Visible) ?? Pick(_ => true);
    }

    /// <summary>contacts.AddFromChip: the state of Add to Contacts….</summary>
    public sealed record AddFromChip
    {
        /// <summary>contacts.NewAddFromChip: prefilled for an address of a header of a message of <paramref name="account"/>.</summary>
        public AddFromChip(Address address, AccountId? account, IReadOnlyList<AddressBook> books, IReadOnlyList<Account> accounts)
        {
            ArgumentNullException.ThrowIfNull(address);
            var name = Clean(address.Name, API.Limits.MaxContactNameBytes);
            Address = Clean(address.Email, API.Limits.MaxContactFieldBytes);
            Books = BookChoices(books, accounts);
            Book = DefaultBook(books, null, account);
            if (string.Equals(name.ToLowerInvariant(), Address.ToLowerInvariant(), StringComparison.Ordinal))
            {
                name = "";
            }
            Name = name;
            if (name.Length > 0)
            {
                SelectName = true;
                Warning = ContactsText.SenderNameWarning;
            }
        }

        /// <summary>The name (the sender's text).</summary>
        public string Name { get; init; } = "";

        /// <summary>The address.</summary>
        public string Address { get; init; } = "";

        /// <summary>The name field starts selected (a name came from the header).</summary>
        public bool SelectName { get; init; }

        /// <summary><see cref="ContactsText.SenderNameWarning"/> when the name came from the header ("" else).</summary>
        public string Warning { get; init; } = "";

        /// <summary>The books to choose from.</summary>
        public IReadOnlyList<BookChoice> Books { get; init; } = [];

        /// <summary>The chosen book; null none.</summary>
        public AddressBookId? Book { get; init; }

        /// <summary>contacts.AddFromChip.CanAdd: a book and a valid address.</summary>
        public bool CanAdd => Book is not null && ValidEmail(TrimSpace(Address));

        /// <summary>
        /// contacts.AddFromChip.Form: the form Add saves and More Details…
        /// opens: a new contact in the chosen book with the name split and
        /// the address without a label.
        /// </summary>
        public Form Form()
        {
            var f = ContactBook.Form.New(Book);
            (f.Given, f.Family) = SplitName(Name);
            var addr = TrimSpace(Address);
            if (addr.Length > 0)
            {
                f.Emails = [new LabelledValue(Value: addr)];
            }
            return f;
        }
    }

    /// <summary>contacts.ChipItemFor: Show Contact for a known address, else Add to Contacts… when some book can take it.</summary>
    public static ChipItem ChipItemFor(bool known, IReadOnlyList<AddressBook> books)
    {
        ArgumentNullException.ThrowIfNull(books);
        if (known)
        {
            return ChipItem.Show;
        }
        return books.Any(Writable) ? ChipItem.Add : ChipItem.None;
    }

    /// <summary>contacts.ChipItemText ("" for <see cref="ChipItem.None"/>).</summary>
    public static string ChipItemText(ChipItem i) => i switch
    {
        ChipItem.Show => ContactsText.ShowContact,
        ChipItem.Add => ContactsText.AddToContacts,
        _ => "",
    };
}
