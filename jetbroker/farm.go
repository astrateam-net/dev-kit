package jetbroker

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// farm.go selects one gateway per launch from the workspace's declared farm — mirroring how DVLS
// picks a gateway from the farm. Selection is broker-side, NOT a load balancer: a token is bound to
// one gateway by jet_gw_id (the gateway rejects a jet_gw_id mismatch) and preflight stores the
// credential in that instance's memory by jti, so the player MUST reach the exact instance we
// preflighted. Liveness is checked BEFORE the mint via /jet/heartbeat (same as DVLS); there is no
// post-mint failover — DVLS has none either.

// errNoUsableGateway means every declared member was dead, draining, or zero-weight at launch time.
var errNoUsableGateway = errors.New("jetbroker: no usable gateway in the workspace farm")

// chosenGateway is the farm member a launch will use: its base URL (preflight + the player's
// gateway_url), its heartbeat-discovered UUID (jet_gw_id), and its hostname (shown to the user as
// the Gateway name, DVLS-style).
type chosenGateway struct {
	BaseURL    string
	BrowserURL string
	ID         string
	Name       string
}

// heartbeat is the subset of GET /jet/heartbeat we consume: the gateway's id (→ jet_gw_id), its
// hostname (→ display Gateway name), and its current load.
type heartbeat struct {
	ID                  string `json:"id"`
	Hostname            string `json:"hostname"`
	RunningSessionCount int    `json:"running_session_count"`
}

// selectGateway probes each declared member's /jet/heartbeat, drops the dead / zero-weight ones,
// and picks the least-loaded-relative-to-weight member, ties broken at random — mirroring DVLS's
// gateway selection (Priority = relativeLoad − relativeWeight; the minimum group is the most
// under-loaded vs its weight; a random pick breaks ties within that group).
func (b *Broker) selectGateway(ctx context.Context, candidates []Gateway) (chosenGateway, error) {
	type member struct {
		g        Gateway
		id       string
		hostname string
		sessions int
	}

	var alive []member
	for _, g := range candidates {
		if g.Weight <= 0 { // DVLS: LoadBalancingWeight <= 0 is excluded.
			continue
		}
		hb, err := b.gatewayHeartbeat(ctx, g.BaseURL)
		if err != nil {
			continue // Dead / unreachable / no id — skip (DVLS logs the heartbeat exception + continues).
		}
		alive = append(alive, member{g: g, id: hb.ID, hostname: hb.Hostname, sessions: hb.RunningSessionCount})
	}
	if len(alive) == 0 {
		return chosenGateway{}, errNoUsableGateway
	}

	var totalWeight, totalSessions int
	for _, m := range alive {
		totalWeight += m.g.Weight
		totalSessions += m.sessions
	}
	relWeight := func(w int) float64 {
		if totalWeight == 0 {
			return 0
		}
		return float64(w) / float64(totalWeight)
	}
	relLoad := func(s int) float64 {
		if totalSessions == 0 {
			return 0
		}
		return float64(s) / float64(totalSessions)
	}

	// Lowest Priority = most under-loaded relative to weight. Collect the min group, pick at random.
	minPriority := math.Inf(1)
	priorities := make([]float64, len(alive))
	for i, m := range alive {
		p := relLoad(m.sessions) - relWeight(m.g.Weight)
		priorities[i] = p
		if p < minPriority {
			minPriority = p
		}
	}
	const eps = 1e-9
	var group []member
	for i, m := range alive {
		if priorities[i]-minPriority <= eps {
			group = append(group, m)
		}
	}

	idx := 0
	if len(group) > 1 {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(group))))
		if err != nil {
			return chosenGateway{}, fmt.Errorf("jetbroker: select gateway: %w", err)
		}
		idx = int(n.Int64())
	}
	pick := group[idx]
	return chosenGateway{BaseURL: pick.g.BaseURL, BrowserURL: pick.g.BrowserURL, ID: pick.id, Name: pick.hostname}, nil
}

// gatewayHeartbeat performs GET /jet/heartbeat with a gateway.heartbeat.read scope token (no
// jet_gw_id — see mintHeartbeatScope). A non-2xx, a transport error, or a missing/!UUID id all
// mean "not usable" so the caller skips the member.
func (b *Broker) gatewayHeartbeat(ctx context.Context, baseURL string) (heartbeat, error) {
	scope, err := b.mintHeartbeatScope()
	if err != nil {
		return heartbeat{}, err
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/jet/heartbeat?token=" + url.QueryEscape(scope)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return heartbeat{}, err
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return heartbeat{}, fmt.Errorf("jetbroker: heartbeat request: %w", err)
	}
	defer resp.Body.Close()

	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return heartbeat{}, fmt.Errorf("jetbroker: heartbeat HTTP %d: %s", resp.StatusCode, payload)
	}

	var hb heartbeat
	if err := json.Unmarshal(payload, &hb); err != nil {
		return heartbeat{}, fmt.Errorf("jetbroker: heartbeat response not understood: %w", err)
	}
	// We need a real UUID for jet_gw_id; a gateway with no configured id can't be bound and would
	// silently accept any token — exclude it from selection.
	if _, err := uuid.Parse(hb.ID); err != nil {
		return heartbeat{}, fmt.Errorf("jetbroker: heartbeat returned no gateway id (got %q)", hb.ID)
	}
	return hb, nil
}
