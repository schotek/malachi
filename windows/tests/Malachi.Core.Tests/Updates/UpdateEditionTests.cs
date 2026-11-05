// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/updates/edition_test.go (TestEditionLine). The
// process-wide catalogue is English here, so every msgid is its own
// translation.

using Malachi.Core.Updates;
using Xunit;

namespace Malachi.Core.Tests.Updates;

public sealed class UpdateEditionTests
{
    [Theory]
    [InlineData(UpdateEdition.Website, "Version from malachimail.app")]
    [InlineData(UpdateEdition.MacAppStore, "Version from the Mac App Store")]
    [InlineData(UpdateEdition.MicrosoftStore, "Version from the Microsoft Store")]
    [InlineData((UpdateEdition)99, "Version from malachimail.app")]
    public void EditionLine(UpdateEdition e, string want) => Assert.Equal(want, UpdateCheck.EditionLine(e));
}
