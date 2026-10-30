// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The smoke tests of the Contacts window and Preferences → Contacts
// (docs/plans/address-book.md W1, W3–W6; docs/windows-port.md §11.12) over
// the published app with the invented address books
// (MALACHI_CONTACTS_SAMPLES=1, ContactsSmokeFixture): the "…" menu's
// Contacts opens the window with All Contacts, the samples' books under
// their accounts and Recently Used, Microsoft's consent of the account Petr
// as an InfoBar with Allow…; selecting Jana Nováková shows her detail and
// the CommandBar, whose Copy says so in a toast; Ctrl+E puts the keyboard
// in the title bar's search and typing filters the list; New Contact opens
// the form and Cancel closes it; Edit, a change and Cancel ask "Discard
// Changes?" and Discard closes the form; Preferences → Contacts (after
// Calendar) shows a section per account with its switch, its books and the
// consent, and Recipient Suggestions. Elements are found by AutomationId
// (the names follow the user's language); the samples' ids are data.

using System;
using System.Linq;
using System.Windows.Automation;
using Xunit;

namespace Malachi.App.UiTests;

/// <summary>The Contacts window and Preferences → Contacts over the invented address books.</summary>
[Collection(OneAppAtATime.Name)]
public sealed class ContactsSmokeTests(ContactsSmokeFixture fixture) : IClassFixture<ContactsSmokeFixture>
{
    private const byte VkControl = 0x11;
    private const byte VkBack = 0x08;
    private const byte VkA = 0x41;
    private const byte VkE = 0x45;

    // The samples' Jana Nováková (Kontakty of the work account, writable) and Marie Svobodová.
    private const string Jana = "ContactsRow:c_1";
    private const string Marie = "ContactsRow:c_3";

    private AppSession App
    {
        get
        {
            Assert.SkipWhen(fixture.SkipReason is not null, fixture.SkipReason ?? "");
            return fixture.Session;
        }
    }

    [Fact]
    public void TheMenuOpensTheWindowWithTheBooksAndTheConsent()
    {
        var window = OpenContacts();
        Assert.NotNull(Uia.Find(window, "ContactsBook:all"));
        Assert.NotNull(Uia.Find(window, "ContactsBook:ab_work"));
        Assert.NotNull(Uia.Find(window, "ContactsBook:ab_family"));
        Assert.NotNull(Uia.Find(window, "ContactsBook:recent"));
        Assert.NotNull(Uia.Find(window, "ContactsAccount:acc_work"));
        // Petr's Microsoft account lacks the contacts' consent.
        Assert.NotNull(Uia.Find(window, "ContactsConsent:acc_petr"));
        Assert.NotNull(Uia.Find(window, "ContactsAllow:acc_petr"));
        Assert.NotNull(Uia.Find(window, "ContactsSearch"));
        Assert.True(Uia.Find(window, "ContactsNew").Current.IsEnabled);
        Assert.NotNull(Uia.Find(window, Jana));
        Uia.WaitFor(() => Uia.Find(window, "ContactsStatus").Current.Name.Length > 0, "the status line");
    }

    [Fact]
    public void AContactShowsItsDetailAndCopySaysSo()
    {
        var window = OpenContacts();
        Uia.Select(Uia.Find(window, Jana));
        var title = Uia.Find(window, "ContactsDetailTitle");
        Assert.Equal("Jana Nováková", title.Current.Name);
        Assert.NotNull(Uia.Find(window, "ContactsDetailSubtitle"));
        Assert.NotNull(Uia.Find(window, "ContactsDetailSource"));
        Assert.NotNull(Uia.Find(window, "ContactsDetailRow:0"));
        Assert.True(Uia.Find(window, "ContactsWriteEmail").Current.IsEnabled);
        Assert.True(Uia.Find(window, "ContactsShowMail").Current.IsEnabled);
        Assert.True(Uia.Find(window, "ContactsEdit").Current.IsEnabled);
        Assert.Null(Uia.TryFind(window, "ContactsReadOnly"));
        // Delete and Copy go to the CommandBar's overflow first when the
        // detail is narrow.
        Assert.True(Command(window, "ContactsDelete").Current.IsEnabled);

        Uia.Invoke(Command(window, "ContactsCopy"));
        var toast = Uia.Find(window, "ToastText", TimeSpan.FromSeconds(10));
        Uia.WaitFor(() => toast.Current.Name.Length > 0, "the toast of Copy");
    }

    // A command of the detail's CommandBar, from its overflow ("…", the
    // template's MoreButton) when it is not in the bar; the overflow is a
    // popup of its own, found from the desktop.
    private static AutomationElement Command(AutomationElement window, string id)
    {
        if ((Uia.TryFind(window, id) ?? Uia.TryFind(AutomationElement.RootElement, id)) is { } found)
        {
            return found;
        }
        Uia.Invoke(Uia.Find(Uia.Find(window, "ContactsCommands"), "MoreButton"));
        return Uia.Until(() => Uia.TryFind(window, id) ?? Uia.TryFind(AutomationElement.RootElement, id), null, "the overflow's " + id);
    }

    [Fact]
    public void CtrlEPutsTheKeyboardInTheSearchWhichFiltersTheList()
    {
        var window = OpenContacts();
        var jana = Uia.Find(window, Jana);
        PressChord(jana, App.ProcessId, VkControl, VkE);
        var box = Edit(Uia.Find(window, "ContactsSearch"));
        Uia.WaitFor(() => box.Current.HasKeyboardFocus, "Ctrl+E to focus the search");
        Type("svob");
        Uia.WaitFor(() => Uia.TryFind(window, Jana) is null, "the search to filter the list");
        Assert.NotNull(Uia.Find(window, Marie));

        // Emptied, the book lists everything again.
        PressChord(box, App.ProcessId, VkControl, VkA);
        Key(VkBack);
        Uia.Find(window, Jana);
    }

    [Fact]
    public void NewContactOpensTheFormAndCancelClosesIt()
    {
        var window = OpenContacts();
        Uia.Invoke(Uia.Find(window, "ContactsNew"));
        Assert.NotNull(Uia.Find(window, "ContactsForm:Given"));
        Assert.NotNull(Uia.Find(window, "ContactsForm:Family"));
        Assert.NotNull(Uia.Find(window, "ContactsFormBook"));
        Assert.NotNull(Uia.Find(window, "ContactsAdd:Email"));
        Assert.NotNull(Uia.Find(window, "ContactsSave"));
        // Nothing typed: Cancel closes the form without a question.
        Uia.Invoke(Uia.Find(window, "ContactsCancel"));
        Uia.WaitFor(() => Uia.TryFind(window, "ContactsForm:Given") is null, "Cancel to close the form");
    }

    [Fact]
    public void EditThenCancelAsksToDiscardTheChanges()
    {
        var window = OpenContacts();
        Uia.Select(Uia.Find(window, Jana));
        var edit = Uia.Find(window, "ContactsEdit");
        Uia.WaitFor(() => edit.Current.IsEnabled, "Edit to be offered");
        Uia.Invoke(edit);
        var given = Uia.Find(window, "ContactsForm:Given");
        Uia.SetValue(given, "Janička");
        Uia.Invoke(Uia.Find(window, "ContactsCancel"));
        // "Discard Changes?": its Discard (the dialog's primary button).
        Uia.Invoke(Uia.Find(window, "PrimaryButton", TimeSpan.FromSeconds(10)));
        Uia.WaitFor(() => Uia.TryFind(window, "ContactsForm:Given") is null, "Discard to close the form");
        Assert.Equal("Jana Nováková", Uia.Find(window, "ContactsDetailTitle").Current.Name);
    }

    [Fact]
    public void PreferencesShowTheContactsPageAfterCalendar()
    {
        App.MenuItem("MenuPreferences");
        var preferences = Uia.WindowWith(App.ProcessId, "PreferencesNavigation");
        try
        {
            var items = Uia.Find(preferences, "PreferencesNavigation");
            Assert.NotNull(Uia.Find(items, "CalendarPage"));
            Uia.Select(Uia.Find(preferences, "ContactsPage"));
            var page = Uia.Find(preferences, "PrefsContacts");
            var work = Uia.Find(page, "PrefsContactsAccount:acc_work");
            Assert.Equal("M365", Uia.Find(work, "PrefsContactsAccountKind").Current.Name);
            Assert.NotNull(Uia.Find(work, "PrefsContactsSync"));
            Assert.NotNull(Uia.Find(work, "PrefsContactsBook:ab_work"));
            Assert.NotNull(Uia.Find(work, "PrefsContactsBook:ab_work_old"));
            var petr = Uia.Find(page, "PrefsContactsAccount:acc_petr");
            Assert.NotNull(Uia.Find(petr, "PrefsContactsConsent"));
            Assert.NotNull(Uia.Find(petr, "PrefsContactsAllow"));
            Assert.NotNull(Uia.Find(page, "PrefsContactsAccount:acc_cloud"));
            Assert.NotNull(Uia.Find(page, "PrefsContactsScope:account"));
            Assert.NotNull(Uia.Find(page, "PrefsContactsScope:all"));
        }
        finally
        {
            Uia.Close(preferences);
            Uia.WaitGone(preferences, what: "the Preferences");
        }
    }

    // The "…" menu's Contacts (no key): the window, with All Contacts listed.
    private AutomationElement OpenContacts()
    {
        if (Uia.Windows(App.ProcessId).FirstOrDefault(w => Uia.TryFind(w, "ContactsBooks") is not null) is { } open)
        {
            Uia.Select(Uia.Find(open, "ContactsBook:all"));
            return open;
        }
        App.MenuItem("MenuContacts");
        var window = Uia.WindowWith(App.ProcessId, "ContactsBooks", AppSession.StartTimeout);
        Uia.Find(window, Jana);
        return window;
    }

    private static AutomationElement Edit(AutomationElement box) =>
        box.Current.ControlType == ControlType.Edit
            ? box
            : box.FindFirst(TreeScope.Descendants, new PropertyCondition(AutomationElement.ControlTypeProperty, ControlType.Edit)) ?? box;

    // Types lower-case letters into the focused field, as a user does (the
    // search follows typing only, not a programmatic value).
    private static void Type(string letters)
    {
        foreach (var c in letters)
        {
            Key((byte)char.ToUpperInvariant(c));
        }
    }

    private static void Key(byte key)
    {
        NativeMethods.KeybdEvent(key, 0, 0, 0);
        NativeMethods.KeybdEvent(key, 0, NativeMethods.KeyEventKeyUp, 0);
    }

    // A key with a modifier, the keyboard focus on element.
    private static void PressChord(AutomationElement element, int processId, byte modifier, byte key)
    {
        Focus(element, processId);
        NativeMethods.KeybdEvent(modifier, 0, 0, 0);
        NativeMethods.KeybdEvent(key, 0, 0, 0);
        NativeMethods.KeybdEvent(key, 0, NativeMethods.KeyEventKeyUp, 0);
        NativeMethods.KeybdEvent(modifier, 0, NativeMethods.KeyEventKeyUp, 0);
    }

    // The keyboard focus on element, in the app's window in the foreground.
    private static void Focus(AutomationElement element, int processId)
    {
        element.SetFocus();
        Uia.WaitFor(
            () =>
            {
                NativeMethods.GetWindowThreadProcessId(NativeMethods.GetForegroundWindow(), out var owner);
                return owner == processId;
            },
            "the app in the foreground");
    }
}
