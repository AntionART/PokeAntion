using System.Security.Cryptography;
using System.Text;
using Launcher.Services;

namespace Launcher.Tests;

public sealed class RomLocatorServiceTests : IDisposable
{
    private readonly string _tempDir = Directory.CreateTempSubdirectory("launcher-tests-").FullName;

    public void Dispose() => Directory.Delete(_tempDir, recursive: true);

    private string WriteFile(string name, string content)
    {
        string path = Path.Combine(_tempDir, name);
        File.WriteAllText(path, content);
        return path;
    }

    private static string Sha1Of(string content) =>
        Convert.ToHexString(SHA1.HashData(Encoding.UTF8.GetBytes(content)));

    [Fact]
    public void ComputeSha1_MatchesKnownHash()
    {
        string path = WriteFile("rom.bin", "hola mundo");

        string hash = RomLocatorService.ComputeSha1(path);

        Assert.Equal(Sha1Of("hola mundo"), hash);
    }

    [Fact]
    public void Resolve_ValidWhenHashMatchesUserConfiguredPath()
    {
        string path = WriteFile("rom.gba", "contenido real de la rom");
        var entry = new RomCatalogEntry("rom_x", "Rom X", "mm.json", Path.Combine(_tempDir, "no-existe.gba"), Sha1Of("contenido real de la rom"));

        var status = RomLocatorService.Resolve(entry, path);

        Assert.True(status.IsValid);
        Assert.Equal(path, status.ResolvedPath);
        Assert.Null(status.ProblemMessage);
    }

    [Fact]
    public void Resolve_InvalidWhenHashDoesNotMatch()
    {
        string path = WriteFile("rom.gba", "contenido modificado");
        var entry = new RomCatalogEntry("rom_x", "Rom X", "mm.json", path, Sha1Of("contenido original"));

        var status = RomLocatorService.Resolve(entry, path);

        Assert.False(status.IsValid);
        Assert.Equal(path, status.ResolvedPath); // se guarda la ruta aunque el hash no matchee, para poder mostrarla en el error
        Assert.Contains("hash", status.ProblemMessage);
    }

    [Fact]
    public void Resolve_FallsBackToExpectedRomPathWhenNoUserPathConfigured()
    {
        string path = WriteFile("Pokemon.gba", "rom por defecto del repo");
        var entry = new RomCatalogEntry("rom_x", "Rom X", "mm.json", path, Sha1Of("rom por defecto del repo"));

        var status = RomLocatorService.Resolve(entry, userConfiguredPath: null);

        Assert.True(status.IsValid);
        Assert.Equal(path, status.ResolvedPath);
    }

    [Fact]
    public void Resolve_InvalidWhenFileDoesNotExistAnywhere()
    {
        var entry = new RomCatalogEntry("rom_x", "Rom X", "mm.json", Path.Combine(_tempDir, "no-existe.gba"), "AAAA");

        var status = RomLocatorService.Resolve(entry, userConfiguredPath: null);

        Assert.False(status.IsValid);
        Assert.Null(status.ResolvedPath);
        Assert.NotNull(status.ProblemMessage);
    }

    [Fact]
    public void Resolve_InvalidWhenUserConfiguredPathWasDeleted()
    {
        var entry = new RomCatalogEntry("rom_x", "Rom X", "mm.json", Path.Combine(_tempDir, "no-existe-tampoco.gba"), "AAAA");

        var status = RomLocatorService.Resolve(entry, Path.Combine(_tempDir, "ya-no-existe.gba"));

        Assert.False(status.IsValid);
    }

    [Fact]
    public void Resolve_AcceptsFileWithoutValidatingWhenNoHashIsConfigured()
    {
        // Sin rom_checksum_sha1 en el memory-map no hay forma de validar contenido — se acepta
        // cualquier archivo en la ruta esperada (comodidad de desarrollo, nunca en detrimento de
        // seguridad ya que jamás se descarga/distribuye nada).
        string path = WriteFile("cualquier-cosa.gba", "no importa el contenido");
        var entry = new RomCatalogEntry("rom_x", "Rom X", "mm.json", path, ExpectedSha1: null);

        var status = RomLocatorService.Resolve(entry, userConfiguredPath: null);

        Assert.True(status.IsValid);
    }
}
