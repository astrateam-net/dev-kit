package jetbroker

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// farm_test.go covers the gateway-farm selection: liveness (dead members are skipped) and
// the session-count load balancing (least-loaded-relative-to-weight wins). The pure tests use
// httptest gateways and run anywhere; TestGatewayHeartbeatLive probes a REAL gateway when the
// DGW_* env is set (used to confirm running_session_count == 1 with a live session).

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	return key
}

func testBroker(t *testing.T) *Broker {
	t.Helper()
	return &Broker{cfg: Config{ProvisionerKey: testKey(t)}, http: http.DefaultClient}
}

// mockGateway answers GET /jet/heartbeat with the given id + running_session_count, mirroring the
// real gateway's heartbeat response. It ignores the scope token (a real gateway verifies it; here
// we test selection, not the gateway).
func mockGateway(t *testing.T, id string, sessions int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jet/heartbeat" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(heartbeat{ID: id, RunningSessionCount: sessions})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// deadGateway answers every request with 503 — a member that is up enough to TCP-accept but not
// serving (the gatewayHeartbeat caller treats any non-2xx as "skip this member").
func deadGateway(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestSelectGateway_PicksLeastLoaded(t *testing.T) {
	b := testBroker(t)
	busy := mockGateway(t, "11111111-1111-1111-1111-111111111111", 5)
	idle := mockGateway(t, "22222222-2222-2222-2222-222222222222", 1) // least loaded
	mid := mockGateway(t, "33333333-3333-3333-3333-333333333333", 3)

	chosen, err := b.selectGateway(context.Background(), []Gateway{
		{BaseURL: busy, Weight: 100},
		{BaseURL: idle, Weight: 100},
		{BaseURL: mid, Weight: 100},
	})
	if err != nil {
		t.Fatalf("selectGateway: %v", err)
	}
	if chosen.ID != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("expected the least-loaded (1 session) gateway, got id %s", chosen.ID)
	}
	if chosen.BaseURL != idle {
		t.Fatalf("chosen BaseURL = %s, want %s", chosen.BaseURL, idle)
	}
}

func TestSelectGateway_ExcludesDead(t *testing.T) {
	b := testBroker(t)
	dead := deadGateway(t)
	live := mockGateway(t, "44444444-4444-4444-4444-444444444444", 9) // loaded but the only live one

	chosen, err := b.selectGateway(context.Background(), []Gateway{
		{BaseURL: dead, Weight: 100},
		{BaseURL: live, Weight: 100},
	})
	if err != nil {
		t.Fatalf("selectGateway: %v", err)
	}
	if chosen.ID != "44444444-4444-4444-4444-444444444444" {
		t.Fatalf("expected the only live gateway, got id %s", chosen.ID)
	}
}

func TestSelectGateway_NoUsable(t *testing.T) {
	b := testBroker(t)
	_, err := b.selectGateway(context.Background(), []Gateway{
		{BaseURL: deadGateway(t), Weight: 100},
		{BaseURL: deadGateway(t), Weight: 100},
	})
	if !errors.Is(err, errNoUsableGateway) {
		t.Fatalf("expected errNoUsableGateway, got %v", err)
	}
}

func TestSelectGateway_ExcludesZeroWeight(t *testing.T) {
	b := testBroker(t)
	drained := mockGateway(t, "55555555-5555-5555-5555-555555555555", 0) // idle but weight 0 => excluded
	active := mockGateway(t, "66666666-6666-6666-6666-666666666666", 4)

	chosen, err := b.selectGateway(context.Background(), []Gateway{
		{BaseURL: drained, Weight: 0},
		{BaseURL: active, Weight: 100},
	})
	if err != nil {
		t.Fatalf("selectGateway: %v", err)
	}
	if chosen.ID != "66666666-6666-6666-6666-666666666666" {
		t.Fatalf("expected the weighted gateway (zero-weight excluded), got id %s", chosen.ID)
	}
}

// TestSelectGateway_WeightSharesLoad: a heavy-weight member with MORE raw sessions is still chosen
// when it is under its weight share — the DVLS relativeLoad − relativeWeight rule, not raw count.
func TestSelectGateway_WeightSharesLoad(t *testing.T) {
	b := testBroker(t)
	small := mockGateway(t, "77777777-7777-7777-7777-777777777777", 1) // weight 1,  1 session
	large := mockGateway(t, "88888888-8888-8888-8888-888888888888", 5) // weight 100, 5 sessions
	// small: 1/6 − 1/101 ≈ +0.157 ; large: 5/6 − 100/101 ≈ −0.157 (lower => chosen).
	chosen, err := b.selectGateway(context.Background(), []Gateway{
		{BaseURL: small, Weight: 1},
		{BaseURL: large, Weight: 100},
	})
	if err != nil {
		t.Fatalf("selectGateway: %v", err)
	}
	if chosen.ID != "88888888-8888-8888-8888-888888888888" {
		t.Fatalf("expected the high-weight member (under its share) despite more sessions, got id %s", chosen.ID)
	}
}

// TestSelectGateway_ExcludesNoID: a gateway with no configured id (empty) cannot be bound via
// jet_gw_id and is skipped (gatewayHeartbeat rejects a non-UUID id).
func TestSelectGateway_ExcludesNoID(t *testing.T) {
	b := testBroker(t)
	noID := mockGateway(t, "", 0)
	good := mockGateway(t, "99999999-9999-9999-9999-999999999999", 2)
	chosen, err := b.selectGateway(context.Background(), []Gateway{
		{BaseURL: noID, Weight: 100},
		{BaseURL: good, Weight: 100},
	})
	if err != nil {
		t.Fatalf("selectGateway: %v", err)
	}
	if chosen.ID != "99999999-9999-9999-9999-999999999999" {
		t.Fatalf("expected the gateway with an id, got id %s", chosen.ID)
	}
}

// TestHeartbeatScopeHasNoGatewayID asserts the probe token carries scope=gateway.heartbeat.read and
// NO jet_gw_id — required so any candidate accepts it before we know its id (an empty jet_gw_id would
// 401 at the gateway as a malformed claim).
func TestHeartbeatScopeHasNoGatewayID(t *testing.T) {
	b := testBroker(t)

	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.URL.Query().Get("token")
		_ = json.NewEncoder(w).Encode(heartbeat{ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", RunningSessionCount: 0})
	}))
	t.Cleanup(srv.Close)

	if _, err := b.gatewayHeartbeat(context.Background(), srv.URL); err != nil {
		t.Fatalf("gatewayHeartbeat: %v", err)
	}

	claims := decodeJWTClaims(t, gotToken)
	if claims["scope"] != "gateway.heartbeat.read" {
		t.Fatalf("scope = %v, want gateway.heartbeat.read", claims["scope"])
	}
	if _, present := claims["jet_gw_id"]; present {
		t.Fatalf("heartbeat scope token must NOT carry jet_gw_id, claims: %v", claims)
	}
}

func decodeJWTClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", token)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode JWT payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal JWT payload: %v", err)
	}
	return claims
}

// TestGatewayHeartbeatLive probes a REAL gateway. Skipped unless DGW_GATEWAY_URL +
// DGW_PROVISIONER_KEY are set. It asserts the gateway is alive and exposes its id (liveness) and
// reports running_session_count. Set DGW_EXPECT_SESSIONS=1 (with a live session open) to assert the
// count — the "healthcheck shows liveness + session count 1" end-to-end check.
//
//	DGW_GATEWAY_URL=https://gateway.example.com:7171 DGW_PROVISIONER_KEY=... DGW_TLS_INSECURE=false \
//	DGW_EXPECT_SESSIONS=1 go test -run TestGatewayHeartbeatLive -v
func TestGatewayHeartbeatLive(t *testing.T) {
	gwURL := os.Getenv("DGW_GATEWAY_URL")
	keyPath := os.Getenv("DGW_PROVISIONER_KEY")
	if gwURL == "" || keyPath == "" {
		t.Skip("set DGW_GATEWAY_URL and DGW_PROVISIONER_KEY to run the live heartbeat test")
	}

	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read DGW_PROVISIONER_KEY: %v", err)
	}
	key, err := LoadProvisionerKey(keyPEM)
	if err != nil {
		t.Fatalf("load provisioner key: %v", err)
	}

	client := http.DefaultClient
	if truthyEnv(os.Getenv("DGW_TLS_INSECURE")) {
		client = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // test only
	}
	b := &Broker{cfg: Config{ProvisionerKey: key}, http: client}

	hb, err := b.gatewayHeartbeat(context.Background(), gwURL)
	if err != nil {
		t.Fatalf("live heartbeat against %s: %v", gwURL, err)
	}
	if _, err := uuid.Parse(hb.ID); err != nil {
		t.Fatalf("gateway returned no valid id (not alive/bindable): %q", hb.ID)
	}
	t.Logf("LIVE gateway %s is alive; running_session_count=%d", hb.ID, hb.RunningSessionCount)

	if want := os.Getenv("DGW_EXPECT_SESSIONS"); want != "" {
		n, err := strconv.Atoi(want)
		if err != nil {
			t.Fatalf("DGW_EXPECT_SESSIONS must be an integer: %v", err)
		}
		if hb.RunningSessionCount != n {
			t.Fatalf("running_session_count = %d, want %d", hb.RunningSessionCount, n)
		}
	}
}

func truthyEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
