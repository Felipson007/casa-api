-- Real login (password + Google OAuth) replaces the raw-user-id-as-bearer-token placeholder that
-- AuthMiddleware's own doc comment flagged as "a template; swap for a verifier when real auth
-- lands". A session is an opaque random token (hashed at rest, same pattern as
-- password_reset_tokens) handed to the browser as an HttpOnly cookie; AuthMiddleware resolves it
-- to a user instead of trusting a client-supplied id directly.
CREATE TABLE sessions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_user_id uuid NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    token_hash  varchar(255) NOT NULL,
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_sessions_token_hash ON sessions (token_hash);
CREATE INDEX idx_sessions_app_user_id ON sessions (app_user_id);
