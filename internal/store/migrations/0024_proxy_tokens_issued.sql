-- Authentication stays on once any proxy token has been issued, so revoking
-- the last one cannot open the gateway. The token table cannot say that on its
-- own -- it is empty both before the first token and after the last is
-- revoked -- so a settings row records it. A database that already holds a
-- token has issued one; one whose tokens were all revoked before this
-- migration cannot be told apart from a fresh install and stays open.
INSERT OR IGNORE INTO settings (key, value)
SELECT 'proxy_tokens.issued', '1'
 WHERE EXISTS (SELECT 1 FROM proxy_tokens);
