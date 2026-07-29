using System.Globalization;
using System.Windows.Data;

namespace Launcher.Converters;

/// <summary>MultiBinding: [Value, Maximum, ActualWidth del track] -> ancho en píxeles del
/// relleno. El ProgressBar por defecto de WPF no expone esto de forma directa vía template
/// simple sin este cálculo manual.</summary>
public sealed class ProgressFillConverter : IMultiValueConverter
{
    public object Convert(object[] values, Type targetType, object? parameter, CultureInfo culture)
    {
        if (values is [double value, double maximum, double trackWidth] && maximum > 0)
            return Math.Clamp(value / maximum * trackWidth, 0, trackWidth);
        return 0.0;
    }

    public object[] ConvertBack(object value, Type[] targetTypes, object? parameter, CultureInfo culture) =>
        throw new NotSupportedException();
}
