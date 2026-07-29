using System.Globalization;
using System.Windows;
using System.Windows.Data;

namespace Launcher.Converters;

/// <summary>Igual que BooleanToVisibilityConverter pero invertido — para mostrar la pantalla
/// principal exactamente cuando IsBusy es false, sin negar la propiedad en el ViewModel.</summary>
public sealed class InverseBoolToVisibilityConverter : IValueConverter
{
    public object Convert(object? value, Type targetType, object? parameter, CultureInfo culture) =>
        value is true ? Visibility.Collapsed : Visibility.Visible;

    public object ConvertBack(object value, Type targetType, object? parameter, CultureInfo culture) =>
        throw new NotSupportedException();
}
