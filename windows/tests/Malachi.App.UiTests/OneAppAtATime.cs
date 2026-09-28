// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Every UI test class is in this collection: one app at a time, since the
// app is a single instance per executable and the tests share its folder.

using Xunit;

namespace Malachi.App.UiTests;

/// <summary>The UI tests run one after the other.</summary>
[CollectionDefinition(Name, DisableParallelization = true)]
public sealed class OneAppAtATime
{
    /// <summary>The collection's name.</summary>
    public const string Name = "Malachi Mail app";
}
