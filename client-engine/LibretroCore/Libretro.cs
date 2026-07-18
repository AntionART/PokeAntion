using System.Runtime.InteropServices;

namespace LibretroCore;

// Subconjunto MÍNIMO de la ABI de libretro (libretro.h) necesario para: cargar un core,
// darle una ROM, correr frames, leer su RAM, y recibir el framebuffer de video. No
// implementa audio/input/save-states real: esos callbacks existen porque el core los
// requiere (puede crashear si son null), pero quedan en no-op salvo que el llamador
// haga algo con ellos. Referencia: https://github.com/libretro/libretro-common
internal static class Libretro
{
    public const int RETRO_ENVIRONMENT_SET_PIXEL_FORMAT = 10;
    public const int RETRO_ENVIRONMENT_EXPERIMENTAL = 0x10000;
    // El core llama a esto (no lo pedimos nosotros) para anunciar su mapa de memoria con
    // nombre. Es la vía confiable para ubicar EWRAM: el id legado RETRO_MEMORY_SYSTEM_RAM
    // en mgba resultó ser poco confiable (devuelve el puntero de EWRAM pero el tamaño de
    // IWRAM) — confirmado empíricamente en la PoC de D.1.
    public const uint RETRO_ENVIRONMENT_SET_MEMORY_MAPS = 36 | RETRO_ENVIRONMENT_EXPERIMENTAL;

    public const int RETRO_MEMORY_SAVE_RAM = 0;
    public const int RETRO_MEMORY_RTC = 1;
    public const int RETRO_MEMORY_SYSTEM_RAM = 2; // en mgba: NO confiable, usar SET_MEMORY_MAPS
    public const int RETRO_MEMORY_VIDEO_RAM = 3;

    public const ulong RETRO_MEMDESC_SYSTEM_RAM = 1 << 2;

    [UnmanagedFunctionPointer(CallingConvention.Cdecl)]
    public delegate bool EnvironmentCallback(uint cmd, IntPtr data);

    [UnmanagedFunctionPointer(CallingConvention.Cdecl)]
    public delegate void VideoRefreshCallback(IntPtr data, uint width, uint height, nuint pitch);

    [UnmanagedFunctionPointer(CallingConvention.Cdecl)]
    public delegate void AudioSampleCallback(short left, short right);

    [UnmanagedFunctionPointer(CallingConvention.Cdecl)]
    public delegate nuint AudioSampleBatchCallback(IntPtr data, nuint frames);

    [UnmanagedFunctionPointer(CallingConvention.Cdecl)]
    public delegate void InputPollCallback();

    [UnmanagedFunctionPointer(CallingConvention.Cdecl)]
    public delegate short InputStateCallback(uint port, uint device, uint index, uint id);

    [StructLayout(LayoutKind.Sequential)]
    public struct RetroSystemInfo
    {
        public IntPtr library_name;
        public IntPtr library_version;
        public IntPtr valid_extensions;
        [MarshalAs(UnmanagedType.I1)] public bool need_fullpath;
        [MarshalAs(UnmanagedType.I1)] public bool block_extract;
    }

    [StructLayout(LayoutKind.Sequential)]
    public struct RetroGameInfo
    {
        public IntPtr path;
        public IntPtr data;
        public nuint size;
        public IntPtr meta;
    }

    // Layout exacto de libretro.h (x64: size_t/puntero = 8 bytes, ya alineado a 8).
    [StructLayout(LayoutKind.Sequential)]
    public struct RetroMemoryDescriptor
    {
        public ulong flags;
        public IntPtr ptr;
        public nuint offset;
        public nuint start;
        public nuint select;
        public nuint disconnect;
        public nuint len;
        public IntPtr addrspace;
    }

    [StructLayout(LayoutKind.Sequential)]
    public struct RetroMemoryMap
    {
        public IntPtr descriptors; // const struct retro_memory_descriptor*
        public uint num_descriptors;
    }

    private const string Lib = "mgba_libretro.dll";

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    public static extern void retro_set_environment(EnvironmentCallback cb);

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    public static extern void retro_set_video_refresh(VideoRefreshCallback cb);

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    public static extern void retro_set_audio_sample(AudioSampleCallback cb);

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    public static extern void retro_set_audio_sample_batch(AudioSampleBatchCallback cb);

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    public static extern void retro_set_input_poll(InputPollCallback cb);

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    public static extern void retro_set_input_state(InputStateCallback cb);

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    public static extern void retro_init();

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    public static extern void retro_deinit();

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    public static extern uint retro_api_version();

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    public static extern void retro_get_system_info(out RetroSystemInfo info);

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    public static extern void retro_run();

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    [return: MarshalAs(UnmanagedType.I1)]
    public static extern bool retro_load_game(ref RetroGameInfo game);

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    public static extern void retro_unload_game();

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    public static extern nuint retro_serialize_size();

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    [return: MarshalAs(UnmanagedType.I1)]
    public static extern bool retro_serialize(IntPtr data, nuint size);

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    [return: MarshalAs(UnmanagedType.I1)]
    public static extern bool retro_unserialize(IntPtr data, nuint size);

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    public static extern IntPtr retro_get_memory_data(uint id);

    [DllImport(Lib, CallingConvention = CallingConvention.Cdecl)]
    public static extern nuint retro_get_memory_size(uint id);
}
