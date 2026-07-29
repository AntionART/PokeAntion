using System.IO;
using System.Text.Json;

namespace Launcher.Services;

/// <summary>Una ROM soportada, leída directamente de memory-maps/*.json — el Launcher no
/// referencia RomLoader/ClientApp (el motor no debe ser una dependencia del launcher), así que
/// parsea el mismo esquema por su cuenta en vez de compartir el tipo.</summary>
public sealed record RomCatalogEntry(string RomId, string DisplayName, string MemoryMapPath, string ExpectedRomPath, string? ExpectedSha1);

public static class RomCatalogService
{
    /// <summary>Mismo criterio que ClientApp.Program.FindRepoRoot: subir directorios buscando
    /// memory-maps/ + data/pokemon/, en vez de asumir una profundidad fija — sobrevive a
    /// dotnet publish agregando carpetas intermedias (bin/Debug/net10.0-windows/, win-x64/, etc.)</summary>
    public static string FindRepoRoot(string startDir)
    {
        for (var dir = new DirectoryInfo(startDir); dir != null; dir = dir.Parent)
        {
            if (Directory.Exists(Path.Combine(dir.FullName, "memory-maps")) && Directory.Exists(Path.Combine(dir.FullName, "data", "pokemon")))
                return dir.FullName;
        }
        return Path.GetFullPath(Path.Combine(startDir, "..", "..", "..", "..", ".."));
    }

    public static List<RomCatalogEntry> Discover(string memoryMapsDir, string repoRoot)
    {
        var result = new List<RomCatalogEntry>();
        if (!Directory.Exists(memoryMapsDir)) return result;

        foreach (string file in Directory.GetFiles(memoryMapsDir, "*.json"))
        {
            try
            {
                using var doc = JsonDocument.Parse(File.ReadAllText(file));
                var root = doc.RootElement;

                string romId = root.TryGetProperty("rom_id", out var idEl) ? idEl.GetString() ?? "" : "";
                string romPathRel = root.TryGetProperty("rom_path", out var rpEl) ? rpEl.GetString() ?? "" : "";
                if (romId.Length == 0 || romPathRel.Length == 0) continue;

                string displayName = root.TryGetProperty("display_name", out var dnEl) ? dnEl.GetString() ?? romId : romId;
                string? sha1 = root.TryGetProperty("rom_checksum_sha1", out var shaEl) ? shaEl.GetString() : null;
                string expectedRomPath = Path.GetFullPath(Path.Combine(repoRoot, romPathRel));

                result.Add(new RomCatalogEntry(romId, displayName, file, expectedRomPath, sha1));
            }
            catch
            {
                // memory-map inválido/no parseable: se ignora, no bloquea el resto del catálogo
                // (mismo criterio que RomLoader.RomCatalog.Discover en el motor).
            }
        }
        return result;
    }
}
