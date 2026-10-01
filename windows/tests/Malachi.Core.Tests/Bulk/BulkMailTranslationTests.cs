// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BulkMailTranslationTests.swift: the
// Czech side of ui/internal/bulkmail, and the intent of
// ui/internal/bulkmail/po_test.go (TestMsgidsInTemplate): the port asks for
// exactly the msgids, contexts and plurals po/malachi.pot assigns to
// ui/internal/bulkmail. The process-wide catalogue stays English (the tests
// run in parallel), so the port's own calls are read from its sources
// (Malachi.Core/Bulk). Windows reads po/cs.po, so these always run.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using System.Text.RegularExpressions;
using Malachi.Core.I18n;
using Malachi.Core.Tests.I18n;
using Xunit;

namespace Malachi.Core.Tests.Bulk;

public sealed partial class BulkMailTranslationTests
{
    private static readonly Lazy<Catalogue> Czech = new(() => Catalogue.Load(RepositoryPo.Directory, ["cs"]));

    private static Catalogue Cs => Czech.Value;

    // po_test.go msgKey: a msgid with its context and plural ("" for none).
    private readonly record struct MsgKey(string Ctx, string Msgid, string Plural)
    {
        public override string ToString() => $"msgid \"{Msgid}\" (context \"{Ctx}\", plural \"{Plural}\")";
    }

    // A C-quoted string of a PO file or a C# literal without its quotes,
    // unescaped (\" \\ \n \t).
    private static string Unquote(string s)
    {
        var output = new StringBuilder(s.Length);
        for (var i = 0; i < s.Length; i++)
        {
            if (s[i] == '\\' && i + 1 < s.Length)
            {
                i++;
                output.Append(s[i] switch { 'n' => '\n', 't' => '\t', var c => c });
                continue;
            }
            output.Append(s[i]);
        }
        return output.ToString();
    }

    // po_test.go template: every entry of po/malachi.pot, and whether it
    // names a file of ui/internal/bulkmail among its references.
    private static Dictionary<MsgKey, bool> Template()
    {
        var output = new Dictionary<MsgKey, bool>();
        string ctx = "", msgid = "", plural = "";
        var ours = false;
        string? field = null;
        void Flush()
        {
            if (msgid.Length > 0)
            {
                output[new MsgKey(ctx, msgid, plural)] = ours;
            }
            ctx = msgid = plural = "";
            ours = false;
            field = null;
        }
        void Append(string name, string value)
        {
            switch (name)
            {
                case "msgctxt":
                    ctx += value;
                    break;
                case "msgid":
                    msgid += value;
                    break;
                case "msgid_plural":
                    plural += value;
                    break;
                default:
                    break;
            }
        }
        foreach (var line in File.ReadAllText(RepositoryPo.Pot).Replace("\r\n", "\n", StringComparison.Ordinal).Split('\n'))
        {
            if (line.Trim().Length == 0)
            {
                Flush();
            }
            else if (line.StartsWith("#:", StringComparison.Ordinal))
            {
                ours = ours || line.Contains("ui/internal/bulkmail/", StringComparison.Ordinal);
            }
            else if (line.Length >= 2 && line[0] == '"' && line[^1] == '"' && field is not null)
            {
                Append(field, Unquote(line[1..^1]));
            }
            else if (line.IndexOf(' ', StringComparison.Ordinal) is var space and > 0 && space + 1 < line.Length && line[space + 1] == '"' && line[^1] == '"')
            {
                field = line[..space] is "msgctxt" or "msgid" or "msgid_plural" ? line[..space] : null;
                if (field is not null)
                {
                    Append(field, Unquote(line[(space + 2)..^1]));
                }
            }
        }
        Flush();
        return output;
    }

    [GeneratedRegex("""L10n\.([TNC])\(\s*"((?:[^"\\]|\\.)*)"(?:\s*,\s*"((?:[^"\\]|\\.)*)")?""")]
    private static partial Regex L10nCall();

    // What the port translates: every L10n.T("…"), L10n.N("…", "…" and
    // L10n.C("…", "…") of its sources (po_test.go exercise with a recorder;
    // the sources stand in for the calls).
    private static HashSet<MsgKey> PortKeys()
    {
        var keys = new HashSet<MsgKey>();
        var dir = Path.Combine(RepositoryPo.Root, "windows", "src", "Malachi.Core", "Bulk");
        foreach (var file in Directory.EnumerateFiles(dir, "*.cs"))
        {
            foreach (Match m in L10nCall().Matches(File.ReadAllText(file)))
            {
                var first = Unquote(m.Groups[2].Value);
                var second = m.Groups[3].Success ? Unquote(m.Groups[3].Value) : "";
                keys.Add(m.Groups[1].Value switch
                {
                    "C" => new MsgKey(first, second, ""),
                    "N" => new MsgKey("", first, second),
                    _ => new MsgKey("", first, ""),
                });
            }
        }
        return keys;
    }

    // po_test.go TestMsgidsInTemplate for the port: every msgid it
    // translates is in the template with its context and plural, and every
    // entry that names ui/internal/bulkmail is one it translates.
    [Fact]
    public void PortUsesTheMsgidsOfTheGoPackage()
    {
        var pot = Template();
        var used = PortKeys();
        Assert.Contains(true, pot.Values); // some entry names ui/internal/bulkmail
        Assert.True(used.Count >= 20, $"found only {used.Count} calls");
        var missing = used.Where(k => !pot.ContainsKey(k)).ToArray();
        Assert.True(missing.Length == 0, "po/malachi.pot lacks " + string.Join("; ", missing));
        var unused = pot.Where(e => e.Value && !used.Contains(e.Key)).Select(e => e.Key).ToArray();
        Assert.True(unused.Length == 0, "po/malachi.pot names ui/internal/bulkmail for " + string.Join("; ", unused) + ", which the Windows port does not translate");
    }

    // Every msgid the port asks for has a Czech translation.
    [Fact]
    public void EveryMsgidOfThePortIsTranslated()
    {
        var cs = Cs;
        foreach (var k in PortKeys())
        {
            if (k.Ctx.Length > 0)
            {
                Assert.True(cs.Lookup(Catalogue.ContextKey(k.Ctx, k.Msgid)) is not null, $"no Czech translation for {k}");
            }
            else
            {
                Assert.True(cs.Lookup(k.Msgid) is not null, $"no Czech translation for {k}");
            }
        }
    }

    // The tags carry a context, which must win over the plain msgid (the
    // plain "Bulk" and "Automated" are other entries, or none).
    [Fact]
    public void TagsPickTheirContextEntries()
    {
        var cs = Cs;
        Assert.Equal("Hromadná", cs.Context("message tag", "Bulk"));
        Assert.Equal("Konference", cs.Context("message tag", "Mailing List"));
        Assert.Equal("Automatická", cs.Context("message tag", "Automated"));
    }

    // The sentences of the strip, the confirmations and the toasts in Czech
    // (the lead's table of the spec, as po/cs.po has them).
    [Fact]
    public void TextsInCzech()
    {
        var cs = Cs;
        Assert.Equal("Odhlášením byste odesílateli potvrdili, že adresa existuje.", cs.Translate("Unsubscribing would confirm to the sender that your address exists."));
        Assert.Equal("Automatická zpráva", cs.Translate("Automated message"));
        Assert.Equal("Odhlášeno 30. 9. 2026", cs.Translate("Unsubscribed on %s", "30. 9. 2026"));
        Assert.Equal("Hromadná zpráva od shop.example", cs.Translate("Bulk message from %s", "shop.example"));
        Assert.Equal("Zpráva z konference l.example", cs.Translate("Message from mailing list %s", "l.example"));
        Assert.Equal("_Odhlásit odběr", cs.Translate("_Unsubscribe"));
        Assert.Equal("_Odhlásit odběr…", cs.Translate("_Unsubscribe…"));
        Assert.Equal("_Opustit konferenci", cs.Translate("_Leave List"));
        Assert.Equal("_Opustit konferenci…", cs.Translate("_Leave List…"));
        Assert.Equal("Odhlásit odběr od shop.example?", cs.Translate("Unsubscribe from %s?", "shop.example"));
        Assert.Equal("Opustit konferenci l.example?", cs.Translate("Leave the mailing list %s?", "l.example"));
        Assert.Equal("_Odeslat žádost", cs.Translate("_Send Request"));
        Assert.Equal("Otevřít stránku pro odhlášení?", cs.Translate("Open the unsubscribe page?"));
        Assert.Equal("_Otevřít v prohlížeči", cs.Translate("_Open in Browser"));
        Assert.Equal("Odesílatele se nepodařilo ověřit", cs.Translate("The sender could not be verified"));
        Assert.Equal("Žádost o odhlášení čeká ve frontě k odeslání", cs.Translate("Unsubscribe request queued"));
        Assert.Equal("Odhlášení", cs.Translate("Unsubscribing"));
        Assert.Equal("Server odesílatele žádost odmítl.", cs.Translate("The sender's server refused the request."));
    }
}
