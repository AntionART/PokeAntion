using System.Runtime.InteropServices;

namespace LibretroCore;

public enum PixelFormat
{
    Unknown = -1,
    ZeroRGB1555 = 0, // 16 bits: 0RRRRRGGGGGBBBBB
    XRGB8888 = 1,    // 32 bits
    RGB565 = 2,      // 16 bits: RRRRRGGGGGGBBBBB
}

public readonly record struct MemoryRegion(string Name, IntPtr Ptr, nuint Start, nuint Len, ulong Flags);

/// <summary>RETRO_DEVICE_ID_JOYPAD_* de libretro.h, para usar con MgbaCore.InputState.</summary>
public enum JoypadButton : uint
{
    B = 0, Y = 1, Select = 2, Start = 3,
    Up = 4, Down = 5, Left = 6, Right = 7,
    A = 8, X = 9, L = 10, R = 11,
}

public static class RetroDevice
{
    public const uint Joypad = 1;
}

/// <summary>
/// Envoltorio de alto nivel sobre <see cref="Libretro"/>: oculta los callbacks y el manejo
/// de punteros, y expone lo que el resto del cliente necesita — cargar una ROM, correr
/// frames, el último framebuffer, y las regiones de memoria anunciadas por el core (para el
/// adaptador de memoria del juego, ver Fase D.3/D.4).
/// </summary>
public sealed class MgbaCore : IDisposable
{
    private readonly List<MemoryRegion> _regions = [];

    // Mantener referencias vivas a los delegates: si el GC los recolecta mientras el core
    // nativo todavía tiene el puntero de función, es un crash garantizado.
    private readonly Libretro.EnvironmentCallback _envCb;
    private readonly Libretro.VideoRefreshCallback _videoCb;
    private readonly Libretro.AudioSampleCallback _audioCb;
    private readonly Libretro.AudioSampleBatchCallback _audioBatchCb;
    private readonly Libretro.InputPollCallback _inputPollCb;
    private readonly Libretro.InputStateCallback _inputStateCb;

    private IntPtr _pathPtr;
    private GCHandle _romHandle;
    private bool _loaded;
    private bool _disposed;

    public IReadOnlyList<MemoryRegion> Regions => _regions;
    public PixelFormat Format { get; private set; } = PixelFormat.Unknown;

    /// <summary>Se dispara cada vez que el core produce un frame de video nuevo.</summary>
    public event Action<IntPtr, uint, uint, nuint>? FrameReady;

    /// <summary>
    /// Query de estado de input: (port, device, index, id) -> distinto de 0 si está presionado.
    /// Para RETRO_DEVICE_JOYPAD, "id" es uno de los RETRO_DEVICE_ID_JOYPAD_* (ver JoypadButton).
    /// Si es null, el core recibe "nada presionado" siempre (comportamiento por defecto).
    /// </summary>
    public Func<uint, uint, uint, uint, short>? InputState { get; set; }

    public MgbaCore()
    {
        _envCb = OnEnvironment;
        _videoCb = (data, w, h, pitch) => FrameReady?.Invoke(data, w, h, pitch);
        _audioCb = (_, _) => { };
        _audioBatchCb = (_, frames) => frames;
        _inputPollCb = () => { };
        _inputStateCb = (port, device, index, id) => InputState?.Invoke(port, device, index, id) ?? 0;

        Libretro.retro_set_environment(_envCb);
        Libretro.retro_set_video_refresh(_videoCb);
        Libretro.retro_set_audio_sample(_audioCb);
        Libretro.retro_set_audio_sample_batch(_audioBatchCb);
        Libretro.retro_set_input_poll(_inputPollCb);
        Libretro.retro_set_input_state(_inputStateCb);
        Libretro.retro_init();
    }

    private bool OnEnvironment(uint cmd, IntPtr data)
    {
        if (cmd == Libretro.RETRO_ENVIRONMENT_SET_PIXEL_FORMAT)
        {
            Format = (PixelFormat)Marshal.ReadInt32(data);
            return true;
        }

        if (cmd == Libretro.RETRO_ENVIRONMENT_SET_MEMORY_MAPS)
        {
            var map = Marshal.PtrToStructure<Libretro.RetroMemoryMap>(data);
            _regions.Clear();
            int descSize = Marshal.SizeOf<Libretro.RetroMemoryDescriptor>();
            for (int i = 0; i < map.num_descriptors; i++)
            {
                IntPtr descPtr = IntPtr.Add(map.descriptors, i * descSize);
                var desc = Marshal.PtrToStructure<Libretro.RetroMemoryDescriptor>(descPtr);
                if (desc.ptr == IntPtr.Zero) continue;
                string name = desc.addrspace == IntPtr.Zero ? "" : (Marshal.PtrToStringAnsi(desc.addrspace) ?? "");
                _regions.Add(new MemoryRegion(name, desc.ptr, desc.start, desc.len, desc.flags));
            }
            return true;
        }

        return false;
    }

    /// <summary>Carga una ROM desde disco. Devuelve false si el core la rechazó.</summary>
    public bool LoadGame(string romPath)
    {
        Libretro.retro_get_system_info(out var sysInfo);
        _pathPtr = Marshal.StringToHGlobalAnsi(romPath);
        var gameInfo = new Libretro.RetroGameInfo { path = _pathPtr, meta = IntPtr.Zero };

        if (sysInfo.need_fullpath)
        {
            gameInfo.data = IntPtr.Zero;
            gameInfo.size = 0;
        }
        else
        {
            byte[] romBytes = File.ReadAllBytes(romPath);
            _romHandle = GCHandle.Alloc(romBytes, GCHandleType.Pinned);
            gameInfo.data = _romHandle.AddrOfPinnedObject();
            gameInfo.size = (nuint)romBytes.Length;
        }

        _loaded = Libretro.retro_load_game(ref gameInfo);
        return _loaded;
    }

    public void RunFrame() => Libretro.retro_run();

    /// <summary>Región de EWRAM (256KB en 0x02000000): donde viven posición del jugador, equipo, etc.</summary>
    public MemoryRegion? FindEwram()
    {
        foreach (var r in _regions)
            if (r.Len == 0x40000 || r.Start == 0x02000000)
                return r;
        return null;
    }

    /// <summary>
    /// Snapshot completo del estado del core (save state de libretro, no del juego).
    /// Permite retomar exploración de memoria entre ejecuciones del proceso sin tener que
    /// rebootear la ROM y re-navegar menús cada vez.
    /// </summary>
    public byte[] SaveState()
    {
        nuint size = Libretro.retro_serialize_size();
        byte[] buffer = new byte[size];
        var handle = GCHandle.Alloc(buffer, GCHandleType.Pinned);
        try
        {
            if (!Libretro.retro_serialize(handle.AddrOfPinnedObject(), size))
                throw new InvalidOperationException("retro_serialize devolvió false");
            return buffer;
        }
        finally
        {
            handle.Free();
        }
    }

    public void LoadState(byte[] data)
    {
        var handle = GCHandle.Alloc(data, GCHandleType.Pinned);
        try
        {
            if (!Libretro.retro_unserialize(handle.AddrOfPinnedObject(), (nuint)data.Length))
                throw new InvalidOperationException("retro_unserialize devolvió false");
        }
        finally
        {
            handle.Free();
        }
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        if (_loaded) Libretro.retro_unload_game();
        Libretro.retro_deinit();
        if (_pathPtr != IntPtr.Zero) Marshal.FreeHGlobal(_pathPtr);
        if (_romHandle.IsAllocated) _romHandle.Free();
    }
}
