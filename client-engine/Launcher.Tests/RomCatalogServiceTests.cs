using Launcher.Services;

namespace Launcher.Tests;

public sealed class RomCatalogServiceTests : IDisposable
{
    private readonly string _tempDir = Directory.CreateTempSubdirectory("launcher-tests-").FullName;

    public void Dispose() => Directory.Delete(_tempDir, recursive: true);

    [Fact]
    public void Discover_ParsesValidEntry()
    {
        string mapsDir = Path.Combine(_tempDir, "memory-maps");
        Directory.CreateDirectory(mapsDir);
        File.WriteAllText(Path.Combine(mapsDir, "emerald_es.json"), """
            {
              "rom_id": "emerald_es",
              "display_name": "Pokémon Edición Esmeralda",
              "rom_path": "Pokemon - Edicion Esmeralda (Spain).gba",
              "rom_checksum_sha1": "ABCDEF0123456789"
            }
            """);

        var catalog = RomCatalogService.Discover(mapsDir, _tempDir);

        var entry = Assert.Single(catalog);
        Assert.Equal("emerald_es", entry.RomId);
        Assert.Equal("Pokémon Edición Esmeralda", entry.DisplayName);
        Assert.Equal("ABCDEF0123456789", entry.ExpectedSha1);
        Assert.Equal(Path.Combine(_tempDir, "Pokemon - Edicion Esmeralda (Spain).gba"), entry.ExpectedRomPath);
    }

    [Fact]
    public void Discover_SkipsEntryMissingRomId()
    {
        string mapsDir = Path.Combine(_tempDir, "memory-maps");
        Directory.CreateDirectory(mapsDir);
        File.WriteAllText(Path.Combine(mapsDir, "incomplete.json"), """{ "rom_path": "x.gba" }""");

        var catalog = RomCatalogService.Discover(mapsDir, _tempDir);

        Assert.Empty(catalog);
    }

    [Fact]
    public void Discover_SkipsEntryMissingRomPath()
    {
        string mapsDir = Path.Combine(_tempDir, "memory-maps");
        Directory.CreateDirectory(mapsDir);
        File.WriteAllText(Path.Combine(mapsDir, "incomplete.json"), """{ "rom_id": "x" }""");

        var catalog = RomCatalogService.Discover(mapsDir, _tempDir);

        Assert.Empty(catalog);
    }

    [Fact]
    public void Discover_IgnoresUnparseableJsonWithoutThrowing()
    {
        string mapsDir = Path.Combine(_tempDir, "memory-maps");
        Directory.CreateDirectory(mapsDir);
        File.WriteAllText(Path.Combine(mapsDir, "broken.json"), "{ this is not json");
        File.WriteAllText(Path.Combine(mapsDir, "good.json"), """
            { "rom_id": "good_rom", "rom_path": "good.gba" }
            """);

        var catalog = RomCatalogService.Discover(mapsDir, _tempDir);

        var entry = Assert.Single(catalog);
        Assert.Equal("good_rom", entry.RomId);
    }

    [Fact]
    public void Discover_DisplayNameFallsBackToRomId()
    {
        string mapsDir = Path.Combine(_tempDir, "memory-maps");
        Directory.CreateDirectory(mapsDir);
        File.WriteAllText(Path.Combine(mapsDir, "x.json"), """{ "rom_id": "x", "rom_path": "x.gba" }""");

        var entry = Assert.Single(RomCatalogService.Discover(mapsDir, _tempDir));

        Assert.Equal("x", entry.DisplayName);
        Assert.Null(entry.ExpectedSha1);
    }

    [Fact]
    public void Discover_ReturnsEmptyWhenDirectoryDoesNotExist()
    {
        var catalog = RomCatalogService.Discover(Path.Combine(_tempDir, "no-existe"), _tempDir);

        Assert.Empty(catalog);
    }

    [Fact]
    public void FindRepoRoot_WalksUpToDirectoryContainingMarkers()
    {
        Directory.CreateDirectory(Path.Combine(_tempDir, "memory-maps"));
        Directory.CreateDirectory(Path.Combine(_tempDir, "data", "pokemon"));
        string deepStart = Path.Combine(_tempDir, "client-engine", "Launcher", "bin", "Debug", "net10.0-windows");
        Directory.CreateDirectory(deepStart);

        string found = RomCatalogService.FindRepoRoot(deepStart);

        Assert.Equal(Path.GetFullPath(_tempDir), Path.GetFullPath(found));
    }

    [Fact]
    public void FindRepoRoot_FallsBackToFixedHopCountWhenMarkersMissing()
    {
        string deepStart = Path.Combine(_tempDir, "a", "b", "c", "d", "e");
        Directory.CreateDirectory(deepStart);

        string found = RomCatalogService.FindRepoRoot(deepStart);

        Assert.Equal(Path.GetFullPath(_tempDir), Path.GetFullPath(found));
    }
}
