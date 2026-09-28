// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of LaunchAtLogin, the Windows counterpart of LoginItemService.swift
// and of the Background portal request (ui/internal/background): the Run
// value, the user's switch in StartupApproved\Run that is never
// overwritten, the outcome of a request (preferences.go bindLaunchAtLogin
// flips the switch only when it was granted), the mirror key, and a Run
// value left by a moved app folder. Every test works under its own
// HKCU\Software\io.github.schotek.Malachi.Tests.<guid> (TestRegistryRoot).

using System;
using System.Collections.Generic;
using System.IO;
using Malachi.Core.Settings;
using Malachi.Platform.Windows.Startup;
using Microsoft.Win32;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Startup;

public sealed class LaunchAtLoginTests : IDisposable
{
    private const string Run = @"Software\Microsoft\Windows\CurrentVersion\Run";
    private const string Approved = @"Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run";

    private static readonly byte[] DisabledByUser = [0x03, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
    private static readonly byte[] EnabledByUser = [0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0];

    private readonly TestRegistryRoot root = new();
    private readonly List<string> opened = [];
    private readonly string exe = Path.Combine(AppContext.BaseDirectory, "MalachiMail.exe");

    public void Dispose() => root.Dispose();

    [Fact]
    public void NothingIsRegisteredAtFirst()
    {
        var l = Make();
        Assert.Equal(LaunchAtLoginStatus.NotRegistered, l.Status);
        Assert.False(l.IsEnabled);
        Assert.Null(l.RegisteredCommand);
        Assert.Null(root.Key.OpenSubKey(Run));
    }

    [Fact]
    public void TurningItOnWritesTheRunValueWithBackground()
    {
        var l = Make();
        Assert.Equal(LaunchAtLoginOutcome.Granted, l.Set(true));
        Assert.Equal(LaunchAtLoginStatus.Enabled, l.Status);
        using var run = root.Key.OpenSubKey(Run)!;
        Assert.Equal("\"" + exe + "\" --background", run.GetValue("Malachi Mail"));
        Assert.Equal(RegistryValueKind.String, run.GetValueKind("Malachi Mail"));
        Assert.Equal(l.Command, l.RegisteredCommand);

        Assert.Equal(LaunchAtLoginOutcome.Granted, l.Set(false));
        Assert.Equal(LaunchAtLoginStatus.NotRegistered, l.Status);
        using var after = root.Key.OpenSubKey(Run)!;
        Assert.Null(after.GetValue("Malachi Mail"));
        // Off again is still granted: there is nothing to remove.
        Assert.Equal(LaunchAtLoginOutcome.Granted, l.Set(false));
    }

    [Fact]
    public void TheUsersSwitchInWindowsSettingsDecides()
    {
        var l = Make();
        l.Set(true);
        Approve(EnabledByUser);
        Assert.Equal(LaunchAtLoginStatus.Enabled, l.Status);
        Approve(DisabledByUser);
        Assert.Equal(LaunchAtLoginStatus.DisabledByUser, l.Status);
        Assert.False(l.IsEnabled);
        // An approval of something that is not binary is no approval.
        using (var approved = root.Key.CreateSubKey(Approved, writable: true))
        {
            approved.SetValue("Malachi Mail", "03", RegistryValueKind.String);
        }
        Assert.Equal(LaunchAtLoginStatus.Enabled, l.Status);
        // Without a Run value an approval says nothing.
        Approve(DisabledByUser);
        l.Set(false);
        Assert.Equal(LaunchAtLoginStatus.NotRegistered, l.Status);
    }

    [Fact]
    public void TurningOnWhatTheUserTurnedOffNeedsTheUserAndNeverOverwritesTheirChoice()
    {
        var l = Make();
        Approve(DisabledByUser);
        Assert.Equal(LaunchAtLoginOutcome.RequiresApproval, l.Set(true));
        Assert.Equal(LaunchAtLoginStatus.DisabledByUser, l.Status);
        Assert.Equal(DisabledByUser, ApprovalValue());
        Assert.Equal(LaunchAtLoginOutcome.Granted, l.Set(false));
        Assert.Equal(DisabledByUser, ApprovalValue());

        Assert.True(l.OpenSystemSettings());
        Assert.Equal(["ms-settings:startupapps"], opened);
    }

    [Fact]
    public void TheSettingOnlyMirrorsWindows()
    {
        var l = Make();
        var settings = new SettingsStore(new InMemorySettingsBackend(), null);
        settings.LaunchAtLogin = true;
        Assert.False(l.MirrorInto(settings));
        Assert.False(settings.LaunchAtLogin);
        l.Set(true);
        Assert.True(l.MirrorInto(settings));
        Assert.True(settings.LaunchAtLogin);
        Approve(DisabledByUser);
        Assert.False(l.MirrorInto(settings));
        Assert.False(settings.LaunchAtLogin);
    }

    [Fact]
    public void ARunValueOfAMovedAppFolderFollowsIt()
    {
        var l = Make();
        Assert.False(l.RepairMovedExecutable());
        Approve(DisabledByUser);
        WriteRun("\"C:\\Nowhere\\Malachi Mail\\MalachiMail.exe\" --background");
        Assert.True(l.RepairMovedExecutable());
        Assert.Equal(l.Command, l.RegisteredCommand);
        // The user's switch belongs to the name and stays.
        Assert.Equal(LaunchAtLoginStatus.DisabledByUser, l.Status);
        Assert.Equal(DisabledByUser, ApprovalValue());
        Assert.False(l.RepairMovedExecutable());
    }

    [Fact]
    public void ThisCopysOwnValueIsBroughtUpToDate()
    {
        var l = Make();
        WriteRun("\"" + exe.ToUpperInvariant() + "\"");
        Assert.True(l.RepairMovedExecutable());
        Assert.Equal(l.Command, l.RegisteredCommand);
    }

    [Fact]
    public void AnotherExistingCopysValueIsLeftAlone()
    {
        var l = Make();
        // The test host exists, and is not MalachiMail.exe.
        var other = "\"" + Environment.ProcessPath + "\" --background";
        WriteRun(other);
        Assert.False(l.RepairMovedExecutable());
        Assert.Equal(other, l.RegisteredCommand);
        Assert.Equal(LaunchAtLoginStatus.Enabled, l.Status);
    }

    [Fact]
    public void TheTextsOfTheSwitch()
    {
        Assert.Null(LaunchAtLogin.RefusalText(LaunchAtLoginOutcome.Granted));
        Assert.Equal("Autostart was not granted", LaunchAtLogin.RefusalText(LaunchAtLoginOutcome.RequiresApproval));
        Assert.Equal("Autostart was not granted", LaunchAtLogin.RefusalText(LaunchAtLoginOutcome.NotGranted));
        Assert.Equal("Turned off in Windows Settings", LaunchAtLogin.StatusText(LaunchAtLoginStatus.DisabledByUser));
        Assert.Null(LaunchAtLogin.StatusText(LaunchAtLoginStatus.Enabled));
        Assert.Null(LaunchAtLogin.StatusText(LaunchAtLoginStatus.NotRegistered));
        Assert.Equal(
            "Launch at Login could not be changed: Access is denied.",
            LaunchAtLogin.FailureText(new UnauthorizedAccessException("Access is denied.")));
    }

    [Theory]
    [InlineData("\"C:\\Program Files\\Malachi Mail\\MalachiMail.exe\" --background", "C:\\Program Files\\Malachi Mail\\MalachiMail.exe")]
    [InlineData("  \"C:\\a b\\x.exe\"", "C:\\a b\\x.exe")]
    [InlineData("C:\\apps\\x.exe --background", "C:\\apps\\x.exe")]
    [InlineData("C:\\apps\\x.exe", "C:\\apps\\x.exe")]
    [InlineData("\"C:\\unterminated\\x.exe", "C:\\unterminated\\x.exe")]
    [InlineData("\"\" --background", null)]
    [InlineData("   ", null)]
    public void TheProgramOfARunCommand(string command, string? program)
    {
        Assert.Equal(program, LaunchAtLogin.ExecutableOf(command));
    }

    private LaunchAtLogin Make() => new(root.Key, exe, Run, Approved, uri =>
    {
        opened.Add(uri);
        return true;
    });

    private void Approve(byte[] value)
    {
        using var approved = root.Key.CreateSubKey(Approved, writable: true);
        approved.SetValue("Malachi Mail", value, RegistryValueKind.Binary);
    }

    private byte[]? ApprovalValue()
    {
        using var approved = root.Key.OpenSubKey(Approved);
        return approved?.GetValue("Malachi Mail") as byte[];
    }

    private void WriteRun(string command)
    {
        using var run = root.Key.CreateSubKey(Run, writable: true);
        run.SetValue("Malachi Mail", command, RegistryValueKind.String);
    }
}
