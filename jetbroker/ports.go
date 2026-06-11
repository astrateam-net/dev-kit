package jetbroker

import "context"

// ports.go defines the THIN SEAMS into the host platform. The core (jetbroker.go, token.go,
// preflight.go) depends only on these interfaces — the host supplies adapters. This is what keeps
// the core portable across host platforms.

// Identity is the verified caller, supplied by the host (already SSO-authenticated by its OIDC
// provider). The core trusts it and does not re-verify.
type Identity struct {
	UserID   string // host user id — identity context / audit (the secret is keyed by workspace, not this)
	Username string // AD sAMAccountName / UPN local part
	Domain   string // optional; overrides Config.Realm when set
}

// IdentityResolver returns the verified caller for a launch request.
//
// Host adapter: read the authenticated session user (the request must be owner-gated), and map it
// to the AD username via the OIDC claims on the host user.
type IdentityResolver interface {
	Resolve(ctx context.Context, req LaunchRequest) (Identity, error)
}

// TargetResolver returns the RDP target HOST for a workspace. The port may be omitted
// (host only, like the upstream windows-rdp module) — the core defaults it to 3389
// (in dstHst). An explicit "host:port" is respected.
//
// Host adapter: read the workspace's OWN declared data (e.g. a metadata key "coder.rdp.target", a
// parameter, or a typed resource) by workspace id — NOT a value from the URL, which the browser
// could spoof.
type TargetResolver interface {
	Resolve(ctx context.Context, workspaceID string) (string, error)
}

// Gateway is one candidate member of the farm declared for a workspace. Only the base
// URL and the load-balancing weight are declared; the gateway's UUID is DISCOVERED at
// launch from its /jet/heartbeat response (it becomes jet_gw_id) — never declared, so
// the module can't drift from the gateway's real id.
type Gateway struct {
	BaseURL string // e.g. "https://gateway.example.com:7171"
	Weight  int    // DVLS LoadBalancingWeight; <= 0 excludes the member
}

// GatewayResolver returns the gateway farm available to a workspace. There is NO
// deployment-wide single gateway: the RDP system function only fires for a workspace
// whose RDP module declared its farm, so the core takes the list per launch and selects
// a live, least-loaded member (farm.go, mirroring how DVLS picks a gateway from the farm).
//
// Host adapter: read a metadata key "coder.rdp.gateways" (a JSON list of {url, weight}) by
// workspace id.
type GatewayResolver interface {
	Resolve(ctx context.Context, workspaceID string) ([]Gateway, error)
}

// SecretStore returns the RDP password for a workspace, read server-side by workspace id (NOT from
// the URL — not spoofable). The credential is bound to the WORKSPACE, not to the calling user, so a
// shared/service account works and whoever may use the workspace connects under the configured
// credential (owner-gated upstream).
//
// Host adapter (active path): read the workspace's `rdp_password` parameter value by workspace id;
// `name` is the parameter name. Future hardening uses the SAME seam, still keyed by workspace: an
// encrypted host-side store, or a Vault path `secret/rdp/ws/<workspaceID>` read with the host's own
// AppRole. (A per-user secret does NOT fit — it is per-user and delivered into the workspace, not a
// connection-bound credential.)
type SecretStore interface {
	Get(ctx context.Context, workspaceID, name string) (string, error)
}
