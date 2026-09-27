// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A registry root of one test: HKCU\Software\io.github.schotek.Malachi.Tests\<guid>,
// deleted with everything below it when the test ends (the convention of
// RegistrySettingsBackendTests). The launch-at-login and mailto: tests write
// the keys they would write under HKEY_CURRENT_USER below it, so that no
// test ever touches the real Run key or the real associations.

using System;
using Microsoft.Win32;

namespace Malachi.Platform.Windows.Tests.Startup;

internal sealed class TestRegistryRoot : IDisposable
{
    private const string TestsRoot = @"Software\io.github.schotek.Malachi.Tests";

    public TestRegistryRoot()
    {
        Path = TestsRoot + @"\" + Guid.NewGuid().ToString("N");
        Key = Registry.CurrentUser.CreateSubKey(Path, writable: true);
    }

    /// <summary>The root's path under HKEY_CURRENT_USER.</summary>
    public string Path { get; }

    /// <summary>The root, writable.</summary>
    public RegistryKey Key { get; }

    public void Dispose()
    {
        Key.Dispose();
        Registry.CurrentUser.DeleteSubKeyTree(Path, throwOnMissingSubKey: false);
        try
        {
            // Other tests may be using it at the same time.
            Registry.CurrentUser.DeleteSubKey(TestsRoot, throwOnMissingSubKey: false);
        }
        catch (Exception e) when (e is InvalidOperationException or UnauthorizedAccessException or System.IO.IOException)
        {
            // It still has subkeys, or another test is deleting it: it is
            // left for the last one. Inside an MSIX container (Claude
            // Desktop's agents) a key that also exists outside may not be
            // deleted at all.
        }
    }
}
