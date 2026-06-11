// Package jetbroker brokers browser-based RDP sessions through a Devolutions
// Gateway, driven by a host platform's identity (for example, Coder).
//
// Per launch it mints provisioner-signed gateway (Jet) tokens, injects the real
// RDP credential into the gateway via /jet/preflight (server-to-server), and
// hands the browser ONLY a one-time association token + a synthetic proxy
// credential. The real target password never reaches the browser.
//
// This is the PORTABLE CORE. Everything host-specific (who the user is, where the
// credential lives, the workspace's target host, the gateway farm) is behind the
// interfaces in ports.go, so the same core survives a migration to another host
// platform.
package jetbroker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Broker is the stateless RDP-launch broker. No password is kept at rest;
// it is read per launch and discarded.
type Broker struct {
	cfg      Config
	identity IdentityResolver
	targets  TargetResolver
	secrets  SecretStore
	gateways GatewayResolver
	http     *http.Client
}

// New builds a Broker. The four resolvers are the host-platform adapters (ports.go); all are
// required — the gateway is per-workspace (GatewayResolver), there is no deployment default.
// It returns an error when Config is incomplete (see Config.Validate) so a misconfigured
// deployment fails at boot rather than minting tokens the gateway will reject.
func New(cfg Config, id IdentityResolver, tr TargetResolver, ss SecretStore, gr GatewayResolver) (*Broker, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if id == nil || tr == nil || ss == nil || gr == nil {
		return nil, fmt.Errorf("jetbroker: identity, target, secret, and gateway resolvers are all required")
	}
	if cfg.CredentialTTL == 0 {
		cfg.CredentialTTL = 900 // mirrors the DVLS default credential time-to-live.
	}
	if cfg.HTTPClient == nil {
		// Don't reuse http.DefaultClient: it has no timeout, so a hung or black-holed
		// gateway would hang Launch indefinitely — and this is the leg that carries the
		// real AD password. Override HTTPClient to pin the gateway's (often private-CA) TLS.
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Broker{cfg: cfg, identity: id, targets: tr, secrets: ss, gateways: gr, http: cfg.HTTPClient}, nil
}

// LaunchRequest is what the host's HTTP handler passes per click.
type LaunchRequest struct {
	WorkspaceID string
}

// LaunchResult is returned to the browser/player. The real target password is
// NEVER here — only the one-time association token + synthetic proxy credential.
//
// KdcProxyURL: for a domain target the gateway presents a Kerberos acceptor to the player (the
// gateway selects Kerberos when the target credential carries a domain — always our case,
// DOMAIN\user). The browser player can only reach a KDC over MS-KKDCP, so it needs this URL;
// IronRDP feeds it into its Kerberos config. It points at the gateway's in-process fake KDC via a
// cty=KDC token whose jet_cred_id is the association jti. Empty for domainless (NTLM) targets.
type LaunchResult struct {
	GatewayURL       string `json:"gateway_url"`             // wss base + /jet/rdp
	AssociationToken string `json:"association_token"`       // signed JWT, carried in RDCleanPath
	ProxyUsername    string `json:"proxy_username"`          // synthetic GUID
	ProxyPassword    string `json:"proxy_password"`          // synthetic GUID
	Target           string `json:"target"`                  // the RDP host (no port; the gateway gets :3389 via dst_hst)
	KdcProxyURL      string `json:"kdc_proxy_url,omitempty"` // https base + /jet/KdcProxy/<KDC token>; domain targets only
	// WebAppToken is the WEBAPP login token (cty=WEBAPP). DVLS mints an equivalent token; the
	// /launch page stores it as the stock gateway-webapp session so the webapp's periodic
	// session-expiration check does NOT kill the player after ~1 min. Not a credential — just the
	// gateway-webapp auth token.
	WebAppToken string `json:"webapp_token,omitempty"`
	// Display-only rows for the session-info popover (DVLS parity). Empty => the row hides.
	GatewayName     string `json:"gateway_name,omitempty"`     // the chosen gateway's hostname (DVLS "Gateway name")
	DisplayUsername string `json:"display_username,omitempty"` // the REAL login name (e.g. "ceo") — name only, never the password
	DisplayDomain   string `json:"display_domain,omitempty"`   // e.g. "example.net"; empty for NTLM/domainless
}

// Launch is the per-click orchestration; it mirrors how DVLS generates a gateway session token.
func (b *Broker) Launch(ctx context.Context, req LaunchRequest) (*LaunchResult, error) {
	// 1. Identity — already SSO-verified by the host platform (we trust it, don't re-verify).
	id, err := b.identity.Resolve(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("jetbroker: resolve identity: %w", err)
	}

	// 2. Target — from the workspace's declared data, NOT a spoofable URL param. The module
	//    declares a HOST (no port). We keep it clean here (the player's withDestination + the
	//    session-info row both show it); the RDP port is defaulted to 3389 only inside the token's
	//    dst_hst (dstHst), where the gateway actually needs it to dial the host.
	target, err := b.targets.Resolve(ctx, req.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("jetbroker: resolve target: %w", err)
	}

	// 3. Real RDP password — read server-side, keyed by the WORKSPACE (the rdp_password param;
	// later an encrypted store / Vault-by-workspace). Wrap with a fixed string only: never include
	// the returned value in the error.
	password, err := b.secrets.Get(ctx, req.WorkspaceID, b.cfg.SecretName)
	if err != nil {
		return nil, fmt.Errorf("jetbroker: read user-secret: %w", err)
	}

	// 4. Synthetic per-session proxy credential = two GUIDs (mirrors DVLS, which uses two fresh GUIDs).
	proxy := appCredential{
		Kind:     credKindUsernamePassword,
		Username: uuid.NewString(),
		Password: uuid.NewString(),
	}

	// 5. Target credential = DOMAIN\user + the real password (server-side only).
	targetCred := appCredential{
		Kind:     credKindUsernamePassword,
		Username: b.formatUsername(id),
		Password: password,
	}

	// 6. Choose the gateway: resolve the workspace's declared farm and select a live,
	//    least-loaded member. Its heartbeat-discovered id becomes jet_gw_id; preflight and the
	//    player's gateway_url both target THIS instance (the credential mapping is per-instance).
	gw, err := b.chooseGateway(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}

	// 7. Mint association + scope tokens (provisioner key, cty header), bound to the chosen gateway.
	assoc, assocJTI, err := b.mintAssociation(target, gw.ID)
	if err != nil {
		return nil, err
	}
	scope, err := b.mintScope(gw.ID)
	if err != nil {
		return nil, err
	}

	// 8. Inject creds into the CHOSEN gateway (server-to-server; password never to browser).
	if err := b.provisionCredentials(ctx, gw.BaseURL, scope, assoc, proxy, targetCred); err != nil {
		return nil, err
	}

	// 9. Mint the WEBAPP login token (cty=WEBAPP) so the standalone gateway-webapp treats the player
	//    page as logged in — without it, the webapp's ~60s session-expiration check tears the session
	//    down after about a minute. DVLS mints an equivalent token. Subject = the real login name
	//    (informational; gateway auth is None).
	webAppToken, err := b.mintWebApp(id.Username)
	if err != nil {
		return nil, err
	}

	// 10. Hand the player only the tokens + synthetic proxy credential (+ display-only rows).
	displayDomain := id.Domain
	if displayDomain == "" {
		displayDomain = b.cfg.Realm
	}
	// The browser-facing leg may differ from the server-facing one: heartbeat/preflight (above)
	// always use gw.BaseURL, but the descriptor URLs the browser opens use gw.BrowserURL when the
	// host put one there (e.g. a front proxy in front of an internal gateway). Empty => BaseURL.
	browserBase := gw.BrowserURL
	if browserBase == "" {
		browserBase = gw.BaseURL
	}
	res := &LaunchResult{
		GatewayURL:       websocketURL(browserBase) + "/jet/rdp",
		AssociationToken: assoc,
		ProxyUsername:    proxy.Username,
		ProxyPassword:    proxy.Password,
		Target:           target,
		WebAppToken:      webAppToken,
		GatewayName:      gw.Name,
		DisplayUsername:  id.Username,
		DisplayDomain:    displayDomain,
	}

	// 11. Domain target => the gateway's client-facing leg is Kerberos, so the browser player needs
	//     a KDC proxy URL to ticket the synthetic realm. Point it at the CHOSEN gateway's in-process
	//     fake KDC via a cty=KDC token bound to this session (jet_cred_id = association jti).
	//     Domainless (NTLM) targets need none.
	if strings.Contains(targetCred.Username, `\`) {
		kdcToken, err := b.mintKdc(assocJTI, gw.ID)
		if err != nil {
			return nil, err
		}
		res.KdcProxyURL = browserBase + "/jet/KdcProxy/" + kdcToken
	}

	return res, nil
}

// chooseGateway resolves the workspace's declared farm and selects a live, least-loaded member.
// An empty farm is a misconfigured RDP module, not a server error.
func (b *Broker) chooseGateway(ctx context.Context, workspaceID string) (chosenGateway, error) {
	candidates, err := b.gateways.Resolve(ctx, workspaceID)
	if err != nil {
		return chosenGateway{}, fmt.Errorf("jetbroker: resolve gateways: %w", err)
	}
	if len(candidates) == 0 {
		return chosenGateway{}, errors.New("jetbroker: workspace declares no RDP gateways")
	}
	return b.selectGateway(ctx, candidates)
}

// websocketURL rewrites the gateway's HTTP base to the ws/wss scheme the player expects.
// The chosen gateway's base is the HTTPS API listener used for preflight; /jet/rdp is a
// WebSocket-upgrade route, and the IronRDP player's withProxyAddress wants ws/wss (DVLS does the
// same rewrite when building player URLs).
func websocketURL(base string) string {
	switch {
	case strings.HasPrefix(base, "https://"):
		return "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		return "ws://" + strings.TrimPrefix(base, "http://")
	default:
		return base
	}
}

// formatUsername builds the down-level DOMAIN\user form (or bare user), mirroring DVLS. A domain
// present => the gateway picks the Kerberos acceptor; absent => NTLM.
func (b *Broker) formatUsername(id Identity) string {
	domain := id.Domain
	if domain == "" {
		domain = b.cfg.Realm
	}
	if domain == "" {
		return id.Username // domainless => NTLM acceptor
	}
	// Preserve the domain as supplied, like DVLS (which does not uppercase). Down-level domain
	// and Kerberos realm are case-insensitive on the gateway, so don't silently transform it.
	return domain + "\\" + id.Username
}
