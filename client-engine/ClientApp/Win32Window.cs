using System.Runtime.InteropServices;

namespace ClientApp;

/// <summary>
/// Ventana Win32 mínima hecha a mano (RegisterClassEx + CreateWindowEx + message pump),
/// sin WinForms/WPF/ninguna librería de UI: es el "Win32 crudo" que pidió el diseño de
/// Fase D para poder controlar el loop de render nosotros mismos (PeekMessage no bloqueante,
/// para poder correr retro_run() + render en cada iteración).
/// </summary>
internal sealed class Win32Window : IDisposable
{
    private const int CS_HREDRAW = 0x0002;
    private const int CS_VREDRAW = 0x0001;
    private const int WS_OVERLAPPEDWINDOW = 0x00CF0000;
    private const int SW_SHOW = 5;
    private const uint WM_DESTROY = 0x0002;
    private const uint WM_CLOSE = 0x0010;
    private const uint WM_KEYDOWN = 0x0100;
    private const uint WM_KEYUP = 0x0101;
    private const uint WM_CHAR = 0x0102;
    private const uint PM_REMOVE = 0x0001;

    private delegate IntPtr WndProcDelegate(IntPtr hWnd, uint msg, IntPtr wParam, IntPtr lParam);

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct WNDCLASSEX
    {
        public uint cbSize;
        public uint style;
        public IntPtr lpfnWndProc;
        public int cbClsExtra;
        public int cbWndExtra;
        public IntPtr hInstance;
        public IntPtr hIcon;
        public IntPtr hCursor;
        public IntPtr hbrBackground;
        public string? lpszMenuName;
        public string lpszClassName;
        public IntPtr hIconSm;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct POINT { public int X; public int Y; }

    [StructLayout(LayoutKind.Sequential)]
    private struct MSG
    {
        public IntPtr hwnd;
        public uint message;
        public IntPtr wParam;
        public IntPtr lParam;
        public uint time;
        public POINT pt;
    }

    [DllImport("kernel32.dll")]
    private static extern IntPtr GetModuleHandleW(string? lpModuleName);

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern ushort RegisterClassExW(ref WNDCLASSEX lpwcx);

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr CreateWindowExW(
        int dwExStyle, string lpClassName, string lpWindowName, int dwStyle,
        int x, int y, int nWidth, int nHeight,
        IntPtr hWndParent, IntPtr hMenu, IntPtr hInstance, IntPtr lpParam);

    [DllImport("user32.dll")]
    private static extern bool ShowWindow(IntPtr hWnd, int nCmdShow);

    [DllImport("user32.dll")]
    private static extern bool UpdateWindow(IntPtr hWnd);

    [DllImport("user32.dll")]
    private static extern bool PeekMessageW(out MSG lpMsg, IntPtr hWnd, uint wMsgFilterMin, uint wMsgFilterMax, uint wRemoveMsg);

    [DllImport("user32.dll")]
    private static extern bool TranslateMessage(ref MSG lpMsg);

    [DllImport("user32.dll")]
    private static extern IntPtr DispatchMessageW(ref MSG lpMsg);

    [DllImport("user32.dll")]
    private static extern IntPtr DefWindowProcW(IntPtr hWnd, uint msg, IntPtr wParam, IntPtr lParam);

    [DllImport("user32.dll")]
    private static extern void PostQuitMessage(int nExitCode);

    public IntPtr Handle { get; private set; }
    public bool ShouldClose { get; private set; }

    // Mantener viva la referencia al delegate: si el GC la recolecta, Windows termina
    // llamando a memoria liberada apenas llegue el primer mensaje. Crash garantizado.
    private readonly WndProcDelegate _wndProc;

    public Win32Window(string title, int width, int height)
    {
        _wndProc = WndProc;
        IntPtr hInstance = GetModuleHandleW(null);
        const string className = "PokemonOnlineClientWindow";

        var wc = new WNDCLASSEX
        {
            cbSize = (uint)Marshal.SizeOf<WNDCLASSEX>(),
            style = CS_HREDRAW | CS_VREDRAW,
            lpfnWndProc = Marshal.GetFunctionPointerForDelegate(_wndProc),
            hInstance = hInstance,
            lpszClassName = className,
        };
        if (RegisterClassExW(ref wc) == 0)
            throw new InvalidOperationException($"RegisterClassEx falló: {Marshal.GetLastWin32Error()}");

        Handle = CreateWindowExW(
            0, className, title, WS_OVERLAPPEDWINDOW,
            100, 100, width, height,
            IntPtr.Zero, IntPtr.Zero, hInstance, IntPtr.Zero);
        if (Handle == IntPtr.Zero)
            throw new InvalidOperationException($"CreateWindowEx falló: {Marshal.GetLastWin32Error()}");

        ShowWindow(Handle, SW_SHOW);
        UpdateWindow(Handle);
    }

    private IntPtr WndProc(IntPtr hWnd, uint msg, IntPtr wParam, IntPtr lParam)
    {
        switch (msg)
        {
            case WM_CLOSE:
                ShouldClose = true;
                return IntPtr.Zero;
            case WM_DESTROY:
                PostQuitMessage(0);
                return IntPtr.Zero;
            case WM_KEYDOWN:
                _pressedKeys.Add((int)wParam);
                return IntPtr.Zero;
            case WM_KEYUP:
                _pressedKeys.Remove((int)wParam);
                return IntPtr.Zero;
            case WM_CHAR:
                // WM_CHAR ya viene traducido por TranslateMessage: layout de teclado, Shift,
                // muertas para acentos, todo resuelto por Windows. Es la única forma correcta
                // de capturar texto tipeado (para el chat) en vez de mapear VK a mano, que no
                // soportaría tildes/ñ escritas con teclado en español.
                _typedChars.Enqueue((char)wParam);
                return IntPtr.Zero;
        }
        return DefWindowProcW(hWnd, msg, wParam, lParam);
    }

    private readonly HashSet<int> _pressedKeys = [];
    private readonly Queue<char> _typedChars = new();

    /// <summary>Virtual-key code de Windows (ej. 0x26 = flecha arriba). Ver System.Windows.Forms.Keys o winuser.h.</summary>
    public bool IsKeyDown(int virtualKeyCode) => _pressedKeys.Contains(virtualKeyCode);

    /// <summary>
    /// Devuelve y vacía los caracteres tipeados desde la última llamada (incluye control
    /// characters como '\b' backspace y '\r' enter, tal cual los manda WM_CHAR).
    /// </summary>
    public List<char> ConsumeTypedChars()
    {
        var chars = new List<char>(_typedChars.Count);
        while (_typedChars.Count > 0) chars.Add(_typedChars.Dequeue());
        return chars;
    }

    /// <summary>Procesa los mensajes pendientes sin bloquear. Llamar una vez por iteración del loop principal.</summary>
    public void PumpMessages()
    {
        while (PeekMessageW(out var msg, IntPtr.Zero, 0, 0, PM_REMOVE))
        {
            TranslateMessage(ref msg);
            DispatchMessageW(ref msg);
        }
    }

    public void Dispose() { }
}
