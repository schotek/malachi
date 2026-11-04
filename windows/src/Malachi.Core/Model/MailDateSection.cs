// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/maildate/groups.go and
// macos/Sources/MalachiCore/Model/MailDateGroups.swift.

using System.Collections.Generic;

namespace Malachi.Core.Model;

/// <summary>One nonempty section, including expanded conversation members.</summary>
public sealed record MailDateSection(MailDateGroup Group, IReadOnlyList<ListRow> Rows);
