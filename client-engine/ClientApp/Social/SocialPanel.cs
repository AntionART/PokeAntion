using System.Text;
using System.Text.Json;
using ClientApp;
using ClientApp.Network;
using PokemonOnline.Protocol;

namespace ClientApp.Social;

/// <summary>
/// Fase G: panel de amigos / grupo / comercio, dibujado con el mismo pipeline de texto que el
/// chat (Renderer.AddText) — sin librería de UI externa, sin mouse (Win32Window no lo expone
/// todavía). Toggle con F5 (tecla de función: nunca genera WM_CHAR, mismo motivo que F2 para
/// el chat — ver Win32Window/Program.cs).
///
/// El servidor ya soportaba amigos/grupo/trade por protocolo (probado con smoke tests) pero no
/// tenía ninguna superficie de cliente real: esta clase es esa superficie. También depende de
/// dos adiciones al protocolo hechas junto con esto: list_my_pokemon/my_pokemon_list (sin eso,
/// un jugador real no tiene forma de saber qué Pokémon tiene para ofrecer) y trade_accepted/
/// trade_offer_updated (sin eso, cada jugador confirmaría un trade "a ciegas").
///
/// Solicitudes entrantes (amistad/grupo/trade) solo se conocen por notificación en vivo — el
/// servidor no expone una consulta de "pendientes" para lo que llegó mientras el panel estaba
/// cerrado o el jugador desconectado; limitación conocida, documentada donde corresponde.
/// </summary>
internal sealed class SocialPanel
{
    private enum Tab { Friends, Party, Trade, Battle, Market, Guild, Appearance }
    private const int TabCount = 7;

    private const int VK_F5 = 0x74;
    private const int VK_LEFT = 0x25, VK_RIGHT = 0x27, VK_UP = 0x26, VK_DOWN = 0x28;
    private const int VK_RETURN = 0x0D, VK_BACK = 0x08, VK_ESCAPE = 0x1B;
    // F4 (no una tecla de letra) para "agregar amigo": una tecla de letra puede generar un
    // WM_CHAR vía TranslateMessage que, si llega el mismo frame en que recién se activa el
    // modo de texto, se pierde o corrompe el nombre tipeado — mismo motivo que F2 para el
    // chat y F5 para este panel (ver Win32Window/Program.cs). C si es una tecla de letra
    // seguro: solo se lee como estado (IsKeyDown) para confirmar un trade, nunca compite con
    // un campo de texto activo en ese momento (agregar amigo y confirmar trade son mutuamente
    // excluyentes: no se puede estar escribiendo un usuario y confirmando un trade a la vez).
    private const int VK_ADD_FRIEND = 0x73; // F4
    private const int VK_C = 0x43;

    private readonly WebSocketClient _ws;
    private readonly Func<IReadOnlyDictionary<string, (string Nickname, string MapId, int X, int Y, string Color)>> _nearbyPlayers;
    private readonly string _myCharacterId;
    private readonly Func<string> _myColor;

    public bool IsActive { get; private set; }

    /// <summary>Fuerza el cierre del panel — usado cuando BattleScreen toma control total de
    /// la pantalla (ver Program.cs), para que ambas superficies no queden dibujándose a la vez.</summary>
    public void Close() => IsActive = false;

    private Tab _tab;
    private int _selection;
    private string _status = "";
    private bool _statusIsError;

    // ---- Amigos ----
    private List<FriendListEntryPayload> _friends = new();
    private readonly List<(string AccountId, string Username)> _pendingFriendRequests = new();
    private bool _addingFriend;
    private readonly StringBuilder _friendInput = new();

    // ---- Grupo ----
    private PartyUpdatePayload? _party;
    private readonly List<(string PartyId, string FromCharacterId, string FromNickname)> _pendingPartyInvites = new();

    // ---- Comercio ----
    private sealed class ActiveTrade
    {
        public string SessionId = "";
        public bool Accepted;
        public PokemonSummaryPayload? MyOffer;
        public PokemonSummaryPayload? TheirOffer;
        public bool IConfirmed;
        public string OtherNickname = "";
    }
    private ActiveTrade? _trade;
    private readonly List<(string SessionId, string FromCharacterId, string FromNickname)> _pendingTradeRequests = new();
    private List<PokemonSummaryPayload> _myPokemon = new();

    // ---- Batalla PvP: solo la negociación (desafiar/aceptar/rechazar) vive acá, mismo patrón
    // row-based que trade — la batalla en sí (sprites, HP, turnos) la toma BattleScreen apenas
    // llega "battle_start" (ese mensaje también llega acá, pero se ignora: BattleScreen es quien
    // lo procesa). Un jugador no puede tener dos desafíos entrantes simultáneos hoy (no hace
    // falta más para 1v1 casual) — se guardan todos igual, por si acaso, como en trade.
    private readonly List<(string SessionId, string FromCharacterId, string FromNickname)> _pendingBattleChallenges = new();

    // ---- Mercado (asincrónico: no hace falta que el otro jugador esté online) ----
    private List<MarketListingPayload> _marketListings = new(); // "explorar" (de todos)
    private List<MarketListingPayload> _myMarketListings = new(); // "mis publicaciones"
    private bool _sellingPickingPokemon; // sub-modo: eligiendo cuál de mis Pokémon vender
    private bool _sellingEnteringPrice; // sub-modo: escribiendo el precio de venta
    private PokemonSummaryPayload? _sellingPokemon;
    private readonly StringBuilder _priceInput = new();

    // ---- Gremio (persistente: a diferencia de party, no arranca vacío al reconectar — ver
    // el pedido "guild_info" en RefreshForTab, que resuelve el gremio actual al abrir la
    // pestaña, porque el servidor no empuja ese estado espontáneamente al loguear). ----
    private GuildUpdatePayload? _guild;
    private readonly List<(string GuildId, string GuildName, string FromCharacterId, string FromNickname)> _pendingGuildInvites = new();
    private bool _creatingGuild; // sub-modo: escribiendo el nombre del gremio a fundar
    private readonly StringBuilder _guildNameInput = new();

    private bool _prevF5, _prevLeft, _prevRight, _prevUp, _prevDown, _prevReturn, _prevBack, _prevEscape, _prevAddFriend, _prevC;

    public SocialPanel(WebSocketClient ws, string myCharacterId,
        Func<IReadOnlyDictionary<string, (string Nickname, string MapId, int X, int Y, string Color)>> nearbyPlayers,
        Func<string> myColor)
    {
        _ws = ws;
        _myCharacterId = myCharacterId;
        _nearbyPlayers = nearbyPlayers;
        _myColor = myColor;
    }

    /// <summary>Procesa mensajes del servidor relevantes para este panel. Se llama desde el
    /// handler general de gameplay en Program.cs para cada mensaje entrante.</summary>
    public void HandleMessage(string type, JsonElement payload)
    {
        switch (type)
        {
            case "friend_list":
                var list = payload.Deserialize<FriendListPayload>();
                if (list != null) _friends = new List<FriendListEntryPayload>(list.Friends);
                break;
            case "friend_request_received":
                var freq = payload.Deserialize<FriendRequestReceivedPayload>();
                if (freq != null) _pendingFriendRequests.Add((freq.FromAccountId, freq.FromUsername));
                break;
            case "friend_status_update":
                var status = payload.Deserialize<FriendStatusUpdatePayload>();
                if (status != null)
                {
                    int idx = _friends.FindIndex(f => f.AccountId == status.AccountId);
                    if (idx >= 0) _friends[idx].Online = status.Online;
                }
                break;

            case "party_invite_received":
                var pinv = payload.Deserialize<PartyInviteReceivedPayload>();
                if (pinv != null) _pendingPartyInvites.Add((pinv.PartyId, pinv.FromCharacterId, pinv.FromNickname));
                break;
            case "party_update":
                _party = payload.Deserialize<PartyUpdatePayload>();
                break;
            case "party_disbanded":
                _party = null;
                break;

            case "trade_request_received":
                var treq = payload.Deserialize<TradeRequestReceivedPayload>();
                if (treq != null) _pendingTradeRequests.Add((treq.TradeSessionId, treq.FromCharacterId, treq.FromNickname));
                break;
            case "trade_accepted":
                // El que MANDÓ el trade_request (ver "Pedir trade a X" en BuildRows) no conoce
                // el session_id hasta este momento — solo el destinatario lo recibe en
                // trade_request_received. Por eso _trade.SessionId puede venir vacío ("pendiente
                // de que acepten") y hay que completarlo acá, no solo comparar por igualdad.
                var tacc = payload.Deserialize<TradeSessionRefPayload>();
                if (tacc != null && _trade != null && (_trade.SessionId == tacc.TradeSessionId || _trade.SessionId == ""))
                {
                    _trade.SessionId = tacc.TradeSessionId;
                    _trade.Accepted = true;
                    _status = "";
                    RequestMyPokemon();
                }
                break;
            case "trade_offer_updated":
                var tupd = payload.Deserialize<TradeOfferUpdatedPayload>();
                if (tupd != null && _trade != null && _trade.SessionId == tupd.TradeSessionId)
                {
                    if (tupd.CharacterId == _myCharacterId) _trade.MyOffer = tupd.Pokemon;
                    else _trade.TheirOffer = tupd.Pokemon;
                }
                break;
            case "trade_completed":
                _status = "¡Intercambio completado!";
                _statusIsError = false;
                _trade = null;
                break;
            case "trade_cancelled":
                var tcan = payload.Deserialize<TradeCancelledPayload>();
                _status = $"Trade cancelado ({tcan?.Reason}).";
                _statusIsError = true;
                _trade = null;
                break;

            case "battle_challenge_received":
                var breq = payload.Deserialize<BattleChallengeReceivedPayload>();
                if (breq != null) _pendingBattleChallenges.Add((breq.BattleSessionId, breq.FromCharacterId, breq.FromNickname));
                break;
            case "battle_start":
                // Lo consume BattleScreen — acá solo hace falta limpiar el desafío de la lista
                // de pendientes si estaba ahí (lo aceptamos nosotros, ver BuildRows Tab.Battle).
                var bstart = payload.Deserialize<BattleStartPayload>();
                if (bstart != null) _pendingBattleChallenges.RemoveAll(b => b.SessionId == bstart.BattleSessionId);
                break;
            case "battle_cancelled":
                var bcan = payload.Deserialize<BattleCancelledPayload>();
                if (bcan != null) _pendingBattleChallenges.RemoveAll(b => b.SessionId == bcan.BattleSessionId);
                break;

            case "my_pokemon_list":
                var mine = payload.Deserialize<MyPokemonListPayload>();
                if (mine != null) _myPokemon = new List<PokemonSummaryPayload>(mine.Pokemon);
                break;

            case "market_listings":
                var browse = payload.Deserialize<MarketListingsPayload>();
                if (browse != null) _marketListings = new List<MarketListingPayload>(browse.Listings);
                break;
            case "market_my_listings":
                var mineListings = payload.Deserialize<MarketListingsPayload>();
                if (mineListings != null) _myMarketListings = new List<MarketListingPayload>(mineListings.Listings);
                break;
            case "market_list_ok":
                _sellingPickingPokemon = false;
                _sellingEnteringPrice = false;
                _sellingPokemon = null;
                _priceInput.Clear();
                _status = "Publicado en el mercado.";
                _statusIsError = false;
                RefreshMarket();
                break;
            case "market_cancelled":
                _status = "Publicación cancelada.";
                _statusIsError = false;
                RefreshMarket();
                break;
            case "market_purchased":
                var purchased = payload.Deserialize<MarketPurchasedPayload>();
                _status = purchased != null ? $"Compraste #{purchased.Pokemon.SpeciesId} Nv.{purchased.Pokemon.Level} por {purchased.Price}." : "Compra realizada.";
                _statusIsError = false;
                RefreshMarket();
                break;
            case "market_sold":
                var sold = payload.Deserialize<MarketSoldPayload>();
                _status = sold != null ? $"{sold.BuyerNickname} te compró una publicación por {sold.Price}." : "Se vendió una publicación.";
                _statusIsError = false;
                RefreshMarket();
                break;

            case "guild_invite_received":
                var ginv = payload.Deserialize<GuildInviteReceivedPayload>();
                if (ginv != null) _pendingGuildInvites.Add((ginv.GuildId, ginv.GuildName, ginv.FromCharacterId, ginv.FromNickname));
                break;
            case "guild_update":
                _guild = payload.Deserialize<GuildUpdatePayload>();
                // Si el update llegó mientras se estaba escribiendo el nombre de un gremio nuevo,
                // ya se fundó con éxito: cerrar el sub-modo (mismo patrón que market_list_ok).
                _creatingGuild = false;
                _guildNameInput.Clear();
                break;
            case "guild_disbanded":
                var gdis = payload.Deserialize<GuildDisbandedPayload>();
                if (gdis != null && _guild != null && gdis.GuildId == _guild.GuildId)
                {
                    _guild = null;
                    if (gdis.Reason == "kicked") { _status = "Fuiste expulsado del gremio."; _statusIsError = true; }
                }
                break;

            case "error":
                var err = payload.Deserialize<ErrorPayload>();
                if (err != null) { _status = err.Message; _statusIsError = true; }
                break;
        }
    }

    /// <summary>Llamar una vez por frame. `charsThisFrame` son los caracteres tipeados este
    /// frame (mismo origen que el chat: Win32Window.ConsumeTypedChars) — el llamador decide
    /// si se los da al panel o al chat según cuál esté activo, nunca a los dos.</summary>
    public void HandleInput(Win32Window window, IReadOnlyList<char> charsThisFrame)
    {
        bool f5Now = window.IsKeyDown(VK_F5);
        if (f5Now && !_prevF5)
        {
            IsActive = !IsActive;
            if (IsActive) RefreshForTab();
        }
        _prevF5 = f5Now;

        if (!IsActive) return;

        if (_addingFriend)
        {
            foreach (char c in charsThisFrame)
            {
                // Un '\r' con el campo todavía vacío se ignora en vez de tratarse como "enviar
                // vacío y salir": un Enter que activó ESTE modo (ej. otra fila con OnEnter que
                // dispara `_addingFriend = true`) puede generar su propio WM_CHAR('\r') que
                // llega recién un frame después, cuando el modo de texto ya está activo — sin
                // esta guarda, ese eco fantasma cerraba el campo antes de que el usuario tipeara
                // nada (bug real, encontrado en vivo con el flujo de precio del mercado, ver
                // más abajo — mismo mecanismo, mismo fix acá por las dudas).
                if (c == '\r')
                {
                    string username = _friendInput.ToString().Trim();
                    if (username.Length == 0) continue;
                    _ws.SendAsync("friend_request", new FriendRequestPayload { TargetUsername = username }).GetAwaiter().GetResult();
                    _addingFriend = false;
                    _friendInput.Clear();
                }
                else if (c == '\b') { if (_friendInput.Length > 0) _friendInput.Remove(_friendInput.Length - 1, 1); }
                else if (c == 0x1B) { _addingFriend = false; _friendInput.Clear(); }
                else if (!char.IsControl(c)) { if (_friendInput.Length < 24) _friendInput.Append(c); }
            }
            return; // mientras se escribe un usuario, el resto de las teclas del panel no actúan
        }

        if (_creatingGuild)
        {
            foreach (char c in charsThisFrame)
            {
                // Mismo motivo que _addingFriend/_sellingEnteringPrice: el Enter que activó este
                // sub-modo (la fila "-- Fundar gremio --", ver BuildRows) puede dejar un '\r'
                // fantasma que llega un frame después — con el campo todavía vacío, se ignora en
                // vez de intentar fundar un gremio con nombre vacío. Aplicado preventivamente acá
                // aunque no se haya reproducido en vivo todavía, porque es exactamente el mismo
                // patrón (Enter en una fila que activa un modo de texto) que ya causó el bug real
                // en el flujo de precio del mercado.
                if (c == '\r' && _guildNameInput.Length == 0) continue;
                if (c == '\r')
                {
                    string name = _guildNameInput.ToString().Trim();
                    if (name.Length == 0) continue;
                    _ws.SendAsync("guild_create", new GuildCreatePayload { Name = name }).GetAwaiter().GetResult();
                    _creatingGuild = false;
                    _guildNameInput.Clear();
                }
                else if (c == '\b') { if (_guildNameInput.Length > 0) _guildNameInput.Remove(_guildNameInput.Length - 1, 1); }
                else if (c == 0x1B) { _creatingGuild = false; _guildNameInput.Clear(); }
                else if (!char.IsControl(c)) { if (_guildNameInput.Length < 30) _guildNameInput.Append(c); } // VARCHAR(30) en guilds.name
            }
            return;
        }

        if (_sellingEnteringPrice)
        {
            foreach (char c in charsThisFrame)
            {
                // Mismo motivo que en _addingFriend: el Enter que eligió el Pokémon a vender
                // (la fila anterior, ver "sellingPickingPokemon" en BuildRows) puede dejar un
                // '\r' fantasma que llega un frame después de activarse este modo — con el
                // campo todavía vacío, se ignora en vez de salir con "precio inválido" antes de
                // que el usuario alcance a tipear nada. Confirmado en vivo: sin esta guarda, el
                // flujo de venta fallaba SIEMPRE en el primer intento.
                if (c == '\r' && _priceInput.Length == 0) continue;
                if (c == '\r')
                {
                    if (_sellingPokemon != null && int.TryParse(_priceInput.ToString(), out int price) && price > 0)
                    {
                        _ws.SendAsync("market_list", new MarketListPayload { PokemonId = _sellingPokemon.Id, Price = price }).GetAwaiter().GetResult();
                    }
                    else
                    {
                        _status = "Precio inválido.";
                        _statusIsError = true;
                        _sellingEnteringPrice = false;
                        _sellingPokemon = null;
                        _priceInput.Clear();
                    }
                }
                else if (c == '\b') { if (_priceInput.Length > 0) _priceInput.Remove(_priceInput.Length - 1, 1); }
                else if (c == 0x1B) { _sellingEnteringPrice = false; _sellingPokemon = null; _priceInput.Clear(); }
                else if (char.IsDigit(c)) { if (_priceInput.Length < 7) _priceInput.Append(c); }
            }
            return;
        }

        bool leftNow = window.IsKeyDown(VK_LEFT), rightNow = window.IsKeyDown(VK_RIGHT);
        bool upNow = window.IsKeyDown(VK_UP), downNow = window.IsKeyDown(VK_DOWN);
        bool returnNow = window.IsKeyDown(VK_RETURN), backNow = window.IsKeyDown(VK_BACK);
        bool escapeNow = window.IsKeyDown(VK_ESCAPE), addFriendNow = window.IsKeyDown(VK_ADD_FRIEND), cNow = window.IsKeyDown(VK_C);

        if (escapeNow && !_prevEscape)
        {
            if (_sellingPickingPokemon) _sellingPickingPokemon = false;
            else IsActive = false;
        }

        if (leftNow && !_prevLeft && !_sellingPickingPokemon) { _tab = (Tab)(((int)_tab + TabCount - 1) % TabCount); _selection = 0; _status = ""; RefreshForTab(); }
        if (rightNow && !_prevRight && !_sellingPickingPokemon) { _tab = (Tab)(((int)_tab + 1) % TabCount); _selection = 0; _status = ""; RefreshForTab(); }

        var rows = BuildRows();
        if (rows.Count > 0)
        {
            if (upNow && !_prevUp) _selection = (_selection - 1 + rows.Count) % rows.Count;
            if (downNow && !_prevDown) _selection = (_selection + 1) % rows.Count;
        }
        else _selection = 0;

        if (addFriendNow && !_prevAddFriend && _tab == Tab.Friends && !_addingFriend) { _addingFriend = true; _friendInput.Clear(); }
        if (addFriendNow && !_prevAddFriend && _tab == Tab.Market && !_sellingPickingPokemon && !_sellingEnteringPrice)
        {
            _sellingPickingPokemon = true;
            _selection = 0;
            RequestMyPokemon();
        }

        if (returnNow && !_prevReturn && rows.Count > 0 && _selection < rows.Count) rows[_selection].OnEnter?.Invoke();
        if (backNow && !_prevBack)
        {
            if (rows.Count > 0 && _selection < rows.Count) rows[_selection].OnBack?.Invoke();
            else OnGlobalBack();
        }
        if (cNow && !_prevC && _tab == Tab.Trade) TryConfirmTrade();

        _prevLeft = leftNow; _prevRight = rightNow; _prevUp = upNow; _prevDown = downNow;
        _prevReturn = returnNow; _prevBack = backNow; _prevEscape = escapeNow; _prevAddFriend = addFriendNow; _prevC = cNow;
    }

    private void RefreshForTab()
    {
        if (_tab == Tab.Friends) _ws.SendAsync("friend_list", new { }).GetAwaiter().GetResult();
        else if (_tab == Tab.Market) RefreshMarket();
        else if (_tab == Tab.Guild) _ws.SendAsync("guild_info", new { }).GetAwaiter().GetResult();
    }

    private void RefreshMarket()
    {
        _ws.SendAsync("market_browse", new { }).GetAwaiter().GetResult();
        _ws.SendAsync("market_my_listings", new { }).GetAwaiter().GetResult();
    }

    private void RequestMyPokemon() => _ws.SendAsync("list_my_pokemon", new { }).GetAwaiter().GetResult();

    private void OnGlobalBack()
    {
        if (_tab == Tab.Party && _party != null)
            _ws.SendAsync("party_leave", new { }).GetAwaiter().GetResult();
        else if (_tab == Tab.Trade && _trade != null)
        {
            _ws.SendAsync("trade_decline", new TradeSessionRefPayload { TradeSessionId = _trade.SessionId }).GetAwaiter().GetResult();
            _trade = null;
        }
    }

    private void TryConfirmTrade()
    {
        if (_trade == null || !_trade.Accepted || _trade.MyOffer == null || _trade.TheirOffer == null || _trade.IConfirmed) return;
        _trade.IConfirmed = true;
        _ws.SendAsync("trade_confirm", new TradeSessionRefPayload { TradeSessionId = _trade.SessionId }).GetAwaiter().GetResult();
        _status = "Confirmado. Esperando al otro jugador...";
        _statusIsError = false;
    }

    private sealed class Row
    {
        public required string Text;
        public float R = 0.85f, G = 0.85f, B = 0.85f;
        public Action? OnEnter;
        public Action? OnBack;
    }

    private List<Row> BuildRows()
    {
        var rows = new List<Row>();
        switch (_tab)
        {
            case Tab.Friends:
                foreach (var (accountId, username) in _pendingFriendRequests)
                {
                    rows.Add(new Row
                    {
                        Text = $"Solicitud de {username}  (Enter: aceptar, Retroceso: rechazar)",
                        R = 1f, G = 0.85f, B = 0.3f,
                        OnEnter = () =>
                        {
                            _ws.SendAsync("friend_accept", new FriendRefPayload { TargetAccountId = accountId }).GetAwaiter().GetResult();
                            _pendingFriendRequests.RemoveAll(p => p.AccountId == accountId);
                            RefreshForTab();
                        },
                        OnBack = () =>
                        {
                            _ws.SendAsync("friend_decline", new FriendRefPayload { TargetAccountId = accountId }).GetAwaiter().GetResult();
                            _pendingFriendRequests.RemoveAll(p => p.AccountId == accountId);
                        },
                    });
                }
                foreach (var f in _friends)
                {
                    string accountId = f.AccountId;
                    rows.Add(new Row
                    {
                        Text = $"[{(f.Online ? "conectado" : "desconectado")}] {f.Username}",
                        R = f.Online ? 0.4f : 0.6f, G = f.Online ? 1f : 0.6f, B = f.Online ? 0.4f : 0.6f,
                        OnBack = () => { _ws.SendAsync("friend_remove", new FriendRefPayload { TargetAccountId = accountId }).GetAwaiter().GetResult(); _friends.RemoveAll(x => x.AccountId == accountId); },
                    });
                }
                break;

            case Tab.Party:
                foreach (var (partyId, fromCharId, fromNick) in _pendingPartyInvites)
                {
                    rows.Add(new Row
                    {
                        Text = $"Invitación de {fromNick}  (Enter: aceptar, Retroceso: rechazar)",
                        R = 1f, G = 0.85f, B = 0.3f,
                        OnEnter = () => { _ws.SendAsync("party_accept", new PartyRefPayload { PartyId = partyId }).GetAwaiter().GetResult(); _pendingPartyInvites.RemoveAll(p => p.PartyId == partyId); },
                        OnBack = () => { _ws.SendAsync("party_decline", new PartyRefPayload { PartyId = partyId }).GetAwaiter().GetResult(); _pendingPartyInvites.RemoveAll(p => p.PartyId == partyId); },
                    });
                }
                if (_party != null)
                {
                    foreach (var m in _party.Members)
                        rows.Add(new Row { Text = $"{m.Nickname}{(m.IsLeader ? " (líder)" : "")}", R = 0.6f, G = 0.85f, B = 1f });
                }
                foreach (var (charId, info) in _nearbyPlayers())
                {
                    string targetId = charId;
                    rows.Add(new Row
                    {
                        Text = $"Invitar a {info.Nickname}",
                        OnEnter = () => _ws.SendAsync("party_invite", new PartyInvitePayload { TargetCharacterId = targetId }).GetAwaiter().GetResult(),
                    });
                }
                break;

            case Tab.Trade:
                if (_trade != null)
                {
                    if (_trade.Accepted)
                    {
                        foreach (var mon in _myPokemon)
                        {
                            var pokemon = mon;
                            bool isOffered = _trade.MyOffer?.Id == pokemon.Id;
                            rows.Add(new Row
                            {
                                Text = $"{(isOffered ? "[ofrecido] " : "")}#{pokemon.SpeciesId} {pokemon.Nickname} Nv.{pokemon.Level}",
                                R = isOffered ? 0.4f : 0.85f, G = isOffered ? 1f : 0.85f, B = isOffered ? 0.4f : 0.85f,
                                OnEnter = _trade.IConfirmed ? null : () => _ws.SendAsync("trade_offer_set",
                                    new TradeOfferSetPayload { TradeSessionId = _trade.SessionId, PokemonId = pokemon.Id }).GetAwaiter().GetResult(),
                            });
                        }
                    }
                    break;
                }
                foreach (var (sessionId, fromCharId, fromNick) in _pendingTradeRequests)
                {
                    rows.Add(new Row
                    {
                        Text = $"Trade de {fromNick}  (Enter: aceptar, Retroceso: rechazar)",
                        R = 1f, G = 0.85f, B = 0.3f,
                        OnEnter = () =>
                        {
                            _ws.SendAsync("trade_accept", new TradeSessionRefPayload { TradeSessionId = sessionId }).GetAwaiter().GetResult();
                            _trade = new ActiveTrade { SessionId = sessionId, OtherNickname = fromNick };
                            _pendingTradeRequests.RemoveAll(t => t.SessionId == sessionId);
                        },
                        OnBack = () =>
                        {
                            _ws.SendAsync("trade_decline", new TradeSessionRefPayload { TradeSessionId = sessionId }).GetAwaiter().GetResult();
                            _pendingTradeRequests.RemoveAll(t => t.SessionId == sessionId);
                        },
                    });
                }
                foreach (var (charId, info) in _nearbyPlayers())
                {
                    string targetId = charId;
                    string nick = info.Nickname;
                    rows.Add(new Row
                    {
                        Text = $"Pedir trade a {info.Nickname}",
                        OnEnter = () =>
                        {
                            _ws.SendAsync("trade_request", new TradeRequestPayload { TargetCharacterId = targetId }).GetAwaiter().GetResult();
                            _trade = new ActiveTrade { SessionId = "", OtherNickname = nick };
                            _status = $"Esperando que {nick} acepte...";
                            _statusIsError = false;
                        },
                    });
                }
                break;

            case Tab.Battle:
                foreach (var (sessionId, fromCharId, fromNick) in _pendingBattleChallenges)
                {
                    rows.Add(new Row
                    {
                        Text = $"Desafío de {fromNick}  (Enter: aceptar, Retroceso: rechazar)",
                        R = 1f, G = 0.85f, B = 0.3f,
                        OnEnter = () =>
                        {
                            _ws.SendAsync("battle_accept", new BattleSessionRefPayload { BattleSessionId = sessionId }).GetAwaiter().GetResult();
                            _pendingBattleChallenges.RemoveAll(b => b.SessionId == sessionId);
                            IsActive = false; // battle_start va a activar BattleScreen; el panel no debe tapar la pantalla de batalla
                        },
                        OnBack = () =>
                        {
                            _ws.SendAsync("battle_decline", new BattleSessionRefPayload { BattleSessionId = sessionId }).GetAwaiter().GetResult();
                            _pendingBattleChallenges.RemoveAll(b => b.SessionId == sessionId);
                        },
                    });
                }
                foreach (var (charId, info) in _nearbyPlayers())
                {
                    string targetId = charId;
                    rows.Add(new Row
                    {
                        Text = $"Retar a batalla a {info.Nickname}",
                        OnEnter = () => _ws.SendAsync("battle_challenge", new BattleChallengePayload { TargetCharacterId = targetId }).GetAwaiter().GetResult(),
                    });
                }
                break;

            case Tab.Market:
                if (_sellingPickingPokemon)
                {
                    foreach (var mon in _myPokemon)
                    {
                        var pokemon = mon;
                        rows.Add(new Row
                        {
                            Text = $"#{pokemon.SpeciesId} {pokemon.Nickname} Nv.{pokemon.Level}",
                            OnEnter = () =>
                            {
                                _sellingPickingPokemon = false;
                                _sellingEnteringPrice = true;
                                _sellingPokemon = pokemon;
                                _priceInput.Clear();
                            },
                        });
                    }
                    break;
                }
                foreach (var l in _myMarketListings)
                {
                    var listing = l;
                    rows.Add(new Row
                    {
                        Text = $"[mío] #{listing.Pokemon.SpeciesId} {listing.Pokemon.Nickname} Nv.{listing.Pokemon.Level} — {listing.Price}  (Retroceso: cancelar)",
                        R = 0.6f, G = 0.85f, B = 1f,
                        OnBack = () => _ws.SendAsync("market_cancel", new MarketListingRefPayload { ListingId = listing.ListingId }).GetAwaiter().GetResult(),
                    });
                }
                foreach (var l in _marketListings)
                {
                    var listing = l;
                    if (listing.SellerCharId == _myCharacterId) continue; // ya está arriba como "[mío]"
                    rows.Add(new Row
                    {
                        Text = $"#{listing.Pokemon.SpeciesId} {listing.Pokemon.Nickname} Nv.{listing.Pokemon.Level} — {listing.Price} (de {listing.SellerNickname})",
                        OnEnter = () => _ws.SendAsync("market_buy", new MarketListingRefPayload { ListingId = listing.ListingId }).GetAwaiter().GetResult(),
                    });
                }
                break;

            case Tab.Guild:
                foreach (var (guildId, guildName, fromCharId, fromNick) in _pendingGuildInvites)
                {
                    rows.Add(new Row
                    {
                        Text = $"Invitación de {fromNick} al gremio {guildName}  (Enter: aceptar, Retroceso: rechazar)",
                        R = 1f, G = 0.85f, B = 0.3f,
                        OnEnter = () => { _ws.SendAsync("guild_accept", new GuildRefPayload { GuildId = guildId }).GetAwaiter().GetResult(); _pendingGuildInvites.RemoveAll(g => g.GuildId == guildId); },
                        OnBack = () => { _ws.SendAsync("guild_decline", new GuildRefPayload { GuildId = guildId }).GetAwaiter().GetResult(); _pendingGuildInvites.RemoveAll(g => g.GuildId == guildId); },
                    });
                }
                if (_guild != null)
                {
                    bool isLeader = false;
                    foreach (var m in _guild.Members) if (m.CharacterId == _myCharacterId) isLeader = m.IsOfficer;

                    foreach (var m in _guild.Members)
                    {
                        var member = m;
                        bool canKick = isLeader && member.CharacterId != _myCharacterId;
                        rows.Add(new Row
                        {
                            Text = $"[{(member.Online ? "conectado" : "desconectado")}] {member.Nickname}{(member.IsOfficer ? " (líder)" : "")}{(canKick ? "  (Retroceso: expulsar)" : "")}",
                            R = member.Online ? 0.4f : 0.6f, G = member.Online ? 1f : 0.6f, B = member.Online ? 0.4f : 0.6f,
                            OnBack = canKick ? () => _ws.SendAsync("guild_kick", new GuildKickPayload { TargetCharacterId = member.CharacterId }).GetAwaiter().GetResult() : null,
                        });
                    }
                    if (isLeader)
                    {
                        foreach (var (charId, info) in _nearbyPlayers())
                        {
                            bool alreadyMember = false;
                            foreach (var m in _guild.Members) if (m.CharacterId == charId) alreadyMember = true;
                            if (alreadyMember) continue;
                            string targetId = charId;
                            rows.Add(new Row
                            {
                                Text = $"Invitar a {info.Nickname}",
                                OnEnter = () => _ws.SendAsync("guild_invite", new GuildInvitePayload { TargetCharacterId = targetId }).GetAwaiter().GetResult(),
                            });
                        }
                    }
                    rows.Add(new Row
                    {
                        Text = "-- Salir del gremio --",
                        R = 1f, G = 0.5f, B = 0.5f,
                        OnEnter = () => { _ws.SendAsync("guild_leave", new { }).GetAwaiter().GetResult(); _guild = null; },
                    });
                }
                else
                {
                    rows.Add(new Row
                    {
                        Text = "-- Fundar gremio --",
                        R = 0.4f, G = 1f, B = 0.4f,
                        OnEnter = () => { _creatingGuild = true; _guildNameInput.Clear(); },
                    });
                }
                break;

            case Tab.Appearance:
                string current = _myColor();
                foreach (var (name, label, r, g, b) in SpriteColors.Presets)
                {
                    bool selected = name == current;
                    rows.Add(new Row
                    {
                        Text = selected ? $"[{label}]" : label,
                        R = r > 1f ? 1f : r, G = g > 1f ? 1f : g, B = b > 1f ? 1f : b,
                        OnEnter = () => _ws.SendAsync("set_color", new SetColorPayload { Color = name }).GetAwaiter().GetResult(),
                    });
                }
                break;
        }
        return rows;
    }

    public void Draw(Renderer renderer, float windowWidth, float windowHeight)
    {
        if (!IsActive) return;

        const float PanelX = 40f, PanelY = 40f, PanelWidth = 560f;
        float lineH = renderer.TextLineHeight;
        float y = PanelY;

        // Fondo de la tarjeta (paleta pedida: fondo casi negro, borde/título en cian
        // primario) — antes este panel era texto flotando sobre el juego sin ningún
        // contenedor visual, ver Theme.cs para la paleta completa.
        renderer.AddRect(PanelX - 16f, PanelY - 16f, PanelWidth, windowHeight - PanelY, Theme.Background.R, Theme.Background.G, Theme.Background.B, 0.88f);
        renderer.AddRect(PanelX - 16f, PanelY - 16f, PanelWidth, 3f, Theme.Primary.R, Theme.Primary.G, Theme.Primary.B, 1f);

        renderer.AddText("PANEL SOCIAL", PanelX, y, Theme.Primary.R, Theme.Primary.G, Theme.Primary.B, 1f, 1.3f); y += lineH * 1.8f;

        string[] tabNames = ["Amigos", "Grupo", "Comercio", "Batalla", "Mercado", "Gremio", "Apariencia"];
        var tabLine = new StringBuilder();
        for (int i = 0; i < tabNames.Length; i++)
        {
            tabLine.Append(i == (int)_tab ? $"[{tabNames[i]}]" : $" {tabNames[i]} ");
            if (i < tabNames.Length - 1) tabLine.Append("  ");
        }
        renderer.AddText(tabLine.ToString(), PanelX, y, 1f, 1f, 1f, 0.95f); y += lineH * 1.6f;

        if (_addingFriend)
        {
            renderer.AddText($"Usuario a agregar: {_friendInput}_", PanelX, y, 0.4f, 1f, 0.4f, 1f);
            y += lineH * 1.6f;
        }
        else if (_sellingEnteringPrice)
        {
            renderer.AddText($"Vendiendo #{_sellingPokemon?.SpeciesId} {_sellingPokemon?.Nickname} Nv.{_sellingPokemon?.Level}", PanelX, y, 0.85f, 0.85f, 0.85f, 1f);
            y += lineH;
            renderer.AddText($"Precio: {_priceInput}_", PanelX, y, 0.4f, 1f, 0.4f, 1f);
            y += lineH * 1.6f;
        }
        else if (_creatingGuild)
        {
            renderer.AddText($"Nombre del gremio: {_guildNameInput}_", PanelX, y, 0.4f, 1f, 0.4f, 1f);
            y += lineH * 1.6f;
        }
        else
        {
            if (_tab == Tab.Guild && _guild != null)
            {
                renderer.AddText($"Gremio: {_guild.Name}", PanelX, y, 1f, 0.85f, 0.3f, 1f);
                y += lineH * 1.4f;
            }
            var rows = BuildRows();
            if (rows.Count == 0)
            {
                renderer.AddText("(nada por acá todavía)", PanelX, y, 0.6f, 0.6f, 0.6f, 0.8f);
                y += lineH;
            }
            for (int i = 0; i < rows.Count; i++)
            {
                bool sel = i == _selection;
                string prefix = sel ? "> " : "  ";
                var row = rows[i];
                if (sel)
                    renderer.AddRect(PanelX - 4f, y - 1f, PanelWidth - 24f, lineH, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 0.25f);
                renderer.AddText(prefix + row.Text, PanelX, y, row.R, row.G, row.B, 1f);
                y += lineH;
            }
            y += lineH * 0.5f;
        }

        if (_tab == Tab.Trade && _trade != null)
        {
            y += lineH * 0.3f;
            renderer.AddText($"Trade con {_trade.OtherNickname}: {(_trade.Accepted ? "en curso" : "esperando aceptación")}", PanelX, y, 0.8f, 0.8f, 1f, 1f);
            y += lineH;
            renderer.AddText($"Tu oferta: {(_trade.MyOffer != null ? $"#{_trade.MyOffer.SpeciesId} Nv.{_trade.MyOffer.Level}" : "(nada todavía)")}", PanelX, y, 0.85f, 0.85f, 0.85f, 1f);
            y += lineH;
            renderer.AddText($"Su oferta: {(_trade.TheirOffer != null ? $"#{_trade.TheirOffer.SpeciesId} Nv.{_trade.TheirOffer.Level}" : "(nada todavía)")}", PanelX, y, 0.85f, 0.85f, 0.85f, 1f);
            y += lineH * 1.5f;
        }

        string hint = _tab switch
        {
            Tab.Friends => "Izq/Der: pestaña  Arr/Abajo: elegir  Enter/Retroceso: aceptar/rechazar  F4: agregar por usuario  F5: cerrar",
            Tab.Party => "Izq/Der: pestaña  Arr/Abajo: elegir  Enter: invitar/aceptar  Retroceso: rechazar/salir del grupo  F5: cerrar",
            Tab.Trade => "Izq/Der: pestaña  Arr/Abajo: elegir  Enter: pedir/aceptar/ofrecer  C: confirmar  Retroceso: cancelar  F5: cerrar",
            Tab.Battle => "Izq/Der: pestaña  Arr/Abajo: elegir  Enter: retar/aceptar  Retroceso: rechazar  F5: cerrar",
            Tab.Market => _sellingPickingPokemon
                ? "Arr/Abajo: elegir  Enter: elegir este  Escape: cancelar"
                : "Izq/Der: pestaña  Arr/Abajo: elegir  Enter: comprar  Retroceso: cancelar publicación propia  F4: vender  F5: cerrar",
            Tab.Guild => "Izq/Der: pestaña  Arr/Abajo: elegir  Enter: fundar/invitar/aceptar/salir  Retroceso: rechazar/expulsar  F5: cerrar",
            Tab.Appearance => "Izq/Der: pestaña  Arr/Abajo: elegir  Enter: aplicar color  F5: cerrar",
            _ => "",
        };
        renderer.AddText(hint, PanelX, windowHeight - 28f, 0.6f, 0.6f, 0.6f, 0.8f);

        if (_status.Length > 0)
        {
            (float r, float g, float b) = _statusIsError ? (1f, 0.35f, 0.35f) : (0.4f, 1f, 0.4f);
            renderer.AddText(_status, PanelX, y, r, g, b, 1f);
        }
    }
}
