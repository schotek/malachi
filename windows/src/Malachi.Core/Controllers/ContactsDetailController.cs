// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ContactsDetailController.swift;
// GTK: ui/internal/contactsctl contact.go (ContactController, refused,
// saveFailed).
//
// The contact shown in the detail and edited on its card (W1, W4): the card
// from contact.get, the form (ContactBook.Form) while editing, Done as
// contact.create or contact.update with the card's revision, Delete
// Contact… with its question and contact.delete. Writes go to the server
// (online only): a failure keeps the form open with the sentence (offline,
// read only, a refused card), a field the daemon refused is marked as a
// problem (ContactBook.ProblemOf), a contactConflict asks
// ContactsText.ConflictQuestion and Reload rebases the form on the server's
// version keeping what the user changed; nothing is overwritten blindly. An
// unchanged form's Done sends nothing. A Microsoft book's limits are
// checked before anything is sent (the form gets the book's kind).
//
// Windows: Swift's onChange, onCreated and onDeleted are the events
// Changed, Created and Deleted; onQuestion is the hook AskQuestion (true is
// the question's confirm button); the resolver is Books
// (ContactsListController.Book). Swift's updateForm(inout) is UpdateForm
// over the open form. Drive it from the window: Open on the list's
// Selected, ContactsChanged for every notify.contactsChanged, BeginEdit,
// UpdateForm, Save, CancelEdit, BeginNew and Delete from the views, Stop
// when the window closes.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Contacts;

namespace Malachi.Core.Controllers;

/// <summary>contactsctl.ContactController: the contact of the detail and its form.</summary>
/// <remarks>Create and use it on the UI thread.</remarks>
public sealed class ContactsDetailController
{
    private int gen;

    // Counts the forms opened, so that a late answer meets only the form it
    // was asked for (Go compares the form's pointer).
    private int formGen;
    private bool closed;

    /// <summary>A controller over <paramref name="options"/>, its books resolved by <paramref name="books"/>.</summary>
    public ContactsDetailController(ContactsOptions options, Func<AddressBookId, (AddressBook Book, Account? Account)?>? books = null)
    {
        ArgumentNullException.ThrowIfNull(options);
        Options = options;
        Books = books;
    }

    /// <summary>The card, the form or a state changed.</summary>
    public event EventHandler? Changed;

    /// <summary>Done created the contact (the window selects it and reloads).</summary>
    public event EventHandler<ContactId>? Created;

    /// <summary>The contact was deleted (or was gone already).</summary>
    public event EventHandler<ContactId>? Deleted;

    /// <summary>The shared options.</summary>
    public ContactsOptions Options { get; }

    /// <summary>Resolves a book and its account for the source line, the question's account and the form's book kind; null knows none.</summary>
    public Func<AddressBookId, (AddressBook Book, Account? Account)?>? Books { get; set; }

    /// <summary>Asks; true is the question's confirm button, false Cancel. Without it nothing is deleted and no conflict is reloaded.</summary>
    public Func<ContactsQuestion, Task<bool>>? AskQuestion { get; set; }

    /// <summary>The shown contact (null none or a new one).</summary>
    public ContactId? Id { get; private set; }

    /// <summary>The shown card (null before contact.get answers).</summary>
    public ContactCard? Card { get; private set; }

    /// <summary>The form while editing (null when not editing); the view changes it through <see cref="UpdateForm"/>.</summary>
    public ContactBook.Form? Form { get; private set; }

    /// <summary>The form's problems of the last Done (a refused field included).</summary>
    public IReadOnlyList<ContactBook.Problem> Problems { get; private set; } = [];

    /// <summary>A contact.get without its answer.</summary>
    public bool Loading { get; private set; }

    /// <summary>Done without its answer.</summary>
    public bool Saving { get; private set; }

    /// <summary>Delete without its answer.</summary>
    public bool Deleting { get; private set; }

    /// <summary>The last failure's sentence ("" none).</summary>
    public string Error { get; private set; } = "";

    /// <summary>Editing (the form is open).</summary>
    public bool Editing => Form is not null;

    /// <summary>The detail of the shown card.</summary>
    public ContactBook.ContactDetail? Detail => Card is { } c ? ContactBook.Detail(c, Options.Format) : null;

    /// <summary>The detail's footer ("Address book Contacts · Microsoft 365 (…)"); Recently Used says On This Computer.</summary>
    public string SourceLine
    {
        get
        {
            if (Card is not { } card || Resolve(card.BookId) is not { } r)
            {
                return "";
            }
            var label = ContactBook.AccountLabel(r.Account);
            var acc = r.Book.Id == AddressBookId.Recent || label.Length == 0 ? ContactsText.OnThisComputer : label;
            return ContactsText.SourceLine(ContactBook.BookName(r.Book), acc);
        }
    }

    /// <summary>Copy's text.</summary>
    public string CopyText => Card is { } c ? ContactBook.CopyText(c, Options.Format) : "";

    /// <summary>Edit: a card of a writable book.</summary>
    public bool CanEdit => Card is { ReadOnly: false } && Form is null && !Deleting;

    /// <summary>Delete Contact…: a card of a writable book.</summary>
    public bool CanDelete => Card is { ReadOnly: false } && !Saving && !Deleting;

    /// <summary>A form with a change (closing or selecting another contact asks <see cref="ContactsText.DiscardQuestion"/> first).</summary>
    public bool Dirty => Form?.Dirty ?? false;

    /// <summary>Ends the controller: no answer is taken any more.</summary>
    public void Stop() => closed = true;

    /// <summary>Shows a contact (null clears the detail); a form being edited is dropped (the window asks before).</summary>
    public void Open(ContactId? id)
    {
        gen++;
        formGen++;
        Id = id;
        Card = null;
        Form = null;
        Problems = [];
        Error = "";
        Saving = false;
        Deleting = false;
        if (id is null)
        {
            Loading = false;
            RaiseChanged();
            return;
        }
        Load(quiet: false);
        RaiseChanged();
    }

    /// <summary>
    /// contactsctl.ContactController.ContactsChanged: the shown card is asked
    /// again when its book (or any of its account) changed and no form is
    /// open (a form meets the change as a conflict on Done).
    /// </summary>
    public void ContactsChanged(ContactsChangedNotification n)
    {
        ArgumentNullException.ThrowIfNull(n);
        if (Card is not { } card || Form is not null || Saving || Deleting || Id is null)
        {
            return;
        }
        var r = Resolve(card.BookId);
        var changedAccount = n.AccountId;
        if (r?.Account is { } a && changedAccount is { } ca && ca != a.Id)
        {
            return;
        }
        if (r?.Account is null && changedAccount is { } cb && r?.Book.AccountId != cb)
        {
            return;
        }
        if (n.BookIds.Count > 0 && !System.Linq.Enumerable.Contains(n.BookIds, card.BookId))
        {
            return;
        }
        Load(quiet: true);
    }

    /// <summary>Opens the form over the shown card.</summary>
    public void BeginEdit()
    {
        if (!CanEdit || Card is not { } card)
        {
            return;
        }
        formGen++;
        Form = ContactBook.Form.Edit(card);
        Problems = [];
        Error = "";
        RaiseChanged();
    }

    /// <summary>
    /// Opens the form of a new contact in <paramref name="book"/> (the detail
    /// shows no card meanwhile); <paramref name="prefilled"/>, when given, is
    /// the form (Add to Contacts' More Details…).
    /// </summary>
    public void BeginNew(AddressBookId? book, ContactBook.Form? prefilled = null)
    {
        gen++;
        formGen++;
        Id = null;
        Card = null;
        Loading = false;
        Saving = false;
        Deleting = false;
        Form = prefilled ?? ContactBook.Form.New(book);
        Problems = [];
        Error = "";
        RaiseChanged();
    }

    /// <summary>Changes the open form (the view's fields); nothing without a form. Raises no change: the view already shows it.</summary>
    public void UpdateForm(Action<ContactBook.Form> edit)
    {
        ArgumentNullException.ThrowIfNull(edit);
        if (Form is { } f)
        {
            edit(f);
        }
    }

    /// <summary>Closes the form without saving (Cancel).</summary>
    public void CancelEdit()
    {
        if (Form is null || Saving)
        {
            return;
        }
        formGen++;
        Form = null;
        Problems = [];
        Error = "";
        RaiseChanged();
    }

    /// <summary>Done: the form validated (with its book's limits), then created or updated on the server.</summary>
    public void Save()
    {
        if (Form is not { } f || Saving)
        {
            return;
        }
        if (f.Book is { } fb && Resolve(fb) is { } rb)
        {
            f.Kind = rb.Book.Kind;
        }
        Problems = f.Validate();
        if (Problems.Count > 0)
        {
            RaiseChanged();
            return;
        }
        if (!f.IsNew && !f.Dirty)
        {
            formGen++;
            Form = null;
            Error = "";
            RaiseChanged();
            return;
        }
        Saving = true;
        Error = "";
        var g = gen;
        var token = formGen;
        var acc = AccountLabelOf(f.Book);
        if (f.IsNew)
        {
            var p = new ContactCreateParams { BookId = f.Book ?? new AddressBookId(""), Card = f.Card };
            Options.Call(API.ContactCreate, p, ContactsOptions.WriteTimeout, r =>
            {
                if (closed || g != gen)
                {
                    return;
                }
                Saving = false;
                if (!r.TryGetValue(out var res, out var e))
                {
                    Refused(e!, acc);
                    RaiseChanged();
                    return;
                }
                var cur = formGen == token && Form is { } open ? open : f;
                cur.Saved(res.ContactId, res.Revision);
                formGen++;
                Id = res.ContactId;
                Card = cur.Original;
                Form = null;
                Load(quiet: true);
                RaiseChanged();
                Created?.Invoke(this, res.ContactId);
            });
            RaiseChanged();
            return;
        }
        var update = new ContactUpdateParams { ContactId = f.Original.Id, Revision = f.Original.Revision, Card = f.Card };
        Options.Call(API.ContactUpdate, update, ContactsOptions.WriteTimeout, r =>
        {
            if (closed || g != gen)
            {
                return;
            }
            Saving = false;
            if (!r.TryGetValue(out var res, out var e))
            {
                SaveFailed(e!, acc, f.Original.Id, token);
                return;
            }
            var cur = formGen == token && Form is { } open ? open : f;
            cur.Saved(null, res.Revision);
            formGen++;
            Card = cur.Original;
            Form = null;
            Load(quiet: true);
            RaiseChanged();
        });
        RaiseChanged();
    }

    /// <summary>Asks <see cref="ContactsText.DeleteQuestion"/> and then deletes the contact on the server.</summary>
    public void Delete()
    {
        if (!CanDelete || AskQuestion is not { } ask || Card is not { } card)
        {
            return;
        }
        var acc = AccountLabelOf(card.BookId);
        var g = gen;
        var q = ContactsText.DeleteQuestion(ContactBook.Title(card), acc);
        Options.Spawn(async () =>
        {
            var yes = await ask(q);
            if (!yes || closed || g != gen || Deleting)
            {
                return;
            }
            Deleting = true;
            Error = "";
            RaiseChanged();
            var p = new ContactDeleteParams { ContactId = card.Id, Revision = card.Revision };
            Options.Call(API.ContactDelete, p, ContactsOptions.WriteTimeout, r =>
            {
                if (closed || g != gen)
                {
                    return;
                }
                Deleting = false;
                var code = ContactsOptions.Code(r);
                if (code is null || code == ErrorCode.ContactNotFound)
                {
                    gen++;
                    formGen++;
                    Id = null;
                    Card = null;
                    Form = null;
                    RaiseChanged();
                    Deleted?.Invoke(this, card.Id);
                    return;
                }
                if (code == ErrorCode.ContactConflict)
                {
                    // Somebody changed it meanwhile: show the new version,
                    // the user decides again.
                    if (ContactsOptions.ConflictCard(r.Error) is { } s)
                    {
                        Card = s;
                    }
                    else
                    {
                        Load(quiet: true);
                    }
                }
                Error = Options.ErrorMessage(r.Error, acc);
                RaiseChanged();
            });
        });
    }

    private void RaiseChanged()
    {
        if (!closed)
        {
            Changed?.Invoke(this, EventArgs.Empty);
        }
    }

    private (AddressBook Book, Account? Account)? Resolve(AddressBookId id) => Books?.Invoke(id);

    // The account label of a book (for the questions and the consent sentence).
    private string AccountLabelOf(AddressBookId? book) =>
        book is { } b && Resolve(b) is { } r ? ContactBook.AccountLabel(r.Account) : "";

    // Asks contact.get; quiet keeps the shown card while it loads.
    private void Load(bool quiet)
    {
        if (Id is not { } id)
        {
            return;
        }
        var g = gen;
        Loading = !quiet;
        Options.Call(API.ContactGet, new ContactGetParams { ContactId = id }, r =>
        {
            if (closed || g != gen)
            {
                return;
            }
            Loading = false;
            if (r.TryGetValue(out var res, out var e))
            {
                Card = res.Contact;
            }
            else
            {
                if (ContactsOptions.AsRpcError(e!).Code == ErrorCode.ContactNotFound)
                {
                    Card = null;
                }
                Error = Options.ErrorMessage(e);
            }
            RaiseChanged();
        });
    }

    // contactsctl refused: a card the daemon refused for one field marks that
    // field, anything else is the sentence.
    private void Refused(Exception e, string acc)
    {
        if (ContactBook.ProblemOf(e) is { } p)
        {
            Error = "";
            Problems = [p];
            return;
        }
        Error = Options.ErrorMessage(e, acc);
    }

    // Keeps the form: a conflict asks for Reload, anything else shows its
    // sentence (or marks its field).
    private void SaveFailed(Exception e, string acc, ContactId original, int token)
    {
        Refused(e, acc);
        RaiseChanged();
        if (ContactsOptions.AsRpcError(e).Code != ErrorCode.ContactConflict)
        {
            return;
        }
        if (ContactsOptions.ConflictCard(e) is { } server)
        {
            AskReload(server, token);
            return;
        }
        // A conflict without the server's version: ask contact.get for it.
        var g = gen;
        Options.Call(API.ContactGet, new ContactGetParams { ContactId = original }, r =>
        {
            if (!closed && g == gen && r.TryGetValue(out var res, out _))
            {
                AskReload(res.Contact, token);
            }
        });
    }

    // ConflictQuestion; Reload rebases the form on the server's card.
    private void AskReload(ContactCard server, int token)
    {
        if (AskQuestion is not { } ask)
        {
            return;
        }
        var g = gen;
        var q = ContactsText.ConflictQuestion;
        Options.Spawn(async () =>
        {
            var reload = await ask(q);
            if (!reload || closed || g != gen || token != formGen || Form is not { } f)
            {
                return;
            }
            f.Rebase(server);
            Card = server;
            Error = "";
            RaiseChanged();
        });
    }
}
