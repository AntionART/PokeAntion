using System.IO;
using System.Security.Cryptography;

namespace Launcher.Services;

public sealed record RomStatus(RomCatalogEntry Catalog, string? ResolvedPath, bool IsValid, string? ProblemMessage);

public static class RomLocatorService
{
    public static string ComputeSha1(string path)
    {
        using var stream = File.OpenRead(path);
        return Convert.ToHexString(SHA1.HashData(stream));
    }

    /// <summary>Resuelve una entrada del catálogo: preferí la ruta que el usuario ya eligió antes
    /// (persistida en launcher-config.json), si no probá la ruta por default del repo (comodidad
    /// para desarrollo local). Sin <see cref="RomCatalogEntry.ExpectedSha1"/> no hay forma de
    /// validar el contenido — nunca se descarga ni distribuye la ROM, solo se verifica la que el
    /// usuario ya tiene en su equipo.</summary>
    public static RomStatus Resolve(RomCatalogEntry entry, string? userConfiguredPath)
    {
        string? candidate = userConfiguredPath ?? (File.Exists(entry.ExpectedRomPath) ? entry.ExpectedRomPath : null);
        if (candidate == null || !File.Exists(candidate))
            return new RomStatus(entry, null, false, "No se encontró el archivo de la ROM en la ubicación configurada.");

        if (entry.ExpectedSha1 is { Length: > 0 } expected)
        {
            string actual = ComputeSha1(candidate);
            if (!string.Equals(actual, expected, StringComparison.OrdinalIgnoreCase))
                return new RomStatus(entry, candidate, false, "El archivo no coincide con esta ROM (el hash SHA1 no coincide) — puede estar dañado o modificado.");
        }

        return new RomStatus(entry, candidate, true, null);
    }
}
