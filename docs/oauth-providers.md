# Sign-in providers

vig signs users in with Google, GitHub, Discord and a Solana wallet (Sign-In-With-Solana). The GitHub App is separate: it grants audit access to private repositories and is not a sign-in method. The Google and GitHub OAuth apps are ones forefy already owns, with vig's callbacks added.

Every provider is optional. An unconfigured provider answers 503 on its routes and the login page hides its button.

## How the flow works

- Start: `GET /v1/auth/{provider}/start` redirects the browser to the provider.
- Callback: `GET /v1/auth/{provider}/callback` on the **backend** origin (`BACKEND_URL`) exchanges the code, finds or creates the user (provider id, then verified email, then provision), sets the HttpOnly session cookies and redirects to `FRONTEND_URL`.
- Linking a second provider to a signed-in user inserts an `identities` row; nothing is overwritten.

Callback URLs to register per environment:

| Env | Callback |
|---|---|
| Local | `http://localhost:7112/v1/auth/{google,github,discord}/callback` |
| Prod | `https://vig.forefy.com/v1/auth/{google,github,discord}/callback` |

Values go in `secrets/.env.backend.local` (then `make encrypt-env`) locally and in the `vig-secrets` Secret in prod (keys listed in `deploy/chart/values.yaml`).

## Google

Console: https://console.cloud.google.com/apis/credentials

1. APIs & Services, Credentials, Create Credentials, OAuth 2.0 Client ID, type Web application (reuse forefy's existing client).
2. Authorized redirect URIs: add `http://localhost:7112/v1/auth/google/callback` and `https://vig.forefy.com/v1/auth/google/callback`.
3. Scopes: `openid email profile`.

```bash
GOOGLE_CLIENT_ID=
GOOGLE_CLIENT_SECRET=
```

## GitHub (OAuth App)

Console: https://github.com/settings/developers, OAuth Apps.

1. Reuse forefy's OAuth App or create one. Homepage `https://vig.forefy.com`.
2. Authorization callback URL: a GitHub OAuth App accepts one callback, so local and prod need two apps: `http://localhost:7112/v1/auth/github/callback` and `https://vig.forefy.com/v1/auth/github/callback`.
3. Scope requested: `read:user user:email`.

```bash
GITHUB_OAUTH_CLIENT_ID=
GITHUB_OAUTH_CLIENT_SECRET=
```

## Discord

Console: https://discord.com/developers/applications

1. New Application. OAuth2, Redirects: `http://localhost:7112/v1/auth/discord/callback` and `https://vig.forefy.com/v1/auth/discord/callback`.
2. Scopes requested at sign-in: `identify email`.
3. For notifications and the support channel (deferred integration, UI states exist): Bot, Reset Token, invite the bot to the guild with `bot` scope and Send Messages permission. `DISCORD_GUILD_ID` is the server id (Developer Mode, copy id), `DISCORD_INVITE_URL` a permanent invite link shown to users.

```bash
DISCORD_CLIENT_ID=
DISCORD_CLIENT_SECRET=
DISCORD_BOT_TOKEN=
DISCORD_GUILD_ID=
DISCORD_INVITE_URL=
```

## Sign-In-With-Solana

No console, no variables. The browser calls `POST /v1/auth/siws/nonce` with the wallet public key, gets a message to sign whose domain and URI are derived from `FRONTEND_URL`, signs it with the wallet adapter and posts the signature to `POST /v1/auth/siws/verify`. Nonces expire after 10 minutes and are single use; the nonce route is limited to 10 requests per minute per IP. The same wallet is what ownership proof and credit purchases use, so sign in with the wallet that deployed the program when you can.

`VITE_RPC_URL` (frontend) selects the RPC the wallet adapter talks to; the default is the public mainnet endpoint.

## GitHub App (private repositories)

Deferred by decision until the UI is complete; the routes and the UI states exist against the real shapes. One App for the org, installed by users on the repositories they want audited. Installation tokens are minted per job, last an hour and are never stored.

Console: https://github.com/organizations/forefy/settings/apps, New GitHub App.

1. Name and slug: the slug goes in `GITHUB_APP_SLUG` and builds the install link the UI shows.
2. Homepage `https://vig.forefy.com`. Callback URL is not used (the App is not a sign-in method). Leave "Request user authorization (OAuth) during installation" off.
3. Setup URL: `https://vig.forefy.com/v1/github/setup`, "Redirect on update" on. GitHub sends the user there with `installation_id` and `setup_action` after installing; the route records the installation against the signed-in user and returns them to the app.
4. Webhook: active, URL `https://vig.forefy.com/v1/github/webhook`, secret `GITHUB_APP_WEBHOOK_SECRET`. Locally, use a tunnel (any free one) to `http://localhost:7112/v1/github/webhook`.
5. Repository permissions: Contents read, Metadata read. Subscribe to events: Installation, Installation repositories.
6. Where can this App be installed: Any account.
7. Generate a private key; paste the PEM into `GITHUB_APP_PRIVATE_KEY` (newlines escaped as `\n` in an env file, verbatim multi-line in the Kubernetes Secret).

```bash
GITHUB_APP_ID=
GITHUB_APP_SLUG=
GITHUB_APP_PRIVATE_KEY=
GITHUB_APP_WEBHOOK_SECRET=
GITHUB_TOKEN=          # optional read-only PAT, only raises the rate limit on public tarballs
```

## Checklist for a new environment

- `FRONTEND_URL`, `BACKEND_URL` and `SITE_URL` match the origins the browser sees; in the prod image all three are the same host.
- Every provider callback above is registered for that host.
- `SESSION_SECRET` is unique to the environment (`openssl rand -hex 32`).
- `VIG_ENV=prod` so the dev login route is not registered.
