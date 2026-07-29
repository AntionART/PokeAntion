using System.Net.Http;
using System.Net.Http.Json;
using System.Text.Json.Serialization;

namespace Launcher.Services;

public sealed record NewsItem(
    [property: JsonPropertyName("type")] string Type,
    [property: JsonPropertyName("title")] string Title,
    [property: JsonPropertyName("body")] string Body,
    [property: JsonPropertyName("date")] string Date);

public sealed class NewsService
{
    private readonly HttpClient _http = new() { Timeout = TimeSpan.FromSeconds(5) };

    public async Task<List<NewsItem>> GetNewsAsync(string serverHttp)
    {
        try
        {
            return await _http.GetFromJsonAsync<List<NewsItem>>($"{serverHttp}/news") ?? [];
        }
        catch
        {
            return [];
        }
    }
}
