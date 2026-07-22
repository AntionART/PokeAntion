using System.Runtime.InteropServices;
using Vortice.D3DCompiler;
using Vortice.Direct3D;
using Vortice.Direct3D11;
using Vortice.DXGI;
using Vortice.Mathematics;
using static Vortice.Direct3D11.D3D11;
using static Vortice.DXGI.DXGI;

namespace ClientApp;

/// <summary>Vértice de texto: posición en NDC + UV del atlas + color con alfa.</summary>
[StructLayout(LayoutKind.Sequential)]
internal struct TextVertex
{
    public float X, Y;
    public float U, V;
    public float R, G, B, A;
}

/// <summary>
/// Renderer Direct3D 11 mínimo: crea device/swapchain para una ventana Win32, sube el
/// framebuffer del emulador (RGB565) a una textura dinámica convirtiéndolo a BGRA8 en CPU
/// (evita depender de que el hardware soporte texturas B5G6R5 nativas, que en D3D11 es un
/// feature opcional), y lo dibuja de fondo con un triángulo fullscreen (sin vertex buffer,
/// truco clásico vía SV_VertexID) — encima de eso, más adelante (D.2b/F), se dibuja overlay
/// de UI en la misma pasada.
/// </summary>
internal sealed class Renderer : IDisposable
{
    private const string ShaderSource = """
        Texture2D tex : register(t0);
        SamplerState samp : register(s0);

        struct VSOutput { float4 pos : SV_POSITION; float2 uv : TEXCOORD0; };

        VSOutput VSMain(uint id : SV_VertexID)
        {
            VSOutput o;
            float2 uv = float2((id << 1) & 2, id & 2);
            o.uv = uv;
            o.pos = float4(uv.x * 2.0 - 1.0, 1.0 - uv.y * 2.0, 0, 1);
            return o;
        }

        float4 PSMain(VSOutput input) : SV_TARGET
        {
            return tex.Sample(samp, input.uv);
        }
        """;

    // Fase F: texto real de UI (chat, nombres) dibujado con un atlas de glifos rasterizado
    // por GDI+ (FontAtlas) y sampleado como una textura más, con blending alfa normal. Fase G:
    // este mismo shader ("cuadrilátero con textura + tinte de vértice") se reutiliza tal cual
    // Nota: el pixel shader de texto SOLO usa el canal alfa de la textura muestreada — el
    // color final sale del vértice (así una misma letra en el atlas sirve para texto blanco,
    // rojo de error, verde de "conectado", etc.). Eso es exactamente lo QUE NO se quiere para
    // sprites, donde el color de cada píxel lo define la textura (el sprite ya tiene sus
    // propios colores decodificados de la paleta del GBA) — reutilizar este shader tal cual
    // para sprites pintaba el personaje como una silueta blanca sólida (bug real, encontrado
    // en vivo: el sprite remoto aparecía como un blob blanco con la forma correcta pero sin
    // color). El vertex shader SÍ es idéntico (mismo formato de vértice, misma transformación),
    // así que se comparte _textInputLayout/_textVertexShader — solo el pixel shader difiere,
    // ver SpritePixelShaderSource.
    private const string TextShaderSource = """
        Texture2D fontTex : register(t0);
        SamplerState fontSamp : register(s0);

        struct VSInput { float2 pos : POSITION; float2 uv : TEXCOORD0; float4 color : COLOR; };
        struct VSOutput { float4 pos : SV_POSITION; float2 uv : TEXCOORD0; float4 color : COLOR; };

        VSOutput VSMain(VSInput input)
        {
            VSOutput o;
            o.pos = float4(input.pos, 0, 1);
            o.uv = input.uv;
            o.color = input.color;
            return o;
        }

        float4 PSMain(VSOutput input) : SV_TARGET
        {
            float4 glyph = fontTex.Sample(fontSamp, input.uv);
            return float4(input.color.rgb, input.color.a * glyph.a);
        }
        """;

    // Mismo VSInput/VSOutput que TextShaderSource (comparten _textVertexShader/_textInputLayout).
    // El color final es la textura del sprite (ya decodificada de OAM/VRAM/paleta) MULTIPLICADA
    // por el color del vértice — con blanco (1,1,1,1), el default, el sprite sale exactamente
    // igual que antes (multiplicar por 1 no cambia nada); con otro tinte por vértice, cada
    // jugador remoto puede pintarse su propio color elegido (ver SpriteColors.cs) sin necesitar
    // una textura distinta por jugador — un solo shared _spriteView, tinte por instancia.
    private const string SpritePixelShaderSource = """
        Texture2D spriteTex : register(t0);
        SamplerState spriteSamp : register(s0);

        struct VSOutput { float4 pos : SV_POSITION; float2 uv : TEXCOORD0; float4 color : COLOR; };

        float4 PSMain(VSOutput input) : SV_TARGET
        {
            float4 texel = spriteTex.Sample(spriteSamp, input.uv);
            return float4(texel.rgb * input.color.rgb, texel.a * input.color.a);
        }
        """;

    private const int MaxSprites = 64;
    private const int VerticesPerSprite = 6; // dos triángulos por cuadrado
    private const int MaxTextChars = 4096;
    private const int VerticesPerChar = 6;

    private readonly IDXGIFactory2 _factory;
    private readonly ID3D11Device _device;
    private readonly ID3D11DeviceContext _context;
    private readonly IDXGISwapChain1 _swapChain;
    private readonly ID3D11RenderTargetView _renderTargetView;
    private readonly ID3D11VertexShader _vertexShader;
    private readonly ID3D11PixelShader _pixelShader;
    private readonly ID3D11SamplerState _sampler;

    // Fase G: sprites de jugadores remotos, extraídos en vivo de OAM/VRAM/paleta del propio
    // GBA (ver LibretroCore.SpriteExtractor) — reemplaza los cuadrados de color de D.2b por el
    // sprite real del personaje, con transparencia real (no hay que adivinar un color de fondo
    // a descartar: el índice de paleta 0 de un OBJ del GBA siempre es transparente).
    private readonly ID3D11PixelShader _spritePixelShader;
    private readonly ID3D11Buffer _spriteVertexBuffer;
    private readonly List<TextVertex> _spriteVertices = new();
    private ID3D11Texture2D? _spriteTexture;
    private ID3D11ShaderResourceView? _spriteView;
    private int _spriteTexWidth, _spriteTexHeight;

    // Fase battle-4: sprites de Pokémon (front/back de "yours"/"opponent" en batalla) —
    // a diferencia del sprite único de overworld (_spriteView, un solo jugador reusado con
    // tinte), acá conviven varias texturas DISTINTAS a la vez (especies distintas), cacheadas
    // por clave (ver ClientApp.Battle.BattleScreen.SpriteKey) para no volver a subir el mismo
    // PNG decodificado en cada frame. Reusa el mismo pipeline de texto/sprite (_textInputLayout/
    // _textVertexShader/_spritePixelShader) en coordenadas de píxel de VENTANA (como AddRect/
    // AddText), no de espacio GBA (como SetRemoteSprites).
    private readonly Dictionary<string, (ID3D11Texture2D Tex, ID3D11ShaderResourceView View, int W, int H)> _uiTextures = new();
    private readonly List<(string Key, TextVertex[] Verts)> _uiSprites = new();
    private readonly ID3D11Buffer _uiSpriteVertexBuffer;

    private readonly FontAtlas _fontAtlas;
    private readonly ID3D11Texture2D _fontTexture;
    private readonly ID3D11ShaderResourceView _fontView;
    private readonly ID3D11SamplerState _fontSampler;
    private readonly ID3D11VertexShader _textVertexShader;
    private readonly ID3D11PixelShader _textPixelShader;
    private readonly ID3D11InputLayout _textInputLayout;
    private readonly ID3D11Buffer _textVertexBuffer;
    private readonly ID3D11BlendState _alphaBlendState;
    private readonly List<TextVertex> _textVertices = new();

    private ID3D11Texture2D? _sourceTexture;
    private ID3D11ShaderResourceView? _sourceView;
    private uint _sourceWidth, _sourceHeight;

    private readonly int _windowWidth, _windowHeight;

    public Renderer(IntPtr hwnd, int width, int height)
    {
        _windowWidth = width;
        _windowHeight = height;

        _factory = CreateDXGIFactory1<IDXGIFactory2>();

        FeatureLevel[] levels = [FeatureLevel.Level_11_1, FeatureLevel.Level_11_0];
        D3D11CreateDevice(null, DriverType.Hardware, DeviceCreationFlags.BgraSupport, levels,
            out _device, out _, out _context).CheckError();

        SwapChainDescription1 swapDesc = new()
        {
            Width = (uint)width,
            Height = (uint)height,
            Format = Format.B8G8R8A8_UNorm,
            BufferCount = 2,
            BufferUsage = Usage.RenderTargetOutput,
            SampleDescription = SampleDescription.Default,
            Scaling = Scaling.Stretch,
            SwapEffect = SwapEffect.FlipDiscard,
            AlphaMode = AlphaMode.Ignore,
        };
        SwapChainFullscreenDescription fsDesc = new() { Windowed = true };
        _swapChain = _factory.CreateSwapChainForHwnd(_device, hwnd, swapDesc, fsDesc);
        _factory.MakeWindowAssociation(hwnd, WindowAssociationFlags.IgnoreAltEnter);

        using (ID3D11Texture2D backBuffer = _swapChain.GetBuffer<ID3D11Texture2D>(0))
            _renderTargetView = _device.CreateRenderTargetView(backBuffer);

        ReadOnlyMemory<byte> vsBytecode = Compiler.Compile(ShaderSource, "VSMain", "fullscreen_vs", "vs_4_0");
        ReadOnlyMemory<byte> psBytecode = Compiler.Compile(ShaderSource, "PSMain", "fullscreen_ps", "ps_4_0");
        _vertexShader = _device.CreateVertexShader(vsBytecode.Span);
        _pixelShader = _device.CreatePixelShader(psBytecode.Span);

        // Sampling "point" (sin blur): se ve nítido tipo pixel art, como el emulador real.
        _sampler = _device.CreateSamplerState(SamplerDescription.PointClamp);

        // El vertex buffer de sprites usa el mismo TextVertex/_textInputLayout/shaders de texto
        // (se crean más abajo) — solo necesita su propio buffer porque el contenido cambia con
        // una cadencia distinta a la del texto (una vez por posición de jugador remoto, no una
        // vez por char de UI).
        BufferDescription spriteBufDesc = new()
        {
            ByteWidth = (uint)(Marshal.SizeOf<TextVertex>() * MaxSprites * VerticesPerSprite),
            Usage = ResourceUsage.Dynamic,
            BindFlags = BindFlags.VertexBuffer,
            CPUAccessFlags = CpuAccessFlags.Write,
        };
        _spriteVertexBuffer = _device.CreateBuffer(spriteBufDesc);

        ReadOnlyMemory<byte> spritePsBytecode = Compiler.Compile(SpritePixelShaderSource, "PSMain", "sprite_ps", "ps_4_0");
        _spritePixelShader = _device.CreatePixelShader(spritePsBytecode.Span);

        // --- Texto (Fase F): atlas de glifos + pipeline con blending alfa ---
        _fontAtlas = new FontAtlas();
        Texture2DDescription fontDesc = new()
        {
            Width = (uint)_fontAtlas.Width,
            Height = (uint)_fontAtlas.Height,
            MipLevels = 1,
            ArraySize = 1,
            Format = Format.B8G8R8A8_UNorm,
            SampleDescription = SampleDescription.Default,
            Usage = ResourceUsage.Immutable,
            BindFlags = BindFlags.ShaderResource,
        };
        unsafe
        {
            fixed (byte* pixelsPtr = _fontAtlas.PixelsBgra)
            {
                var initData = new SubresourceData { DataPointer = (IntPtr)pixelsPtr, RowPitch = (uint)(_fontAtlas.Width * 4) };
                _fontTexture = _device.CreateTexture2D(fontDesc, initData);
            }
        }
        _fontView = _device.CreateShaderResourceView(_fontTexture);
        _fontSampler = _device.CreateSamplerState(SamplerDescription.LinearClamp);

        ReadOnlyMemory<byte> textVsBytecode = Compiler.Compile(TextShaderSource, "VSMain", "text_vs", "vs_4_0");
        ReadOnlyMemory<byte> textPsBytecode = Compiler.Compile(TextShaderSource, "PSMain", "text_ps", "ps_4_0");
        _textVertexShader = _device.CreateVertexShader(textVsBytecode.Span);
        _textPixelShader = _device.CreatePixelShader(textPsBytecode.Span);
        InputElementDescription[] textLayout =
        [
            new InputElementDescription("POSITION", 0, Format.R32G32_Float, 0, 0),
            new InputElementDescription("TEXCOORD", 0, Format.R32G32_Float, 8, 0),
            new InputElementDescription("COLOR", 0, Format.R32G32B32A32_Float, 16, 0),
        ];
        _textInputLayout = _device.CreateInputLayout(textLayout, textVsBytecode.Span);

        BufferDescription textBufDesc = new()
        {
            ByteWidth = (uint)(Marshal.SizeOf<TextVertex>() * MaxTextChars * VerticesPerChar),
            Usage = ResourceUsage.Dynamic,
            BindFlags = BindFlags.VertexBuffer,
            CPUAccessFlags = CpuAccessFlags.Write,
        };
        _textVertexBuffer = _device.CreateBuffer(textBufDesc);

        _alphaBlendState = _device.CreateBlendState(BlendDescription.NonPremultiplied);

        BufferDescription uiSpriteBufDesc = new()
        {
            ByteWidth = (uint)(Marshal.SizeOf<TextVertex>() * VerticesPerSprite),
            Usage = ResourceUsage.Dynamic,
            BindFlags = BindFlags.VertexBuffer,
            CPUAccessFlags = CpuAccessFlags.Write,
        };
        _uiSpriteVertexBuffer = _device.CreateBuffer(uiSpriteBufDesc);
    }

    /// <summary>
    /// Encola texto para dibujar en el próximo Render(), en coordenadas de PÍXEL DE VENTANA
    /// (no del espacio 240x160 del GBA — la UI vive en la resolución real de la ventana).
    /// Hay que llamar ClearText() antes de la primera llamada de cada frame.
    /// </summary>
    public void AddText(string text, float xPx, float yPx, float r, float g, float b, float a = 1f, float scale = 1f)
    {
        float cursorX = xPx;
        foreach (char c in text)
        {
            if (c == '\n') { continue; } // sin wrap/newline automático todavía; el llamador parte las líneas
            Glyph glyph = _fontAtlas.GetGlyph(c);
            float w = glyph.WidthPx * scale;
            float h = glyph.HeightPx * scale;

            float x0 = ToNdcX(cursorX, _windowWidth);
            float x1 = ToNdcX(cursorX + w, _windowWidth);
            float y0 = ToNdcY(yPx, _windowHeight);
            float y1 = ToNdcY(yPx + h, _windowHeight);

            if (_textVertices.Count + VerticesPerChar <= MaxTextChars * VerticesPerChar)
            {
                _textVertices.Add(new TextVertex { X = x0, Y = y0, U = glyph.U0, V = glyph.V0, R = r, G = g, B = b, A = a });
                _textVertices.Add(new TextVertex { X = x1, Y = y0, U = glyph.U1, V = glyph.V0, R = r, G = g, B = b, A = a });
                _textVertices.Add(new TextVertex { X = x0, Y = y1, U = glyph.U0, V = glyph.V1, R = r, G = g, B = b, A = a });
                _textVertices.Add(new TextVertex { X = x1, Y = y0, U = glyph.U1, V = glyph.V0, R = r, G = g, B = b, A = a });
                _textVertices.Add(new TextVertex { X = x1, Y = y1, U = glyph.U1, V = glyph.V1, R = r, G = g, B = b, A = a });
                _textVertices.Add(new TextVertex { X = x0, Y = y1, U = glyph.U0, V = glyph.V1, R = r, G = g, B = b, A = a });
            }
            cursorX += glyph.AdvancePx * scale;
        }
    }

    /// <summary>
    /// Encola un rectángulo relleno de color sólido (fondo de panel/tarjeta, badge, barra de
    /// HP, etc.), en las mismas coordenadas de píxel de ventana que AddText. Reusa el mismo
    /// vertex buffer/shader que el texto (sampleando la celda "Solid" reservada del atlas,
    /// ver FontAtlas) — no hace falta un pipeline aparte solo para rectángulos.
    /// </summary>
    public void AddRect(float xPx, float yPx, float wPx, float hPx, float r, float g, float b, float a = 1f)
    {
        Glyph solid = _fontAtlas.Solid;
        float x0 = ToNdcX(xPx, _windowWidth);
        float x1 = ToNdcX(xPx + wPx, _windowWidth);
        float y0 = ToNdcY(yPx, _windowHeight);
        float y1 = ToNdcY(yPx + hPx, _windowHeight);

        if (_textVertices.Count + VerticesPerChar > MaxTextChars * VerticesPerChar) return;
        _textVertices.Add(new TextVertex { X = x0, Y = y0, U = solid.U0, V = solid.V0, R = r, G = g, B = b, A = a });
        _textVertices.Add(new TextVertex { X = x1, Y = y0, U = solid.U1, V = solid.V0, R = r, G = g, B = b, A = a });
        _textVertices.Add(new TextVertex { X = x0, Y = y1, U = solid.U0, V = solid.V1, R = r, G = g, B = b, A = a });
        _textVertices.Add(new TextVertex { X = x1, Y = y0, U = solid.U1, V = solid.V0, R = r, G = g, B = b, A = a });
        _textVertices.Add(new TextVertex { X = x1, Y = y1, U = solid.U1, V = solid.V1, R = r, G = g, B = b, A = a });
        _textVertices.Add(new TextVertex { X = x0, Y = y1, U = solid.U0, V = solid.V1, R = r, G = g, B = b, A = a });
    }

    /// <summary>Alto de línea del atlas actual, en píxeles de ventana (para apilar líneas de chat).</summary>
    public float TextLineHeight => _fontAtlas.LineHeightPx;

    /// <summary>Mide el ancho en píxeles que ocuparía un texto, sin dibujarlo (para layout de UI).</summary>
    public float MeasureTextWidth(string text, float scale = 1f)
    {
        float w = 0;
        foreach (char c in text) w += _fontAtlas.GetGlyph(c).AdvancePx * scale;
        return w;
    }

    /// <summary>Limpia texto/rects Y sprites de UI encolados (ver _uiSprites) — se llama una
    /// sola vez al principio de cada frame, antes de que el frame vuelva a declarar todo lo que
    /// quiere dibujar (mismo ciclo "declarar aditivo, Render() al final" que AddText/AddRect).</summary>
    public void ClearText()
    {
        _textVertices.Clear();
        _uiSprites.Clear();
    }

    /// <summary>Sube (si hace falta) una textura de UI identificada por una clave estable (ver
    /// ClientApp.Battle.BattleScreen.SpriteKey) — a diferencia de UploadSpriteTexture (un solo
    /// slot, reusado con tinte para todo jugador remoto), acá conviven varias texturas DISTINTAS
    /// simultáneamente (especies distintas en batalla). No hace nada si la clave ya está subida
    /// (asume contenido inmutable por clave: una especie siempre decodifica al mismo PNG).</summary>
    public unsafe void EnsureUiTexture(string key, byte[] rgba, int width, int height)
    {
        if (_uiTextures.ContainsKey(key)) return;

        Texture2DDescription desc = new()
        {
            Width = (uint)width,
            Height = (uint)height,
            MipLevels = 1,
            ArraySize = 1,
            Format = Format.R8G8B8A8_UNorm,
            SampleDescription = SampleDescription.Default,
            Usage = ResourceUsage.Immutable,
            BindFlags = BindFlags.ShaderResource,
        };
        fixed (byte* p = rgba)
        {
            var initData = new SubresourceData { DataPointer = (IntPtr)p, RowPitch = (uint)(width * 4) };
            ID3D11Texture2D tex = _device.CreateTexture2D(desc, initData);
            ID3D11ShaderResourceView view = _device.CreateShaderResourceView(tex);
            _uiTextures[key] = (tex, view, width, height);
        }
    }

    public bool HasUiTexture(string key) => _uiTextures.ContainsKey(key);

    /// <summary>Encola un sprite de UI (textura completa, sin recorte de UV) en coordenadas de
    /// píxel de ventana, mismo espacio que AddRect/AddText. No hace nada si la clave no fue
    /// subida todavía con EnsureUiTexture (el llamador decide qué mostrar mientras tanto, ej.
    /// nada, o un AddRect placeholder).</summary>
    public void AddSprite(string key, float xPx, float yPx, float wPx, float hPx, float r = 1f, float g = 1f, float b = 1f, float a = 1f)
    {
        if (!_uiTextures.ContainsKey(key)) return;
        float x0 = ToNdcX(xPx, _windowWidth), x1 = ToNdcX(xPx + wPx, _windowWidth);
        float y0 = ToNdcY(yPx, _windowHeight), y1 = ToNdcY(yPx + hPx, _windowHeight);
        _uiSprites.Add((key, [
            new TextVertex { X = x0, Y = y0, U = 0, V = 0, R = r, G = g, B = b, A = a },
            new TextVertex { X = x1, Y = y0, U = 1, V = 0, R = r, G = g, B = b, A = a },
            new TextVertex { X = x0, Y = y1, U = 0, V = 1, R = r, G = g, B = b, A = a },
            new TextVertex { X = x1, Y = y0, U = 1, V = 0, R = r, G = g, B = b, A = a },
            new TextVertex { X = x1, Y = y1, U = 1, V = 1, R = r, G = g, B = b, A = a },
            new TextVertex { X = x0, Y = y1, U = 0, V = 1, R = r, G = g, B = b, A = a },
        ]));
    }

    /// <summary>
    /// Sube (o reemplaza) la textura de sprite usada para dibujar jugadores remotos — hoy una
    /// sola imagen (el sprite del jugador local capturado de OAM/VRAM, ver SpriteExtractor),
    /// reusada para todos los jugadores remotos por igual. rgba: RGBA8 top-down, alfa=0 en los
    /// píxeles transparentes (ya viene así desde SpriteExtractor.DecodeSprite).
    /// </summary>
    public unsafe void UploadSpriteTexture(byte[] rgba, int width, int height)
    {
        _spriteView?.Dispose();
        _spriteTexture?.Dispose();

        Texture2DDescription desc = new()
        {
            Width = (uint)width,
            Height = (uint)height,
            MipLevels = 1,
            ArraySize = 1,
            Format = Format.R8G8B8A8_UNorm,
            SampleDescription = SampleDescription.Default,
            Usage = ResourceUsage.Immutable,
            BindFlags = BindFlags.ShaderResource,
        };
        fixed (byte* pixelsPtr = rgba)
        {
            var initData = new SubresourceData { DataPointer = (IntPtr)pixelsPtr, RowPitch = (uint)(width * 4) };
            _spriteTexture = _device.CreateTexture2D(desc, initData);
        }
        _spriteView = _device.CreateShaderResourceView(_spriteTexture);
        _spriteTexWidth = width;
        _spriteTexHeight = height;
    }

    public bool HasSpriteTexture => _spriteView != null;

    /// <summary>
    /// Reemplaza los sprites a dibujar en el próximo Render(). Cada entrada: posición de un
    /// jugador remoto relativa al jugador local, en tiles (dx,dy) — típicamente
    /// remoto.X - local.X, remoto.Y - local.Y — más el tinte RGB elegido por ESE jugador (ver
    /// SpriteColors.cs; (1,1,1) = sin tinte, colores naturales del sprite). La cámara del
    /// emulador sigue siempre al jugador local centrado, así que ese offset alcanza para
    /// ubicarlos en pantalla (simplificación: no contempla el clamp de cámara cerca de los
    /// bordes del mapa).
    /// </summary>
    public unsafe void SetRemoteSprites(IReadOnlyList<(float dx, float dy, float tintR, float tintG, float tintB)> spritesInTiles)
    {
        const float GbaWidthPx = 240f, GbaHeightPx = 160f, TilePx = 16f;

        _spriteVertices.Clear();
        if (_spriteView == null) return;

        int count = Math.Min(spritesInTiles.Count, MaxSprites);
        float halfW = _spriteTexWidth / 2f, fullH = _spriteTexHeight;

        for (int i = 0; i < count; i++)
        {
            var (dx, dy, tr, tg, tb) = spritesInTiles[i];
            // El sprite del GBA tiene su "pie" (el punto que pisa el tile) cerca del borde
            // inferior, no en el centro vertical (por eso es 16x32: la mitad de arriba es la
            // cabeza/torso por encima del tile que realmente ocupa) — se ancla por abajo, no
            // se centra verticalmente, si no el personaje parecería flotar sobre el tile.
            float centerPxX = GbaWidthPx / 2f + dx * TilePx;
            float bottomPxY = GbaHeightPx / 2f + dy * TilePx + TilePx / 2f;

            float x0 = ToNdcX(centerPxX - halfW, GbaWidthPx);
            float x1 = ToNdcX(centerPxX + halfW, GbaWidthPx);
            float y0 = ToNdcY(bottomPxY - fullH, GbaHeightPx);
            float y1 = ToNdcY(bottomPxY, GbaHeightPx);

            _spriteVertices.Add(new TextVertex { X = x0, Y = y0, U = 0, V = 0, R = tr, G = tg, B = tb, A = 1 });
            _spriteVertices.Add(new TextVertex { X = x1, Y = y0, U = 1, V = 0, R = tr, G = tg, B = tb, A = 1 });
            _spriteVertices.Add(new TextVertex { X = x0, Y = y1, U = 0, V = 1, R = tr, G = tg, B = tb, A = 1 });
            _spriteVertices.Add(new TextVertex { X = x1, Y = y0, U = 1, V = 0, R = tr, G = tg, B = tb, A = 1 });
            _spriteVertices.Add(new TextVertex { X = x1, Y = y1, U = 1, V = 1, R = tr, G = tg, B = tb, A = 1 });
            _spriteVertices.Add(new TextVertex { X = x0, Y = y1, U = 0, V = 1, R = tr, G = tg, B = tb, A = 1 });
        }

        MappedSubresource mapped = _context.Map(_spriteVertexBuffer, 0, MapMode.WriteDiscard);
        try
        {
            var span = System.Runtime.InteropServices.CollectionsMarshal.AsSpan(_spriteVertices);
            if (span.Length > 0)
            {
                fixed (TextVertex* src = span)
                    Buffer.MemoryCopy(src, (void*)mapped.DataPointer, mapped.RowPitch, (nuint)(span.Length * Marshal.SizeOf<TextVertex>()));
            }
        }
        finally
        {
            _context.Unmap(_spriteVertexBuffer, 0);
        }
    }

    private static float ToNdcX(float px, float widthPx) => px / widthPx * 2f - 1f;
    private static float ToNdcY(float px, float heightPx) => 1f - px / heightPx * 2f;

    private void EnsureSourceTexture(uint width, uint height)
    {
        if (_sourceTexture != null && _sourceWidth == width && _sourceHeight == height) return;

        _sourceView?.Dispose();
        _sourceTexture?.Dispose();

        Texture2DDescription desc = new()
        {
            Width = width,
            Height = height,
            MipLevels = 1,
            ArraySize = 1,
            Format = Format.B8G8R8A8_UNorm,
            SampleDescription = SampleDescription.Default,
            Usage = ResourceUsage.Dynamic,
            BindFlags = BindFlags.ShaderResource,
            CPUAccessFlags = CpuAccessFlags.Write,
        };
        _sourceTexture = _device.CreateTexture2D(desc);
        _sourceView = _device.CreateShaderResourceView(_sourceTexture);
        _sourceWidth = width;
        _sourceHeight = height;
    }

    /// <summary>
    /// Sube un frame en formato RGB565 (2 bytes/píxel, el que pide mgba_libretro) a la
    /// textura fuente, convirtiéndolo a BGRA8 en el camino.
    /// </summary>
    public unsafe void UpdateFrame(IntPtr rgb565Data, uint width, uint height, nuint srcPitchBytes)
    {
        if (rgb565Data == IntPtr.Zero || width == 0 || height == 0) return;
        EnsureSourceTexture(width, height);

        MappedSubresource mapped = _context.Map(_sourceTexture!, 0, MapMode.WriteDiscard);
        try
        {
            byte* src = (byte*)rgb565Data;
            byte* dstBase = (byte*)mapped.DataPointer;
            for (uint y = 0; y < height; y++)
            {
                ushort* srcRow = (ushort*)(src + y * srcPitchBytes);
                uint* dstRow = (uint*)(dstBase + y * mapped.RowPitch);
                for (uint x = 0; x < width; x++)
                {
                    ushort px = srcRow[x];
                    // RGB565: RRRRR GGGGGG BBBBB. Replicar bits altos en los bajos para
                    // pasar de 5/6 bits a 8 bits sin oscurecer el resultado.
                    uint r5 = (uint)(px >> 11) & 0x1F;
                    uint g6 = (uint)(px >> 5) & 0x3F;
                    uint b5 = (uint)px & 0x1F;
                    byte r = (byte)((r5 << 3) | (r5 >> 2));
                    byte g = (byte)((g6 << 2) | (g6 >> 4));
                    byte b = (byte)((b5 << 3) | (b5 >> 2));
                    dstRow[x] = (uint)(0xFF << 24 | r << 16 | g << 8 | b); // BGRA8 en memoria = B,G,R,A por byte -> como uint (little-endian) = 0xAARRGGBB
                }
            }
        }
        finally
        {
            _context.Unmap(_sourceTexture!, 0);
        }
    }

    public void Render()
    {
        _context.OMSetRenderTargets(_renderTargetView);
        _context.RSSetViewport(new Viewport(_windowWidth, _windowHeight));
        _context.ClearRenderTargetView(_renderTargetView, Colors.Black);

        // Las tres pasadas de este método (fondo del emulador, marcadores, texto) dibujan
        // listas de triángulos: se fija una sola vez, sin importar si hay frame de juego
        // todavía (pantallas de login, antes de que exista ROM cargada). Antes esto vivía
        // adentro del `if (_sourceView != null)`: cuando esa rama nunca se ejecutaba (como en
        // LoginFlow, que renderiza texto sin frame de juego), Draw() se llamaba con la
        // topología sin fijar — comportamiento indefinido que en el driver de GPU virtual de
        // este entorno (RDP sin GPU física) tumbaba el dispositivo D3D11 con
        // DXGI_ERROR_DEVICE_REMOVED a los pocos frames.
        _context.IASetPrimitiveTopology(PrimitiveTopology.TriangleList);

        if (_sourceView != null)
        {
            _context.IASetInputLayout(null);
            _context.VSSetShader(_vertexShader);
            _context.PSSetShader(_pixelShader);
            _context.PSSetShaderResource(0, _sourceView);
            _context.PSSetSampler(0, _sampler);
            _context.Draw(3, 0);
        }

        _context.OMSetBlendState(_alphaBlendState);

        if (_spriteVertices.Count > 0 && _spriteView != null)
        {
            _context.IASetInputLayout(_textInputLayout);
            _context.IASetVertexBuffer(0, _spriteVertexBuffer, (uint)Marshal.SizeOf<TextVertex>());
            _context.VSSetShader(_textVertexShader);
            _context.PSSetShader(_spritePixelShader);
            _context.PSSetShaderResource(0, _spriteView);
            _context.PSSetSampler(0, _sampler); // PointClamp: nítido, sin blur, mismo criterio que el framebuffer del emulador
            _context.Draw((uint)_spriteVertices.Count, 0);
        }

        // Los rects/texto (incluye el fondo semitransparente de BattleScreen) van ANTES que los
        // sprites de UI: van todos en un solo Draw() cada pasada, así que el orden ENTRE pasadas
        // importa aunque no haya depth test — invertido, un fondo de pantalla completa quedaba
        // pintado ENCIMA de los sprites recién dibujados, oscureciéndolos (bug real, encontrado
        // en vivo: Torchic/Mudkip salían casi negros en la captura de --debug-battle).
        DrawQueuedText();
        DrawQueuedUiSprites();

        _context.OMSetBlendState(null);
        _swapChain.Present(1, PresentFlags.None);
    }

    /// <summary>Un Draw() por sprite (no un batch): en la práctica hay a lo sumo un puñado de
    /// sprites de UI en pantalla a la vez (2 en batalla: yours/opponent), así que el costo de
    /// bindear una textura distinta por Draw es irrelevante — batchear exigiría texture arrays
    /// o un atlas compartido, complejidad que no se justifica para este volumen.</summary>
    private unsafe void DrawQueuedUiSprites()
    {
        if (_uiSprites.Count == 0) return;

        _context.IASetInputLayout(_textInputLayout);
        _context.IASetVertexBuffer(0, _uiSpriteVertexBuffer, (uint)Marshal.SizeOf<TextVertex>());
        _context.VSSetShader(_textVertexShader);
        _context.PSSetShader(_spritePixelShader);
        _context.PSSetSampler(0, _sampler);

        foreach (var (key, verts) in _uiSprites)
        {
            if (!_uiTextures.TryGetValue(key, out var tex)) continue;

            MappedSubresource mapped = _context.Map(_uiSpriteVertexBuffer, 0, MapMode.WriteDiscard);
            try
            {
                fixed (TextVertex* src = verts)
                    Buffer.MemoryCopy(src, (void*)mapped.DataPointer, mapped.RowPitch, (nuint)(verts.Length * Marshal.SizeOf<TextVertex>()));
            }
            finally
            {
                _context.Unmap(_uiSpriteVertexBuffer, 0);
            }

            _context.PSSetShaderResource(0, tex.View);
            _context.Draw((uint)verts.Length, 0);
        }
    }

    private unsafe void DrawQueuedText()
    {
        if (_textVertices.Count == 0) return;

        MappedSubresource mapped = _context.Map(_textVertexBuffer, 0, MapMode.WriteDiscard);
        try
        {
            var span = System.Runtime.InteropServices.CollectionsMarshal.AsSpan(_textVertices);
            fixed (TextVertex* src = span)
            {
                Buffer.MemoryCopy(src, (void*)mapped.DataPointer, mapped.RowPitch,
                    (nuint)(span.Length * Marshal.SizeOf<TextVertex>()));
            }
        }
        finally
        {
            _context.Unmap(_textVertexBuffer, 0);
        }

        _context.IASetInputLayout(_textInputLayout);
        _context.IASetVertexBuffer(0, _textVertexBuffer, (uint)Marshal.SizeOf<TextVertex>());
        _context.VSSetShader(_textVertexShader);
        _context.PSSetShader(_textPixelShader);
        _context.PSSetShaderResource(0, _fontView);
        _context.PSSetSampler(0, _fontSampler);
        _context.Draw((uint)_textVertices.Count, 0);
    }

    /// <summary>
    /// Vuelca el backbuffer actual a un BMP de 32bpp. Solo para verificación/diagnóstico
    /// en entornos sin captura de pantalla disponible (ej. sesión RDP sin driver de video) —
    /// lee los píxeles reales que produjo el pipeline, no una captura de escritorio.
    /// </summary>
    public unsafe void CaptureToFile(string path)
    {
        using ID3D11Texture2D backBuffer = _swapChain.GetBuffer<ID3D11Texture2D>(0);
        Texture2DDescription stagingDesc = backBuffer.Description;
        stagingDesc.Usage = ResourceUsage.Staging;
        stagingDesc.BindFlags = BindFlags.None;
        stagingDesc.CPUAccessFlags = CpuAccessFlags.Read;
        stagingDesc.MiscFlags = ResourceOptionFlags.None;

        using ID3D11Texture2D staging = _device.CreateTexture2D(stagingDesc);
        _context.CopyResource(staging, backBuffer);

        MappedSubresource mapped = _context.Map(staging, 0, MapMode.Read);
        try
        {
            int width = _windowWidth, height = _windowHeight;
            int rowBytes = width * 4;
            byte[] pixels = new byte[rowBytes * height];
            byte* src = (byte*)mapped.DataPointer;
            fixed (byte* dstFixed = pixels)
            {
                for (int y = 0; y < height; y++)
                    Buffer.MemoryCopy(src + y * mapped.RowPitch, dstFixed + y * rowBytes, rowBytes, rowBytes);
            }
            WriteBmp32(path, width, height, pixels);
        }
        finally
        {
            _context.Unmap(staging, 0);
        }
    }

    private static void WriteBmp32(string path, int width, int height, byte[] bgraPixelsTopDown)
    {
        int rowBytes = width * 4;
        int dataSize = rowBytes * height;
        int fileSize = 14 + 40 + dataSize;

        using var fs = new FileStream(path, FileMode.Create, FileAccess.Write);
        using var w = new BinaryWriter(fs);

        // BITMAPFILEHEADER
        w.Write((byte)'B'); w.Write((byte)'M');
        w.Write(fileSize);
        w.Write(0); // reserved
        w.Write(14 + 40); // pixel data offset

        // BITMAPINFOHEADER
        w.Write(40); // header size
        w.Write(width);
        w.Write(-height); // negativo = top-down (fila 0 = arriba), coincide con cómo leímos el backbuffer
        w.Write((short)1); // planes
        w.Write((short)32); // bits per pixel
        w.Write(0); // BI_RGB, sin compresión
        w.Write(dataSize);
        w.Write(0); w.Write(0); // resolución, sin importancia
        w.Write(0); w.Write(0); // paleta

        w.Write(bgraPixelsTopDown);
    }

    public void Dispose()
    {
        foreach (var tex in _uiTextures.Values) { tex.View.Dispose(); tex.Tex.Dispose(); }
        _uiSpriteVertexBuffer.Dispose();
        _sourceView?.Dispose();
        _sourceTexture?.Dispose();
        _spriteView?.Dispose();
        _spriteTexture?.Dispose();
        _sampler.Dispose();
        _pixelShader.Dispose();
        _vertexShader.Dispose();
        _spriteVertexBuffer.Dispose();
        _spritePixelShader.Dispose();
        _alphaBlendState.Dispose();
        _textVertexBuffer.Dispose();
        _textInputLayout.Dispose();
        _textPixelShader.Dispose();
        _textVertexShader.Dispose();
        _fontSampler.Dispose();
        _fontView.Dispose();
        _fontTexture.Dispose();
        _renderTargetView.Dispose();
        _swapChain.Dispose();
        _context.Dispose();
        _device.Dispose();
        _factory.Dispose();
    }
}
