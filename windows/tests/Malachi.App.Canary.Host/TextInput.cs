// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The parameters of the DevTools protocol's Input.insertText.

namespace Malachi.App.Canary.Host;

/// <summary>Input.insertText: text typed at the caret.</summary>
/// <param name="Text">What to type.</param>
internal sealed record TextInput(string Text);
