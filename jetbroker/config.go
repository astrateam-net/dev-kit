package jetbroker

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
)

// Config is the durable, deployment-level configuration of the broker.
// It is loaded once at boot from injected config/secrets.
type Config struct {
	// ProvisionerKey is the RSA PRIVATE half of the provisioner keypair.
	// Its PUBLIC half is configured on the gateway as the MAIN provisioner_public_key.
	// Mirrors the DVLS gateway private key.
	ProvisionerKey *rsa.PrivateKey

	// NOTE: there is deliberately NO single gateway here. The gateway farm is declared
	// per workspace by the RDP module and supplied via GatewayResolver (ports.go); the
	// core selects a live, least-loaded member per launch (farm.go) and discovers its
	// UUID from /jet/heartbeat. The RDP system function only fires when such a workspace
	// invokes the endpoint — it is never a deployment-wide default.

	// Realm builds the DOWN-LEVEL DOMAIN\user form, e.g. "EXAMPLE" (NetBIOS) — selects the
	// Kerberos acceptor on the gateway for domain targets. Note: the NetBIOS domain differs from
	// the Kerberos DNS realm; the target-facing leg resolves tickets via the gateway's configured
	// KDC, so verify the down-level form is accepted there, or supply a per-user Identity.Domain in
	// UPN form (user@dns.realm) instead.
	Realm string

	// SecretName is the name of the workspace credential the SecretStore reads (e.g. the
	// `rdp_password` parameter).
	SecretName string

	// RedirectMode switches Handler from JSON to a 302 redirect to the player page of the
	// SELECTED gateway, with the launch descriptor in the URL *fragment*. The fragment never
	// reaches a server and a top-level navigation isn't subject to CORS, so this is the clean
	// cross-origin path. REQUIRES the launch to be a top-level navigation (not fetch/XHR).
	// The page host is the chosen farm member (derived from the descriptor's gateway_url), NOT a
	// fixed URL — there is no single gateway to point at. False => JSON mode.
	RedirectMode bool

	// PlayerLaunchPath is the gateway-hosted player route appended to the chosen gateway in
	// redirect mode. Empty => the stock gateway-webapp path "/jet/webapp/client/launch".
	PlayerLaunchPath string

	// CredentialTTL is the preflight time_to_live in seconds. DVLS default 900;
	// DVLS otherwise derives it from Clamp(RDPTokenReuseWindow, 2, 15) * 60.
	CredentialTTL int

	// RDPReuseWindow is the jet_reuse RDP reconnection window in seconds (0 = disabled). DVLS sets
	// this only when RDP reconnection is enabled (Clamp(RDPTokenReuseWindow, 2, 15) * 60). With 0
	// the gateway allows only the ~10s reuse fallback between connections.
	RDPReuseWindow int

	// HTTPClient calls the gateway. Defaults to a client with a 10s timeout. Override to pin
	// the gateway's TLS / set timeouts.
	HTTPClient *http.Client
}

// Validate checks the mandatory deployment fields. Call it once at boot: a misconfigured
// broker must fail fast, not per launch. The gateway is NOT here — it is per-workspace
// (GatewayResolver), so New also requires a non-nil resolver.
func (c Config) Validate() error {
	if c.ProvisionerKey == nil {
		return errors.New("jetbroker: ProvisionerKey is required")
	}
	if c.SecretName == "" {
		return errors.New("jetbroker: SecretName is required")
	}
	return nil
}

// LoadProvisionerKey parses a PEM RSA private key (PKCS#8 "PRIVATE KEY" — what
// New-DGatewayProvisionerKeyPair emits — or PKCS#1 "RSA PRIVATE KEY"). The gateway
// holds the SPKI public half. RS256 is the only algorithm the gateway accepts.
func LoadProvisionerKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("jetbroker: no PEM block in provisioner key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("jetbroker: provisioner key is neither PKCS#1 nor PKCS#8 RSA (encrypted keys unsupported): %w", err)
	}
	key, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("jetbroker: provisioner key is not RSA")
	}
	return key, nil
}
