// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the Mark of the Web (Attachments/MarkOfTheWeb.cs), the
// counterpart of the quarantine attribute of macos/Sources/MalachiMail/
// Attachments/AttachmentActions.swift, which has no test of its own there.
// The first group runs Attachment Services on this machine and pins down
// what the design rests on (the zones written, the policy deleting a
// blocked type); the second drives the decisions through a stand-in.

using System;
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

    [Fact]
    public async Task ADocumentForOpeningIsMarkedRestricted()
    {
        SkipUnlessDefaultPolicy();
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "%PDF-1.7 hello");

        var mark = await new MarkOfTheWeb().MarkAsync(path, "report.pdf", AttachmentUse.Open, TestContext.Current.CancellationToken);

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

        var mark = await new MarkOfTheWeb().MarkAsync(path, "letter.docx", AttachmentUse.Save, TestContext.Current.CancellationToken);

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

        var mark = await marker.MarkAsync(path, "setup.exe", AttachmentUse.Save, TestContext.Current.CancellationToken);

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
        var mark = await new MarkOfTheWeb().MarkAsync(path, "invoice.js", AttachmentUse.Open, TestContext.Current.CancellationToken);

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

        var mark = await new MarkOfTheWeb().MarkAsync(path, "empty.txt", AttachmentUse.Open, TestContext.Current.CancellationToken);

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

        await new MarkOfTheWeb(services, () => false).MarkAsync(path, "a.txt", AttachmentUse.Open, TestContext.Current.CancellationToken);

        Assert.Equal(ApartmentState.STA, services.Apartment);
    }

    [Fact]
    public async Task MarkRefusesWhatIsNotAFullPath()
    {
        var marker = new MarkOfTheWeb(new StandInServices(), () => false);

        await Assert.ThrowsAsync<ArgumentException>(() => marker.MarkAsync("a.txt", "a.txt", AttachmentUse.Open, TestContext.Current.CancellationToken));
        await Assert.ThrowsAsync<ArgumentException>(() => marker.MarkAsync("C:\\x\\a.txt", "", AttachmentUse.Open, TestContext.Current.CancellationToken));
        await Assert.ThrowsAsync<ArgumentException>(() => marker.MarkAsync("C:\\x\\a.txt:hidden", "a.txt", AttachmentUse.Open, TestContext.Current.CancellationToken));
    }

    [Fact]
    public async Task ACancelledMarkDoesNothing()
    {
        var services = new StandInServices();
        using var temp = new TestDirectory();
        var path = Write(temp, "a.txt", "x");

        await Assert.ThrowsAnyAsync<OperationCanceledException>(
            () => new MarkOfTheWeb(services, () => false).MarkAsync(path, "a.txt", AttachmentUse.Open, new CancellationToken(canceled: true)));

        Assert.Equal(0, services.Saves);
    }

    [Fact]
    public void WithoutAttachmentServicesTheZoneIsWrittenDirectly()
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "x");
        var services = new StandInServices { Result = ClassNotRegistered };

        var mark = new MarkOfTheWeb(services, () => false).Mark(path, "report.pdf", AttachmentUse.Open);

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
        var services = new StandInServices { Blocks = true, Result = ClassNotRegistered };

        var mark = new MarkOfTheWeb(services, () => false).Mark(path, "setup.exe", AttachmentUse.Save);

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

        new MarkOfTheWeb(services, () => false).Mark(path, "setup.exe", AttachmentUse.Open);

        Assert.Equal(new (string, string, string?)[] { (path, "setup.exe", null) }, services.Calls);
        Assert.Equal(0, services.PolicyChecks);
    }

    [Theory]
    [InlineData(ReportedByAntivirus)]
    [InlineData(BlockedByPolicy)]
    public void AVerdictRejectsTheFileItLeft(int verdict)
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "x");
        var services = new StandInServices { Result = verdict };

        var mark = new MarkOfTheWeb(services, () => false).Mark(path, "report.pdf", AttachmentUse.Save);

        Assert.Equal(ZoneMarkOutcome.Rejected, mark.Outcome);
        Assert.False(mark.MayOpen);
        // The user's file, still there, marked all the same.
        Assert.True(mark.FileKept);
        Assert.Equal(ZoneIdentifier.Restricted, mark.ZoneId);
    }

    [Fact]
    public void AFileTheCheckRemovedIsRemoved()
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "x");
        var services = new StandInServices { Result = ReportedByAntivirus, Delete = true };

        var mark = new MarkOfTheWeb(services, () => false).Mark(path, "report.pdf", AttachmentUse.Save);

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

        var mark = new MarkOfTheWeb(new StandInServices(), () => true).Mark(path, "report.pdf", AttachmentUse.Open);

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

        var mark = new MarkOfTheWeb(new StandInServices { Result = ReportedByAntivirus }, () => true)
            .Mark(path, "report.pdf", AttachmentUse.Open);

        Assert.Equal(ZoneMarkOutcome.Rejected, mark.Outcome);
        Assert.False(mark.MayOpen);
        Assert.Null(ZoneIdentifier.Read(path));
    }

    [Fact]
    public void ASaveThatWroteNoZoneIsMarkedDirectly()
    {
        using var temp = new TestDirectory();
        var path = Write(temp, "report.pdf", "x");

        var mark = new MarkOfTheWeb(new StandInServices(), () => false).Mark(path, "report.pdf", AttachmentUse.Open);

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

        var mark = new MarkOfTheWeb(new StandInServices(), () => false).Mark(path, "report.pdf", AttachmentUse.Open);

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

        var mark = new MarkOfTheWeb(new StandInServices(), () => false).Mark(path, "report.pdf", AttachmentUse.Open);

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
    // them (the antivirus settings do not matter). A machine whose
    // administrator changed either is told so.
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
    }

    // Attachment Services played: the result it returns, whether it blocks,
    // whether it deletes the file, and what it was asked.
    private sealed class StandInServices : IAttachmentServices
    {
        public int Result { get; init; }

        public bool Blocks { get; init; }

        public bool Delete { get; init; }

        public int Saves { get; private set; }

        public int PolicyChecks { get; private set; }

        public ApartmentState? Apartment { get; private set; }

        public System.Collections.Generic.List<(string Path, string FileName, string? Source)> Calls { get; } = [];

        public bool PolicyBlocks(string fileName)
        {
            PolicyChecks++;
            return Blocks;
        }

        public int Save(string path, string fileName, string? source)
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
