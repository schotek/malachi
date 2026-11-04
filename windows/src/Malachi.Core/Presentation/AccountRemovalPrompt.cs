// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/PrefsContracts.swift
// (PrefsConfirmation); GTK: ui/internal/window/accounts_page.go
// (removeAccount: widget.ConfirmDestructiveExtra with its check button).

namespace Malachi.Core.Presentation;

/// <summary>
/// The texts of "Remove this account?": heading and body are plain text (the
/// body names the account's address); the labels keep their GTK mnemonic
/// marker, which the dialog turns into its access key or strips.
/// </summary>
/// <param name="Heading">"Remove this account?".</param>
/// <param name="Body">What happens, naming the account.</param>
/// <param name="ConfirmLabel">"_Remove".</param>
/// <param name="ExtraLabel">The check box: "Also delete _drafts and downloaded data".</param>
/// <param name="ExtraDefault">Whether the check box starts checked (GTK: yes).</param>
public sealed record AccountRemovalPrompt(string Heading, string Body, string ConfirmLabel, string ExtraLabel, bool ExtraDefault);
