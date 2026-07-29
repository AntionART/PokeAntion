-- Verificación de email (opcional, no bloqueante): una cuenta sigue siendo 100% usable sin
-- verificar (no se rechaza login/registro por esto) — es una mejora de confianza/recuperación
-- de cuenta, no un gate de acceso. El token se genera en el registro y se manda por correo
-- (internal/email.Sender: SMTP real si está configurado, si no un fallback que solo loguea el
-- link en la consola del servidor — ver internal/config.Config).
ALTER TABLE accounts ADD COLUMN email_verified BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE accounts ADD COLUMN email_verify_token VARCHAR(64);
ALTER TABLE accounts ADD COLUMN email_verify_sent_at TIMESTAMPTZ;
