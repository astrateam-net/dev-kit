# jetbroker

> A module of [**DevKit**](../README.md) — extension modules for [Coder](https://coder.com).
>
> **Unofficial, community project.** Not affiliated with or endorsed by Coder Technologies or
> Devolutions.

`jetbroker` is an extensible **browser-RDP authority** that drives a
[Devolutions Gateway](https://github.com/Devolutions/devolutions-gateway). It is the portable
"brains" that turns a Coder workspace into a one-click, fully in-browser RDP session — with the real
target password **never** leaving the server. Per launch it:

- selects a live, least-loaded gateway from the workspace's declared farm (heartbeat-based),
- mints provisioner-signed gateway (**Jet**) tokens (RS256),
- injects the real RDP credential into that gateway **server-to-server** via its `/jet/preflight`
  endpoint (so the password never reaches the browser),
- and hands the browser only a one-time association token + a synthetic proxy credential.

It does, driven by Coder identity, roughly what DVLS does toward a Devolutions Gateway. The name
comes from the gateway's own **Jet** protocol namespace (`/jet/*` routes, `jet_gw_id` claims): this
is a broker of Jet tokens and sessions.

## Design — ports & adapters

The core (package `jetbroker`) is platform-agnostic. It depends only on four small interfaces in
[ports.go](ports.go); a host platform supplies the adapters. Keeping the seams thin is what lets the
same core survive a migration to another host platform.

The Coder integration (the adapters + an HTTP route) lives in a separate codebase (a Coder fork) and
is not part of this repository. [cmd/test-authority](../cmd/test-authority) is a dev-only harness
that backs the four interfaces with environment variables, so you can exercise the whole chain
end-to-end against a real gateway and a real Windows host without Coder.

### The four seams

A host adapter implements these (see [ports.go](ports.go) for the full contract):

| Interface | Returns | Host adapter reads… |
| --- | --- | --- |
| `IdentityResolver` | the verified caller (`DOMAIN\user`) | the authenticated, owner-gated session user, mapped via OIDC claims |
| `TargetResolver` | the RDP target host | the workspace's own declared target, by workspace id (never a URL param) |
| `GatewayResolver` | the gateway farm | the workspace's declared `{url, weight}` list, by workspace id |
| `SecretStore` | the RDP password | a workspace-bound credential (e.g. an `rdp_password` parameter), server-side by workspace id |

The credential is bound to the **workspace**, not the calling user — a shared/service account works,
and access is gated upstream by the host's ownership check.

## The launch flow

```
1. identity   ← host session (already SSO-verified)                       [IdentityResolver]
2. target     ← workspace's declared host (NOT a URL param); :3389 defaulted   [TargetResolver]
3. password   ← workspace-bound credential, read server-side              [SecretStore]
4. proxy cred = two fresh UUIDs (synthetic, per session)
5. target cred = DOMAIN\user + the real password
6. choose gw  ← resolve the workspace's farm, heartbeat each, pick        [GatewayResolver]
                live + least-loaded (its id, from /jet/heartbeat, becomes jet_gw_id)
7. mint association (cty=ASSOCIATION, jet_gw_id=chosen) + scope (cty=SCOPE), RS256, provisioner key
8. POST {chosen}/jet/preflight?token=<scope> provision-credentials   (creds → gateway, not browser)
9. mint webapp login token (cty=WEBAPP) — keeps the gateway-webapp's session-expiration check happy
10. return { gateway_url=wss://{chosen}/jet/rdp, association_token, proxy_username, proxy_password,
             target, webapp_token, kdc_proxy_url? }
```

The browser player then connects with
`withProxyAddress(gateway_url).withAuthToken(association_token).withDestination(target).withUsername(proxy_username).withPassword(proxy_password)`
— and, for domain targets, `kdcProxyUrl(kdc_proxy_url)`. The gateway injects the real credential into
NLA server-to-server and forwards to Windows.

### Gateway farm — there is no single configured gateway

The gateway is **per-workspace**: the RDP module declares its farm, so the core takes the list per
launch (`GatewayResolver`) and selects one member — authority-side, not a load balancer:

- a token binds to one gateway by `jet_gw_id` (the gateway rejects a mismatch), and preflight stores
  the credential in that instance's memory keyed by the token `jti` — so the player **must** reach
  the exact instance we preflighted;
- liveness and load come from `GET /jet/heartbeat` (scope `gateway.heartbeat.read`): it returns the
  gateway's `id` and `running_session_count`;
- selection drops dead / zero-weight members, then picks the one most under-loaded relative to its
  weight, ties broken at random. Checked **before** the mint; there is no post-mint failover.

### What the browser receives (`LaunchResult`)

Only short-lived, single-use material — never the real password:

| field | json | meaning |
| --- | --- | --- |
| `GatewayURL` | `gateway_url` | `wss`/`ws` base + `/jet/rdp` |
| `AssociationToken` | `association_token` | signed JWT (`cty=ASSOCIATION`), carried in the RDCleanPath PDU |
| `ProxyUsername` / `ProxyPassword` | `proxy_username` / `proxy_password` | synthetic per-session GUIDs |
| `Target` | `target` | the RDP host (port defaulted to 3389 inside the token only) |
| `WebAppToken` | `webapp_token` | WEBAPP login token (`cty=WEBAPP`); not a credential |
| `KdcProxyURL` | `kdc_proxy_url` | KDC-proxy URL; **present only for domain (Kerberos) targets** |

In redirect mode the handler 302-redirects to the chosen gateway's player page with the result
encoded as `base64url(JSON)` in the URL **fragment** — client-side only, never sent to the gateway,
and not subject to CORS for a top-level navigation. JSON mode returns the same object as the body.

## Dev harness — `cmd/test-authority`

A dev-only stand-in for the host adapters: it reads identity / target / password / gateway from a
`.env` file and runs the **real** core (same minting, selection, preflight, descriptor), so you can
point a browser at a real gateway + Windows host end-to-end, without a host platform. See
[cmd/test-authority/.env.example](../cmd/test-authority/.env.example) for the variables.

```sh
set -a; . ./cmd/test-authority/.env; set +a
go run ./cmd/test-authority/
# open the printed URL in a browser (top-level navigation, NOT fetch)
```

A live heartbeat check against a real gateway is opt-in (skipped unless its env is set):

```sh
set -a; . ./cmd/test-authority/.env; set +a
DGW_EXPECT_SESSIONS=1 go test -run TestGatewayHeartbeatLive -v ./jetbroker
```
