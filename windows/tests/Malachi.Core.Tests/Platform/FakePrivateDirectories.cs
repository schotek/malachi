// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A stand-in for Malachi.Platform.Windows.Files.PrivateDirectory in the
// OpenDir tests: ordinary directories, and a record of what was asked.

using System.Collections.Generic;
using System.IO;
using Malachi.Core.Platform;

namespace Malachi.Core.Tests.Platform;

/// <summary>Creates ordinary directories and remembers the calls.</summary>
internal sealed class FakePrivateDirectories : IPrivateDirectoryFactory
{
    /// <summary>The paths <see cref="Ensure"/> was asked for, in order.</summary>
    public List<string> Ensured { get; } = [];

    /// <summary>The paths <see cref="CreateNew"/> created, in order.</summary>
    public List<string> Created { get; } = [];

    /// <summary>
    /// How many of the next <see cref="CreateNew"/> calls find their path
    /// taken (something else created it first).
    /// </summary>
    public int Collisions { get; set; }

    /// <summary>What <see cref="Ensure"/> throws, if anything.</summary>
    public IOException? EnsureFails { get; set; }

    public void Ensure(string path)
    {
        Ensured.Add(path);
        if (EnsureFails is not null)
        {
            throw EnsureFails;
        }
        Directory.CreateDirectory(path);
    }

    public void CreateNew(string path)
    {
        if (Path.Exists(path))
        {
            throw new IOException(path + " already exists");
        }
        if (Collisions > 0)
        {
            Collisions--;
            Directory.CreateDirectory(path);
            throw new IOException(path + " already exists");
        }
        Directory.CreateDirectory(path);
        Created.Add(path);
    }
}
