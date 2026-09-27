// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AttachmentsTests.swift
// (executableAttachmentTest); GTK: ui/internal/window/attachments_test.go
// (TestExecutableAttachment). The Windows cases (Outlook's list, what
// Windows runs, installs or follows, disk images, names Windows rewrites)
// are new. AttachmentsTests.swift is split by the class under test:
// DangerousTypesTests, WindowsFileNamesTests and OpenDirTests hold the
// suites of Malachi.Core.Platform; the rest of it (the chips) belongs in
// AttachmentsTests.

using System.Linq;
using Malachi.Core.Platform;
using Xunit;

namespace Malachi.Core.Tests.Platform;

public sealed class DangerousTypesTests
{
    [Theory]
    // GTK.
    [InlineData("setup.exe", "application/octet-stream", true)]
    [InlineData("invoice.pdf.exe", "application/pdf", true)]
    [InlineData("Report.PDF", "application/pdf", false)]
    [InlineData("run.SH", "text/plain", true)]
    [InlineData("notes.txt", "text/plain", false)]
    [InlineData("payload", "application/x-shellscript", true)]
    [InlineData("payload", "application/x-executable; name=x", true)]
    [InlineData("photo.jpg", "image/jpeg", false)]
    [InlineData("launcher.desktop", "application/x-desktop", true)]
    [InlineData("tool.jar", "application/java-archive", true)]
    [InlineData("", "", false)]
    [InlineData(".bashrc", "", false)] // no extension of its own
    // macOS: what the Finder runs, installs or follows.
    [InlineData("Install.command", "text/plain", true)]
    [InlineData("photo.jpg.APP", "image/jpeg", true)]
    [InlineData("Update.pkg", "application/octet-stream", true)]
    [InlineData("Bundle.mpkg", "", true)]
    [InlineData("profile.mobileconfig", "application/x-apple-aspen-config", true)]
    [InlineData("launch.jnlp", "", true)]
    [InlineData("site.webloc", "", true)]
    [InlineData("share.afploc", "", true)]
    [InlineData("link.url", "", true)]
    [InlineData("script.scpt", "", true)]
    [InlineData("script.applescript", "", true)]
    [InlineData("flow.workflow", "", true)]
    [InlineData("lib.dylib", "", true)]
    [InlineData("driver.kext", "", true)]
    [InlineData("pane.prefpane", "", true)]
    [InlineData("screen.saver", "", true)]
    [InlineData("x.osax", "", true)]
    [InlineData("run.fish", "", true)]
    [InlineData("run.tcsh", "", true)]
    [InlineData("shortcut.shortcut", "", true)]
    [InlineData("blob", "application/x-mach-binary", true)]
    [InlineData("blob", "application/vnd.apple.installer+xml; name=x", true)]
    [InlineData("blob", "application/x-java-jnlp-file", true)]
    // Still opened: documents that merely look Apple-ish.
    [InlineData("deck.key", "application/octet-stream", false)]
    [InlineData("notes.pages", "", false)]
    [InlineData("image.dmg", "application/x-apple-diskimage", false)]
    public void ExecutableAttachmentTest(string name, string contentType, bool want)
    {
        Assert.Equal(want, DangerousTypes.IsDangerous(name, contentType));
    }

    [Fact]
    public void ExecutableAttachmentTestListsTheMacExtensions()
    {
        string[] mac =
        [
            "command", "tool", "terminal", "webloc", "inetloc", "fileloc", "afploc", "ftploc", "url", "app", "pkg",
            "mpkg", "mobileconfig", "jnlp", "shortcut", "scpt", "scptd", "applescript", "workflow", "action",
            "dylib", "bundle", "plugin", "kext", "prefpane", "saver", "osax", "csh", "ksh", "tcsh", "fish",
        ];
        foreach (var ext in mac)
        {
            Assert.Contains(ext, DangerousTypes.Extensions);
        }
    }

    [Fact]
    public void EveryListIsInTheUnion()
    {
        var parts = DangerousTypes.GtkExtensions.Concat(DangerousTypes.MacExtensions)
            .Concat(DangerousTypes.OutlookExtensions).Concat(DangerousTypes.WindowsExtensions)
            .Concat(DangerousTypes.DiskImageExtensions);
        foreach (var ext in parts)
        {
            Assert.Contains(ext, DangerousTypes.Extensions);
            Assert.True(DangerousTypes.IsDangerous("file." + ext, ""), ext);
            Assert.True(DangerousTypes.IsDangerous("FILE." + ext.ToUpperInvariant(), ""), ext);
        }
        foreach (var type in DangerousTypes.GtkMediaTypes.Concat(DangerousTypes.MacMediaTypes).Concat(DangerousTypes.WindowsMediaTypes))
        {
            Assert.Contains(type, DangerousTypes.MediaTypes);
            Assert.True(DangerousTypes.IsDangerous("blob", type), type);
        }
        // The GTK lists whole, as attachments.go has them.
        Assert.Equal(31, DangerousTypes.GtkExtensions.Count);
        Assert.Equal(25, DangerousTypes.GtkMediaTypes.Count);
        Assert.Equal(31, DangerousTypes.MacExtensions.Count);
        Assert.Equal(4, DangerousTypes.MacMediaTypes.Count);
    }

    [Theory]
    // Outlook's Level-1 list.
    [InlineData("a.ade")]
    [InlineData("a.appref-ms")]
    [InlineData("a.cab")]
    [InlineData("a.cer")]
    [InlineData("a.chm")]
    [InlineData("a.cpl")]
    [InlineData("a.diagcab")]
    [InlineData("a.gadget")]
    [InlineData("a.hta")]
    [InlineData("a.inf")]
    [InlineData("a.library-ms")]
    [InlineData("a.mht")]
    [InlineData("a.msc")]
    [InlineData("a.msu")]
    [InlineData("a.printerexport")]
    [InlineData("a.psd1")]
    [InlineData("a.pyzw")]
    [InlineData("a.scf")]
    [InlineData("a.search-ms")]
    [InlineData("a.settingcontent-ms")]
    [InlineData("a.theme")]
    [InlineData("a.website")]
    [InlineData("a.wsb")]
    [InlineData("a.xbap")]
    [InlineData("a.xll")]
    [InlineData("a.xnk")]
    // What else Windows runs, installs, connects or follows.
    [InlineData("a.rdp")]
    [InlineData("a.ica")]
    [InlineData("a.msrcincident")]
    [InlineData("a.appinstaller")]
    [InlineData("a.msix")]
    [InlineData("a.msixbundle")]
    [InlineData("a.appx")]
    [InlineData("a.appxbundle")]
    [InlineData("a.searchConnector-ms")]
    [InlineData("a.iqy")]
    [InlineData("a.slk")]
    [InlineData("a.wll")]
    [InlineData("a.vsto")]
    [InlineData("a.vsix")]
    [InlineData("a.psm1")]
    [InlineData("a.wsc")]
    [InlineData("a.ocx")]
    [InlineData("a.sys")]
    [InlineData("a.themepack")]
    [InlineData("a.deskthemepack")]
    [InlineData("a.msstyles")]
    [InlineData("a.ppkg")]
    [InlineData("a.emsix")]
    [InlineData("a.emsixbundle")]
    [InlineData("a.eappx")]
    [InlineData("a.eappxbundle")]
    [InlineData("a.dqy")]
    [InlineData("a.oqy")]
    [InlineData("a.rqy")]
    [InlineData("a.odc")]
    [InlineData("a.p7b")]
    [InlineData("a.p7c")]
    [InlineData("a.pfx")]
    [InlineData("a.p12")]
    [InlineData("a.sst")]
    [InlineData("a.spc")]
    [InlineData("a.stl")]
    // Disk images: mounting one skipped the Mark of the Web.
    [InlineData("a.iso")]
    [InlineData("a.img")]
    [InlineData("a.vhd")]
    [InlineData("a.VHDX")]
    public void WindowsTypesAreNeverOpened(string name)
    {
        Assert.True(DangerousTypes.IsDangerous(name, "application/octet-stream"));
    }

    [Theory]
    [InlineData("report.pdf", "application/pdf")]
    [InlineData("letter.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")]
    [InlineData("budget.xlsm", "application/vnd.ms-excel.sheet.macroEnabled.12")]
    [InlineData("slides.pptx", "")]
    [InlineData("photo.png", "image/png")]
    [InlineData("scan.tiff", "image/tiff")]
    [InlineData("archive.zip", "application/zip")]
    [InlineData("archive.7z", "application/x-7z-compressed")]
    [InlineData("notes.txt", "text/plain; charset=utf-8")]
    [InlineData("page.html", "text/html")]
    [InlineData("invite.ics", "text/calendar")]
    [InlineData("card.vcf", "text/vcard")]
    [InlineData("message.eml", "message/rfc822")]
    [InlineData("README", "")]
    [InlineData("image.dmg", "")]
    public void DocumentsAreOpened(string name, string contentType)
    {
        Assert.False(DangerousTypes.IsDangerous(name, contentType));
    }

    [Theory]
    // Windows drops trailing dots and spaces: this is invoice.exe on disk.
    [InlineData("invoice.exe.", true)]
    [InlineData("invoice.exe . . ", true)]
    [InlineData("invoice.exe\t", true)]
    // A stream of report.pdf would be written, and the name that is written is report.pdf_evil.exe.
    [InlineData("report.pdf:evil.exe", true)]
    // Only the last extension counts, as it does for Windows.
    [InlineData("setup.exe.pdf", false)]
    [InlineData("C:\\temp\\setup.exe", true)]
    [InlineData("../../setup.exe", true)]
    // A class ID as the extension picks a handler the shell hides from view.
    [InlineData("readme.txt.{3050F4D8-98B5-11CF-BB82-00AA00BDCE0B}", true)]
    [InlineData("x.{}", false)]
    // Bidi controls, which the daemon removes already.
    [InlineData("photo\u202Egnp.exe", true)]
    // Look-alikes of an extension are other extensions, which nothing runs.
    [InlineData("setup.ex\u200Be", false)]
    [InlineData("setup.\uFF45\uFF58\uFF45", false)]
    public void NamesAreJudgedAsWindowsReadsThem(string name, bool want)
    {
        Assert.Equal(want, DangerousTypes.IsDangerous(name, ""));
    }

    [Theory]
    [InlineData("application/x-dosexec", true)]
    [InlineData("APPLICATION/X-MSDOWNLOAD", true)]
    [InlineData("  application/x-ms-application ; charset=binary", true)]
    [InlineData("application/msix", true)]
    [InlineData("application/x-rdp", true)]
    [InlineData("application/x-iso9660-image", true)]
    [InlineData("text/vbscript", true)]
    [InlineData("application/octet-stream", false)]
    [InlineData("application/pdf", false)]
    [InlineData("text/plain", false)]
    [InlineData(null, false)]
    public void ClaimedTypesAreJudgedWithoutParameters(string? contentType, bool want)
    {
        Assert.Equal(want, DangerousTypes.IsDangerous("blob", contentType));
    }

    [Theory]
    [InlineData("exe", true)]
    [InlineData(".exe", true)]
    [InlineData(".EXE", true)]
    [InlineData("pdf", false)]
    [InlineData("", false)]
    [InlineData(".", false)]
    [InlineData(null, false)]
    public void IsDangerousExtensionTakesTheDotOrNot(string? extension, bool want)
    {
        Assert.Equal(want, DangerousTypes.IsDangerousExtension(extension));
    }

    [Theory]
    [InlineData("a.txt", "txt")]
    [InlineData("a.tar.gz", "gz")]
    [InlineData(".bashrc", "bashrc")]
    [InlineData("noext", "")]
    [InlineData("dir.d/noext", "")]
    [InlineData("dir.d\\noext", "")]
    [InlineData("trailing.", "")]
    [InlineData("", "")]
    [InlineData(null, "")]
    public void ExtensionIsGoFilepathExt(string? name, string want)
    {
        Assert.Equal(want, DangerousTypes.Extension(name));
    }

    [Fact]
    public void CandidateExtensionsAreTheGivenAndTheWrittenName()
    {
        Assert.Equal(["exe"], DangerousTypes.CandidateExtensions("setup.exe"));
        Assert.Equal(["exe"], DangerousTypes.CandidateExtensions("setup.exe."));
        Assert.Equal(["pdf:evil", "pdf_evil"], DangerousTypes.CandidateExtensions("report.pdf:evil"));
        Assert.Empty(DangerousTypes.CandidateExtensions("README"));
        Assert.Empty(DangerousTypes.CandidateExtensions(null));
    }
}
