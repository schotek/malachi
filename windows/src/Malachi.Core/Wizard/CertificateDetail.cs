// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Wizard/TrustPrompt.swift
// (CertificateDetail); GTK: ui/internal/accountwizard/trust.go (the rows of
// certificateDetails).

namespace Malachi.Core.Wizard;

/// <summary>
/// One line of a certificate's details: a label and its value. Every value is
/// untrusted text from the server (cleaned by <see cref="CertTrust.Details"/>),
/// shown as selectable plain text.
/// </summary>
/// <param name="Label">The label.</param>
/// <param name="Value">The value; "" for a line that is only a label ("Self-signed").</param>
/// <param name="Monospaced">The fingerprint, compared by eye: monospaced.</param>
public sealed record CertificateDetail(string Label, string Value, bool Monospaced = false);
