namespace LibretroCore;

/// <summary>
/// Lector genérico sobre TODAS las regiones de memoria que el core anuncia (EWRAM, IWRAM,
/// VRAM, paleta, OAM, I/O...), no solo EWRAM como MemoryAdapter. Pensado para leer estructuras
/// de hardware del GBA (OAM, tiles, paleta de sprites) que son estándar del hardware — iguales
/// en CUALQUIER ROM/revisión de Pokémon, a diferencia de las direcciones de EWRAM específicas
/// del save block, que sí varían por ROM y necesitan el memory-map JSON por juego.
/// </summary>
public sealed class GbaMemoryBus
{
    private readonly MgbaCore _core;

    public GbaMemoryBus(MgbaCore core) => _core = core;

    private MemoryRegion? FindRegion(uint address)
    {
        foreach (var r in _core.Regions)
            if (address >= r.Start && address < r.Start + r.Len)
                return r;
        return null;
    }

    public unsafe byte ReadU8(uint address)
    {
        var region = FindRegion(address) ?? throw new ArgumentOutOfRangeException(nameof(address), $"0x{address:X8} no está en ninguna región de memoria anunciada por el core.");
        return ((byte*)region.Ptr)[address - region.Start];
    }

    public unsafe ushort ReadU16(uint address)
    {
        var region = FindRegion(address) ?? throw new ArgumentOutOfRangeException(nameof(address), $"0x{address:X8} no está en ninguna región de memoria anunciada por el core.");
        byte* p = (byte*)region.Ptr + (address - region.Start);
        return (ushort)(p[0] | (p[1] << 8));
    }

    public unsafe uint ReadU32(uint address)
    {
        var region = FindRegion(address) ?? throw new ArgumentOutOfRangeException(nameof(address), $"0x{address:X8} no está en ninguna región de memoria anunciada por el core.");
        byte* p = (byte*)region.Ptr + (address - region.Start);
        return (uint)(p[0] | (p[1] << 8) | (p[2] << 16) | (p[3] << 24));
    }

    /// <summary>Copia `len` bytes crudos a partir de `address`. Falla si el rango no cae
    /// completo dentro de UNA sola región anunciada (no maneja lecturas que cruzan regiones,
    /// no hace falta para tiles/paleta/OAM del GBA, que viven cada uno en su propia región).</summary>
    public unsafe byte[] ReadBytes(uint address, int len)
    {
        var region = FindRegion(address) ?? throw new ArgumentOutOfRangeException(nameof(address), $"0x{address:X8} no está en ninguna región de memoria anunciada por el core.");
        uint offset = address - (uint)region.Start;
        if (offset + (uint)len > (uint)region.Len)
            throw new ArgumentOutOfRangeException(nameof(len), $"0x{address:X8}+{len} se sale de la región (start=0x{region.Start:X8} len=0x{region.Len:X}).");
        var span = new ReadOnlySpan<byte>((byte*)region.Ptr + offset, len);
        return span.ToArray();
    }

    public unsafe void WriteU8(uint address, byte value)
    {
        var region = FindRegion(address) ?? throw new ArgumentOutOfRangeException(nameof(address), $"0x{address:X8} no está en ninguna región de memoria anunciada por el core.");
        ((byte*)region.Ptr)[address - region.Start] = value;
    }

    public unsafe void WriteU16(uint address, ushort value)
    {
        var region = FindRegion(address) ?? throw new ArgumentOutOfRangeException(nameof(address), $"0x{address:X8} no está en ninguna región de memoria anunciada por el core.");
        byte* p = (byte*)region.Ptr + (address - region.Start);
        p[0] = (byte)(value & 0xFF);
        p[1] = (byte)(value >> 8);
    }

    public unsafe void WriteU32(uint address, uint value)
    {
        var region = FindRegion(address) ?? throw new ArgumentOutOfRangeException(nameof(address), $"0x{address:X8} no está en ninguna región de memoria anunciada por el core.");
        byte* p = (byte*)region.Ptr + (address - region.Start);
        p[0] = (byte)(value & 0xFF);
        p[1] = (byte)((value >> 8) & 0xFF);
        p[2] = (byte)((value >> 16) & 0xFF);
        p[3] = (byte)(value >> 24);
    }

    /// <summary>Escribe `bytes` a partir de `address`. Igual que <see cref="ReadBytes"/>, falla
    /// si el rango no cae completo dentro de UNA sola región anunciada.</summary>
    public unsafe void WriteBytes(uint address, ReadOnlySpan<byte> bytes)
    {
        var region = FindRegion(address) ?? throw new ArgumentOutOfRangeException(nameof(address), $"0x{address:X8} no está en ninguna región de memoria anunciada por el core.");
        uint offset = address - (uint)region.Start;
        if (offset + (uint)bytes.Length > (uint)region.Len)
            throw new ArgumentOutOfRangeException(nameof(bytes), $"0x{address:X8}+{bytes.Length} se sale de la región (start=0x{region.Start:X8} len=0x{region.Len:X}).");
        var dest = new Span<byte>((byte*)region.Ptr + offset, bytes.Length);
        bytes.CopyTo(dest);
    }

    public bool IsAvailable => _core.Regions.Count > 0;
}
