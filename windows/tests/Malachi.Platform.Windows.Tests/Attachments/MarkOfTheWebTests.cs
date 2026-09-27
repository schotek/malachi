// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the Mark of the Web (Attachments/MarkOfTheWeb.cs), the
// counterpart of the quarantine attribute of macos/Sources/MalachiMail/
// Attachments/AttachmentActions.swift, which has no test of its own there.
// The first group runs Attachment Services on this machine and pins down
// what the design rests on (the zones written, the policy deleting a
// blocked type); the second drives the decisions through a stand-in.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Platform;
using Malachi.Platform.Windows.Attachments;
using Malachi.Platform.Windows.Tests.Files;
using Microsoft.Win32;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Attachments;

public sealed class MarkOfTheWebTests
{
    private const int ClassNotRegistered = unchecked((int)0x80040154); // REGDB_E_CLASSNOTREG
    private const int BlockedByPolicy = unchecked((int)0x800C000E); // INET_E_SECURITY_PROBLEM
    private const int ReportedByAntivirus = unchecked((int)0x80004005); // E_FAIL
    private const int VirusInfected = unchecked((int)0x800700E1); // HRESULT_FROM_WIN32(ERROR_VIRUS_INFECTED)
    private const int VirusDeleted = unchecked((int)0x800700E2); // HRESULT_FROM_WIN32(ERROR_VIRUS_DELETED)
    private const int AccessDenied = unchecked((int)0x80070005); // E_ACCESSDENIED

    [Fact]
    public async Task ADocumentForOpeningIsMarkedRestricted()
    {
        SkipUnlessDefaultPolicy();
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "%PDF-1.7 hello");

        var mark = await new MarkOfTheWeb().MarkAsync(path, AttachmentUse.Open, TestContext.Current.CancellationToken);

        // Measured: without a source, Save writes the zone of mail.
        Assert.Equal(ZoneMarkOutcome.Marked, mark.Outcome);
        Assert.Equal(ZoneIdentifier.Restricted, mark.ZoneId);
        Assert.Equal(0, mark.SaveResult);
        Assert.True(mark.MayOpen);
        Assert.True(mark.FileKept);
        Assert.Equal(ZoneIdentifier.Restricted, ZoneIdentifier.Read(path));
        Assert.Equal("%PDF-1.7 hello", File.ReadAllText(path));
    }

    [Fact]
    public async Task ASavedDocumentIsMarkedRestricted()
    {
        SkipUnlessDefaultPolicy();
        using var temp = new TestDirectory();
        var path = Write(temp, "letter.docx", "PK");

        var mark = await new MarkOfTheWeb().MarkAsync(path, AttachmentUse.Save, TestContext.Current.CancellationToken);

        Assert.Equal(ZoneMarkOutcome.Marked, mark.Outcome);
        Assert.Equal(ZoneIdentifier.Restricted, ZoneIdentifier.Read(path));
    }

    [Fact]
    public async Task ASavedProgramIsKeptAndMarkedInternet()
    {
        SkipUnlessDefaultPolicy();
        using var temp = new TestDirectory();
        var path = Write(temp, "setup.exe", "not a program, but named like one");
        var marker = new MarkOfTheWeb();
        Assert.True(await marker.PolicyBlocksAsync("setup.exe", TestContext.Current.CancellationToken));

        var mark = await marker.MarkAsync(path, AttachmentUse.Save, TestContext.Current.CancellationToken);

        // The Restricted zone would have Save delete it; as a download from
        // the internet it stays, scanned, and running it will prompt.
        Assert.Equal(ZoneMarkOutcome.Marked, mark.Outcome);
        Assert.True(mark.FileKept);
        Assert.Equal(ZoneIdentifier.Internet, mark.ZoneId);
        Assert.True(File.Exists(path));
        Assert.Contains("HostUrl=about:internet", File.ReadAllText(path + ":Zone.Identifier"), StringComparison.Ordinal);
    }

    [Fact]
    public async Task TheRestrictedPolicyDeletesAProgramItIsAskedToOpen()
    {
        SkipUnlessDefaultPolicy();
        using var temp = new TestDirectory();
        var path = Write(temp, "invoice.js", "WScript.Echo(1)");

        // Opening never gets here (IFileTypePolicy refuses it first); if it
        // did, the mark would not let it through.
        var mark = await new MarkOfTheWeb().MarkAsync(path, AttachmentUse.Open, TestContext.Current.CancellationToken);

        Assert.Equal(ZoneMarkOutcome.Removed, mark.Outcome);
        Assert.Equal(BlockedByPolicy, mark.SaveResult);
        Assert.False(mark.MayOpen);
        Assert.False(mark.FileKept);
        Assert.False(File.Exists(path));
    }

    [Fact]
    public async Task AnEmptyFileIsMarkedAndKept()
    {
        SkipUnlessDefaultPolicy();
        using var temp = new TestDirectory();
        var path = Write(temp, "empty.txt", "");

        var mark = await new MarkOfTheWeb().MarkAsync(path, AttachmentUse.Open, TestContext.Current.CancellationToken);

        Assert.Equal(ZoneMarkOutcome.Marked, mark.Outcome);
        Assert.True(File.Exists(path));
    }

    [Theory]
    [InlineData("x.exe", true)]
    [InlineData("x.js", true)]
    [InlineData("x.lnk", true)]
    [InlineData("x.url", true)]
    [InlineData("x.iso", true)]
    [InlineData("x.vhdx", true)]
    [InlineData("x.msi", true)]
    [InlineData("x.pdf", false)]
    [InlineData("x.txt", false)]
    [InlineData("x.docx", false)]
    [InlineData("x.zip", false)]
    // Missed by the policy, listed by DangerousTypes.
    [InlineData("x.rdp", false)]
    [InlineData("x.appinstaller", false)]
    public async Task PolicyBlocksTheHighRiskTypes(string name, bool blocked)
    {
        SkipUnlessDefaultPolicy();

        var got = await new MarkOfTheWeb().PolicyBlocksAsync(name, TestContext.Current.CancellationToken);

        Assert.Equal(blocked, got);
        // Measured: the same verdict as the shell's.
        Assert.Equal(blocked, FileTypePolicy.IsDangerousToTheShell(Path.GetExtension(name)));
    }

    [Fact]
    public async Task MarkRunsOnAnStaThread()
    {
        var services = new StandInServices();
        using var temp = new TestDirectory();
        var path = Write(temp, "a.txt", "x");

        await new MarkOfTheWeb(services, () => false).MarkAsync(path, AttachmentUse.Open, TestContext.Current.CancellationToken);

        Assert.Equal(ApartmentState.STA, services.Apartment);
    }

    [Theory]
    [InlineData("secret-name.txt")]
    [InlineData("C:\\x\\")]
    [InlineData("C:\\x\\secret-name.txt:hidden")]
    [InlineData("")]
    public async Task MarkRefusesWhatIsNotAFilesFullPath(string path)
    {
        var services = new StandInServices();
        var marker = new MarkOfTheWeb(services, () => false);

        var e = await Assert.ThrowsAsync<ArgumentException>(() => marker.MarkAsync(path, AttachmentUse.Open, TestContext.Current.CancellationToken));

        // The path carries the attachment's name: never in a message.
        Assert.DoesNotContain("secret-name", e.Message, StringComparison.Ordinal);
        Assert.Equal(0, services.Saves);
    }

    [Theory]
    [InlineData(AttachmentUse.Open)]
    [InlineData(AttachmentUse.Save)]
    public void TheNameJudgedIsThePathsOwn(AttachmentUse use)
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "Invoice 2026.PDF", "x");
        var services = new StandInServices();

        new MarkOfTheWeb(services, () => false).Mark(path, use);

        // Both the policy check (for a saved file) and Save see the name
        // the file has, so the zone chosen is the one for that file.
        Assert.All(services.PolicyNames, name => Assert.Equal("Invoice 2026.PDF", name));
        Assert.Equal(use == AttachmentUse.Save ? 1 : 0, services.PolicyNames.Count);
        Assert.Equal("Invoice 2026.PDF", Assert.Single(services.Calls).FileName);
    }

    [Fact]
    public async Task ACancelledMarkDoesNothing()
    {
        var services = new StandInServices();
        using var temp = new TestDirectory();
        var path = Write(temp, "a.txt", "x");

        await Assert.ThrowsAnyAsync<OperationCanceledException>(
            () => new MarkOfTheWeb(services, () => false).MarkAsync(path, AttachmentUse.Open, new CancellationToken(canceled: true)));

        Assert.Equal(0, services.Saves);
    }

    [Fact]
    public void WithoutAttachmentServicesTheZoneIsWrittenDirectly()
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "x");
        var services = new StandInServices { Result = AttachmentSaveResult.Unavailable(ClassNotRegistered) };

        var mark = new MarkOfTheWeb(services, () => false).Mark(path, AttachmentUse.Open);

        Assert.Equal(ZoneMarkOutcome.MarkedDirectly, mark.Outcome);
        Assert.Equal(ZoneIdentifier.Restricted, mark.ZoneId);
        Assert.Equal(ClassNotRegistered, mark.SaveResult);
        Assert.True(mark.MayOpen);
        Assert.Equal("[ZoneTransfer]\r\nZoneId=4\r\n", File.ReadAllText(path + ":Zone.Identifier"));
        Assert.Equal(new (string, string, string?)[] { (path, "report.pdf", null) }, services.Calls);
    }

    [Fact]
    public void ASavedProgramFallsBackToTheInternetZone()
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "setup.exe", "x");
        var services = new StandInServices { Blocks = true, Result = AttachmentSaveResult.Unavailable(ClassNotRegistered) };

        var mark = new MarkOfTheWeb(services, () => false).Mark(path, AttachmentUse.Save);

        Assert.Equal(ZoneMarkOutcome.MarkedDirectly, mark.Outcome);
        Assert.Equal(ZoneIdentifier.Internet, mark.ZoneId);
        Assert.Equal("[ZoneTransfer]\r\nZoneId=3\r\nHostUrl=about:internet\r\n", File.ReadAllText(path + ":Zone.Identifier"));
        Assert.Equal(new (string, string, string?)[] { (path, "setup.exe", MarkOfTheWeb.InternetSource) }, services.Calls);
    }

    [Fact]
    public void OpeningNeverAsksForTheInternetZone()
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "setup.exe", "x");
        var services = new StandInServices { Blocks = true };

        new MarkOfTheWeb(services, () => false).Mark(path, AttachmentUse.Open);

        Assert.Equal(new (string, string, string?)[] { (path, "setup.exe", null) }, services.Calls);
        Assert.Equal(0, services.PolicyChecks);
    }

    [Theory]
    [InlineData(ReportedByAntivirus)]
    [InlineData(BlockedByPolicy)]
    [InlineData(VirusInfected)]
    [InlineData(VirusDeleted)]
    public void AVerdictRejectsTheFileItLeft(int verdict)
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "x");
        var services = new StandInServices { Result = AttachmentSaveResult.Failed(verdict) };

        var mark = new MarkOfTheWeb(services, () => false).Mark(path, AttachmentUse.Save);

        Assert.Equal(ZoneMarkOutcome.Rejected, mark.Outcome);
        Assert.False(mark.MayOpen);
        // The user's file, still there, marked all the same.
        Assert.True(mark.FileKept);
        Assert.Equal(ZoneIdentifier.Restricted, mark.ZoneId);
        Assert.Equal(verdict, mark.SaveResult);
    }

    [Theory]
    [InlineData(AccessDenied)]
    [InlineData(ClassNotRegistered)]
    [InlineData(unchecked((int)0x80070020))] // ERROR_SHARING_VIOLATION
    public void ACheckThatFailedNeverOpensTheFile(int failure)
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "x");
        var services = new StandInServices { Result = AttachmentSaveResult.Failed(failure) };

        var mark = new MarkOfTheWeb(services, () => false).Mark(path, AttachmentUse.Open);

        // Save ran and failed without a verdict: the file may not have been
        // scanned. It is marked, and it stays shut.
        Assert.Equal(ZoneMarkOutcome.CheckFailed, mark.Outcome);
        Assert.False(mark.MayOpen);
        Assert.True(mark.FileKept);
        Assert.Equal(ZoneIdentifier.Restricted, mark.ZoneId);
        Assert.Equal(failure, mark.SaveResult);
    }

    [Fact]
    public void ASavedFileWhoseCheckFailedIsKeptAndMarked()
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "setup.exe", "x");
        var services = new StandInServices { Blocks = true, Result = AttachmentSaveResult.Failed(AccessDenied) };

        var mark = new MarkOfTheWeb(services, () => false).Mark(path, AttachmentUse.Save);

        Assert.Equal(ZoneMarkOutcome.CheckFailed, mark.Outcome);
        Assert.True(mark.FileKept);
        Assert.Equal(ZoneIdentifier.Internet, ZoneIdentifier.Read(path));
    }

    [Fact]
    public void ACheckThatFailedStaysFailedWhenZoneInformationIsOff()
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "x");

        var mark = new MarkOfTheWeb(new StandInServices { Result = AttachmentSaveResult.Failed(AccessDenied) }, () => true)
            .Mark(path, AttachmentUse.Open);

        Assert.Equal(ZoneMarkOutcome.CheckFailed, mark.Outcome);
        Assert.False(mark.MayOpen);
        Assert.Null(ZoneIdentifier.Read(path));
    }

    [Fact]
    public void WithoutAttachmentServicesAndZoneInformationTheFileOpens()
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "x");

        var mark = new MarkOfTheWeb(new StandInServices { Result = AttachmentSaveResult.Unavailable(ClassNotRegistered) }, () => true)
            .Mark(path, AttachmentUse.Open);

        // No service to scan it and no mark to write: as without the client.
        Assert.Equal(ZoneMarkOutcome.PolicyDisabled, mark.Outcome);
        Assert.True(mark.MayOpen);
    }

    [Fact]
    public void AFileTheCheckRemovedIsRemoved()
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "x");
        var services = new StandInServices { Result = AttachmentSaveResult.Failed(ReportedByAntivirus), Delete = true };

        var mark = new MarkOfTheWeb(services, () => false).Mark(path, AttachmentUse.Save);

        Assert.Equal(ZoneMarkOutcome.Removed, mark.Outcome);
        Assert.Null(mark.ZoneId);
        Assert.False(mark.FileKept);
        Assert.False(mark.MayOpen);
    }

    [Fact]
    public void ZoneInformationTurnedOffByPolicyWritesNothing()
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "x");

        var mark = new MarkOfTheWeb(new StandInServices(), () => true).Mark(path, AttachmentUse.Open);

        Assert.Equal(ZoneMarkOutcome.PolicyDisabled, mark.Outcome);
        Assert.Null(mark.ZoneId);
        Assert.Null(ZoneIdentifier.Read(path));
        // The administrator's call: the file opens without a mark.
        Assert.True(mark.MayOpen);
    }

    [Fact]
    public void AVerdictStandsWhenZoneInformationIsOff()
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "x");

        var mark = new MarkOfTheWeb(new StandInServices { Result = AttachmentSaveResult.Failed(ReportedByAntivirus) }, () => true)
            .Mark(path, AttachmentUse.Open);

        Assert.Equal(ZoneMarkOutcome.Rejected, mark.Outcome);
        Assert.False(mark.MayOpen);
        Assert.Null(ZoneIdentifier.Read(path));
    }

    [Fact]
    public void ASaveThatWroteNoZoneIsMarkedDirectly()
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "x");

        var mark = new MarkOfTheWeb(new StandInServices(), () => false).Mark(path, AttachmentUse.Open);

        Assert.Equal(ZoneMarkOutcome.MarkedDirectly, mark.Outcome);
        Assert.Equal(ZoneIdentifier.Restricted, mark.ZoneId);
    }

    [Fact]
    public void AZoneThatDoesNotMarkIsReplaced()
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "x");
        // Left from before: the Trusted sites zone, which marks nothing.
        ZoneIdentifier.Write(path, 2);

        var mark = new MarkOfTheWeb(new StandInServices(), () => false).Mark(path, AttachmentUse.Open);

        Assert.Equal(ZoneMarkOutcome.MarkedDirectly, mark.Outcome);
        Assert.Equal(ZoneIdentifier.Restricted, ZoneIdentifier.Read(path));
    }

    [Fact]
    public void AFileNoStreamCanBeWrittenToIsNotMarked()
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "x");
        // A read-only file takes no new stream, as a file system without
        // streams takes none.
        File.SetAttributes(path, FileAttributes.ReadOnly);

        var mark = new MarkOfTheWeb(new StandInServices(), () => false).Mark(path, AttachmentUse.Open);

        Assert.Equal(ZoneMarkOutcome.NotMarked, mark.Outcome);
        Assert.Null(mark.ZoneId);
        Assert.False(mark.MayOpen);
        Assert.True(mark.FileKept);
    }

    [Fact]
    public void TheClientGuidIsFixed()
    {
        Assert.Equal(new Guid("470b4a3e-f3ee-4164-9315-5265b7395b63"), MarkOfTheWeb.ClientGuid);
    }

    private static string Write(TestDirectory temp, string name, string content)
    {
        var path = Path.Combine(temp.Path, name);
        File.WriteAllText(path, content);
        return path;
    }

    // What the machine tests pin down holds for the default attachment
    // policy: zone information kept, the file types' risks as Windows ships
    // them, and the zones' "Launching applications and unsafe files"
    // (URL actions 1806–1808, which decide what Save keeps) as Windows
    // ships them, without Internet Explorer Enhanced Security Configuration
    // (on by default on Windows Server). The antivirus settings do not
    // matter. A machine where any of that differs is told so.
    private static void SkipUnlessDefaultPolicy()
    {
        var policies = new (string Key, string[] Values)[]
        {
            (@"Software\Microsoft\Windows\CurrentVersion\Policies\Attachments", ["SaveZoneInformation"]),
            (@"Software\Microsoft\Windows\CurrentVersion\Policies\Associations",
                ["DefaultFileTypeRisk", "HighRiskFileTypes", "ModRiskFileTypes", "LowRiskFileTypes"]),
        };
        foreach (var hive in new[] { Registry.CurrentUser, Registry.LocalMachine })
        {
            foreach (var (name, values) in policies)
            {
                using var key = hive.OpenSubKey(name);
                foreach (var value in values)
                {
                    if (key?.GetValue(value) is not null)
                    {
                        Assert.Skip($"the attachment policy of this machine sets {value}");
                    }
                }
            }
        }
        // Windows' defaults: the Internet zone prompts for high and moderate
        // risk and allows low; Restricted sites disables high risk.
        var defaults = new (int Zone, string Action, int Value)[]
        {
            (3, "1806", 1), (3, "1807", 1), (3, "1808", 0),
            (4, "1806", 3), (4, "1807", 1), (4, "1808", 0),
        };
        foreach (var hive in new[] { Registry.CurrentUser, Registry.LocalMachine })
        {
            foreach (var root in new[] { @"Software\Policies\Microsoft\Windows\CurrentVersion\Internet Settings", @"Software\Microsoft\Windows\CurrentVersion\Internet Settings" })
            {
                foreach (var (zone, action, value) in defaults)
                {
                    using var key = hive.OpenSubKey($@"{root}\Zones\{zone}");
                    if (key?.GetValue(action) is { } set && Convert.ToInt32(set, CultureInfo.InvariantCulture) != value)
                    {
                        Assert.Skip($"zone {zone} of this machine sets {action} to {set}");
                    }
                }
            }
        }
        using (var zoneMap = Registry.CurrentUser.OpenSubKey(@"Software\Microsoft\Windows\CurrentVersion\Internet Settings\ZoneMap"))
        {
            if (zoneMap?.GetValue("IEHarden") is { } harden && Convert.ToInt32(harden, CultureInfo.InvariantCulture) != 0)
            {
                Assert.Skip("Internet Explorer Enhanced Security Configuration is on");
            }
        }
        foreach (var component in new[] { "{A509B1A7-37EF-4b3f-8CFC-4F3A74704073}", "{A509B1A8-37EF-4b3f-8CFC-4F3A74704073}" })
        {
            using var key = Registry.LocalMachine.OpenSubKey($@"SOFTWARE\Microsoft\Active Setup\Installed Components\{component}");
            if (key?.GetValue("IsInstalled") is { } installed && Convert.ToInt32(installed, CultureInfo.InvariantCulture) != 0)
            {
                Assert.Skip("Internet Explorer Enhanced Security Configuration is on");
            }
        }
    }

    // Attachment Services played: the result it returns, whether it blocks,
    // whether it deletes the file, and what it was asked.
    private sealed class StandInServices : IAttachmentServices
    {
        public AttachmentSaveResult Result { get; init; } = AttachmentSaveResult.Saved;

        public bool Blocks { get; init; }

        public bool Delete { get; init; }

        public int Saves { get; private set; }

        public int PolicyChecks => PolicyNames.Count;

        public List<string> PolicyNames { get; } = [];

        public ApartmentState? Apartment { get; private set; }

        public List<(string Path, string FileName, string? Source)> Calls { get; } = [];

        public bool PolicyBlocks(string fileName)
        {
            PolicyNames.Add(fileName);
            return Blocks;
        }

        public AttachmentSaveResult Save(string path, string fileName, string? source)
        {
            Saves++;
            Apartment = Thread.CurrentThread.GetApartmentState();
            Calls.Add((path, fileName, source));
            if (Delete)
            {
                File.Delete(path);
            }
            return Result;
        }
    }
}
