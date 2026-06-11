package jetbroker

import (
	"net"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// token.go mirrors how the Devolutions Gateway's own token utility signs tokens and the *Claims
// structs it expects.
//
// Verified facts:
//   - token TYPE is carried in the JWT "cty" header (ASSOCIATION / SCOPE / KDC / WEBAPP); the
//     gateway dispatches on it.
//   - RS256 is the only accepted algorithm.
//   - signing adds iat / jti / nbf / exp; jti is the key under which the gateway stores the
//     injected credential mapping.

// assocLifetime matches DVLS's default token lifetime (300s). The token is consumed almost
// immediately, but 60s was tight if the browser player bootstraps slowly (WASM download, etc.);
// 300s stays well within the gateway's 5-min validation leeway.
const assocLifetime = 300 * time.Second

// signGateway sets cty and signs RS256 with the provisioner key.
func (b *Broker) signGateway(cty string, claims jwt.Claims) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["cty"] = cty // gateway picks the claim type from cty
	return tok.SignedString(b.cfg.ProvisionerKey)
}

// associationClaims is the gateway's AssociationClaims (cty=ASSOCIATION).
type associationClaims struct {
	DstHst  string `json:"dst_hst"`   // "tcp://host:3389"
	JetAp   string `json:"jet_ap"`    // "rdp"
	JetCm   string `json:"jet_cm"`    // hardcoded "fwd"
	JetAid  string `json:"jet_aid"`   // session/association id
	JetGwID string `json:"jet_gw_id"` // binds the token to one gateway
	// JetReuse is the RDP reconnection window in seconds. The gateway reads jet_reuse as a u32;
	// 0/absent => no reuse window (only the ~10s RDP fallback).
	JetReuse int `json:"jet_reuse,omitempty"`
	// RegisteredClaims supplies jti (ID), iat (IssuedAt), nbf (NotBefore), exp (ExpiresAt).
	jwt.RegisteredClaims
}

// scopeClaims is the gateway's ScopeClaims (cty=SCOPE). MAIN key only.
// jet_gw_id is OMITTED when empty: the heartbeat probe runs before we know the gateway's
// id and must be accepted by any candidate, and the gateway parses jet_gw_id with
// Uuid::parse_str when present — an empty string would 401 (a malformed claim).
type scopeClaims struct {
	Scope   string `json:"scope"` // "gateway.preflight" | "gateway.heartbeat.read"
	JetGwID string `json:"jet_gw_id,omitempty"`
	jwt.RegisteredClaims
}

func registered(now time.Time, ttl time.Duration) jwt.RegisteredClaims {
	return jwt.RegisteredClaims{
		ID:        uuid.NewString(), // jti
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}
}

// mintAssociation builds the session token the player carries (in the RDCleanPath PDU).
// It returns the token's jti, which the gateway uses as the credential-mapping key and which
// the KDC token references via jet_cred_id (mintKdc).
func (b *Broker) mintAssociation(target, gwID string) (token, jti string, err error) {
	jti = uuid.NewString()
	reg := registered(time.Now(), assocLifetime)
	reg.ID = jti
	token, err = b.signGateway("ASSOCIATION", associationClaims{
		DstHst:           dstHst(target),
		JetAp:            "rdp",
		JetCm:            "fwd",
		JetAid:           uuid.NewString(),
		JetGwID:          gwID, // the heartbeat-discovered id of the chosen farm member
		JetReuse:         b.cfg.RDPReuseWindow,
		RegisteredClaims: reg,
	})
	return token, jti, err
}

// kdcClaims is the credential-injection KDC token (cty=KDC). jet_cred_id points at the
// association token's jti; the gateway then serves Kerberos for that session from its in-process
// fake KDC instead of a real DC. No krb_realm/krb_kdc — their presence would select the real-KDC
// path instead.
type kdcClaims struct {
	JetCredID string `json:"jet_cred_id"`
	JetGwID   string `json:"jet_gw_id"`
	jwt.RegisteredClaims
}

// mintKdc builds the KDC-proxy token the browser player uses to reach the gateway's in-process
// fake KDC. assocJTI binds it to the association token's injected credential mapping.
func (b *Broker) mintKdc(assocJTI, gwID string) (string, error) {
	return b.signGateway("KDC", kdcClaims{
		JetCredID:        assocJTI,
		JetGwID:          gwID,
		RegisteredClaims: registered(time.Now(), assocLifetime),
	})
}

// dstHst gives dst_hst an explicit scheme to match DVLS, which always serializes the target as
// "tcp://host:3389". The gateway defaults a scheme-less value to "tcp" anyway, so this is
// byte-for-byte parity, not a behavior change.
func dstHst(target string) string {
	if strings.Contains(target, "://") {
		return target
	}
	// The module declares a HOST with no port; the gateway needs host:3389 to dial it. The port
	// lives ONLY here (the token's dst_hst), so the player + session-info keep the clean host.
	if _, _, err := net.SplitHostPort(target); err != nil {
		target += ":3389"
	}
	return "tcp://" + target
}

// mintScope builds the gateway.preflight scope token authorizing the preflight call on the
// chosen gateway. Bound to that gateway via jet_gw_id (scope tokens need the MAIN key).
func (b *Broker) mintScope(gwID string) (string, error) {
	return b.signGateway("SCOPE", scopeClaims{
		Scope:            "gateway.preflight",
		JetGwID:          gwID,
		RegisteredClaims: registered(time.Now(), assocLifetime),
	})
}

// mintHeartbeatScope builds the gateway.heartbeat.read scope token used to probe a candidate's
// liveness + running-session count (farm.go). It carries NO jet_gw_id: the probe runs BEFORE we
// know the gateway's id and must be accepted by any candidate (scopeClaims.JetGwID is omitempty).
func (b *Broker) mintHeartbeatScope() (string, error) {
	return b.signGateway("SCOPE", scopeClaims{
		Scope:            "gateway.heartbeat.read",
		RegisteredClaims: registered(time.Now(), assocLifetime),
	})
}

// webAppLifetime matches the gateway's app-token default + hard cap and the stock webapp's own
// request (lifetime 7200). 2 hours.
const webAppLifetime = 7200 * time.Second

// mintWebApp builds the WEBAPP login token (cty=WEBAPP) — the gateway-webapp auth token DVLS mints
// to "log in" the web client. Claims match the gateway's WebAppTokenClaims: jti/iat/nbf/exp + sub.
// The standalone gateway-webapp runs a periodic (~60s) session-expiration check; with no stored
// session it tears the player down after about a minute. DVLS hands the browser this token to store
// on "login" so the check is a no-op; the /launch page persists it into the stock webapp session.
// Signed with the provisioner key like every other token; the gateway validates it against the same
// MAIN public key.
func (b *Broker) mintWebApp(subject string) (string, error) {
	reg := registered(time.Now(), webAppLifetime)
	reg.Subject = subject // gateway WebAppTokenClaims.sub
	return b.signGateway("WEBAPP", reg)
}
