// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The gettext shim of the macOS client, the counterpart of
/// ui/internal/i18n. Keys are the GTK msgids verbatim; the catalogues are
/// the `.lproj` directories macos/scripts/po2strings.py generates from po/
/// at build time. Every user-visible string goes through `T`, `N` or `C`.
///
/// Values found in a catalogue are already in Foundation format (the
/// generator converts `%s`/`%d`); only the English fallback, the msgid
/// itself, is converted here with `GettextFormat.toFoundation`, which is
/// idempotent so a second conversion could never do harm.
public enum L10n {
    /// The catalogue in use. Set once at start-up (`Catalogue.default()`),
    /// swapped by tests. Reads and writes are serialised by a lock so `T`
    /// is callable from any context.
    public static var catalogue: Catalogue {
        get { box.value }
        set { box.value = newValue }
    }

    private static let box = CatalogueBox(.english)

    /// Translates `msgid`.
    public static func T(_ msgid: String) -> String {
        catalogue.translate(msgid)
    }

    /// Translates `msgid` and formats it with `args` (`fmt.Sprintf(T(...))`).
    public static func T(_ msgid: String, _ args: any CVarArg...) -> String {
        catalogue.translate(msgid, args)
    }

    /// Translates a plural form for `n` and substitutes `n` (every plural
    /// msgid of the GTK UI carries exactly one `%d`). Without a catalogue
    /// entry the English rule applies: `singular` for 1, `plural` otherwise.
    public static func N(_ singular: String, _ plural: String, _ n: Int) -> String {
        catalogue.plural(singular, plural, n)
    }

    /// Translates `msgid` disambiguated by `context` (pgettext).
    public static func C(_ context: String, _ msgid: String) -> String {
        catalogue.context(context, msgid)
    }

    /// Formats a gettext-style pattern (translated or not) with `args`, for
    /// callers that fetched the string separately.
    public static func format(_ gettextFormat: String, _ args: [any CVarArg]) -> String {
        String(format: GettextFormat.toFoundation(gettextFormat), arguments: args)
    }
}

/// One language's strings, loaded from `<lang>.lproj/Localizable.strings`
/// and `Localizable.stringsdict`. The plural form is selected here with
/// `PluralRules`, not by Foundation, so no bundle is needed and the
/// hand-assembled .app can keep its resources in Contents/Resources.
public struct Catalogue: Sendable {
    /// The `.lproj` name ("cs"); "en" for the empty catalogue.
    public let language: String
    private let strings: [String: String]
    /// singular msgid → CLDR category → Foundation format.
    private let plurals: [String: [String: String]]

    /// No translations: every lookup falls back to the msgid.
    public static let english = Catalogue(language: "en", strings: [:], plurals: [:])

    /// The environment variable naming a directory of `.lproj`s, for runs
    /// outside the bundle (`make test-macos`, `swift run`).
    public static let localeDirEnv = "MALACHI_LOCALE_DIR"

    public init(language: String, strings: [String: String], plurals: [String: [String: String]]) {
        self.language = language
        self.strings = strings
        self.plurals = plurals
    }

    public var isEmpty: Bool { strings.isEmpty && plurals.isEmpty }

    // MARK: Lookup

    /// The raw catalogue value of `key`, if any (already Foundation format).
    public func string(_ key: String) -> String? {
        strings[key]
    }

    /// The plural forms of `singular` by CLDR category, if any.
    public func pluralForms(_ singular: String) -> [String: String]? {
        plurals[singular]
    }

    public func translate(_ msgid: String) -> String {
        strings[msgid] ?? msgid
    }

    public func translate(_ msgid: String, _ args: [any CVarArg]) -> String {
        let pattern = strings[msgid] ?? GettextFormat.toFoundation(msgid)
        return String(format: pattern, arguments: args)
    }

    public func plural(_ singular: String, _ plural: String, _ n: Int) -> String {
        if let forms = plurals[singular] {
            let category = PluralRules.category(language: language, n: n)
            if let pattern = forms[category] ?? forms["other"] {
                return String(format: pattern, arguments: [n])
            }
        }
        let fallback = abs(n) == 1 ? singular : plural
        return String(format: GettextFormat.toFoundation(fallback), arguments: [n])
    }

    public func context(_ context: String, _ msgid: String) -> String {
        strings[Catalogue.contextKey(context, msgid)] ?? msgid
    }

    /// The key of a context entry, gettext's EOT convention.
    public static func contextKey(_ context: String, _ msgid: String) -> String {
        context + "\u{4}" + msgid
    }

    // MARK: Loading

    /// Loads the first of `languages` that has an `.lproj` with a
    /// `Localizable.strings` under `resourceURL`; `.english` when none has.
    public static func load(from resourceURL: URL, languages: [String]) -> Catalogue {
        for lang in languages {
            let lproj = resourceURL.appendingPathComponent("\(lang).lproj", isDirectory: true)
            guard hasStrings(lproj) else { continue }
            return load(lproj: lproj, language: lang)
        }
        return .english
    }

    /// The catalogue for the user's language: the bundle's resources when
    /// they carry `.lproj`s, else `MALACHI_LOCALE_DIR`, else English. The
    /// language comes from `Bundle.preferredLocalizations`, which honours
    /// the per-app language in System Settings.
    public static func `default`(environment env: [String: String] = ProcessInfo.processInfo.environment) -> Catalogue {
        var roots: [URL] = []
        if let r = Bundle.main.resourceURL {
            roots.append(r)
        }
        if let dir = env[localeDirEnv], !dir.isEmpty {
            roots.append(URL(fileURLWithPath: dir, isDirectory: true))
        }
        for root in roots {
            let available = availableLanguages(in: root)
            guard !available.isEmpty else { continue }
            let preferred = Bundle.preferredLocalizations(from: available, forPreferences: nil)
            return load(from: root, languages: preferred + available.filter { !preferred.contains($0) })
        }
        return .english
    }

    /// The languages with an `<lang>.lproj/Localizable.strings` under `root`.
    public static func availableLanguages(in root: URL) -> [String] {
        guard let names = try? FileManager.default.contentsOfDirectory(atPath: root.path) else {
            return []
        }
        return names
            .filter { $0.hasSuffix(".lproj") && hasStrings(root.appendingPathComponent($0, isDirectory: true)) }
            .map { String($0.dropLast(".lproj".count)) }
            .sorted()
    }

    private static func hasStrings(_ lproj: URL) -> Bool {
        FileManager.default.fileExists(atPath: lproj.appendingPathComponent("Localizable.strings").path)
    }

    private static func load(lproj: URL, language: String) -> Catalogue {
        var strings: [String: String] = [:]
        if let dict = NSDictionary(contentsOf: lproj.appendingPathComponent("Localizable.strings")) as? [String: String] {
            strings = dict
        }
        var plurals: [String: [String: String]] = [:]
        if let dict = NSDictionary(contentsOf: lproj.appendingPathComponent("Localizable.stringsdict")) as? [String: Any] {
            for (key, value) in dict {
                guard let entry = value as? [String: Any],
                      let variable = entry["n"] as? [String: Any] else { continue }
                var forms: [String: String] = [:]
                for category in ["zero", "one", "two", "few", "many", "other"] {
                    if let f = variable[category] as? String {
                        forms[category] = f
                    }
                }
                if !forms.isEmpty {
                    plurals[key] = forms
                }
            }
        }
        return Catalogue(language: language, strings: strings, plurals: plurals)
    }
}

/// A lock around the active catalogue. `Catalogue` is a value type, so a
/// reader gets a consistent snapshot and a writer replaces it whole.
private final class CatalogueBox: @unchecked Sendable {
    private let lock = NSLock()
    private var stored: Catalogue

    init(_ initial: Catalogue) {
        stored = initial
    }

    var value: Catalogue {
        get {
            lock.lock()
            defer { lock.unlock() }
            return stored
        }
        set {
            lock.lock()
            defer { lock.unlock() }
            stored = newValue
        }
    }
}
