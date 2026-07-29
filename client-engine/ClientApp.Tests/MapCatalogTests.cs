namespace ClientApp.Tests;

// MapCatalog es estático (mismo patrón que PokedexCatalog) — estos tests no corren en paralelo
// entre sí a propósito (todos en una sola clase, xUnit no paraleliza métodos de la misma clase
// por default) para que un Load() de un test no pise el estado que otro test está por leer.
public sealed class MapCatalogTests : IDisposable
{
    private readonly string _tempDir = Directory.CreateTempSubdirectory("clientapp-tests-").FullName;

    public void Dispose() => Directory.Delete(_tempDir, recursive: true);

    private string WriteMapsJson(string json)
    {
        File.WriteAllText(Path.Combine(_tempDir, "maps.json"), json);
        return _tempDir;
    }

    [Fact]
    public void Load_ThenIdFor_ResolvesKnownMap()
    {
        string dataDir = WriteMapsJson("""
            [
              { "group": 0, "num": 9, "id": "MAP_LITTLEROOT_TOWN", "name": "LittlerootTown" },
              { "group": 0, "num": 16, "id": "MAP_ROUTE101", "name": "Route101" }
            ]
            """);

        MapCatalog.Load(dataDir);

        Assert.True(MapCatalog.Loaded);
        Assert.Equal("MAP_ROUTE101", MapCatalog.IdFor(0, 16));
        Assert.Equal("MAP_LITTLEROOT_TOWN", MapCatalog.IdFor(0, 9));
    }

    [Fact]
    public void IdFor_ReturnsNullForUnknownGroupNum()
    {
        string dataDir = WriteMapsJson("""
            [ { "group": 0, "num": 9, "id": "MAP_LITTLEROOT_TOWN", "name": "LittlerootTown" } ]
            """);

        MapCatalog.Load(dataDir);

        Assert.Null(MapCatalog.IdFor(3, 250)); // combinación que no aparece en el fixture
    }

    [Fact]
    public void Load_DistinguishesGroupFromNum()
    {
        // Mismo num, distinto group, debe resolver a mapas distintos — probando específicamente
        // que la clave del diccionario es la TUPLA (group, num), no solo num.
        string dataDir = WriteMapsJson("""
            [
              { "group": 0, "num": 0, "id": "MAP_PETALBURG_CITY", "name": "PetalburgCity" },
              { "group": 1, "num": 0, "id": "MAP_PETALBURG_CITY_GYM", "name": "PetalburgCityGym" }
            ]
            """);

        MapCatalog.Load(dataDir);

        Assert.Equal("MAP_PETALBURG_CITY", MapCatalog.IdFor(0, 0));
        Assert.Equal("MAP_PETALBURG_CITY_GYM", MapCatalog.IdFor(1, 0));
    }

    [Fact]
    public void Load_EmptyArray_LeavesNothingResolvable()
    {
        string dataDir = WriteMapsJson("[]");

        MapCatalog.Load(dataDir);

        Assert.True(MapCatalog.Loaded);
        Assert.Null(MapCatalog.IdFor(0, 9));
    }

    [Fact]
    public void Load_MissingFile_Throws()
    {
        string emptyDir = Directory.CreateTempSubdirectory("clientapp-tests-missing-").FullName;
        try
        {
            Assert.ThrowsAny<Exception>(() => MapCatalog.Load(emptyDir));
        }
        finally
        {
            Directory.Delete(emptyDir, recursive: true);
        }
    }
}
