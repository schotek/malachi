// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/signature/signature.go (Severity, ListIcon, Badge,
// RowKey, Row, SectionKey, Section, Action, Button, Question, Details,
// KeyRow) and text.go (ImportDialog, SecurityPage); the macOS port is
// macos/Sources/MalachiCore/Signatures/ (written at the same time).
//
// The view models of signed mail (docs/plans/signatures.md, phase 1). Go's
// types are top-level here with a Signature prefix where the name alone
// would be ambiguous (Row, Section, Action, Button, Badge, Details), and
// Go's Details is SignatureDetailsView, because SignatureDetails is the
// API's record.

using System.Collections.Generic;

namespace Malachi.Core.Signatures;

/// <summary>signature.Severity: the colour family of a verdict.</summary>
public enum SignatureSeverity
{
    /// <summary>Nothing is shown (no verdict).</summary>
    None,

    /// <summary>valid: green with the check mark.</summary>
    Good,

    /// <summary>validSeen: green without the check mark.</summary>
    GoodSeen,

    /// <summary>mismatch, untrusted, weak: yellow.</summary>
    Warning,

    /// <summary>invalid, revoked: red.</summary>
    Bad,

    /// <summary>unknownKey, unsupported, error and an unknown status: grey.</summary>
    Neutral,
}

/// <summary>signature.ListIcon: the small icon of a message row, next to the paperclip.</summary>
/// <param name="Icon">The icon name (Adwaita's; <c>IconGlyphs</c> maps it).</param>
/// <param name="Style">The style class of the severity (<see cref="SignatureView.StyleOf"/>).</param>
/// <param name="Tooltip">The verdict's short name.</param>
public sealed record SignatureListIcon(string Icon, string Style, string Tooltip);

/// <summary>signature.Badge: the button in a message's header that opens the details.</summary>
public sealed record SignatureBadge
{
    /// <summary>The message has a verdict; nothing else is set otherwise.</summary>
    public bool Visible { get; init; }

    /// <summary>The colour family.</summary>
    public SignatureSeverity Severity { get; init; }

    /// <summary>The icon name.</summary>
    public string Icon { get; init; } = "";

    /// <summary>The style class.</summary>
    public string Style { get; init; } = "";

    /// <summary>The badge's sentence, plain text.</summary>
    public string Text { get; init; } = "";

    /// <summary>Opens the details.</summary>
    public string Tooltip { get; init; } = "";
}

/// <summary>signature.RowKey: names a row of the details.</summary>
public enum SignatureRowKey
{
    /// <summary>Signed by.</summary>
    Signer,

    /// <summary>Signature type.</summary>
    Kind,

    /// <summary>Problem.</summary>
    Reason,

    /// <summary>Signed, as stated by the signer.</summary>
    SignedAt,

    /// <summary>Arrived.</summary>
    ArrivedAt,

    /// <summary>Algorithm.</summary>
    Algorithm,

    /// <summary>Revocation (S/MIME).</summary>
    Revocation,

    /// <summary>Key (OpenPGP trust).</summary>
    KeyTrust,

    /// <summary>Key fingerprint.</summary>
    KeyFingerprint,

    /// <summary>Key ID.</summary>
    KeyId,

    /// <summary>Key source.</summary>
    KeySource,

    /// <summary>First seen.</summary>
    FirstSeen,

    /// <summary>User IDs.</summary>
    UserIds,

    /// <summary>Previous key.</summary>
    PreviousKey,

    /// <summary>Expired.</summary>
    Expired,

    /// <summary>Revoked (OpenPGP).</summary>
    Revoked,

    /// <summary>Issued to (a certificate section).</summary>
    IssuedTo,

    /// <summary>E-mail addresses (a certificate section).</summary>
    Emails,

    /// <summary>Issued by (a certificate section).</summary>
    IssuedBy,

    /// <summary>Valid (a certificate section).</summary>
    Validity,

    /// <summary>SHA-256 fingerprint (a certificate section).</summary>
    CertFingerprint,

    /// <summary>A row of the signed headers' section; its label is the header's.</summary>
    SignedHeader,
}

/// <summary>signature.Row: one line of the details: a label and a plain-text value.</summary>
/// <param name="Key">Which row.</param>
/// <param name="Label">The label.</param>
/// <param name="Value">The plain-text value (selectable).</param>
/// <param name="Monospace">A fingerprint or key ID.</param>
public readonly record struct SignatureRow(SignatureRowKey Key, string Label, string Value, bool Monospace = false);

/// <summary>signature.SectionKey: names a section of the details.</summary>
public enum SignatureSectionKey
{
    /// <summary>The signer's certificate.</summary>
    SignerCertificate,

    /// <summary>An intermediate certificate.</summary>
    Intermediate,

    /// <summary>The root certificate.</summary>
    Root,

    /// <summary>The signed headers.</summary>
    SignedHeaders,
}

/// <summary>signature.Section: a titled group of rows under the main rows.</summary>
/// <param name="Key">Which section.</param>
/// <param name="Title">Its heading.</param>
/// <param name="Rows">Its rows.</param>
public sealed record SignatureSection(SignatureSectionKey Key, string Title, IReadOnlyList<SignatureRow> Rows);

/// <summary>signature.Action: a button of the details or a key row.</summary>
public enum SignatureAction
{
    /// <summary>None.</summary>
    None,

    /// <summary>Calls <c>message.signatureRevocation</c>.</summary>
    CheckRevocation,

    /// <summary>Calls <c>pgp.keyImport</c> with <see cref="SignatureView.ImportParams"/>.</summary>
    ImportKey,

    /// <summary>Asks <see cref="SignatureText.ConfirmKeyQuestion"/>, then <c>pgp.keyVerify</c> {verified: true}.</summary>
    ConfirmKey,

    /// <summary>Calls <c>pgp.keyVerify</c> {verified: false} (key rows).</summary>
    UnconfirmKey,

    /// <summary>Asks <see cref="SignatureText.RemoveKeyQuestion"/>, then <c>pgp.keyRemove</c> (key rows).</summary>
    RemoveKey,
}

/// <summary>signature.Button: a button of the details.</summary>
/// <param name="Action">What it does.</param>
/// <param name="Label">Its label.</param>
/// <param name="Enabled">Whether it can be pressed.</param>
public readonly record struct SignatureButton(SignatureAction Action, string Label, bool Enabled);

/// <summary>signature.Question: a confirmation dialog.</summary>
/// <param name="Heading">The heading.</param>
/// <param name="Body">The text.</param>
/// <param name="Cancel">The cancel button's label, with its mnemonic underscore.</param>
/// <param name="Accept">The accept button's label, with its mnemonic underscore.</param>
/// <param name="Destructive">Whether the accept button is styled as destructive.</param>
public sealed record SignatureQuestion(string Heading, string Body, string Cancel, string Accept, bool Destructive = false);

/// <summary>
/// signature.Details: the view model of the details dialog: heading with
/// icon, explanation, rows, notes, sections, the caveat and buttons.
/// </summary>
public sealed record SignatureDetailsView
{
    /// <summary>The colour family.</summary>
    public SignatureSeverity Severity { get; init; }

    /// <summary>The icon name.</summary>
    public string Icon { get; init; } = "";

    /// <summary>The style class.</summary>
    public string Style { get; init; } = "";

    /// <summary>The verdict's short name.</summary>
    public string Title { get; init; } = "";

    /// <summary>The paragraph under the heading.</summary>
    public string Explanation { get; init; } = "";

    /// <summary>The main rows.</summary>
    public IReadOnlyList<SignatureRow> Rows { get; init; } = [];

    /// <summary>The sentences of the summary's notes, without those the Revocation row already says.</summary>
    public IReadOnlyList<string> Notes { get; init; } = [];

    /// <summary>The S/MIME chain (signer first), then the signed headers.</summary>
    public IReadOnlyList<SignatureSection> Sections { get; init; } = [];

    /// <summary>The line under a valid verdict; "" otherwise.</summary>
    public string Caveat { get; init; } = "";

    /// <summary>The buttons, in order.</summary>
    public IReadOnlyList<SignatureButton> Buttons { get; init; } = [];

    /// <summary>The line under Check Revocation; "" without that button.</summary>
    public string RevocationCaption { get; init; } = "";

    /// <summary>The formatted fingerprint the confirm question shows; "" without Confirm Key….</summary>
    public string KeyFingerprint { get; init; } = "";
}

/// <summary>signature.KeyRow: a row of the OpenPGP key list.</summary>
/// <param name="Fingerprint">The key's fingerprint, raw: what <c>pgp.keyVerify</c> and <c>pgp.keyRemove</c> take.</param>
/// <param name="Title">The first user ID (or address, or the fingerprint).</param>
/// <param name="Subtitle">The formatted fingerprint (monospace).</param>
/// <param name="Origin">Where and when it came from.</param>
/// <param name="TrustPill">"Confirmed" or "Not confirmed".</param>
/// <param name="Verified">The key is confirmed (styles the pill as success).</param>
/// <param name="StatePill">"Revoked", "Expired" or "".</param>
/// <param name="Actions">The row's menu: Confirm Key… or Remove Confirmation, then Remove Key….</param>
public sealed record SignatureKeyRow(
    string Fingerprint,
    string Title,
    string Subtitle,
    string Origin,
    string TrustPill,
    bool Verified,
    string StatePill,
    IReadOnlyList<SignatureButton> Actions);

/// <summary>signature.ImportDialog: the texts of the Import OpenPGP Key dialog.</summary>
/// <param name="Title">The title.</param>
/// <param name="Body">The explanation above the text area.</param>
/// <param name="ChooseFile">The file chooser's button.</param>
/// <param name="FileFilter">The name of the file type filter.</param>
/// <param name="Cancel">The cancel button.</param>
/// <param name="Import">The import button.</param>
public sealed record SignatureImportDialog(string Title, string Body, string ChooseFile, string FileFilter, string Cancel, string Import);

/// <summary>signature.SecurityPage: the texts of Preferences → Security.</summary>
/// <param name="Title">The page's title (and its navigation item).</param>
/// <param name="Group">The heading of the key list.</param>
/// <param name="Description">The group's description.</param>
/// <param name="Empty">The list's placeholder.</param>
/// <param name="Import">The button that opens the import dialog.</param>
/// <param name="LoadFailed">The line when <c>pgp.keyList</c> failed.</param>
public sealed record SignatureSecurityPage(string Title, string Group, string Description, string Empty, string Import, string LoadFailed);
