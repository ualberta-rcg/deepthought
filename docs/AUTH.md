# DeepThought Authentication, 2FA (DRAC OIDC), and CLI Browser-Link Flow

This document contains everything needed to register `deepthought.vulcan.alliancecan.ca` as an OpenID Connect (OIDC) Relying Party with the Digital Research Alliance of Canada (DRAC) identity team (`idp.alliancecan.ca`), how the 2FA/SSO and CLI Browser-Link login flows work, and how to switch OIDC on once the client credentials are issued.

---

## 1. What We Already Know (Discovered from Vulcan's Open OnDemand Setup)

DeepThought's OIDC implementation (`deepthought-server/server/oidc.go` and `deepthought-server/server/config.go`) is modeled directly after Vulcan's existing Open OnDemand portal (`/etc/ood/config/ood_portal.yml`):

| Setting | Value on Vulcan (`vulcan.alliancecan.ca`) | Pre-configured Value in `deepthought-server` |
|---|---|---|
| **Identity Provider (Issuer)** | `https://idp.alliancecan.ca` | `https://idp.alliancecan.ca` |
| **OIDC Discovery Metadata URL** | `https://idp.alliancecan.ca/.well-known/openid-configuration` | `https://idp.alliancecan.ca/.well-known/openid-configuration` |
| **OAuth2 / OIDC Grant Type** | `authorization_code` (`response_type=code`) | `authorization_code` + PKCE (`S256`) |
| **Requested Scopes** | `openid profile email` | `openid profile email` |
| **Username Claim (`remote_user_claim`)** | `preferred_username` (Alliance CCDB username, e.g. `rahimk`) | `preferred_username` |
| **Email Claim (`email_claim`)** | `email` | `email` |
| **Group Claim (`groups_claim`)** | `groups` (optional filter) | `groups` |
| **Service Hostname / Proposed Client ID** | `vulcan.alliancecan.ca` | `deepthought.vulcan.alliancecan.ca` |
| **Callback / Redirect URI** | `https://vulcan.alliancecan.ca/oidc` | `https://deepthought.vulcan.alliancecan.ca/oidc` (also accepts `/api/v1/auth/oidc/callback`) |
| **Post-Login Email Hook** | `/opt/ood/scripts/ood_pun_oidc_email.sh` | `/opt/ood/scripts/ood_pun_oidc_email.sh` (or `scripts/oidc_post_login.sh`) |

---

## 2. What to Send / Ask the Auth (IdP) Team

You can copy-paste the following request to the Alliance / UAlberta Research Computing Identity & Access Management (IAM) team:

> **Subject:** OIDC Client Registration Request for `deepthought.vulcan.alliancecan.ca` (Vulcan Cluster)
>
> Hi team,
>
> Could you please register a new OpenID Connect (OIDC) Relying Party client on `https://idp.alliancecan.ca` for the DeepThought service on Vulcan, with the same claim and 2FA/MFA profile as `vulcan.alliancecan.ca` (Open OnDemand)?
>
> **Service Details:**
> - **Application / Service Name:** DeepThought (Vulcan Research Computing Harness)
> - **Proposed `client_id`:** `deepthought.vulcan.alliancecan.ca`
> - **Application Type:** Confidential Web Application (OIDC Authorization Code flow with PKCE `S256`)
> - **Issuer / Discovery URL:** `https://idp.alliancecan.ca/.well-known/openid-configuration`
> - **Redirect URI(s) to allowlist:**
>   - `https://deepthought.vulcan.alliancecan.ca/oidc`
>   - `https://deepthought.vulcan.alliancecan.ca/api/v1/auth/oidc/callback`
> - **Post-Logout Redirect URI (optional):**
>   - `https://deepthought.vulcan.alliancecan.ca/`
> - **Grant Types:** `authorization_code` (and `refresh_token` if standard)
> - **Response Type:** `code`
> - **Scopes Requested:** `openid profile email`
> - **Required Claims in ID Token / UserInfo:**
>   - `preferred_username` (Alliance username, matching POSIX/Slurm UID on Vulcan)
>   - `email`
>   - `sub`
>   - `groups` (if group membership claims are released)
>
> **What we need back from you:**
> 1. Confirmed `client_id` (e.g., `deepthought.vulcan.alliancecan.ca`)
> 2. The generated `client_secret` (delivered securely)
> 3. Confirmation of whether any specific group claim filter (e.g., Vulcan cluster access group) is recommended on the Relying Party side or enforced at the IdP.

---

## 3. What We Need Back from the Auth Team

To turn on OIDC + 2FA in production, we only need **two values** from them:

1. **`client_id`** — expected to be `deepthought.vulcan.alliancecan.ca` (already set as the default in `deepthought-server/server/config.go`).
2. **`client_secret`** — the confidential client secret issued by `idp.alliancecan.ca`.

Everything else (discovery URL `https://idp.alliancecan.ca/.well-known/openid-configuration`, JWKS RSA/ECDSA key rotation, PKCE `S256`, `/oidc` callback handler, and `preferred_username` / `email` extraction) is already implemented and tested in `deepthought-server`.

---

## 4. How Authentication Works (Text Blurb / Architecture Summary)

> **How DeepThought Authentication & 2FA Works**
>
> DeepThought uses `deepthought.vulcan.alliancecan.ca` as a central identity and coordination hub for both the **Web Console** and the **SSH/Terminal CLI (`deepthought-cli`)** across clusters:
>
> 1. **Web Console Login (SSO + 2FA via `idp.alliancecan.ca`):**
>    When a researcher opens `https://deepthought.vulcan.alliancecan.ca` and clicks **Sign in with SSO**, the server initiates a standard **OIDC Authorization Code + PKCE (`S256`)** flow against `https://idp.alliancecan.ca`. The user authenticates with their Alliance / institutional credentials and completes 2FA in their browser. Upon callback to `https://deepthought.vulcan.alliancecan.ca/oidc`, DeepThought verifies the cryptographic signature of the `id_token` against the IdP's JWKS keys, extracts `preferred_username` (their POSIX/Slurm cluster username) and `email`, records the identity in MySQL, and issues a 24-hour sliding bearer session token (stored only as a SHA-256 hash on the server).
>
> 2. **CLI Browser-Link Login (Cursor / Antigravity-Style Device Flow):**
>    When a user launches `deepthought-cli` over SSH on a login node (or laptop) and selects **`[2P] MULTIPLAYER · Log in to server`** (or `/sync` → **Sign in via Browser Link**), the CLI never asks them to type their institutional password or 2FA code into the terminal:
>    - The CLI calls `POST /api/v1/auth/device/start` on `deepthought.vulcan.alliancecan.ca` and receives a short-lived `device_code`, human-readable verification code (e.g. `ABCD-1234`), and browser link.
>    - The CLI automatically copies the browser URL to the user's local clipboard (via terminal OSC 52) and displays the link on screen while polling `POST /api/v1/auth/device/poll` every 2 seconds.
>    - The user opens the link in their browser:
>      - **In OIDC/2FA mode:** the link immediately redirects through `https://idp.alliancecan.ca` (`GET /api/v1/auth/oidc/login?device_code=...`). As soon as 2FA succeeds in the browser, the callback marks the CLI's `device_code` approved with a fresh session token bound to their `preferred_username`.
>      - **In Hybrid / Web-Session mode:** if the user is already signed into the DeepThought Web UI, they see a one-click **"Approve CLI Session"** banner (`POST /api/v1/auth/device/approve`).
>    - On the next 2-second poll, the terminal CLI receives its bearer token, transitions to `Connected as <user> @ deepthought.vulcan.alliancecan.ca`, syncs encrypted provider credentials and settings, and joins the user's live Fleet fabric.

---

## 5. How to Enable OIDC on the Server Once Credentials Arrive

By default, `deepthought-server` ships with `auth.mode = "password"` and `auth.oidc.enabled = false` so existing password login continues working until the IdP client secret is provisioned.

Once the auth team provides the `client_secret`, create or update the Kubernetes secret in the `deepthought` namespace and enable OIDC via environment variables (or `server.json`):

```bash
kubectl -n deepthought create secret generic deepthought-oidc \
  --from-literal=client-secret='<CLIENT_SECRET_FROM_AUTH_TEAM>'
```

And set on the `deepthought-server` Deployment:

| Environment Variable | Value |
|---|---|
| `DEEPTHOUGHT_AUTH_MODE` | `hybrid` (allows both OIDC 2FA and fallback password during rollout) or `oidc` (OIDC 2FA only) |
| `DEEPTHOUGHT_OIDC_ENABLED` | `true` |
| `DEEPTHOUGHT_OIDC_ISSUER` | `https://idp.alliancecan.ca` |
| `DEEPTHOUGHT_OIDC_METADATA_URL` | `https://idp.alliancecan.ca/.well-known/openid-configuration` |
| `DEEPTHOUGHT_OIDC_CLIENT_ID` | `deepthought.vulcan.alliancecan.ca` |
| `DEEPTHOUGHT_OIDC_CLIENT_SECRET` | *(from `deepthought-oidc` secret)* |
| `DEEPTHOUGHT_OIDC_REDIRECT_URI` | `https://deepthought.vulcan.alliancecan.ca/oidc` |
