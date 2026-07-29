// Package email manda correos de verificación de cuenta. Deliberadamente chico: un Sender real
// (SMTP, compatible con Gmail) y uno de consola (para desarrollo/este entorno, donde no hay
// credenciales SMTP reales configuradas) — mismo criterio que internal/ratelimit con Redis: si
// la pieza de infraestructura externa no está configurada, el resto del sistema sigue
// funcionando (acá: el registro nunca falla por no poder mandar un correo).
package email

import (
	"fmt"
	"log/slog"
	"net/smtp"
)

// Sender abstrae el envío de un correo — implementado por SMTPSender (real) y ConsoleSender
// (fallback de desarrollo). auth.Service solo conoce esta interfaz, nunca SMTP directamente.
type Sender interface {
	Send(to, subject, body string) error
}

// ConsoleSender no manda nada de verdad — loguea el correo (asunto + cuerpo, que incluye el
// link de verificación) a stdout. Es el default cuando no hay SMTP_HOST configurado: registrar
// una cuenta sigue funcionando de punta a punta en un entorno de desarrollo sin credenciales de
// correo reales, y el link de verificación queda visible igual (en el log del servidor) para
// poder probar el flujo completo a mano.
type ConsoleSender struct{}

func (ConsoleSender) Send(to, subject, body string) error {
	slog.Info("email (modo consola: SMTP_HOST no configurado, no se manda de verdad)",
		"component", "email", "to", to, "subject", subject, "body", body)
	return nil
}

// SMTPSender manda correos reales vía SMTP con autenticación (STARTTLS automático si el
// servidor lo ofrece, que es el caso de Gmail en el puerto 587 — ver smtp.SendMail). Gmail
// requiere una "contraseña de aplicación" (no la contraseña real de la cuenta) desde que
// desactivó el acceso de "apps menos seguras": https://myaccount.google.com/apppasswords.
type SMTPSender struct {
	Host, Port, Username, Password, From string
}

func (s SMTPSender) Send(to, subject, body string) error {
	addr := s.Host + ":" + s.Port
	auth := smtp.PlainAuth("", s.Username, s.Password, s.Host)
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n",
		s.From, to, subject, body)
	return smtp.SendMail(addr, auth, s.From, []string{to}, []byte(msg))
}
