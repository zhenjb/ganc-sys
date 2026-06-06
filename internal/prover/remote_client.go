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

type remoteErrorResponse struct {
	Error string `json:"error"`
}

var _ Client = (*RemoteClient)(nil)
