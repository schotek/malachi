// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/AttachmentChips.swift
// (executableExtensions, executableTypes, executableAttachment) and the name
// half of macos/Sources/MalachiMail/Attachments/AttachmentActions.swift
// (mustNotOpen); GTK: ui/internal/window/attachments.go
// (executableExtensions, executableTypes, executableAttachment). What
// Windows adds (Outlook's Level-1 list, the types Windows runs, installs or
// follows, disk images) is the listed deviation of windows/README.md and
// docs/windows-port.md §10; Malachi.Platform.Windows.Attachments.
// FileTypePolicy adds what AssocIsDangerous says, as AttachmentActions.swift
// adds what the type system says (executableByType).

using System;
using System.Collections.Frozen;
using System.Collections.Generic;
using System.Linq;

namespace Malachi.Core.Platform;

/// <summary>
/// The attachments that are never handed to an application, only saved
/// (docs/security.md §4): anything the desktop might run, install, mount or
/// follow rather than display. Judged by the last extension and the claimed
/// media type; either is enough.
/// </summary>
public static class DangerousTypes
{
    /// <summary>
    /// The GTK UI's list (attachments.go <c>executableExtensions</c>), kept
    /// whole: parity first, although most of it means nothing to Windows.
    /// </summary>
    public static readonly IReadOnlySet<string> GtkExtensions = Set(
        "exe", "com", "bat", "cmd", "msi", "scr",
        "pif", "ps1", "vbs", "vbe", "js", "jse",
        "wsf", "wsh", "hta", "jar", "sh", "bash",
        "zsh", "run", "bin", "appimage", "desktop",
        "py", "pl", "rb", "php", "lnk", "reg",
        "dll", "so");

    /// <summary>
    /// What the macOS client adds (AttachmentChips.swift
    /// <c>executableExtensions</c>): Terminal scripts, bundles, installer
    /// packages, configuration profiles, Java Web Start, AppleScript and
    /// Automator documents, and the location files.
    /// </summary>
    public static readonly IReadOnlySet<string> MacExtensions = Set(
        "command", "tool", "terminal", "webloc", "inetloc", "fileloc",
        "afploc", "ftploc", "url", "app", "pkg", "mpkg", "mobileconfig",
        "jnlp", "shortcut", "scpt", "scptd", "applescript", "workflow",
        "action", "dylib", "bundle", "plugin", "kext", "prefpane", "saver",
        "osax", "csh", "ksh", "tcsh", "fish");

    /// <summary>
    /// Outlook's Level-1 list, the attachments Outlook never lets through
    /// ("Blocked attachments in Outlook", support.microsoft.com, as of
    /// 2026-09).
    /// </summary>
    public static readonly IReadOnlySet<string> OutlookExtensions = Set(
        "ade", "adp", "apk", "app", "appcontent-ms", "application", "appref-ms", "appx",
        "asp", "aspx", "asx", "bas", "bat", "bgi", "cab", "cdxml", "cer", "chm", "cmd",
        "cnt", "com", "cpl", "crt", "csh", "der", "diagcab", "exe", "fxp", "gadget",
        "grp", "hlp", "hpj", "hta", "htc", "img", "inf", "ins", "iso", "isp", "its",
        "jar", "jnlp", "js", "jse", "ksh", "library-ms", "lnk", "mad", "maf", "mag",
        "mam", "maq", "mar", "mas", "mat", "mau", "mav", "maw", "mcf", "mda", "mdb",
        "mde", "mdt", "mdw", "mdz", "msc", "mht", "mhtml", "msh", "msh1", "msh2",
        "mshxml", "msh1xml", "msh2xml", "msi", "msp", "mst", "msu", "ops", "osd",
        "pcd", "pif", "pl", "plg", "prf", "prg", "printerexport", "ps1", "ps1xml",
        "ps2", "ps2xml", "psc1", "psc2", "psd1", "psdm1", "pssc", "pst", "py", "pyc",
        "pyo", "pyw", "pyz", "pyzw", "reg", "scf", "scr", "sct", "search-ms",
        "settingcontent-ms", "shb", "shs", "theme", "tmp", "udl", "url", "vb", "vbe",
        "vbp", "vbs", "vhd", "vhdx", "vsmacros", "vsw", "webpnp", "website", "ws",
        "wsb", "wsc", "wsf", "wsh", "xbap", "xll", "xnk");

    /// <summary>
    /// What else Windows runs, installs, connects or follows on a double
    /// click, and where AssocIsDangerous and the attachment policy are
    /// silent (measured: <c>.rdp</c>, <c>.search-ms</c>,
    /// <c>.searchconnector-ms</c>, <c>.settingcontent-ms</c>,
    /// <c>.appinstaller</c>, <c>.msix</c>, <c>.xll</c>, <c>.jar</c>,
    /// <c>.py</c>, <c>.sh</c>): remote-session files, app packages and
    /// their installers, search and settings shortcuts that reach out to
    /// other hosts, Office add-ins and data links, the script hosts,
    /// installers, drivers, themes (their pictures can be fetched from a
    /// share), and invitations that open a connection.
    /// </summary>
    public static readonly IReadOnlySet<string> WindowsExtensions = Set(
        // Remote sessions, remote assistance, RemoteApp.
        "rdp", "ica", "msrcincident", "wcx",
        // App packages and what installs them.
        "appinstaller", "msix", "msixbundle", "appx", "appxbundle",
        "application", "appref-ms", "xbap", "vsto", "vsix",
        "msi", "msp", "mst", "msu", "diagcab", "wsb",
        // Shell shortcuts, searches, libraries and settings.
        "lnk", "pif", "url", "website", "searchconnector-ms", "search-ms",
        "settingcontent-ms", "library-ms", "scf", "glk", "mcl",
        // Office: web queries, SYLK, add-in libraries.
        "iqy", "slk", "xll", "wll",
        // The script hosts: PowerShell, Windows Script Host, Python, Java, shells.
        "ps1", "psm1", "psd1", "ps1xml", "psc1", "pssc", "cdxml",
        "vbs", "vbe", "js", "jse", "wsf", "wsh", "wsc", "ws", "sct", "hta",
        "py", "pyw", "pyz", "pyzw", "jar", "jnlp", "sh",
        // Programs, libraries, drivers, control panel items, consoles.
        "exe", "com", "scr", "bat", "cmd", "dll", "ocx", "sys", "drv", "cpl",
        "msc", "gadget", "job",
        // Setup information, registry files, compiled help.
        "inf", "reg", "chm", "hlp",
        // Themes.
        "theme", "themepack", "deskthemepack");

    /// <summary>
    /// Disk images Windows mounts on a double click; mounting one has been
    /// a way past the Mark of the Web, so they are saved, never opened.
    /// </summary>
    public static readonly IReadOnlySet<string> DiskImageExtensions = Set("iso", "img", "vhd", "vhdx");

    /// <summary>Every extension above: never opened, only saved.</summary>
    public static readonly IReadOnlySet<string> Extensions = Set(
        GtkExtensions.Concat(MacExtensions).Concat(OutlookExtensions)
            .Concat(WindowsExtensions).Concat(DiskImageExtensions).ToArray());

    /// <summary>The GTK UI's claimed types (attachments.go <c>executableTypes</c>).</summary>
    public static readonly IReadOnlySet<string> GtkMediaTypes = Set(
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
        "text/x-msdos-batch");

    /// <summary>
    /// The Mach-O, installer, configuration-profile and Java Web Start types
    /// of the macOS client (AttachmentChips.swift <c>executableTypes</c>).
    /// </summary>
    public static readonly IReadOnlySet<string> MacMediaTypes = Set(
        "application/x-mach-binary",
        "application/vnd.apple.installer+xml",
        "application/x-apple-aspen-config",
        "application/x-java-jnlp-file");

    /// <summary>
    /// The claimed types of what <see cref="WindowsExtensions"/> and
    /// <see cref="DiskImageExtensions"/> name, as senders and libmagic
    /// label them.
    /// </summary>
    public static readonly IReadOnlySet<string> WindowsMediaTypes = Set(
        "application/x-dosexec",
        "application/x-ms-ne-executable",
        "application/x-ms-installer",
        "application/x-ms-application",
        "application/x-ms-xbap",
        "application/appx",
        "application/appxbundle",
        "application/msix",
        "application/msixbundle",
        "application/appinstaller",
        "application/x-rdp",
        "application/vnd.ms-cab-compressed",
        "application/vnd.ms-htmlhelp",
        "application/x-mswinurl",
        "application/internet-shortcut",
        "text/x-ms-regedit",
        "application/x-javascript",
        "text/jscript",
        "text/vbscript",
        "application/x-sh",
        "application/x-csh",
        "application/x-python",
        "application/x-python-code",
        "application/x-iso9660-image",
        "application/x-cd-image",
        "application/x-raw-disk-image",
        "application/x-vhd-disk",
        "application/x-vhdx-disk");

    /// <summary>Every claimed type above.</summary>
    public static readonly IReadOnlySet<string> MediaTypes = Set(
        GtkMediaTypes.Concat(MacMediaTypes).Concat(WindowsMediaTypes).ToArray());

    /// <summary>
    /// Whether an attachment must not be opened directly (AttachmentChips.swift
    /// <c>executableAttachment</c>, attachments.go <c>executableAttachment</c>).
    /// The extension is judged on the name as given and on the name Windows
    /// would give the file (<see cref="WindowsFileNames.Sanitize(string)"/>:
    /// "invoice.exe." is "invoice.exe" on disk); either is enough, and so is
    /// the claimed type.
    /// </summary>
    public static bool IsDangerous(string? filename, string? contentType)
    {
        foreach (var extension in CandidateExtensions(filename))
        {
            if (IsDangerousExtension(extension))
            {
                return true;
            }
        }
        return MediaTypes.Contains(MediaType(contentType));
    }

    /// <summary>
    /// Whether an extension, with or without its dot, is one of
    /// <see cref="Extensions"/> or names a class ID (<c>{…}</c>), which the
    /// shell resolves to a handler of its own and hides from view.
    /// </summary>
    public static bool IsDangerousExtension(string? extension)
    {
        var ext = extension ?? "";
        if (ext.StartsWith('.'))
        {
            ext = ext[1..];
        }
        if (ext.Length == 0)
        {
            return false;
        }
        return Extensions.Contains(ext) || (ext.Length > 2 && ext[0] == '{' && ext[^1] == '}');
    }

    /// <summary>
    /// The extensions an attachment's name is judged by, without their dot:
    /// that of the name as given (trimmed) and that of the name it gets on
    /// disk, once each, empty ones left out. A shorter cap or this
    /// machine's look-alikes give no other type: a cut leaves the same
    /// extension or none, and a look-alike becomes <c>_</c>, which nothing
    /// listed contains.
    /// </summary>
    public static IReadOnlyList<string> CandidateExtensions(string? filename)
    {
        var raw = Extension((filename ?? "").Trim());
        var written = Extension(WindowsFileNames.Sanitize(filename ?? ""));
        var list = new List<string>(2);
        if (raw.Length > 0)
        {
            list.Add(raw);
        }
        if (written.Length > 0 && !string.Equals(written, raw, StringComparison.OrdinalIgnoreCase))
        {
            list.Add(written);
        }
        return list;
    }

    /// <summary>
    /// Go's <c>filepath.Ext</c> without the dot (AttachmentChips.swift
    /// <c>fileExtension</c>): the suffix after the last dot of the final
    /// path element, both separators counting, "" without one. ".bashrc"
    /// gives "bashrc", as in Go.
    /// </summary>
    public static string Extension(string? name)
    {
        var s = name ?? "";
        var start = s.LastIndexOfAny(['/', '\\']) + 1;
        var dot = s.LastIndexOf('.');
        return dot < start ? "" : s[(dot + 1)..];
    }

    /// <summary>
    /// The claimed media type without its parameters and surrounding white
    /// space (<c>text/plain; charset=utf-8</c> is <c>text/plain</c>),
    /// compared without regard to case.
    /// </summary>
    public static string MediaType(string? contentType)
    {
        var ct = (contentType ?? "").Trim();
        var semicolon = ct.IndexOf(';', StringComparison.Ordinal);
        return semicolon < 0 ? ct : ct[..semicolon].Trim();
    }

    private static FrozenSet<string> Set(params string[] items) =>
        items.ToFrozenSet(StringComparer.OrdinalIgnoreCase);
}
