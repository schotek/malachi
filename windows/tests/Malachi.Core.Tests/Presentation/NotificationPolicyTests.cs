// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of NotificationPolicy, the rules of ui/internal/window/notify.go
// (notifyNewMessage) and macos/Sources/MalachiMail/Notifications/
// NotificationService.swift (deliver), which neither GTK nor macOS tests
// (notify_test.go and NotificationTextTests cover only the texts, in
// NotificationTextTests here): nothing while the main window is active,
// the notification behind desktop-notifications, the sound behind
// notification-sound and independent of it (the parity report's N2).
// Windows-only: the sound is skipped in quiet hours, the notification not.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Presentation;
using Malachi.Core.Settings;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class NotificationPolicyTests
{
    [Theory]
    // desktop-notifications, notification-sound, main window active, quiet: shown, played
    [InlineData(true, false, false, false, true, false)] // the defaults
    [InlineData(true, true, false, false, true, true)]
    [InlineData(false, true, false, false, false, true)] // the sound is its own switch
    [InlineData(false, false, false, false, false, false)]
    [InlineData(true, true, true, false, false, false)] // the user is looking at the main window
    [InlineData(true, true, false, true, true, false)] // quiet hours: Windows holds the toast back itself
    [InlineData(false, true, false, true, false, false)]
    public void Rules(bool notifications, bool sound, bool active, bool quiet, bool shown, bool played)
    {
        var h = new Harness { Active = active, Quiet = quiet };
        h.Settings.DesktopNotifications = notifications;
        h.Settings.NotificationSound = sound;
        h.Policy.Deliver(New("m1"));
        Assert.Equal(shown ? 1 : 0, h.Notifier.Shown.Count);
        Assert.Equal(played ? 1 : 0, h.Sound.Played);
        // Quiet hours are only asked about for a sound that would play.
        Assert.Equal(sound && !active ? 1 : 0, h.Sound.Asked);
    }

    [Fact]
    public void TheNotificationIsTheMessages()
    {
        var h = new Harness();
        h.Policy.Deliver(New("m_123", from: "Alice Example", subject: "Lunch?"));
        var d = Assert.Single(h.Notifier.Shown);
        Assert.Equal(DesktopNotification.For(New("m_123", from: "Alice Example", subject: "Lunch?")), d);
        Assert.Equal("Alice Example", d.Title);
        Assert.Equal("Lunch?", d.Body);
        Assert.Equal("message-m_123", d.Tag);
        Assert.Equal("acc", d.Group);
    }

    [Fact]
    public void TheSettingsAndTheWindowAreReadForEveryMessage()
    {
        var h = new Harness();
        h.Policy.Deliver(New("m1"));
        h.Settings.DesktopNotifications = false;
        h.Settings.NotificationSound = true;
        h.Policy.Deliver(New("m2"));
        h.Active = true;
        h.Policy.Deliver(New("m3"));
        h.Active = false;
        h.Settings.DesktopNotifications = true;
        h.Policy.Deliver(New("m4"));
        Assert.Equal(["message-m1", "message-m4"], h.Notifier.Shown.ConvertAll(d => d.Tag));
        Assert.Equal(2, h.Sound.Played);
    }

    private static NewMessageNotification New(string id, string? from = null, string subject = "s") => new()
    {
        AccountId = new AccountId("acc"),
        FolderId = new FolderId("f"),
        Message = new MessageSummary
        {
            Id = new MessageId(id),
            AccountId = new AccountId("acc"),
            FolderId = new FolderId("f"),
            From = from is null ? [] : [new Address { Name = from, Email = "alice@example.invalid" }],
            Subject = subject,
            Date = DateTimeOffset.MinValue,
            Snippet = "",
            HasAttachments = false,
            Size = 0,
        },
    };

    private sealed class Harness
    {
        public Harness()
        {
            Policy = new NotificationPolicy(Settings, () => Active, Notifier, Sound);
        }

        public SettingsStore Settings { get; } = new(new InMemorySettingsBackend(), null);

        public RecordingNotifier Notifier { get; } = new();

        public RecordingSound Sound { get; } = new();

        public NotificationPolicy Policy { get; }

        public bool Active { get; set; }

        public bool Quiet
        {
            get => Sound.Quiet;
            set => Sound.Quiet = value;
        }
    }

    private sealed class RecordingNotifier : IDesktopNotifier
    {
        public List<DesktopNotification> Shown { get; } = [];

        public void Show(DesktopNotification notification) => Shown.Add(notification);
    }

    private sealed class RecordingSound : INewMailSound
    {
        public bool Quiet { get; set; }

        public int Asked { get; private set; }

        public int Played { get; private set; }

        public bool IsQuietTime
        {
            get
            {
                Asked++;
                return Quiet;
            }
        }

        public void Play() => Played++;
    }
}
