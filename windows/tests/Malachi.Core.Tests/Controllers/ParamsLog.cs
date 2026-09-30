// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The ParamsLog actor of the Swift controller suites (WizardControllerTests,
// JiraWizardControllerTests, JiraAccountControllerTests): what a fake
// daemon's handler was called with, decoded on demand.

using System.Collections.Generic;
using System.Threading;
using Malachi.Core.Api;

namespace Malachi.Core.Tests.Controllers;

/// <summary>Records the parameters each method was called with (the Swift suite's ParamsLog).</summary>
internal sealed class ParamsLog
{
    private readonly Lock gate = new();
    private readonly Dictionary<string, List<string>> byMethod = [];

    public void Record(string method, string json)
    {
        lock (gate)
        {
            if (!byMethod.TryGetValue(method, out var list))
            {
                byMethod[method] = list = [];
            }
            list.Add(json);
        }
    }

    /// <summary>How often <paramref name="method"/> was called.</summary>
    public int Count(string method)
    {
        lock (gate)
        {
            return byMethod.TryGetValue(method, out var list) ? list.Count : 0;
        }
    }

    /// <summary>The params of every call of <paramref name="method"/>, decoded, in order.</summary>
    public IReadOnlyList<T> All<T>(string method)
    {
        List<string> list;
        lock (gate)
        {
            list = byMethod.TryGetValue(method, out var calls) ? [.. calls] : [];
        }
        return [.. list.ConvertAll(JsonCoding.Decode<T>)];
    }
    /// <summary>The params of the last call of <paramref name="method"/> as sent; null before any.</summary>
    public string? LastRaw(string method)
    {
        lock (gate)
        {
            return byMethod.TryGetValue(method, out var list) && list.Count > 0 ? list[^1] : null;
        }
    }

    /// <summary>The params of the last call of <paramref name="method"/>, decoded; null before any.</summary>
    public T? Last<T>(string method)
        where T : class
    {
        string? json;
        lock (gate)
        {
            json = byMethod.TryGetValue(method, out var list) && list.Count > 0 ? list[^1] : null;
        }
        return json is null ? null : JsonCoding.Decode<T>(json);
    }
}
