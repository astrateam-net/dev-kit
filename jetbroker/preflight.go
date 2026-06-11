package jetbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// preflight.go mirrors how DVLS provisions credentials into the gateway before a launch.

const credKindUsernamePassword = "username-password" // the only AppCredentialKind

// appCredential is the gateway's AppCredential model.
type appCredential struct {
	Kind     string `json:"kind"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// provisionOperation is one credential-provisioning operation in a preflight batch.
type provisionOperation struct {
	ID               string        `json:"id"`
	Kind             string        `json:"kind"`  // "provision-credentials"
	Token            string        `json:"token"` // the association JWT (gateway keys the mapping by its jti)
	ProxyCredential  appCredential `json:"proxy_credential"`
	TargetCredential appCredential `json:"target_credential"`
	TimeToLive       int           `json:"time_to_live"`
}

// preflightOutput is one entry of the gateway's preflight output array. The per-operation outcome
// lives here, NOT in the HTTP status: the gateway returns 200 even when an operation fails,
// capturing the failure as an {"kind":"alert", ...} entry.
type preflightOutput struct {
	OperationID  string `json:"operation_id"`
	Kind         string `json:"kind"` // "ack" on success, "alert" on failure (others for non-provision ops)
	AlertStatus  string `json:"alert_status"`
	AlertMessage string `json:"alert_message"`
}

// provisionCredentials posts the provision-credentials operation to the gateway.
//
// Verified shape:
//
//	POST {GatewayBaseURL}/jet/preflight?token=<scope JWT>      (scope token as QUERY param)
//	body: [ {kind:"provision-credentials", token:<assoc JWT>, proxy_credential, target_credential, time_to_live} ]
//
// A 2xx alone is not success: the gateway returns 200 and reports each operation's outcome in
// the body. We require an "ack" for our op and reject any non-info "alert" (see the inline notes).
func (b *Broker) provisionCredentials(ctx context.Context, baseURL, scopeToken, assocToken string, proxy, target appCredential) error {
	op := provisionOperation{
		ID:               uuid.NewString(),
		Kind:             "provision-credentials",
		Token:            assocToken,
		ProxyCredential:  proxy,
		TargetCredential: target,
		TimeToLive:       b.cfg.CredentialTTL,
	}
	body, err := json.Marshal([]provisionOperation{op})
	if err != nil {
		return err
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/jet/preflight?token=" + url.QueryEscape(scopeToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("jetbroker: preflight request: %w", err)
	}
	defer resp.Body.Close()

	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("jetbroker: preflight provision-credentials failed: HTTP %d: %s", resp.StatusCode, payload)
	}

	// A 2xx does NOT mean the credential was provisioned. The gateway returns 200 and reports
	// each operation's outcome in the body: an "ack" for our op id on success, or an "alert"
	// (invalid token, time_to_live over the 2h cap, dst_alt present, internal error) on failure.
	// An "info" alert ("an existing credential entry was replaced") is benign. Treat a missing
	// ack or any non-info alert for our op as a hard failure, so we never hand the player a token
	// whose credential mapping was silently never stored.
	var outputs []preflightOutput
	if err := json.Unmarshal(payload, &outputs); err != nil {
		return fmt.Errorf("jetbroker: preflight response not understood: %w: %s", err, payload)
	}

	acked := false
	for _, out := range outputs {
		if out.OperationID != op.ID {
			continue
		}
		switch out.Kind {
		case "ack":
			acked = true
		case "alert":
			if strings.EqualFold(out.AlertStatus, "info") {
				continue // e.g. "an existing credential entry was replaced"
			}
			return fmt.Errorf("jetbroker: preflight provision-credentials rejected: %s: %s", out.AlertStatus, out.AlertMessage)
		}
	}
	if !acked {
		return fmt.Errorf("jetbroker: preflight provision-credentials returned no ack for op %s: %s", op.ID, payload)
	}
	return nil
}
