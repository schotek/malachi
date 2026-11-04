// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/contacts avatar_test.go (TestAvatar, TestNearRows);
// Go-first. Fictional people only.

using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Xunit;

namespace Malachi.Core.Tests.Contacts;

public sealed class ContactBookAvatarTests
{
    [Fact]
    public void Avatar()
    {
        var row = new ContactBook.Row(Contact: new ContactSummary { Id = "p_1", BookId = "b_1" }, Initials: "JN");
        var a = ContactBook.AvatarOfRow(row, ContactPhotoAvailability.Ready);
        Assert.True(a.Contact == "p_1" && a.Initials == "JN" && a.MayShowPhoto, "ready");
        Assert.Equal(new ContactId("p_1"), ContactBook.AvatarOfRow(row, ContactPhotoAvailability.Remote).Contact);
        a = ContactBook.AvatarOfRow(row, null);
        Assert.True(!a.MayShowPhoto && a.Initials == "JN", "none");
        Assert.False(ContactBook.AvatarOfRow(row, new ContactPhotoAvailability("bogus")).MayShowPhoto); // an unknown state
        var recent = new ContactBook.Row(Contact: new ContactSummary { Id = "r_1", BookId = AddressBookId.Recent });
        Assert.False(ContactBook.AvatarOfRow(recent, ContactPhotoAvailability.Ready).MayShowPhoto); // a recent contact with a photo
        var card = new ContactCard { Id = "p_2", BookId = "b_1" };
        a = ContactBook.AvatarOfDetail(card, new ContactBook.ContactDetail { Initials = "A" }, ContactPhotoAvailability.Ready);
        Assert.True(a.Contact == "p_2" && a.Initials == "A", "detail");
        Assert.False(ContactBook.AvatarOfDetail(new ContactCard { Id = "r_9" }, new ContactBook.ContactDetail(), ContactPhotoAvailability.Ready).MayShowPhoto);

        // Senders: by the exact address, never for the user's own messages.
        a = ContactBook.AvatarOfSender(" Jana@Firma.CZ ", "Jana Nová", false);
        Assert.True(a.Sender == "jana@firma.cz" && a.Initials == "JN" && a.Contact is null, $"sender {a}");
        a = ContactBook.AvatarOfSender("jana@firma.cz", "Jana", true);
        Assert.True(!a.MayShowPhoto && a.Initials == "J", "own message");
        Assert.False(ContactBook.AvatarOfSender("", "Jana", false).MayShowPhoto);
        Assert.False(ContactBook.AvatarOfSender("undisclosed", "", false).MayShowPhoto);

        // monochrome-avatars greys initials, never a photo.
        Assert.True(ContactBook.Monochrome(true, false));
        Assert.False(ContactBook.Monochrome(true, true));
        Assert.False(ContactBook.Monochrome(false, false));
    }

    [Theory]
    [InlineData(10, 19, 100, 0, 30)]
    [InlineData(40, 49, 100, 30, 60)]
    [InlineData(90, 99, 100, 80, 100)]
    [InlineData(0, 4, 3, 0, 3)]
    [InlineData(5, 4, 100, 0, 0)]
    [InlineData(0, 0, 0, 0, 0)]
    [InlineData(-3, 2, 10, 0, 6)]
    public void NearRows(int first, int last, int count, int from, int to)
    {
        Assert.Equal((from, to), ContactBook.NearRows(first, last, count));
    }
}
