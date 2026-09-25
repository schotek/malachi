// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
import UniformTypeIdentifiers
@testable import MalachiCore

// The counterpart of ui/internal/window/attachments_test.go: the pure
// helpers behind the attachment chips, and the open directory.

private func attachment(_ partId: String = "", filename: String = "", contentType: String = "", size: Int = 0, contentId: String? = nil) -> MalachiCore.Attachment {
    MalachiCore.Attachment(partId: partId, filename: filename, contentType: contentType, size: size, inline: contentId != nil, contentId: contentId)
}

private func body(_ state: BodyState, html: String? = nil, text: String = "", withheld: Bool? = nil, inlineParts: [String: String]? = nil) -> MessageBodyResult {
    MessageBodyResult(
        messageId: "m", bodyState: state, hasHtml: html != nil, html: html, htmlWithheld: withheld, text: text,
        inlineParts: inlineParts, remoteContent: .block, sanitizerVersion: "1"
    )
}

private func part(filename: String) -> MessagePartResult {
    MessagePartResult(partId: "1", contentType: "application/octet-stream", filename: filename, size: 0, data: Data())
}

@Suite struct AttachmentsTests {
    @Test func chipAttachmentsTest() {
        let logo = attachment("1.2", filename: "logo.png", contentId: "logo@x")
        let stray = attachment("1.3", filename: "stray.png", contentId: "stray@x")
        let pdf = attachment("2", filename: "report.pdf")
        let atts = [logo, stray, pdf]
        let html = body(.fetched, html: "<p>x</p>", inlineParts: ["logo@x": "1.2"])

        let cases: [(String, MessageBodyResult?, [String])] = [
            ("html shows the logo", html, ["1.3", "2"]),
            ("text only lists every part", body(.fetched, text: "hi", inlineParts: ["logo@x": "1.2"]), ["1.2", "1.3", "2"]),
            ("withheld html lists every part", body(.fetched, text: "hi", withheld: true), ["1.2", "1.3", "2"]),
            ("no inline parts", body(.fetched, html: "<p>x</p>"), ["1.2", "1.3", "2"]),
            ("another part under the same cid", body(.fetched, html: "<p>x</p>", inlineParts: ["logo@x": "9"]), ["1.2", "1.3", "2"]),
            ("no body yet", nil, ["1.2", "1.3", "2"]),
        ]
        for (name, b, want) in cases {
            #expect(chipAttachments(atts, b).map(\.partId) == want, Comment(rawValue: name))
        }

        // A part without a part number is never mistaken for a shown picture.
        let odd = [attachment(contentId: "x@x")]
        #expect(chipAttachments(odd, body(.fetched, html: "<p>x</p>")).count == 1, "empty partId hidden")
    }

    @Test func partAvailableTest() {
        let small = attachment(size: 1024)
        let huge = attachment(size: API.Limits.maxAttachmentDataBytes + 1)
        let edge = attachment(size: API.Limits.maxAttachmentDataBytes)
        let fetched = body(.fetched)
        let cases: [(String, MalachiCore.Attachment, MessageBodyResult?, Bool, String)] = [
            ("no body", small, nil, false, ""),
            ("pending", small, body(.pending), false, "This message has not been downloaded yet."),
            ("tooBig", small, body(.tooBig), false, "This message is too large to download."),
            ("failed", small, body(.failed), false, "This message could not be read."),
            ("fetched", small, fetched, true, ""),
            ("at the cap", edge, fetched, true, ""),
            ("over the cap", huge, fetched, false, "Attachments over 16.0 MiB cannot be opened or saved yet."),
        ]
        for (name, a, b, ok, why) in cases {
            let got = partAvailable(a, b)
            #expect(got.ok == ok && got.why == why, "\(name): got (\(got.ok), \(got.why))")
        }
    }

    @Test func executableAttachmentTest() {
        let cases: [(String, String, Bool)] = [
            ("setup.exe", "application/octet-stream", true),
            ("invoice.pdf.exe", "application/pdf", true),
            ("Report.PDF", "application/pdf", false),
            ("run.SH", "text/plain", true),
            ("notes.txt", "text/plain", false),
            ("payload", "application/x-shellscript", true),
            ("payload", "application/x-executable; name=x", true),
            ("photo.jpg", "image/jpeg", false),
            ("launcher.desktop", "application/x-desktop", true),
            ("tool.jar", "application/java-archive", true),
            ("", "", false),
            (".bashrc", "", false), // no extension of its own
            // macOS: what the Finder runs, installs or follows.
            ("Install.command", "text/plain", true),
            ("photo.jpg.APP", "image/jpeg", true),
            ("Update.pkg", "application/octet-stream", true),
            ("Bundle.mpkg", "", true),
            ("profile.mobileconfig", "application/x-apple-aspen-config", true),
            ("launch.jnlp", "", true),
            ("site.webloc", "", true),
            ("share.afploc", "", true),
            ("link.url", "", true),
            ("script.scpt", "", true),
            ("script.applescript", "", true),
            ("flow.workflow", "", true),
            ("lib.dylib", "", true),
            ("driver.kext", "", true),
            ("pane.prefpane", "", true),
            ("screen.saver", "", true),
            ("x.osax", "", true),
            ("run.fish", "", true),
            ("run.tcsh", "", true),
            ("shortcut.shortcut", "", true),
            ("blob", "application/x-mach-binary", true),
            ("blob", "application/vnd.apple.installer+xml; name=x", true),
            ("blob", "application/x-java-jnlp-file", true),
            // Still opened: documents that merely look Apple-ish.
            ("deck.key", "application/octet-stream", false),
            ("notes.pages", "", false),
            ("image.dmg", "application/x-apple-diskimage", false),
        ]
        for (name, ct, want) in cases {
            #expect(executableAttachment(filename: name, contentType: ct) == want, "executableAttachment(\(name), \(ct))")
        }
        for ext in ["command", "tool", "terminal", "webloc", "inetloc", "fileloc", "afploc", "ftploc", "url", "app", "pkg",
                    "mpkg", "mobileconfig", "jnlp", "shortcut", "scpt", "scptd", "applescript", "workflow", "action",
                    "dylib", "bundle", "plugin", "kext", "prefpane", "saver", "osax", "csh", "ksh", "tcsh", "fish"] {
            #expect(executableExtensions.contains(ext), "\(ext) missing")
        }
    }

    /// The types handed to the conformance check: the media type first,
    /// the extension second, unknown claims skipped.
    @Test func claimedTypesTest() {
        #expect(claimedTypes(filename: "run.sh", contentType: "application/pdf; name=x") == [.pdf, .shellScript])
        #expect(claimedTypes(filename: "run.sh", contentType: "") == [.shellScript])
        #expect(claimedTypes(filename: "blob", contentType: "image/png") == [.png])
        #expect(claimedTypes(filename: "", contentType: "").isEmpty)
        #expect(claimedTypes(filename: "x.app", contentType: "").first?.conforms(to: .application) == true)
    }

    /// backend/internal/safename `TestFilename` and `TestFilenameTruncates`,
    /// plus the colon rule of macOS.
    @Test func safeFileNameTest() {
        let cases: [(String, String)] = [
            ("report.pdf", "report.pdf"),
            ("../../x", "x"),
            ("..\\..\\x", "x"),
            ("/etc/passwd", "passwd"),
            ("a\u{0}b\n.txt", "ab.txt"),
            (".hidden", "hidden"),
            ("...", "attachment"),
            ("", "attachment"),
            ("   ", "attachment"),
            ("  spaced.txt  ", "spaced.txt"),
            ("Jörg's Übersicht.ods", "Jörg's Übersicht.ods"),
            ("bad\u{FFFD}.txt", "bad.txt"),
            // Bidi controls would make the displayed extension lie.
            ("photo\u{202E}gnp.exe", "photognp.exe"),
            ("\u{202B}x\u{202C}\u{2066}y\u{2069}.txt", "xy.txt"),
            ("\u{200E}name\u{200F}\u{061C}.pdf", "name.pdf"),
            // The colon is a separator to the Finder.
            ("photo:1.png", "photo_1.png"),
            ("a:b/c:d", "c_d"),
            ("\u{1B}[31mred.txt", "[31mred.txt"),
        ]
        for (input, want) in cases {
            #expect(safeFileName(input) == want, "safeFileName(\(input.debugDescription))")
        }
        let long = String(repeating: "ž", count: 300) + ".txt"
        let got = safeFileName(long)
        #expect(got.utf8.count <= safeNameMaxBytes && got.hasSuffix(".txt") && got.contains("ž"), "truncated = \(got.utf8.count) bytes")
        #expect(safeFileName(String(repeating: "a", count: 400)).utf8.count == safeNameMaxBytes)
        // A long "extension" is not one: cut like the rest.
        let dotted = String(repeating: "b", count: 300) + "." + String(repeating: "c", count: 40)
        #expect(safeFileName(dotted).utf8.count == safeNameMaxBytes)
        #expect(safeFileName(".\(String(repeating: "d", count: 300))") == String(repeating: "d", count: 255))
    }

    @Test func attachedMessageTest() {
        let cases: [(MalachiCore.Attachment, Bool)] = [
            (attachment(filename: "report.eml", contentType: "message/rfc822"), true),
            (attachment(filename: "attachment-1.eml", contentType: "message/rfc822"), true),
            (attachment(filename: "whatever", contentType: "Message/RFC822; name=x"), true),
            (attachment(filename: "Forwarded.EML", contentType: "application/octet-stream"), true),
            (attachment(filename: "report.eml.exe", contentType: "application/octet-stream"), false),
            (attachment(filename: "a.pdf", contentType: "application/pdf"), false),
            (attachment(), false),
        ]
        for (a, want) in cases {
            #expect(attachedMessage(a) == want, "attachedMessage(\(a.filename), \(a.contentType))")
        }
    }

    @Test func uniqueNameTest() {
        let taken: Set<String> = ["a.txt", "a (2).txt", "b", "c.tar.gz"]
        let has = { (n: String) in taken.contains(n) }
        let cases = [
            "a.txt": "a (3).txt",
            "b": "b (2)",
            "c.tar.gz": "c.tar (2).gz",
            "new.txt": "new.txt",
        ]
        for (input, want) in cases {
            #expect(uniqueName(input, taken: has) == want, "uniqueName(\(input))")
        }
        #expect(uniqueName("x", taken: { _ in true }) == "x", "giving up should return the name itself")
    }

    @Test func fileNameTest() {
        let a = attachment(filename: "listed.pdf")
        let cases: [(MessagePartResult?, String)] = [
            (part(filename: "served.pdf"), "served.pdf"),
            (part(filename: ""), "listed.pdf"),
            (nil, "listed.pdf"),
            (part(filename: "../../etc/passwd"), "passwd"),
            (part(filename: "/"), "listed.pdf"),
            (part(filename: "..."), "listed.pdf"),
            (part(filename: "inv\u{202E}fdp.exe"), "invfdp.exe"),
            (part(filename: "a:b.txt"), "a_b.txt"),
        ]
        for (res, want) in cases {
            #expect(fileName(res, a) == want, "fileName(\(res?.filename ?? "nil"))")
        }
        #expect(fileName(part(filename: ""), attachment()) == "attachment", "nameless part")
    }

    @Test func saveAllSummaryTest() {
        #expect(saveAllSummary(saved: 1, failed: 0) == "Saved 1 attachment")
        #expect(saveAllSummary(saved: 3, failed: 0) == "Saved 3 attachments")
        #expect(saveAllSummary(saved: 2, failed: 1) == "1 of 3 attachments could not be saved")
    }

    @Test func sweepOpenDir() throws {
        let fm = FileManager.default
        let dir = fm.temporaryDirectory.appendingPathComponent("malachi-open-\(UUID().uuidString.prefix(8))", isDirectory: true)
        defer { try? fm.removeItem(at: dir) }
        let old = dir.appendingPathComponent("old", isDirectory: true)
        let fresh = dir.appendingPathComponent("fresh", isDirectory: true)
        for d in [old, fresh] {
            try fm.createDirectory(at: d, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        }
        let stale = Date().addingTimeInterval(-2 * 3600)
        try fm.setAttributes([.modificationDate: stale], ofItemAtPath: old.path)
        OpenDir(url: dir).sweep(maxAge: 3600)
        #expect(!fm.fileExists(atPath: old.path), "the old entry should be gone")
        #expect(fm.fileExists(atPath: fresh.path), "the fresh entry should stay")
        OpenDir(url: dir.appendingPathComponent("missing")).sweep(maxAge: 3600) // no directory: no-op
    }

    /// The open directory writes each file exclusively into a private
    /// subdirectory (no Go counterpart; writeOpenFile is untested there).
    @Test func openDirWrite() throws {
        let fm = FileManager.default
        let root = fm.temporaryDirectory.appendingPathComponent("malachi-open-\(UUID().uuidString.prefix(8))", isDirectory: true)
        defer { try? fm.removeItem(at: root) }
        let open = OpenDir(url: root.appendingPathComponent("open", isDirectory: true))
        let url = try open.write(name: "a.txt", data: Data("hello".utf8))
        #expect(url.lastPathComponent == "a.txt")
        #expect(try Data(contentsOf: url) == Data("hello".utf8))
        let filePerms = try fm.attributesOfItem(atPath: url.path)[.posixPermissions] as? Int
        #expect(filePerms == 0o600)
        let subPerms = try fm.attributesOfItem(atPath: url.deletingLastPathComponent().path)[.posixPermissions] as? Int
        #expect(subPerms == 0o700)
        let dirPerms = try fm.attributesOfItem(atPath: open.url.path)[.posixPermissions] as? Int
        #expect(dirPerms == 0o700)
        // A second write of the same name lands in another subdirectory.
        let again = try open.write(name: "a.txt", data: Data())
        #expect(again != url)
        #expect(again.deletingLastPathComponent() != url.deletingLastPathComponent())
        open.removeAll()
        #expect(!fm.fileExists(atPath: open.url.path))
    }

    @Test func chipIconTypeTest() {
        #expect(chipIconType(attachment(filename: "x.bin", contentType: "application/pdf; name=x")) == .pdf)
        #expect(chipIconType(attachment(filename: "photo.JPG", contentType: "application/octet-stream")) == .jpeg)
        #expect(chipIconType(attachment(filename: "notes.txt")) == .plainText)
        #expect(chipIconType(attachment(filename: "blob")) == .data)
        #expect(chipIconType(attachment()) == .data)
        #expect(chipName(attachment(filename: "  x.pdf ")) == "x.pdf")
        #expect(chipName(attachment(filename: " ")) == "Attachment")
    }
}
