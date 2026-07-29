using System.ComponentModel;

namespace Launcher.Localization;

public enum AppLanguage { Es, En }

/// <summary>Textos de la interfaz del launcher en es/en — a propósito NO es un sistema de
/// recursos .resx (overkill para un puñado de pantallas): un objeto bindeable cuyas
/// propiedades cambian de idioma y dispara un solo PropertyChanged(null), que en WPF significa
/// "revisá todas las propiedades" y refresca cualquier binding a este objeto sin tener que
/// listarlas una por una.</summary>
public sealed class Loc : INotifyPropertyChanged
{
    public event PropertyChangedEventHandler? PropertyChanged;

    private AppLanguage _language = AppLanguage.Es;
    public AppLanguage Language
    {
        get => _language;
        set
        {
            if (_language == value) return;
            _language = value;
            PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(null));
        }
    }

    private bool IsEs => _language == AppLanguage.Es;

    public string NewsHeader => IsEs ? "NOTICIAS Y EVENTOS" : "NEWS & EVENTS";
    public string ServerStatusHeader => IsEs ? "ESTADO DEL SERVIDOR" : "SERVER STATUS";
    public string PlayButton => IsEs ? "JUGAR" : "PLAY";
    public string RetryButton => IsEs ? "Reintentar" : "Retry";
    public string ExitButton => IsEs ? "Salir" : "Exit";
    public string BrowseRomButton => IsEs ? "Seleccionar ROM" : "Select ROM";
    public string NeedsRomTitle => IsEs ? "Falta seleccionar una ROM" : "A ROM needs to be selected";
    public string UsernamePlaceholder => IsEs ? "Usuario (opcional)" : "Username (optional)";
    public string RememberUsernameLabel => IsEs ? "Recordar usuario" : "Remember username";
    public string NewsTag => IsEs ? "NOTICIA" : "NEWS";
    public string EventTag => IsEs ? "EVENTO" : "EVENT";
}
