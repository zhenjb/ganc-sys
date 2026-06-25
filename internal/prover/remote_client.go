package prover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

const DefaultRemoteProverURL = "http://localhost:8090"

type RemoteClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewRemoteClient(baseURL string) *RemoteClient {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultRemoteProverURL
	}

	return &RemoteClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *RemoteClient) GenerateProof(
	ctx context.Context,
	input GenerateProofInput,
) (types.ProofBundle, error) {
	reqBody := remoteProveRequest{
		SettlementUpdate: input.SettlementUpdate,
		BatchCommitments: input.BatchCommitments,
		Witness:          input.Witness,
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return types.ProofBundle{}, fmt.Errorf("marshal remote prove request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/prove",
		bytes.NewReader(raw),
	)
	if err != nil {
		return types.ProofBundle{}, fmt.Errorf("create remote prove request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return types.ProofBundle{}, fmt.Errorf("call remote prover: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp remoteErrorResponse
		if decodeErr := json.NewDecoder(resp.Body).Decode(&errResp); decodeErr == nil && errResp.Error != "" {
			return types.ProofBundle{}, fmt.Errorf("remote prover returned %d: %s", resp.StatusCode, errResp.Error)
		}

		return types.ProofBundle{}, fmt.Errorf("remote prover returned status %d", resp.StatusCode)
	}

	var proveResp remoteProveResponse
	if err := json.NewDecoder(resp.Body).Decode(&proveResp); err != nil {
		return types.ProofBundle{}, fmt.Errorf("decode remote prove response: %w", err)
	}

	if proveResp.ProofBundle.Proof == "" {
		return types.ProofBundle{}, fmt.Errorf("remote prover returned empty proof")
	}

	if len(proveResp.ProofBundle.PublicInputs) != 6 {
		return types.ProofBundle{}, fmt.Errorf(
			"remote prover returned %d public inputs, expected 6",
			len(proveResp.ProofBundle.PublicInputs),
		)
	}

	if proveResp.ProofBundle.VerificationKeyID == "" {
		return types.ProofBundle{}, fmt.Errorf("remote prover returned empty verificationKeyId")
	}

	return proveResp.ProofBundle, nil
}

type remoteProveRequest struct {
	SettlementUpdate types.SettlementUpdate `json:"settlementUpdate"`
	BatchCommitments types.BatchCommitments `json:"batchCommitments"`
	Witness          types.Witness          `json:"witness"`
}

type remoteProveResponse struct {
	ProofBundle types.ProofBundle `json:"proofBundle"`
}

type remoteVerifyRequest struct {
	SettlementUpdate types.SettlementUpdate `json:"settlementUpdate"`
	BatchCommitments types.BatchCommitments `json:"batchCommitments"`
	ProofBundle      types.ProofBundle      `json:"proofBundle"`
}

type remoteVerifyResponse struct {
	Valid bool   `json:"valid"`
	Error string `json:"error,omitempty"`
}

type remoteErrorResponse struct {
	Error string `json:"error"`
}

var _ Client = (*RemoteClient)(nil)
var _ Verifier = (*RemoteClient)(nil)

// Verify calls the gazk /verify endpoint to perform a real Groth16
// verification of the proof against the public inputs derived from the
// settlement update and batch commitments.
//
// It returns nil only if gazk reports the proof valid. Any transport error,
// non-2xx status, or {"valid":false} response is returned as an error so the
// caller can reject the batch without advancing state.
func (c *RemoteClient) Verify(ctx context.Context, input VerifyProofInput) error {
	reqBody := remoteVerifyRequest{
		SettlementUpdate: input.SettlementUpdate,
		BatchCommitments: input.BatchCommitments,
		ProofBundle:      input.ProofBundle,
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal remote verify request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/verify",
		bytes.NewReader(raw),
	)
	if err != nil {
		return fmt.Errorf("create remote verify request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call remote verifier: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp remoteErrorResponse
		if decodeErr := json.NewDecoder(resp.Body).Decode(&errResp); decodeErr == nil && errResp.Error != "" {
			return fmt.Errorf("remote verifier returned %d: %s", resp.StatusCode, errResp.Error)
		}

		return fmt.Errorf("remote verifier returned status %d", resp.StatusCode)
	}

	var verifyResp remoteVerifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&verifyResp); err != nil {
		return fmt.Errorf("decode remote verify response: %w", err)
	}

	if !verifyResp.Valid {
		if verifyResp.Error != "" {
			return fmt.Errorf("proof rejected by verifier: %s", verifyResp.Error)
		}

		return fmt.Errorf("proof rejected by verifier")
	}

	return nil
}

func (c *RemoteClient) GetVerifierArtifact(ctx context.Context) (types.VerifierArtifact, error) {
	if c.baseURL == "" {
		return types.VerifierArtifact{}, fmt.Errorf("remote prover base URL is required")
	}

	url := strings.TrimRight(c.baseURL, "/") + "/verifier-artifact"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return types.VerifierArtifact{}, fmt.Errorf("create remote verifier artifact request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return types.VerifierArtifact{}, fmt.Errorf("call remote verifier artifact endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp remoteErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err == nil && errResp.Error != "" {
			return types.VerifierArtifact{}, fmt.Errorf("remote verifier artifact endpoint returned %d: %s", resp.StatusCode, errResp.Error)
		}

		return types.VerifierArtifact{}, fmt.Errorf("remote verifier artifact endpoint returned status %d", resp.StatusCode)
	}

	var artifact types.VerifierArtifact
	if err := json.NewDecoder(resp.Body).Decode(&artifact); err != nil {
		return types.VerifierArtifact{}, fmt.Errorf("decode remote verifier artifact response: %w", err)
	}

	if err := ValidateVerifierArtifact(artifact); err != nil {
		return types.VerifierArtifact{}, err
	}

	return artifact, nil
}
