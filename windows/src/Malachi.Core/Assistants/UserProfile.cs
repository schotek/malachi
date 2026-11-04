// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantProfile.swift
// (Assistant.Profile, Assistant.AccountNote); Go:
// ui/internal/assistant/profile.go (Profile, AccountNote, ProfileFrom,
// Empty, Global, For). Windows: Swift's Assistant.Profile is UserProfile,
// a record of its own, because Assistant is a static class here; its
// AccountNote is ProfileNote, to stay apart from Api.ProfileAccountNote.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Assistants;

/// <summary>
/// assistant.AccountNote: the note of one account, with its name and
/// address (from <c>account.list</c>).
/// </summary>
/// <param name="AccountId">The account's id.</param>
/// <param name="Name">The account's name ("" when not known).</param>
/// <param name="Email">The account's address ("" when not known).</param>
/// <param name="Note">The note.</param>
public sealed record ProfileNote(string AccountId, string Name, string Email, string Note);

/// <summary>
/// assistant.Profile: the user's profile with the names of its accounts, for
/// the prompts (<see cref="Assistant.WithProfile"/>).
/// </summary>
/// <param name="Text">What the user wrote about themselves.</param>
/// <param name="Gender">The gender they write in.</param>
/// <param name="Accounts">The notes of the accounts, in the profile's order.</param>
public sealed record UserProfile(string Text, ProfileGender Gender, IReadOnlyList<ProfileNote> Accounts)
{
    /// <summary>No profile: nothing goes into a prompt.</summary>
    public static UserProfile Empty { get; } = new("", ProfileGender.Unset, []);

    /// <summary>Profile.Empty: the profile adds nothing to a prompt.</summary>
    public bool IsEmpty =>
        Assistant.GoTrimSpace(Text).Length == 0 && Assistant.GenderSentence(Gender).Length == 0 && Notes.Count == 0;

    /// <summary>The notes that are not blank.</summary>
    internal IReadOnlyList<ProfileNote> Notes =>
        [.. Accounts.Where(n => Assistant.GoTrimSpace(n.Note).Length > 0)];

    /// <summary>
    /// assistant.ProfileFrom: the daemon's profile with the names and
    /// addresses of <paramref name="accounts"/>; a note of an account the
    /// list does not name keeps only its id.
    /// </summary>
    public static UserProfile From(AssistantProfile p, IEnumerable<Account> accounts)
    {
        ArgumentNullException.ThrowIfNull(p);
        ArgumentNullException.ThrowIfNull(accounts);
        var list = accounts.ToList();
        var notes = p.Accounts.Select(n =>
        {
            var a = list.FirstOrDefault(x => x.Id == n.AccountId);
            return new ProfileNote(n.AccountId.Value ?? "", a?.Config.Name ?? "", a?.Config.Email ?? "", n.Note);
        });
        return new UserProfile(p.Text, p.Gender, [.. notes]);
    }

    /// <summary>Profile.Global: without account notes.</summary>
    public UserProfile Global() => this with { Accounts = [] };

    /// <summary>Profile.For: the notes of the named accounts only, in the profile's order.</summary>
    public UserProfile For(params string[] accountIds)
    {
        ArgumentNullException.ThrowIfNull(accountIds);
        return this with { Accounts = [.. Accounts.Where(n => accountIds.Any(id => id.Length > 0 && id == n.AccountId))] };
    }
}
