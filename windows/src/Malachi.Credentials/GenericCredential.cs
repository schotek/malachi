// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows only, no Swift counterpart: an item of Credential Manager as
// ICredentialManager reads it.

namespace Malachi.Credentials;

/// <summary>A generic credential as CredReadW returns it.</summary>
/// <param name="TargetName">Its name, which identifies it (without regard to case).</param>
/// <param name="UserName">The user name, or null.</param>
/// <param name="Comment">The comment, or null.</param>
/// <param name="Blob">
/// A copy of the credential blob; whoever reads it zeroes it after use.
/// </param>
internal sealed record GenericCredential(string TargetName, string? UserName, string? Comment, byte[] Blob);
