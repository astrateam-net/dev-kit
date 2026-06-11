package jetbroker

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

// http.go is the host route handler — the entry point the host's router calls.
//
// Wire it under the host's API, e.g.:
//
//	r.Route("/api/v2", func(r chi.Router) {
//	    r.With(ownerGatedMiddleware).      // owner-gated: only the workspace owner
//	        Post("/rdp-launch", broker.Handler())
//	})
//
// The request MUST already be authenticated by the host's session middleware; the
// identity adapter (ports.go) reads the user from that context.
//
// SECURITY CONTRACT: WorkspaceID arrives from the "ws" query param. The core resolves the
// target from it server-side, but it does NOT verify the caller may use that workspace. The
// host adapters (IdentityResolver / TargetResolver) MUST authorize the authenticated session
// user against this workspace id, or a user could launch RDP against another user's target by
// guessing the id. Keep this owner-gated.
func (b *Broker) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req := LaunchRequest{
			WorkspaceID: r.URL.Query().Get("ws"),
		}

		res, err := b.Launch(r.Context(), req)
		if err != nil {
			// Do not echo the internal error to the client: it can carry gateway hostnames
			// or wrapped resolver detail. Return a generic message; the detail belongs in the
			// server-side log.
			// TODO(host): map to the host's error shape + structured logging.
			http.Error(w, "rdp launch failed", http.StatusBadGateway)
			return
		}

		// Redirect mode: 302 to the CHOSEN gateway's player with the descriptor in the URL fragment.
		// The fragment is client-side only (never sent to the gateway, kept out of its logs) and a
		// top-level navigation isn't subject to CORS — the clean cross-origin path vs. the player
		// fetching the host directly. The player must read location.hash and SHOULD strip it from
		// history (history.replaceState) once consumed. The page host follows the selected farm
		// member (derived from the descriptor's gateway_url) — there is no fixed launch URL.
		if b.cfg.RedirectMode {
			http.Redirect(w, r, b.playerLaunchURL(res)+"#"+res.descriptor(), http.StatusFound)
			return
		}

		// JSON mode: caller reads the descriptor itself (same-origin or a native client).
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}
}

// playerLaunchURL derives the chosen gateway's player page from the descriptor's gateway_url
// (wss://host/jet/rdp -> https://host + PlayerLaunchPath). The player lives on whichever farm
// member was selected, so there is no fixed launch URL to configure.
func (b *Broker) playerLaunchURL(res *LaunchResult) string {
	base := strings.TrimSuffix(res.GatewayURL, "/jet/rdp")
	switch {
	case strings.HasPrefix(base, "wss://"):
		base = "https://" + strings.TrimPrefix(base, "wss://")
	case strings.HasPrefix(base, "ws://"):
		base = "http://" + strings.TrimPrefix(base, "ws://")
	}
	path := b.cfg.PlayerLaunchPath
	if path == "" {
		path = "/jet/webapp/client/launch"
	}
	return strings.TrimRight(base, "/") + path
}

// descriptor encodes the launch result for the player's URL fragment as base64url(JSON). The
// player base64url-decodes and JSON-parses it. It carries only the one-time association token, the
// synthetic proxy credential, and URLs — never the real password.
func (r *LaunchResult) descriptor() string {
	b, _ := json.Marshal(r)
	return base64.RawURLEncoding.EncodeToString(b)
}
