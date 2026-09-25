// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import UniformTypeIdentifiers

// The pure helpers behind the attachment chips (ui/internal/window/
// attachments.go and the `attachedMessage` of embedded.go). Names and types
// are server data and are shown as plain text; programs and scripts are
// never opened directly (docs/security.md §4); the content comes through
// message.part, so a part over `API.Limits.maxAttachmentDataBytes` is out
// of reach.

/// How long a file written for opening is kept before the next open sweeps
/// it: the viewer may still be reading it lazily (attachments.go
/// `openMaxAge`).
public let openMaxAge: TimeInterval = 3600

/// Bounds the file name on a chip, in characters; the middle is elided so
/// the extension survives (attachments.go `chipNameChars`).
public let chipNameChars = 14

/// What gets a chip (attachments.go `chipAttachments`): every attachment,
/// minus the parts the HTML on display shows inline (the cid: references
/// the sanitiser kept, `inlineParts`). A text-only message, withheld HTML
/// or an unreferenced Content-ID leaves the part listed like any other
/// file.
public func chipAttachments(_ atts: [Attachment], _ b: MessageBodyResult?) -> [Attachment] {
    let html = showsHTML(b)
    return atts.filter { a in
        if html, let cid = a.contentId, !cid.isEmpty, !a.partId.isEmpty, b?.inlineParts?[cid] == a.partId {
            return false
        }
        return true
    }
}

/// Whether message.part can deliver `a`, and if not why, as the chip's
/// tooltip (empty while the body is still on its way; attachments.go
/// `partAvailable`). The daemon reads parts from the stored raw message
/// only, and never beyond `API.Limits.maxAttachmentDataBytes`.
public func partAvailable(_ a: Attachment, _ b: MessageBodyResult?) -> (ok: Bool, why: String) {
    guard let b else {
        return (false, "")
    }
    switch b.bodyState {
    case .fetched:
        break
    case .pending:
        return (false, L10n.T("This message has not been downloaded yet."))
    case .tooBig:
        return (false, L10n.T("This message is too large to download."))
    case .failed:
        return (false, L10n.T("This message could not be read."))
    default:
        return (false, "")
    }
    if a.size > API.Limits.maxAttachmentDataBytes {
        // TRANSLATORS: %s is a size such as "16.0 MiB".
        return (false, L10n.T("Attachments over %s cannot be opened or saved yet.", formatSize(API.Limits.maxAttachmentDataBytes)))
    }
    return (true, "")
}

/// Extensions the UI refuses to hand to the default application: anything
/// the desktop might run rather than display (attachments.go
/// `executableExtensions`), plus what macOS runs, installs or follows
/// somewhere on a double click: Terminal scripts, Automator and
/// AppleScript documents, bundles, installer packages, configuration
/// profiles, Java Web Start, and the location files that open a URL
/// (`.webloc` and friends) or mount a share. Where a name list cannot
/// keep up, AttachmentActions asks the type system too.
public let executableExtensions: Set<String> = [
    "exe", "com", "bat", "cmd", "msi", "scr",
    "pif", "ps1", "vbs", "vbe", "js", "jse",
    "wsf", "wsh", "hta", "jar", "sh", "bash",
    "zsh", "run", "bin", "appimage", "desktop",
    "py", "pl", "rb", "php", "lnk", "reg",
    "dll", "so",
    // macOS
    "command", "tool", "terminal", "webloc", "inetloc", "fileloc",
    "afploc", "ftploc", "url", "app", "pkg", "mpkg", "mobileconfig",
    "jnlp", "shortcut", "scpt", "scptd", "applescript", "workflow",
    "action", "dylib", "bundle", "plugin", "kext", "prefpane", "saver",
    "osax", "csh", "ksh", "tcsh", "fish",
]

/// Claimed types the UI refuses to open directly (attachments.go
/// `executableTypes`), plus the Mach-O, installer, configuration-profile
/// and Java Web Start types of macOS.
public let executableTypes: Set<String> = [
    "application/x-mach-binary",
    "application/vnd.apple.installer+xml",
    "application/x-apple-aspen-config",
    "application/x-java-jnlp-file",
    "application/x-executable",
    "application/x-sharedlib",
    "application/x-shellscript",
    "application/x-desktop",
    "application/x-ms-dos-executable",
    "application/x-msdownload",
    "application/x-msi",
    "application/vnd.microsoft.portable-executable",
    "application/x-elf",
    "application/x-pie-executable",
    "application/java-archive",
    "application/x-java-archive",
    "application/vnd.appimage",
    "application/x-iso9660-appimage",
    "application/x-bat",
    "application/x-msdos-program",
    "application/x-perl",
    "application/javascript",
    "application/x-ms-shortcut",
    "application/hta",
    "text/x-shellscript",
    "text/x-python",
    "text/x-perl",
    "text/javascript",
    "text/x-msdos-batch",
]

/// Whether an attachment must not be opened directly (attachments.go
/// `executableAttachment`, docs/security.md §4). Judged by the last
/// extension and the claimed type; either is enough.
public func executableAttachment(filename: String, contentType: String) -> Bool {
    let ext = fileExtension(filename.trimmingCharacters(in: .whitespacesAndNewlines)).lowercased()
    if !ext.isEmpty, executableExtensions.contains(ext) {
        return true
    }
    return executableTypes.contains(mediaType(contentType))
}

/// The system types an attachment claims, for a check by conformance
/// where a list of names cannot keep up (AttachmentActions asks whether
/// any of them is an executable, a script, an application or a bundle):
/// the type of the claimed media type, minus its parameters, and the type
/// of the last extension, both when the system knows them. A claim the
/// system has no type for gives nothing; the name lists above still
/// apply.
public func claimedTypes(filename: String, contentType: String) -> [UTType] {
    var types: [UTType] = []
    let ct = mediaType(contentType)
    if !ct.isEmpty, let t = UTType(mimeType: ct) {
        types.append(t)
    }
    let ext = fileExtension(filename.trimmingCharacters(in: .whitespacesAndNewlines))
    if !ext.isEmpty, let t = UTType(filenameExtension: ext) {
        types.append(t)
    }
    return types
}

/// Whether the attachment is a message (embedded.go `attachedMessage`): by
/// its claimed type or its `.eml` name.
public func attachedMessage(_ a: Attachment) -> Bool {
    mediaType(a.contentType) == "message/rfc822"
        || a.filename.trimmingCharacters(in: .whitespacesAndNewlines).lowercased().hasSuffix(".eml")
}

/// The chip's label: the sanitised file name, or a placeholder for a part
/// without one (attachments.go `chipName`).
public func chipName(_ a: Attachment) -> String {
    let n = a.filename.trimmingCharacters(in: .whitespacesAndNewlines)
    if !n.isEmpty {
        return n
    }
    return L10n.T("Attachment")
}

/// The type the chip's icon is chosen by (attachments.go `chipIconType`):
/// the claimed one, minus its parameters, or a guess from the file name
/// when the sender said nothing useful (no type, `application/octet-stream`,
/// or a type this system does not know); generic data as the last resort.
public func chipIconType(_ a: Attachment) -> UTType {
    let ct = mediaType(a.contentType)
    if !ct.isEmpty, ct != "application/octet-stream", let t = UTType(mimeType: ct) {
        return t
    }
    let ext = fileExtension(a.filename.trimmingCharacters(in: .whitespacesAndNewlines))
    if !ext.isEmpty, let t = UTType(filenameExtension: ext) {
        return t
    }
    return .data
}

/// The name a fetched part is written under (attachments.go `fileName`):
/// what message.part reported, else the listed name, else a constant; each
/// through `safeFileName` (the daemon sanitises, this is defence in depth).
public func fileName(_ res: MessagePartResult?, _ a: Attachment) -> String {
    for candidate in [res?.filename ?? "", a.filename] {
        let n = safeFileName(candidate)
        if n != safeNameFallback {
            return n
        }
    }
    return safeNameFallback
}

/// The longest name `safeFileName` produces, in bytes (NAME_MAX;
/// backend/internal/safename `MaxBytes`).
public let safeNameMaxBytes = 255

/// What `safeFileName` returns when nothing usable remains (safename
/// `Fallback`).
public let safeNameFallback = "attachment"

/// backend/internal/safename `Filename`, mirrored for the names this
/// client writes to disk, so that a daemon that skipped it would still
/// not make the client write outside the folder or a name whose displayed
/// extension lies: the last path component only (both separators), no
/// control characters, no bidirectional formatting characters (a U+202E
/// before "gnp.exe" makes "photo.exe.png" appear on screen), no U+FFFD, surrounding
/// white space and leading dots removed, and at most `safeNameMaxBytes`
/// bytes with a short extension kept. One rule of its own: `:` becomes
/// `_`, the separator the Finder shows as `/`.
public func safeFileName(_ raw: String) -> String {
    var scalars = Array(raw.unicodeScalars)
    if let i = scalars.lastIndex(where: { $0 == "/" || $0 == "\\" }) {
        scalars = Array(scalars[(i + 1)...])
    }
    var kept = String.UnicodeScalarView()
    for c in scalars {
        if c.properties.generalCategory == .control || bidiControl(c) || c.value == 0xFFFD {
            continue
        }
        kept.append(c == ":" ? "_" : c)
    }
    var s = String(kept).trimmingCharacters(in: .whitespacesAndNewlines)
    while s.unicodeScalars.first == "." {
        s.unicodeScalars.removeFirst()
    }
    s = s.trimmingCharacters(in: .whitespacesAndNewlines)
    if s.isEmpty {
        return safeNameFallback
    }
    if s.utf8.count > safeNameMaxBytes {
        s = truncateName(s)
    }
    return s
}

/// safename `bidiControl`: the Unicode bidirectional formatting
/// characters, which are not control characters to the category test.
private func bidiControl(_ c: Unicode.Scalar) -> Bool {
    switch c.value {
    case 0x061C, // ARABIC LETTER MARK
         0x200E, 0x200F, // LRM, RLM
         0x202A...0x202E, // LRE, RLE, PDF, LRO, RLO
         0x2066...0x2069: // LRI, RLI, FSI, PDI
        return true
    default:
        return false
    }
}

/// safename `truncate`: shortens to `safeNameMaxBytes` on a scalar
/// boundary, keeping an extension of at most 16 bytes.
private func truncateName(_ s: String) -> String {
    let bytes = Array(s.utf8)
    var ext: [UInt8] = []
    if let dot = bytes.lastIndex(of: UInt8(ascii: ".")), dot > 0, bytes.count - dot <= 16 {
        ext = Array(bytes[dot...])
    }
    var base = String(decoding: bytes[..<(bytes.count - ext.count)], as: UTF8.self)
    let room = safeNameMaxBytes - ext.count
    while base.utf8.count > room, !base.isEmpty {
        base.unicodeScalars.removeLast()
    }
    let suffix = String(decoding: ext, as: UTF8.self)
    if base.isEmpty {
        return safeNameFallback + suffix
    }
    return base + suffix
}

/// `name`, or `name` with " (2)", " (3)", … before the extension until
/// `taken` says no (attachments.go `uniqueName`). It gives up after 1000
/// tries and returns `name`.
public func uniqueName(_ name: String, taken: (String) -> Bool) -> String {
    if !taken(name) {
        return name
    }
    var base = name
    var ext = ""
    let bytes = Array(name.utf8)
    if let dot = bytes.lastIndex(of: UInt8(ascii: ".")), dot > 0 {
        base = String(decoding: bytes[..<dot], as: UTF8.self)
        ext = String(decoding: bytes[dot...], as: UTF8.self)
    }
    for n in 2..<1000 {
        let c = "\(base) (\(n))\(ext)"
        if !taken(c) {
            return c
        }
    }
    return name
}

/// The toast after Save All (attachments.go `saveAllSummary`).
public func saveAllSummary(saved: Int, failed: Int) -> String {
    if failed == 0 {
        // TRANSLATORS: %d is the number of files written.
        return L10n.N("Saved %d attachment", "Saved %d attachments", saved)
    }
    // TRANSLATORS: the first %d is how many failed, the second how many there were.
    return L10n.T("%d of %d attachments could not be saved", failed, saved + failed)
}

// MARK: Helpers

/// The claimed media type in lower case without its parameters
/// (`text/plain; charset=utf-8` → `text/plain`).
private func mediaType(_ contentType: String) -> String {
    var ct = contentType.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    if let semicolon = ct.firstIndex(of: ";") {
        ct = String(ct[..<semicolon]).trimmingCharacters(in: .whitespacesAndNewlines)
    }
    return ct
}

/// Go's `filepath.Ext` without the dot: the suffix after the last dot of
/// the final path element, "" without one. ".bashrc" gives "bashrc", as in
/// Go.
private func fileExtension(_ name: String) -> String {
    let bytes = Array(name.utf8)
    let start = (bytes.lastIndex(of: UInt8(ascii: "/")) ?? -1) + 1
    guard let dot = bytes[start...].lastIndex(of: UInt8(ascii: ".")) else {
        return ""
    }
    return String(decoding: bytes[(dot + 1)...], as: UTF8.self)
}
