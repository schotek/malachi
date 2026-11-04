// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Contacts/ContactBook.swift (the texts:
// windowTitle … wizardTitle, Question, Empty); GTK: ui/internal/contacts
// text.go (WindowTitle, AllContacts, RecentlyUsed, OnThisComputer,
// SearchContacts, NewContact, ContactCount, AddressCount, TwoParts,
// SyncedLine, WriteEmail, ShowMail, Copy, Edit, Done, Cancel, DeleteContact,
// Delete, ReadOnly, NoName, SourceLine, ShowContact, AddToContacts,
// AddTitle, Add, ContactAdded, MoreDetails, SenderNameWarning,
// SuggestionTooltip, Question, DeleteQuestion, ConflictQuestion,
// DiscardQuestion, Copied, ConsentBanner, Allow, ConsentOnline, Empty,
// EmptyText, OpenPreferences, ErrorText, SyncContacts, CardDAVNote,
// SuggestionsGroup, ScopeText, NoContactAccounts, AddressBooksHeading,
// CalendarsHeading, WizardTitle).
//
// Every text of the Contacts window, the address chips' contacts items and
// Preferences → Contacts, with the GTK msgid as the key; Go's Translator
// is L10n, as in the other ports. A string of a book or a contact is
// untrusted display text: ContactBook.Clean cleans it before it is
// formatted into a msgid, never concatenated into a sentence.
//
// Windows: the class is ContactsText, not Contacts, so that the namespace
// Malachi.Core.Contacts is not hidden; Go's (string, bool) results are
// nullable strings, as Swift's optionals. DiscardQuestion, Copied and
// ContactAdded came to Go with the GTK window after the macOS core; they
// are here ahead of Swift.

using System;
using System.Globalization;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.I18n;

namespace Malachi.Core.Contacts;

/// <summary>contacts.Question: what a dialog asks; <see cref="Confirm"/> is its default button.</summary>
/// <param name="Heading">The heading.</param>
/// <param name="Body">The explanation.</param>
/// <param name="Confirm">The default button.</param>
/// <param name="Destructive">The default button deletes or drops something.</param>
public sealed record ContactsQuestion(string Heading, string Body, string Confirm, bool Destructive = false);

/// <summary>contacts.Empty: an empty state of the window.</summary>
public enum ContactsEmpty
{
    /// <summary>Something to show.</summary>
    None,

    /// <summary>No address book but Recently Used (W5 a), with Open Preferences….</summary>
    NoBooks,

    /// <summary>The selected book has no contact.</summary>
    Book,

    /// <summary>The search found nothing.</summary>
    Search,

    /// <summary>No contact selected (the detail).</summary>
    Selection,
}

/// <summary>ui/internal/contacts (text.go): the texts of the address books.</summary>
public static class ContactsText
{
    // The window

    /// <summary>contacts.WindowTitle: the window's title, its menu item, the Preferences page and the narrow window's back button.</summary>
    public static string WindowTitle =>
        // TRANSLATORS: the Contacts window (address books of the user's accounts), its menu item and its Preferences page.
        L10n.T("Contacts");

    /// <summary>contacts.AllContacts: the sidebar's first row.</summary>
    public static string AllContacts =>
        // TRANSLATORS: the Contacts window's sidebar row that lists the contacts of every address book.
        L10n.T("All Contacts");

    /// <summary>contacts.RecentlyUsed: the virtual book of the addresses the user wrote to.</summary>
    public static string RecentlyUsed =>
        // TRANSLATORS: the Contacts window's address book of the addresses the user has written to (read only).
        L10n.T("Recently Used");

    /// <summary>contacts.OnThisComputer: the heading of Recently Used's group.</summary>
    public static string OnThisComputer =>
        // TRANSLATORS: the Contacts window's sidebar heading of the address book kept on this computer (Recently Used).
        L10n.T("On This Computer");

    /// <summary>contacts.SearchContacts: the search field's placeholder.</summary>
    public static string SearchContacts => L10n.T("Search Contacts");

    /// <summary>contacts.NewContact: the toolbar's "+", its tooltip and the form's title for a new contact.</summary>
    public static string NewContact => L10n.T("New Contact");

    /// <summary>contacts.ContactCount: a book's count, the list's subtitle.</summary>
    public static string ContactCount(int n) => L10n.N("%d contact", "%d contacts", n, n);

    /// <summary>contacts.AddressCount: Recently Used's count.</summary>
    public static string AddressCount(int n) =>
        // TRANSLATORS: the number of recently used e-mail addresses.
        L10n.N("%d address", "%d addresses", n, n);

    /// <summary>contacts.TwoParts: two parts of one line ("All Contacts · 412").</summary>
    public static string TwoParts(string a, string b) =>
        // TRANSLATORS: two parts of one line, e.g. "All Contacts · 412" or "Contacts · Microsoft 365" (address book and account).
        L10n.T("%s · %s", a, b);

    /// <summary>contacts.SyncedLine: the status bar's text; <paramref name="last"/> is the oldest lastSyncAt of the visible books.</summary>
    public static string SyncedLine(DateTimeOffset last, DateTimeOffset now) =>
        // TRANSLATORS: the Contacts window's status bar; %s is a relative time such as "3 minutes ago".
        L10n.T("Contacts synchronized %s", Board.Text.RelativeTime(last, now));

    // The detail

    /// <summary>contacts.WriteEmail: the detail's button that opens New Message to the contact.</summary>
    public static string WriteEmail =>
        // TRANSLATORS: the contact detail's button that writes a new message to the contact.
        L10n.T("Write Email");

    /// <summary>contacts.ShowMail: the detail's button that searches the mail from the contact.</summary>
    public static string ShowMail => L10n.T("Show Mail");

    /// <summary>contacts.Copy: the detail's button that copies the contact as text.</summary>
    public static string Copy =>
        // TRANSLATORS: the contact detail's button that copies the whole contact as plain text.
        L10n.T("Copy");

    /// <summary>contacts.Edit: the detail's button that opens the form.</summary>
    public static string Edit => L10n.T("Edit");

    /// <summary>contacts.Done: the form's button that saves to the server.</summary>
    public static string Done =>
        // TRANSLATORS: the contact form's button that saves the contact.
        L10n.C("contacts", "Done");

    /// <summary>contacts.Cancel: the form's and the questions' Cancel.</summary>
    public static string Cancel => L10n.T("Cancel");

    /// <summary>contacts.DeleteContact: the form's button that asks to delete the contact.</summary>
    public static string DeleteContact => L10n.T("Delete Contact…");

    /// <summary>contacts.Delete: the delete question's button.</summary>
    public static string Delete => L10n.T("Delete");

    /// <summary>contacts.ReadOnly: the capsule in place of Edit for a read-only book.</summary>
    public static string ReadOnly =>
        // TRANSLATORS: a capsule beside a contact whose address book cannot be changed.
        L10n.T("Read Only");

    /// <summary>contacts.NoName: a contact without a name, company or address.</summary>
    public static string NoName =>
        // TRANSLATORS: a contact without any name.
        L10n.T("No Name");

    /// <summary>contacts.SourceLine: the detail's footer, the book and the account ("Address book Contacts · Microsoft 365 (…)").</summary>
    public static string SourceLine(string book, string account) =>
        // TRANSLATORS: the contact detail's footer; the first %s is the address book's name, the second the account it belongs to.
        L10n.T("Address book %s · %s", book, account);

    // Address chips and suggestions

    /// <summary>contacts.ShowContact: the address chip's item for an address with a contact.</summary>
    public static string ShowContact => L10n.T("Show Contact");

    /// <summary>contacts.AddToContacts: the address chip's item for an address without one.</summary>
    public static string AddToContacts => L10n.T("Add to Contacts…");

    /// <summary>contacts.AddTitle: the Add to Contacts popover's heading.</summary>
    public static string AddTitle => AddToContacts;

    /// <summary>contacts.Add: the Add to Contacts popover's button.</summary>
    public static string Add =>
        // TRANSLATORS: the Add to Contacts popover's button that saves the new contact.
        L10n.C("contacts", "Add");

    /// <summary>contacts.ContactAdded: confirms Add of the Add to Contacts popover.</summary>
    public static string ContactAdded =>
        // TRANSLATORS: a short confirmation after Add to Contacts saved the new contact.
        L10n.T("Contact added");

    /// <summary>contacts.MoreDetails: opens the full form from the Add to Contacts popover.</summary>
    public static string MoreDetails => L10n.T("More Details…");

    /// <summary>contacts.SenderNameWarning: under the prefilled name of Add to Contacts (the sender wrote it).</summary>
    public static string SenderNameWarning =>
        // TRANSLATORS: under the name of a new contact taken from a message's header: the sender wrote it, so it may be false.
        L10n.T("The sender wrote this name — check it.");

    /// <summary>
    /// contacts.SuggestionTooltip: a recipient suggestion's tooltip of the
    /// source contacts, the book and the account's label ("" when unknown).
    /// </summary>
    public static string SuggestionTooltip(string book, string account)
    {
        ArgumentNullException.ThrowIfNull(book);
        ArgumentNullException.ThrowIfNull(account);
        return (book.Length == 0, account.Length == 0) switch
        {
            // TRANSLATORS: tooltip of a recipient suggestion that came from a system address book whose name is unknown.
            (true, true) => L10n.T("Address book"),
            (false, true) => book,
            (true, false) => account,
            _ => TwoParts(book, account),
        };
    }

    // Questions, errors and states

    /// <summary>contacts.DeleteQuestion: CardDAV has no trash, so it is always asked; <paramref name="account"/> is the account's label.</summary>
    public static ContactsQuestion DeleteQuestion(string name, string account) => new(
        // TRANSLATORS: the question before a contact is deleted; %s is its name.
        L10n.T("Delete contact %s?", name),
        // TRANSLATORS: %s is the account, e.g. "Microsoft 365 (jana@example.org)".
        L10n.T("The contact will also be deleted on the server %s and on every device that synchronizes it.", account),
        Delete,
        Destructive: true);

    /// <summary>contacts.ConflictQuestion: the server's contact changed since the form opened (contactConflict).</summary>
    public static ContactsQuestion ConflictQuestion => new(
        L10n.T("The contact changed meanwhile"),
        L10n.T("Someone changed the contact on another device. Load the new version; the details you changed stay in the form."),
        // TRANSLATORS: the button that loads the server's new version of a contact into the form.
        L10n.T("Reload"));

    /// <summary>
    /// contacts.DiscardQuestion: asked before a form with changes is left
    /// (another contact or book selected, New Contact, the window closed).
    /// </summary>
    public static ContactsQuestion DiscardQuestion => new(
        L10n.T("Discard Changes?"),
        L10n.T("The changes to this contact have not been saved."),
        // TRANSLATORS: the button that drops the unsaved changes of a contact.
        L10n.T("Discard"),
        Destructive: true);

    /// <summary>contacts.Copied: the toast after Copy (and after a phone number was copied).</summary>
    public static string Copied => L10n.T("Copied to Clipboard");

    /// <summary>contacts.ConsentBanner: a Microsoft account's sign-in lacks contacts access; <paramref name="account"/> is its address.</summary>
    public static string ConsentBanner(string account) =>
        L10n.T("Microsoft needs your permission to access the contacts of %s.", account);

    /// <summary>contacts.Allow: the consent banner's button (<c>account.oauthStart</c> with <c>contacts</c>).</summary>
    public static string Allow =>
        // TRANSLATORS: the button that signs in again to give Microsoft's permission to the contacts.
        L10n.T("Allow…");

    /// <summary>contacts.ConsentOnline: the advice for an account of GNOME Online Accounts (no Allow…); kept for parity.</summary>
    public static string ConsentOnline => L10n.T("Allow contacts access in the desktop’s Online Accounts settings.");

    /// <summary>contacts.EmptyText: an empty state's title and body ("" none).</summary>
    public static (string Title, string Body) EmptyText(ContactsEmpty e) => e switch
    {
        ContactsEmpty.NoBooks => (L10n.T("No Address Books"),
            L10n.T("Turn on contacts from Microsoft 365 or a CardDAV server in Preferences. Until then, here are the addresses you have written to.")),
        ContactsEmpty.Book => (L10n.T("No Contacts"), ""),
        ContactsEmpty.Search => (L10n.T("No Contacts Found"), L10n.T("Try another name, company or address.")),
        ContactsEmpty.Selection => (L10n.T("No Contact Selected"), ""),
        _ => ("", ""),
    };

    /// <summary>contacts.OpenPreferences: <see cref="ContactsEmpty.NoBooks"/>' button (Preferences → Contacts).</summary>
    public static string OpenPreferences => L10n.T("Open Preferences…");

    /// <summary>
    /// contacts.ErrorText: the sentence of a failed contacts call by its
    /// code; null when the contacts have none (the caller then writes its
    /// usual sentence of a failed call). <paramref name="account"/> is the
    /// account's label ("" unknown), for contactsConsentRequired. Never the
    /// daemon's message.
    /// </summary>
    public static string? ErrorText(ErrorCode code, string account)
    {
        ArgumentNullException.ThrowIfNull(account);
        return code.Value switch
        {
            ErrorCode.ContactNotFound => L10n.T("The contact no longer exists."),
            ErrorCode.AddressBookNotFound => L10n.T("The address book no longer exists."),
            ErrorCode.AddressBookReadOnly => L10n.T("This address book cannot be changed."),
            ErrorCode.ContactConflict => ConflictQuestion.Heading,
            ErrorCode.ContactsConsentRequired => account.Length > 0
                ? ConsentBanner(account)
                : L10n.T("Microsoft needs your permission to access these contacts."),
            ErrorCode.Offline => L10n.T("Contacts cannot be changed while offline."),
            ErrorCode.InvalidArgument => L10n.T("The contact could not be saved. Check its details."),
            _ => null,
        };
    }

    // Preferences → Contacts and the CalDAV wizard

    /// <summary>contacts.SyncContacts: a Microsoft account's switch.</summary>
    public static string SyncContacts => L10n.T("Sync Contacts");

    /// <summary>contacts.CardDAVNote: under a CardDAV account's books.</summary>
    public static string CardDAVNote => L10n.T("Edit the server, password and certificate on the Calendar page.");

    /// <summary>contacts.SuggestionsGroup: the group of contacts-suggest-scope.</summary>
    public static string SuggestionsGroup =>
        // TRANSLATORS: the Preferences group that decides which address books complete the recipients of a new message.
        L10n.T("Recipient Suggestions");

    /// <summary>contacts.ScopeText: a choice of Recipient Suggestions.</summary>
    public static string ScopeText(ContactSearchScope s) =>
        s.Value == ContactSearchScope.All
            ? L10n.T("All Address Books")
            : L10n.T("Address Books of the Account You Write From");

    /// <summary>contacts.NoContactAccounts: Preferences → Contacts without an account that can have contacts.</summary>
    public static string NoContactAccounts => L10n.T("Add a Microsoft 365 account or a CardDAV server to see its contacts here.");

    /// <summary>contacts.AddressBooksHeading: the CalDAV wizard's found books.</summary>
    public static string AddressBooksHeading =>
        // TRANSLATORS: the heading of the address books a CardDAV server has.
        L10n.T("Address Books");

    /// <summary>contacts.CalendarsHeading: the CalDAV wizard's found calendars.</summary>
    public static string CalendarsHeading => L10n.T("Calendars");

    /// <summary>contacts.WizardTitle: the CalDAV wizard's title once it also adds contacts.</summary>
    public static string WizardTitle => L10n.T("Add Calendar and Contacts (CalDAV/CardDAV)");

    // The count as text (ListSubtitle's All Contacts), in the invariant culture as Go's strconv.Itoa.
    internal static string Number(int n) => n.ToString(CultureInfo.InvariantCulture);
}
