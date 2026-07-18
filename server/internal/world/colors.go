package world

// AllowedSpriteColors es la paleta predefinida que un jugador puede elegir para su personaje
// (ver client-engine/ClientApp/SpriteColors.cs para los mismos nombres, ahí también con el
// valor RGB de cada uno — el servidor solo necesita los NOMBRES para validar, el color real se
// aplica como tinte en el cliente). "default" = sin tinte, los colores naturales del sprite.
var AllowedSpriteColors = map[string]bool{
	"default": true,
	"red":     true,
	"blue":    true,
	"green":   true,
	"yellow":  true,
	"purple":  true,
	"orange":  true,
	"pink":    true,
	"cyan":    true,
}
