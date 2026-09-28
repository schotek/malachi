// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A registry root of one test: HKCU\Software\io.github.schotek.Malachi.Tests.<guid>,
// deleted with everything below it when the test ends. The launch-at-login
// and mailto: tests write the keys they would write under
// HKEY_CURRENT_USER below it, so that no test ever touches the real Run key
// or the real associations; the settings backend's tests keep their
// settings in one (NewPath).
//
// Each root is a key of its own directly under Software, with no parent the
// tests share. When they shared HKCU\Software\io.github.schotek.Malachi.Tests
// and the last one out deleted it, a test creating its key below it at that
// moment failed ("an illegal operation on a registry key that has been
// marked for deletion"), and the deletion itself could throw IOException or
// UnauthorizedAccessException; four processes doing just that in a loop
// failed about one creation in 170.

using System;
using Microsoft.Win32;

namespace Malachi.Platform.Windows.Tests.Startup;

internal sealed class TestRegistryRoot : IDisposable
{
    private const string Prefix = @"Software\io.github.schotek.Malachi.Tests.";

    public TestRegistryRoot()
    {
        Path = NewPath();
        Key = Registry.CurrentUser.CreateSubKey(Path, writable: true);
    }

    /// <summary>The root's path under HKEY_CURRENT_USER.</summary>
    public string Path { get; }

    /// <summary>The root, writable.</summary>
    public RegistryKey Key { get; }

    /// <summary>A path under HKEY_CURRENT_USER no other test uses, for a key the test creates and deletes itself.</summary>
    public static string NewPath() => Prefix + Guid.NewGuid().ToString("N");

    public void Dispose()
    {
        Key.Dispose();
        Registry.CurrentUser.DeleteSubKeyTree(Path, throwOnMissingSubKey: false);
    }
}
