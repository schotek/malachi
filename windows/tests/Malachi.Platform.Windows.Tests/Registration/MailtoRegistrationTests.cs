// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of MailtoRegistration, Malachi Mail as a mailto: handler (the
// counterpart of the desktop file's MimeType=x-scheme-handler/mailto): the
// keys measured in APP-SPIKES §7 written under a test root of their own,
// rewritten when missing or stale (a moved app folder, a changed value,
// what an older version left), and removed for an uninstaller. The shell
// is never told: the tests count the notifications instead. That Windows
// really lists the handler is checked outside the tests, with the real
// root, from a process outside any MSIX container (docs/windows-port.md §1).

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using Malachi.Platform.Windows.Registration;
using Malachi.Platform.Windows.Tests.Startup;
using Microsoft.Win32;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Registration;

public sealed class MailtoRegistrationTests : IDisposable
{
    private readonly TestRegistryRoot root = new();
    private readonly string exe = Path.Combine(AppContext.BaseDirectory, "Malachi Mail", "MalachiMail.exe");
    private int notified;

    public void Dispose() => root.Dispose();

    [Fact]
    public void WritesTheProgIdTheCapabilitiesAndTheRegisteredApplication()
    {
        var r = Make(exe);
        Assert.False(r.Exists);
        Assert.False(r.IsCurrent);
        Assert.True(r.EnsureRegistered());
        Assert.Equal(1, notified);
        Assert.True(r.IsCurrent);

        var q = "\"" + exe + "\"";
        Assert.Equal("Malachi Mail", Value(@"Software\Classes\io.github.schotek.Malachi.mailto", ""));
        Assert.Equal("", Value(@"Software\Classes\io.github.schotek.Malachi.mailto", "URL Protocol"));
        Assert.Equal(q + ",0", Value(@"Software\Classes\io.github.schotek.Malachi.mailto\DefaultIcon", ""));
        Assert.Equal(q + " \"%1\"", Value(@"Software\Classes\io.github.schotek.Malachi.mailto\shell\open\command", ""));
        Assert.Equal("Malachi Mail", Value(@"Software\Classes\io.github.schotek.Malachi.mailto\Application", "ApplicationName"));
        Assert.Equal("Read and send email", Value(@"Software\Classes\io.github.schotek.Malachi.mailto\Application", "ApplicationDescription"));
        Assert.Equal("Malachi Mail", Value(@"Software\Clients\Mail\Malachi Mail", ""));
        Assert.Equal(q, Value(@"Software\Clients\Mail\Malachi Mail\shell\open\command", ""));
        Assert.Equal("Malachi Mail", Value(@"Software\Clients\Mail\Malachi Mail\Capabilities", "ApplicationName"));
        Assert.Equal("Read and send email", Value(@"Software\Clients\Mail\Malachi Mail\Capabilities", "ApplicationDescription"));
        Assert.Equal(q + ",0", Value(@"Software\Clients\Mail\Malachi Mail\Capabilities", "ApplicationIcon"));
        Assert.Equal("io.github.schotek.Malachi.mailto", Value(@"Software\Clients\Mail\Malachi Mail\Capabilities\URLAssociations", "mailto"));
        Assert.Equal("Malachi Mail", Value(@"Software\Clients\Mail\Malachi Mail\Capabilities\StartMenu", "Mail"));
        Assert.Equal(@"Software\Clients\Mail\Malachi Mail\Capabilities", Value(@"Software\RegisteredApplications", "Malachi Mail"));

        // Current: nothing is written again, and the shell is not bothered.
        Assert.False(r.EnsureRegistered());
        Assert.Equal(1, notified);
    }

    [Fact]
    public void AMovedAppFolderRegistersAgain()
    {
        Make(exe).Register();
        var moved = Make(Path.Combine(AppContext.BaseDirectory, "Elsewhere", "MalachiMail.exe"));
        Assert.True(moved.Exists);
        Assert.False(moved.IsCurrent);
        Assert.True(moved.EnsureRegistered());
        Assert.True(moved.IsCurrent);
        Assert.False(Make(exe).IsCurrent);
        Assert.Equal("\"" + moved.ExecutablePath + "\" \"%1\"", Value(@"Software\Classes\io.github.schotek.Malachi.mailto\shell\open\command", ""));
    }

    [Theory]
    [InlineData(@"Software\Clients\Mail\Malachi Mail\Capabilities\URLAssociations", "mailto", "SomethingElse.mailto")]
    [InlineData(@"Software\RegisteredApplications", "Malachi Mail", @"Software\Clients\Mail\Other")]
    [InlineData(@"Software\Classes\io.github.schotek.Malachi.mailto\Application", "ApplicationName", "MalachiMail")]
    public void AChangedValueIsStale(string key, string name, string value)
    {
        var r = Make(exe);
        r.Register();
        using (var k = root.Key.OpenSubKey(key, writable: true)!)
        {
            k.SetValue(name, value);
        }
        Assert.False(r.IsCurrent);
        Assert.True(r.EnsureRegistered());
        Assert.True(r.IsCurrent);
    }

    [Fact]
    public void AValueOfAnotherKindOrAMissingOneIsStale()
    {
        var r = Make(exe);
        r.Register();
        using (var k = root.Key.OpenSubKey(@"Software\Classes\io.github.schotek.Malachi.mailto", writable: true)!)
        {
            k.SetValue("URL Protocol", "", RegistryValueKind.ExpandString);
        }
        Assert.False(r.IsCurrent);
        r.Register();
        root.Key.DeleteSubKeyTree(@"Software\Clients\Mail\Malachi Mail\Capabilities\StartMenu");
        Assert.False(r.IsCurrent);
    }

    [Fact]
    public void WhatAnOlderVersionLeftUnderItsKeysGoes()
    {
        using (var k = root.Key.CreateSubKey(@"Software\Classes\io.github.schotek.Malachi.mailto\shell\print\command", writable: true))
        {
            k.SetValue("", "old");
        }
        using (var k = root.Key.CreateSubKey(@"Software\Clients\Mail\Malachi Mail\Capabilities", writable: true))
        {
            k.SetValue("Hidden", 1, RegistryValueKind.DWord);
        }
        Make(exe).Register();
        Assert.Null(root.Key.OpenSubKey(@"Software\Classes\io.github.schotek.Malachi.mailto\shell\print"));
        using var capabilities = root.Key.OpenSubKey(@"Software\Clients\Mail\Malachi Mail\Capabilities")!;
        Assert.Null(capabilities.GetValue("Hidden"));
    }

    [Fact]
    public void UnregisteringRemovesItAllAndLeavesOtherApplications()
    {
        using (var k = root.Key.CreateSubKey(@"Software\RegisteredApplications", writable: true))
        {
            k.SetValue("Other Mail", @"Software\Clients\Mail\Other Mail\Capabilities");
        }
        using (var k = root.Key.CreateSubKey(@"Software\Clients\Mail\Other Mail", writable: true))
        {
            k.SetValue("", "Other Mail");
        }
        var r = Make(exe);
        Assert.False(r.Unregister());
        Assert.Equal(0, notified);
        r.Register();
        Assert.True(r.Unregister());
        Assert.Equal(2, notified);
        Assert.False(r.Exists);
        Assert.Null(root.Key.OpenSubKey(@"Software\Classes\io.github.schotek.Malachi.mailto"));
        Assert.Null(root.Key.OpenSubKey(@"Software\Clients\Mail\Malachi Mail"));
        Assert.Null(Value(@"Software\RegisteredApplications", "Malachi Mail"));
        Assert.Equal(@"Software\Clients\Mail\Other Mail\Capabilities", Value(@"Software\RegisteredApplications", "Other Mail"));
        Assert.NotNull(root.Key.OpenSubKey(@"Software\Clients\Mail\Other Mail"));
        Assert.False(r.Unregister());
    }

    [Fact]
    public void TheDefaultIsWhatTheShellSays()
    {
        var answers = new Queue<string?>(["io.github.schotek.Malachi.mailto", "IO.GITHUB.SCHOTEK.MALACHI.MAILTO", "AppXabc", null]);
        var asked = new List<string>();
        var r = new MailtoRegistration(root.Key, exe, null, protocol =>
        {
            asked.Add(protocol);
            return answers.Dequeue();
        });
        Assert.True(r.IsDefault);
        Assert.True(r.IsDefault);
        Assert.False(r.IsDefault);
        Assert.False(r.IsDefault);
        Assert.Equal(["mailto", "mailto", "mailto", "mailto"], asked);
        Assert.False(new MailtoRegistration(root.Key, exe).IsDefault);
    }

    [Fact]
    public void TheValuesNameOnlyItsOwnKeys()
    {
        var keys = Make(exe).Values.Select(v => v.Key).Distinct().ToList();
        Assert.All(keys, k => Assert.True(
            k.StartsWith(@"Software\Classes\io.github.schotek.Malachi.mailto", StringComparison.Ordinal) ||
            k.StartsWith(@"Software\Clients\Mail\Malachi Mail", StringComparison.Ordinal) ||
            k == @"Software\RegisteredApplications", k));
    }

    [Fact]
    public void TheShellsViewAnswers()
    {
        // Whatever this machine has registered, the queries work and do not throw.
        _ = ShellAssociations.DefaultProgId("mailto");
        _ = ShellAssociations.Handlers("mailto");
        Assert.Null(ShellAssociations.OpenCommand("io.github.schotek.Malachi.Tests.NoSuchProgId"));
        Assert.Null(ShellAssociations.FriendlyAppName("io.github.schotek.Malachi.Tests.NoSuchProgId"));
    }

    [Fact]
    public void SettingsPagesOnly()
    {
        Assert.Equal("ms-settings:defaultapps?registeredAppUser=Malachi%20Mail", SystemSettings.DefaultAppsUri);
        Assert.Equal("ms-settings:startupapps", SystemSettings.StartupAppsUri);
        Assert.Throws<ArgumentException>(() => SystemSettings.Open("https://example.com/"));
        Assert.Throws<ArgumentException>(() => SystemSettings.Open("C:\\Windows\\System32\\calc.exe"));
    }

    private MailtoRegistration Make(string executable) => new(root.Key, executable, () => notified++);

    private string? Value(string key, string name)
    {
        using var k = root.Key.OpenSubKey(key);
        return k?.GetValue(name) as string;
    }
}
