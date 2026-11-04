// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalDAVWizard.swift; GTK:
// ui/internal/calendar/caldav.go (WizardPhase, WizardStep, Wizard,
// NewWizard, FieldsEnabled, Edit, serverHost, typedURL, CanDiscover,
// BeginDiscover, DiscoverParams, pinFor, DiscoverDone, ServerURL, config,
// ListParams, ListDone, Toggle, SetMailAccount, ShowsMailAccount, CanAdd,
// BeginAdd, AddParams, AddDone, TrustOffer, Trust, CalDAVTitle,
// CalDAVSubtitle, CalDAVField, CalDAVFieldKind, ServerPlaceholder,
// PasswordNote, DiscoverButton, WizardProgress, FoundVia, FoundLine,
// ServerChanged, CertificateLabel, CertificateText, TrustButton,
// CalendarsHeading, ListedNote, CalDAVFooter, AddButton, WizardErrorText).
//
// The Add CalDAV Calendar sheet: its flow (entering → discovering → listed
// → adding → added), which fields and buttons are enabled, the
// certificate-trust offer (the rules of CertTrust; the confirmation is the
// account assistant's), the account it adds, and its texts. The password
// lives here only while the sheet is open and goes to the daemon's keyring
// with account.add.
//
// Windows: Swift's mutable struct is a class (the controller owns one and
// changes it through its methods; Clone takes a snapshot); Go's *api.Error
// is RpcError. The phase and step enums keep Go's names (WizardPhase,
// WizardStep), because a nested type cannot share its name with the
// properties Phase and Step. A typed server's host keeps its case, as Go's Hostname and
// Swift's URLComponents do: the URL is checked by System.Uri, which lower-
// cases the host it returns, so the host is read from the text itself.
// Hosts compare as CertTrust compares them (OrdinalIgnoreCase, Go's
// EqualFold for ASCII).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Wizard;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>caldav.go <c>Wizard</c> (Swift <c>Cal.CalDAVWizard</c>): the Add CalDAV Calendar sheet's state.</summary>
    public sealed class CalDAVWizard
    {
        private Dictionary<string, bool> selected = new(StringComparer.Ordinal);

        /// <summary>
        /// caldav.go <c>NewWizard</c>: an empty sheet; <paramref name="mailAccount"/>
        /// is the default mail account for invitations (the first compose
        /// account, null none).
        /// </summary>
        public CalDAVWizard(AccountId? mailAccount)
        {
            MailAccount = mailAccount;
        }

        /// <summary>caldav.go <c>WizardPhase</c>: where the sheet is.</summary>
        public enum WizardPhase
        {
            /// <summary>The fields are editable; Discover Calendars is the next step (after an error too).</summary>
            Entering,

            /// <summary>account.discoverCalDAV, then account.listCalDAVCalendars run (<see cref="Step"/> says which).</summary>
            Discovering,

            /// <summary>The calendars are listed with checkboxes; Add adds.</summary>
            Listed,

            /// <summary>account.add runs.</summary>
            Adding,

            /// <summary>The account exists; the sheet closes.</summary>
            Added,
        }

        /// <summary>caldav.go <c>WizardStep</c>: the call of <see cref="WizardPhase.Discovering"/>.</summary>
        public enum WizardStep
        {
            /// <summary>account.discoverCalDAV.</summary>
            Discover,

            /// <summary>account.listCalDAVCalendars.</summary>
            List,
        }

        /// <summary>caldav.go <c>CalDAVFieldKind</c>: a field of the sheet, in the sheet's order.</summary>
        public enum Field
        {
            /// <summary>Account Name.</summary>
            Name,

            /// <summary>Server Address.</summary>
            Server,

            /// <summary>Username.</summary>
            Username,

            /// <summary>Password.</summary>
            Password,
        }

        /// <summary>caldav.go <c>ServerPlaceholder</c>: the server field's example; not translated.</summary>
        public const string ServerPlaceholder = "cloud.example.org";

        /// <summary>Where the sheet is.</summary>
        public WizardPhase Phase { get; private set; } = WizardPhase.Entering;

        /// <summary>The call of <see cref="WizardPhase.Discovering"/>.</summary>
        public WizardStep Step { get; private set; } = WizardStep.Discover;

        /// <summary>The Account Name field.</summary>
        public string Name { get; private set; } = "";

        /// <summary>The Server Address field.</summary>
        public string Server { get; private set; } = "";

        /// <summary>The Username field.</summary>
        public string Username { get; private set; } = "";

        /// <summary>The Password field (only while the sheet is open).</summary>
        public string Password { get; private set; } = "";

        /// <summary>account.discoverCalDAV's answer (null before).</summary>
        public AccountDiscoverCalDAVResult? Discovered { get; private set; }

        /// <summary>account.listCalDAVCalendars's answer (null before).</summary>
        public AccountListCalDAVCalendarsResult? Listed { get; private set; }

        /// <summary>The hrefs to synchronise.</summary>
        public IReadOnlyDictionary<string, bool> Selected => selected;

        /// <summary>The mail account invitations leave from (client scheduling); null none.</summary>
        public AccountId? MailAccount { get; private set; }

        /// <summary>The certificate the user trusted ("" none).</summary>
        public string Pin { get; private set; } = "";

        /// <summary>The last call's error (null none).</summary>
        public RpcError? Error { get; private set; }

        /// <summary>The added account.</summary>
        public AccountId? AccountId { get; private set; }

        /// <summary>caldav.go <c>FieldsEnabled</c>: editable fields (not while a call runs).</summary>
        public bool FieldsEnabled => Phase is WizardPhase.Entering or WizardPhase.Listed;

        /// <summary>A copy to keep (Swift's value semantics).</summary>
        public CalDAVWizard Clone()
        {
            var c = (CalDAVWizard)MemberwiseClone();
            c.selected = new Dictionary<string, bool>(selected, StringComparer.Ordinal);
            return c;
        }

        /// <summary>
        /// caldav.go <c>Edit</c>: takes the fields. A change of the server,
        /// the username or the password drops what was found (back to
        /// entering); the trusted certificate stays only while the server's
        /// host is the same. Ignored while a call runs.
        /// </summary>
        public void Edit(string name, string server, string username, string password)
        {
            ArgumentNullException.ThrowIfNull(name);
            ArgumentNullException.ThrowIfNull(server);
            ArgumentNullException.ThrowIfNull(username);
            ArgumentNullException.ThrowIfNull(password);
            if (!FieldsEnabled)
            {
                return;
            }
            var changed = !string.Equals(TrimSpace(server), TrimSpace(Server), StringComparison.Ordinal)
                || !string.Equals(username, Username, StringComparison.Ordinal)
                || !string.Equals(password, Password, StringComparison.Ordinal);
            if (!SameHost(Host(server), Host(Server)))
            {
                Pin = "";
            }
            Name = name;
            Server = server;
            Username = username;
            Password = password;
            if (changed)
            {
                Phase = WizardPhase.Entering;
                Discovered = null;
                Listed = null;
                Error = null;
                selected.Clear();
            }
        }

        /// <summary>
        /// caldav.go <c>serverHost</c>: the host of a typed server
        /// ("cloud.example.org", "https://cloud.example.org/dav", an IPv6
        /// address without its brackets); "" when it is not one.
        /// </summary>
        public static string Host(string? s)
        {
            var t = TrimSpace(s);
            if (!t.Contains("://", StringComparison.Ordinal))
            {
                t = "https://" + t;
            }
            if (!Uri.TryCreate(t, UriKind.Absolute, out var uri) || string.IsNullOrEmpty(uri.Host))
            {
                return "";
            }
            // The authority as typed: after the scheme, before the path,
            // query or fragment, without user info.
            var authority = t[(t.IndexOf("://", StringComparison.Ordinal) + 3)..];
            var end = authority.IndexOfAny(['/', '?', '#']);
            if (end >= 0)
            {
                authority = authority[..end];
            }
            var at = authority.LastIndexOf('@');
            if (at >= 0)
            {
                authority = authority[(at + 1)..];
            }
            if (authority.StartsWith('['))
            {
                var close = authority.IndexOf(']', StringComparison.Ordinal);
                return close > 0 ? authority[1..close] : "";
            }
            var colon = authority.LastIndexOf(':');
            if (colon >= 0 && authority[(colon + 1)..].All(char.IsAsciiDigit))
            {
                authority = authority[..colon];
            }
            return authority;
        }

        /// <summary>caldav.go <c>typedURL</c>: the typed server as a URL (https assumed, no trailing slash).</summary>
        public static string TypedUrl(string? s)
        {
            var t = TrimSpace(s).TrimEnd('/');
            return t.Contains("://", StringComparison.Ordinal) ? t : "https://" + t;
        }

        private static bool SameHost(string a, string b) => string.Equals(a, b, StringComparison.OrdinalIgnoreCase);

        /// <summary>caldav.go <c>CanDiscover</c>: Discover Calendars enabled: editable, a server, a username and a password.</summary>
        public bool CanDiscover =>
            FieldsEnabled && Host(Server).Length > 0 && TrimSpace(Username).Length > 0 && Password.Length > 0;

        /// <summary>
        /// caldav.go <c>BeginDiscover</c>: starts discovery:
        /// account.discoverCalDAV (<see cref="DiscoverParams"/>), then
        /// account.listCalDAVCalendars (<see cref="ListParams"/>). False when
        /// it cannot start.
        /// </summary>
        public bool BeginDiscover()
        {
            if (!CanDiscover)
            {
                return false;
            }
            Phase = WizardPhase.Discovering;
            Step = WizardStep.Discover;
            Error = null;
            Discovered = null;
            Listed = null;
            return true;
        }

        /// <summary>caldav.go <c>DiscoverParams</c>: account.discoverCalDAV's params, with the trusted certificate of the typed server.</summary>
        public AccountDiscoverCalDAVParams DiscoverParams =>
            new() { Url = TrimSpace(Server), CertificateSha256 = Pin.Length == 0 ? null : Pin };

        // caldav.go pinFor: the pin for the server the account uses: the
        // trusted one while that server is on the typed host (a pin is trusted
        // for the typed server only, never for one a redirect named).
        private string PinForServer() => Pin.Length == 0 || !SameHost(Host(ServerUrl), Host(Server)) ? "" : Pin;

        /// <summary>
        /// caldav.go <c>DiscoverDone</c>: takes account.discoverCalDAV's
        /// answer: an error ends in entering with <see cref="Error"/>; else
        /// the step goes on to the listing.
        /// </summary>
        public void DiscoverDone(AccountDiscoverCalDAVResult? result, RpcError? error)
        {
            if (Phase != WizardPhase.Discovering || Step != WizardStep.Discover)
            {
                return;
            }
            if (error is not null || result is null)
            {
                Phase = WizardPhase.Entering;
                Error = error;
                return;
            }
            Discovered = result;
            Step = WizardStep.List;
        }

        /// <summary>caldav.go <c>ServerURL</c>: the server the account uses: the discovered one, else the typed one.</summary>
        public string ServerUrl => Discovered is { ServerUrl.Length: > 0 } d ? d.ServerUrl : TypedUrl(Server);

        // caldav.go config: the account's configuration.
        private AccountConfig Config()
        {
            var pin = PinForServer();
            var caldav = new CalDAVConfig
            {
                ServerUrl = ServerUrl,
                Username = TrimSpace(Username),
                CertificateSha256 = pin.Length == 0 ? null : pin,
            };
            var name = TrimSpace(Name);
            if (name.Length == 0)
            {
                name = Host(Server);
            }
            var email = "";
            if (Listed is { } l)
            {
                caldav = caldav with { PrincipalUrl = l.PrincipalUrl, HomeSetUrl = l.HomeSetUrl };
                if (l.Addresses.Count > 0)
                {
                    email = l.Addresses[0];
                }
                if (l.Scheduling == CalDAVScheduling.Client && MailAccount is { Value.Length: > 0 } m)
                {
                    caldav = caldav with { MailAccountId = m };
                }
            }
            if (email.Length == 0 && caldav.Username.Contains('@', StringComparison.Ordinal))
            {
                email = caldav.Username;
            }
            return new AccountConfig { Name = name, Email = email, Kind = AccountKind.CalDAV, CalDAV = caldav };
        }

        /// <summary>caldav.go <c>ListParams</c>: account.listCalDAVCalendars's params.</summary>
        public AccountListCalDAVCalendarsParams ListParams =>
            new() { Config = Config(), Credentials = new Credentials { Password = Password } };

        /// <summary>
        /// caldav.go <c>ListDone</c>: takes account.listCalDAVCalendars's
        /// answer: an error ends in entering with <see cref="Error"/>; else
        /// listed with every calendar selected, and the principal's name as
        /// the account's when none was typed.
        /// </summary>
        public void ListDone(AccountListCalDAVCalendarsResult? result, RpcError? error)
        {
            if (Phase != WizardPhase.Discovering || Step != WizardStep.List)
            {
                return;
            }
            if (error is not null || result is null)
            {
                Phase = WizardPhase.Entering;
                Error = error;
                return;
            }
            Phase = WizardPhase.Listed;
            Listed = result;
            selected.Clear();
            foreach (var c in result.Calendars)
            {
                selected[c.Href] = true;
            }
            if (TrimSpace(Name).Length == 0)
            {
                var n = CleanLine(result.DisplayName);
                if (n.Length > 0)
                {
                    Name = n;
                }
            }
        }

        /// <summary>Whether the calendar <paramref name="href"/> is to be synchronised.</summary>
        public bool IsSelected(string href) => selected.TryGetValue(href, out var on) && on;

        /// <summary>caldav.go <c>Toggle</c>: selects or deselects a listed calendar.</summary>
        public void Toggle(string href, bool on)
        {
            ArgumentNullException.ThrowIfNull(href);
            if (Phase != WizardPhase.Listed || Listed is not { } l || !l.Calendars.Any(c => c.Href == href))
            {
                return;
            }
            selected[href] = on;
        }

        /// <summary>caldav.go <c>SetMailAccount</c>: chooses the mail account for invitations.</summary>
        public void SetMailAccount(AccountId? id) => MailAccount = id;

        /// <summary>caldav.go <c>ShowsMailAccount</c>: the mail-account popup: the server leaves the invitations to the client.</summary>
        public bool ShowsMailAccount => Listed is { } l && l.Scheduling == CalDAVScheduling.Client;

        /// <summary>
        /// caldav.go <c>CanAdd</c>: Add enabled: listed, a calendar selected or
        /// an address book found (a server with only address books is added
        /// for its contacts).
        /// </summary>
        public bool CanAdd => Phase == WizardPhase.Listed && Listed is { } l && (l.AddressBooks.Count > 0 || AnyCalendar);

        // caldav.go anyCalendar: a selected calendar.
        private bool AnyCalendar => Listed is { } l && l.Calendars.Any(c => IsSelected(c.Href));

        /// <summary>caldav.go <c>BeginAdd</c>: starts account.add with <see cref="AddParams"/>; false when it cannot.</summary>
        public bool BeginAdd()
        {
            if (!CanAdd)
            {
                return false;
            }
            Phase = WizardPhase.Adding;
            Error = null;
            return true;
        }

        /// <summary>
        /// caldav.go <c>AddParams</c>: account.add's params: the
        /// configuration with the deselected calendars disabled and the
        /// server's default (when selected) as the default calendar.
        /// </summary>
        public AccountAddParams AddParams
        {
            get
            {
                List<string>? disabled = null;
                string? defaultCalendar = null;
                foreach (var c in Listed?.Calendars ?? [])
                {
                    if (!IsSelected(c.Href))
                    {
                        (disabled ??= []).Add(c.Href);
                    }
                    else if (c.IsDefault == true)
                    {
                        defaultCalendar = c.Href;
                    }
                }
                var cfg = Config() with
                {
                    Calendar = new CalendarConfig { Sync = AnyCalendar, DisabledCalendars = disabled, DefaultCalendar = defaultCalendar },
                };
                if (Listed is { AddressBooks.Count: > 0 } listed && cfg.CalDAV is { } dav)
                {
                    // The server's address books come with the account: its
                    // contacts are on, the books chosen in Preferences → Contacts.
                    cfg = cfg with { CalDAV = dav with { AddressBookHomeSetUrl = listed.AddressBookHomeSetUrl, Contacts = true } };
                }
                return new AccountAddParams { Config = cfg, Credentials = new Credentials { Password = Password } };
            }
        }

        /// <summary>caldav.go <c>AddDone</c>: takes account.add's answer: added, or back to listed with <see cref="Error"/>.</summary>
        public void AddDone(AccountId? id, RpcError? error)
        {
            if (Phase != WizardPhase.Adding)
            {
                return;
            }
            if (error is not null)
            {
                Phase = WizardPhase.Listed;
                Error = error;
                return;
            }
            Phase = WizardPhase.Added;
            AccountId = id;
            Password = "";
        }

        /// <summary>
        /// caldav.go <c>TrustOffer</c>: the certificate the sheet offers to
        /// trust after a tlsError: one with a valid fingerprint whose problem
        /// is the certificate (not the connection), at an https server, not
        /// the one already pinned; null for none. The problem is what the
        /// confirmation shows.
        /// </summary>
        public CertTrust.Problem? TrustOffer
        {
            get
            {
                if (Phase != WizardPhase.Entering || Error is not { } e || e.Code.Value != ErrorCode.TlsError
                    || !ServerUrl.StartsWith("https://", StringComparison.OrdinalIgnoreCase))
                {
                    return null;
                }
                if (CertTrust.Details(e) is not { } p || p.Cert is not { } cert || p.Category == CertTrust.Category.Connection
                    || string.Equals(cert.Sha256, Pin, StringComparison.Ordinal))
                {
                    return null;
                }
                return p;
            }
        }

        /// <summary>
        /// caldav.go <c>Trust</c>: pins the offered certificate after the user
        /// confirmed it and starts discovery again (docs/calendar.md §5.8):
        /// back to discovering, the caller sends <see cref="DiscoverParams"/>,
        /// which carry the pin, as do the list and add params after it. False
        /// when nothing was offered.
        /// </summary>
        public bool Trust()
        {
            if (TrustOffer is not { Cert: { } cert })
            {
                return false;
            }
            Pin = cert.Sha256;
            Error = null;
            return BeginDiscover();
        }

        // Texts

        /// <summary>caldav.go <c>CalDAVTitle</c>: the sheet's title.</summary>
        public static string Title => L10n.T("Add CalDAV Calendar");

        /// <summary>caldav.go <c>CalDAVSubtitle</c>: the sheet's subtitle with the server examples.</summary>
        public static string Subtitle => L10n.T("Nextcloud, Fastmail, iCloud, Radicale and other servers that speak CalDAV.");

        /// <summary>caldav.go <c>CalDAVField</c>: a field's label.</summary>
        public static string FieldLabel(Field f) => f switch
        {
            Field.Server => L10n.T("Server Address"),
            Field.Username => L10n.T("Username"),
            Field.Password => L10n.T("Password"),
            _ => L10n.T("Account Name"),
        };

        /// <summary>caldav.go <c>PasswordNote</c>: the note under the password.</summary>
        public static string PasswordNote =>
            L10n.T("The password goes to the system keyring. Malachi Mail never shows it again.");

        /// <summary>caldav.go <c>DiscoverButton</c>: the button that finds the calendars.</summary>
        public static string DiscoverButton => L10n.T("Discover Calendars");

        /// <summary>caldav.go <c>WizardProgress</c>: the line while a call runs ("" otherwise).</summary>
        public string Progress => Phase switch
        {
            WizardPhase.Discovering => L10n.T("Looking for calendars…"),
            WizardPhase.Adding => L10n.T("Adding the account…"),
            _ => "",
        };

        /// <summary>caldav.go <c>FoundVia</c>: how the server was found, for <see cref="FoundLine"/>.</summary>
        public static string FoundVia(CalDAVFoundVia v) => v.Value switch
        {
            CalDAVFoundVia.WellKnown => "/.well-known/caldav",
            // TRANSLATORS: how a CalDAV server was found: a DNS record.
            CalDAVFoundVia.Srv => L10n.T("a DNS SRV record"),
            // TRANSLATORS: how a CalDAV server was found: at the address typed.
            _ => L10n.T("the address you entered"),
        };

        /// <summary>
        /// caldav.go <c>FoundLine</c>: the status line after the listing:
        /// "Found through /.well-known/caldav · 4 calendars"; "" before.
        /// </summary>
        public string FoundLine
        {
            get
            {
                if (Listed is not { } l)
                {
                    return "";
                }
                var via = Discovered?.FoundVia ?? CalDAVFoundVia.Url;
                var n = l.Calendars.Count;
                if (l.AddressBooks.Count is var b && n == 0 && b > 0)
                {
                    // TRANSLATORS: the Add CalDAV sheet's status for a server with
                    // address books and no calendar: how it was found and how many
                    // address books it has.
                    return L10n.N("Found through %s · %d address book", "Found through %s · %d address books", b, FoundVia(via), b);
                }
                // TRANSLATORS: the Add CalDAV sheet's status: how the server was found
                // and how many calendars it has.
                return L10n.N("Found through %s · %d calendar", "Found through %s · %d calendars", n, FoundVia(via), n);
            }
        }

        /// <summary>
        /// caldav.go <c>ServerChanged</c>: the line when discovery ended at
        /// another host than the one typed (a .well-known redirect), shown
        /// before the password goes there; "" otherwise.
        /// </summary>
        public string ServerChanged =>
            Discovered is not { } d || SameHost(Host(d.ServerUrl), Host(Server)) ? "" : L10n.T("The calendars are at %s.", Host(d.ServerUrl));

        /// <summary>caldav.go <c>CertificateLabel</c>: the certificate row's label.</summary>
        public static string CertificateLabel => L10n.T("Certificate");

        /// <summary>
        /// caldav.go <c>CertificateText</c>: the certificate row's value:
        /// "Verified (Let's Encrypt R11)", "Trusted by you" for a pin, ""
        /// before discovery.
        /// </summary>
        public string CertificateText
        {
            get
            {
                if (Pin.Length > 0)
                {
                    return L10n.T("Trusted by you");
                }
                if (Discovered?.Certificate is { } c)
                {
                    var issuer = CleanLine(c.Issuer);
                    // TRANSLATORS: the certificate row of the Add CalDAV sheet; %s is
                    // the certificate's issuer.
                    return issuer.Length > 0 ? L10n.T("Verified (%s)", issuer) : L10n.T("Verified");
                }
                if (Discovered is not null && ServerUrl.StartsWith("http://", StringComparison.OrdinalIgnoreCase))
                {
                    return L10n.T("Not encrypted (local server)");
                }
                return "";
            }
        }

        /// <summary>caldav.go <c>TrustButton</c>: offers to trust the server's certificate.</summary>
        public static string TrustButton => L10n.T("Trust Certificate…");

        /// <summary>caldav.go <c>CalendarsHeading</c>: heads the list of found calendars.</summary>
        public static string CalendarsHeading => L10n.T("Calendars");

        /// <summary>caldav.go <c>ListedNote</c>: the note after a found calendar's name: "default", "read only".</summary>
        public static string ListedNote(CalDAVCalendar c)
        {
            ArgumentNullException.ThrowIfNull(c);
            return Prefs.CalendarRowNote(c.IsDefault == true, c.Access, null);
        }

        /// <summary>caldav.go <c>CalDAVFooter</c>: the sheet's footer note.</summary>
        public static string Footer => L10n.T("Not shown in Mail; in Calendar with the CALDAV capsule.");

        /// <summary>caldav.go <c>AddButton</c>: the sheet's primary button.</summary>
        public static string AddButton =>
            // TRANSLATORS: the Add CalDAV sheet's button that adds the account.
            L10n.C("caldav", "Add");

        /// <summary>caldav.go <c>WizardErrorText</c>: the sentence of the sheet's error ("" none).</summary>
        public string ErrorText
        {
            get
            {
                if (Error is not { } e)
                {
                    return "";
                }
                return e.Code.Value switch
                {
                    ErrorCode.AuthFailed or ErrorCode.AuthRequired => L10n.T("The server did not accept the username or password."),
                    ErrorCode.TlsError => L10n.T("The server’s certificate is not trusted."),
                    ErrorCode.NetworkError or ErrorCode.ServerTimeout or ErrorCode.Offline => L10n.T("The server could not be reached."),
                    ErrorCode.ServerError => L10n.T("The server answered, but it is not a CalDAV server."),
                    ErrorCode.InvalidArgument => L10n.T("This is not a server address Malachi Mail can use."),
                    ErrorCode.KeyringError => L10n.T("The password could not be stored in the system keyring."),
                    _ => L10n.T("The calendars could not be found."),
                };
            }
        }
    }
}
