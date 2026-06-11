// Command test-authority is a DEV-ONLY stand-in for the host-platform adapters.
//
// Instead of reading identity, target, and password from a host platform's
// internals, it reads them from environment variables (an .env file) and then
// calls the REAL jetbroker core — the same token minting, /jet/preflight
// credential injection, and descriptor building that ships in production. This
// lets you point a browser at a real gateway and a real Windows host and verify
// the whole chain end-to-end, without a host platform.
//
// It is NOT for production: it trusts whatever credentials the env hands it and
// performs no authorization.
//
// Usage:
//
//	set -a; . ./cmd/test-authority/.env; set +a
//	go run ./cmd/test-authority
//	# open the printed URL in a browser (top-level navigation, not fetch)
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/astrateam-net/dev-kit/jetbroker"
)

// The ports.go interfaces, each backed by a single env value. They are
// separate types because Go methods can't overload: IdentityResolver.Resolve and
// TargetResolver.Resolve share a name but differ in signature.

type identityEnv struct{ user, domain string }

func (i identityEnv) Resolve(_ context.Context, _ jetbroker.LaunchRequest) (jetbroker.Identity, error) {
	// UserID is identity context only (the secret is keyed by workspace now); secretEnv ignores it.
	return jetbroker.Identity{UserID: "test-user", Username: i.user, Domain: i.domain}, nil
}

type targetEnv struct{ host string }

func (t targetEnv) Resolve(_ context.Context, _ string) (string, error) {
	return t.host, nil
}

type secretEnv struct{ pass string }

func (s secretEnv) Get(_ context.Context, _, _ string) (string, error) {
	return s.pass, nil
}

// gatewayEnv stands in for the host's GatewayResolver: in production the farm is the workspace's
// declared coder.rdp.gateways; here it is the single DGW_GATEWAY_URL (weight 100). The gateway's
// id is discovered from /jet/heartbeat, so no DGW_GATEWAY_ID is needed.
type gatewayEnv struct{ url string }

func (g gatewayEnv) Resolve(_ context.Context, _ string) ([]jetbroker.Gateway, error) {
	return []jetbroker.Gateway{{BaseURL: g.url, Weight: 100}}, nil
}

func main() {
	cfg, listen, err := loadConfig()
	if err != nil {
		log.Fatalf("test-authority: %v", err)
	}

	gwURL := env("DGW_GATEWAY_URL", "")
	if gwURL == "" {
		log.Fatalf("test-authority: DGW_GATEWAY_URL is required (the farm member to probe + use)")
	}
	broker, err := jetbroker.New(
		cfg,
		identityEnv{user: env("TARGET_USER", ""), domain: os.Getenv("REALM")},
		targetEnv{host: env("TARGET_HOST", "")},
		secretEnv{pass: env("TARGET_PASS", "")},
		gatewayEnv{url: gwURL},
	)
	if err != nil {
		log.Fatalf("test-authority: %v", err)
	}

	// One route, mirroring coderd's: GET/POST with ?ws=<anything> (the stub target
	// resolver ignores it). With RedirectMode a browser hitting this lands on the chosen
	// gateway's /launch with the descriptor in the fragment.
	http.HandleFunc("/rdp-launch", broker.Handler())

	url := strings.TrimRight(listen.advertise, "/") + "/rdp-launch?ws=test"
	log.Printf("test-authority listening on %s", listen.addr)
	log.Printf("open this in a browser (top-level navigation, NOT fetch):\n\n    %s\n", url)
	log.Fatal(http.ListenAndServe(listen.addr, nil))
}

type listenConfig struct {
	addr      string // bind address, e.g. "127.0.0.1:8722"
	advertise string // URL to print, e.g. "http://127.0.0.1:8722"
}

func loadConfig() (jetbroker.Config, listenConfig, error) {
	var zero jetbroker.Config

	keyPath := env("DGW_PROVISIONER_KEY", "")
	if keyPath == "" {
		return zero, listenConfig{}, fmt.Errorf("DGW_PROVISIONER_KEY is required (path to the provisioner PRIVATE key PEM)")
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return zero, listenConfig{}, fmt.Errorf("read DGW_PROVISIONER_KEY: %w", err)
	}
	key, err := jetbroker.LoadProvisionerKey(keyPEM)
	if err != nil {
		return zero, listenConfig{}, err
	}

	cfg := jetbroker.Config{
		ProvisionerKey: key,
		Realm:          os.Getenv("REALM"),
		SecretName:     env("DGW_SECRET_NAME", "rdp-password"),
		// Redirect to the chosen gateway's /launch when asked (the descriptor rides the fragment).
		// Kept on if the legacy PLAYER_LAUNCH_URL is present so the existing .env still redirects;
		// the value itself is no longer used (the page host is derived from the selected gateway).
		RedirectMode: truthy(os.Getenv("REDIRECT_MODE")) || os.Getenv("PLAYER_LAUNCH_URL") != "",
	}
	if ttl := os.Getenv("DGW_CREDENTIAL_TTL"); ttl != "" {
		n, err := strconv.Atoi(ttl)
		if err != nil {
			return zero, listenConfig{}, fmt.Errorf("DGW_CREDENTIAL_TTL must be an integer: %w", err)
		}
		cfg.CredentialTTL = n
	}

	// Test gateways usually carry a private-CA or self-signed cert. Opt-in skip,
	// loud by name so it never gets mistaken for a production setting.
	if truthy(os.Getenv("DGW_TLS_INSECURE")) {
		cfg.HTTPClient = &http.Client{
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // dev-only test harness
		}
		log.Printf("WARNING: DGW_TLS_INSECURE set — gateway TLS verification disabled (test only)")
	}

	addr := env("LISTEN_ADDR", "127.0.0.1:8722")
	advertise := env("ADVERTISE_URL", "http://"+addr)
	return cfg, listenConfig{addr: addr, advertise: advertise}, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
