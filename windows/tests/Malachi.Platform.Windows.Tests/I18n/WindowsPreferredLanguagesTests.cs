// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of WindowsPreferredLanguages: the display languages Windows reports
// (GetUserPreferredUILanguages) and the catalogue they pick from the
// repository's po/.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Linq;
using Malachi.Core.I18n;
using Malachi.Platform.Windows.I18n;
using Xunit;

namespace Malachi.Platform.Windows.Tests.I18n;

public sealed class WindowsPreferredLanguagesTests
{
    [Fact]
    public void ReadsTheDisplayLanguages()
    {
        var languages = WindowsPreferredLanguages.Instance.Languages;
        Assert.NotEmpty(languages);
        foreach (var language in languages)
        {
            Assert.DoesNotContain('\0', language);
            Assert.False(string.IsNullOrWhiteSpace(language));
            Assert.Equal(language, CultureInfo.GetCultureInfo(language).Name, StringComparer.OrdinalIgnoreCase);
        }
        Assert.Equal(languages.Count, languages.Distinct(StringComparer.OrdinalIgnoreCase).Count());
        // The UI culture .NET chose is one of them.
        var ui = PluralRules.Base(CultureInfo.CurrentUICulture.Name);
        Assert.Contains(languages, language => PluralRules.Base(language) == ui);
    }

    // The catalogue the app would load from po/ for this user: the first
    // match of the display languages, English when that comes first.
    [Fact]
    public void PicksTheCatalogueOfTheDisplayLanguage()
    {
        var po = Path.Combine(RepositoryRoot(), "po");
        var environment = new Dictionary<string, string> { [Catalogue.LocaleDirEnv] = po };
        var expected = Catalogue.PreferredLocalizations(Catalogue.AvailableLanguages(po), WindowsPreferredLanguages.Instance.Languages)[0];
        var picked = Catalogue.Default(environment, WindowsPreferredLanguages.Instance, appDirectory: po);
        Assert.Equal(expected, picked.Language);
        Assert.Equal(expected == "en", picked.IsEmpty);
    }

    private static string RepositoryRoot()
    {
        for (var dir = new DirectoryInfo(AppContext.BaseDirectory); dir is not null; dir = dir.Parent)
        {
            if (File.Exists(Path.Combine(dir.FullName, "go.work")))
            {
                return dir.FullName;
            }
        }
        throw new InvalidOperationException("the repository root (the directory with go.work) was not found");
    }
}
