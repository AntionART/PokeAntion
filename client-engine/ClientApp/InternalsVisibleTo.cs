using System.Runtime.CompilerServices;

// Permite que ClientApp.Tests vea los tipos internal (ej. MapCatalog) sin tener que hacerlos
// públicos solo para poder testearlos.
[assembly: InternalsVisibleTo("ClientApp.Tests")]
