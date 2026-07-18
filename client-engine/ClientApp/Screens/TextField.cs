using System.Text;

namespace ClientApp.Screens;

/// <summary>
/// Campo de texto de una línea para las pantallas de login/registro. Reutiliza el mismo
/// mecanismo de captura que el chat (Win32Window.ConsumeTypedChars, ver Program.cs): los
/// caracteres de control (Tab, Enter, Escape) llegan acá también vía WM_CHAR, pero
/// char.IsControl los filtra automáticamente — no hace falta ningún truco de tecla especial
/// como el que necesitó el toggle de chat (F2 en vez de T) para evitar que se cuelen.
/// </summary>
public sealed class TextField
{
    private readonly StringBuilder _text = new();
    private readonly int _maxLength;

    public string Label { get; }
    public bool Masked { get; }

    public TextField(string label, bool masked = false, int maxLength = 64)
    {
        Label = label;
        Masked = masked;
        _maxLength = maxLength;
    }

    public string Value => _text.ToString();

    public string Display => Masked ? new string('*', _text.Length) : _text.ToString();

    public void HandleChar(char c)
    {
        if (c == '\b')
        {
            if (_text.Length > 0) _text.Remove(_text.Length - 1, 1);
            return;
        }
        if (char.IsControl(c)) return; // Tab (0x09), Enter (0x0D), Escape (0x1B): se manejan por VK, no como texto
        if (_text.Length < _maxLength) _text.Append(c);
    }

    public void Clear() => _text.Clear();

    public void SetValue(string value)
    {
        _text.Clear();
        _text.Append(value.Length > _maxLength ? value[.._maxLength] : value);
    }
}
