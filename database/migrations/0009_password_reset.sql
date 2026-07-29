-- Recuperación de contraseña por email — mismo patrón que 0008_email_verification.sql (token
-- de un solo uso + timestamp de envío), pero acá el token SÍ expira (ver auth.resetTokenTTL):
-- un link de verificación de cuenta que quede vivo para siempre es una molestia menor, un link
-- que resetea la contraseña de otra persona si se filtra/reenvía por accidente es un problema
-- real, así que se le pone un vencimiento corto.
ALTER TABLE accounts ADD COLUMN password_reset_token VARCHAR(64);
ALTER TABLE accounts ADD COLUMN password_reset_sent_at TIMESTAMPTZ;
