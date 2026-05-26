package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

const defaultRESTTimeout = 10 * time.Second

// RestQueryClient queries the real x/zkdex REST/LCD API.
//
// This client is query-only.
// It does NOT sign or broadcast user transactions.
//
// Real flow note:
// - MsgDeposit is expected to be produced externally by FE wallet later.
// - P4 backend uses this client to query/index chain state after txs exist.
type RestQueryClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewRestQueryClient(baseURL string) *RestQueryClient {
	return &RestQueryClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: defaultRESTTimeout,
		},
	}
}

func (c *RestQueryClient) GetCurrentStateRoot(ctx context.Context) (string, error) {
	var out struct {
		StateRoot string `json:"state_root"`
	}

	if err := c.get(ctx, "/ob/zkdex/v1/current_state_root", &out); err != nil {
		return "", err
	}

	return out.StateRoot, nil
}

func (c *RestQueryClient) GetModuleAccountAddress(ctx context.Context) (string, error) {
	var out struct {
		Address string `json:"address"`
	}

	if err := c.get(ctx, "/ob/zkdex/v1/module_account_address", &out); err != nil {
		return "", err
	}

	return out.Address, nil
}

func (c *RestQueryClient) GetModuleAccountBalance(ctx context.Context) (map[string]string, error) {
	var out struct {
		Balance string `json:"balance"`
	}

	if err := c.get(ctx, "/ob/zkdex/v1/module_account_balance", &out); err != nil {
		return nil, err
	}

	return parseCosmosCoinString(out.Balance), nil
}

func (c *RestQueryClient) GetDepositRecord(ctx context.Context, depositID string) (types.DepositRecord, bool, error) {
	var raw map[string]json.RawMessage

	err := c.get(ctx, "/ob/zkdex/v1/deposit_record/"+url.PathEscape(depositID), &raw)
	if err != nil {
		if isNotFound(err) {
			return types.DepositRecord{}, false, nil
		}

		return types.DepositRecord{}, false, err
	}

	recordRaw := firstRaw(raw, "deposit_record", "depositRecord", "record")
	if len(recordRaw) == 0 {
		recordRaw = mustMarshalRaw(raw)
	}

	record, err := decodeDepositRecord(recordRaw)
	if err != nil {
		return types.DepositRecord{}, false, err
	}

	return record, true, nil
}

func (c *RestQueryClient) GetDepositProcessed(ctx context.Context, depositID string) (bool, error) {
	var out struct {
		Processed bool `json:"processed"`
	}

	if err := c.get(ctx, "/ob/zkdex/v1/deposit_processed/"+url.PathEscape(depositID), &out); err != nil {
		return false, err
	}

	return out.Processed, nil
}

func (c *RestQueryClient) GetWithdrawRecord(ctx context.Context, withdrawID string) (types.WithdrawRecord, bool, error) {
	var raw map[string]json.RawMessage

	err := c.get(ctx, "/ob/zkdex/v1/withdraw_record/"+url.PathEscape(withdrawID), &raw)
	if err != nil {
		if isNotFound(err) {
			return types.WithdrawRecord{}, false, nil
		}

		return types.WithdrawRecord{}, false, err
	}

	recordRaw := firstRaw(raw, "withdraw_record", "withdrawRecord", "record")
	if len(recordRaw) == 0 {
		recordRaw = mustMarshalRaw(raw)
	}

	record, err := decodeWithdrawRecord(recordRaw)
	if err != nil {
		return types.WithdrawRecord{}, false, err
	}

	return record, true, nil
}

func (c *RestQueryClient) GetNullifierUsed(ctx context.Context, nullifier string) (bool, error) {
	var out struct {
		Used bool `json:"used"`
	}

	if err := c.get(ctx, "/ob/zkdex/v1/nullifier_used/"+url.PathEscape(nullifier), &out); err != nil {
		return false, err
	}

	return out.Used, nil
}

func (c *RestQueryClient) get(ctx context.Context, path string, out any) error {
	if c.baseURL == "" {
		return fmt.Errorf("chain REST base URL is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var chainErr struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal(body, &chainErr); err == nil && chainErr.Code != 0 {
		return ChainQueryError{
			Code:    chainErr.Code,
			Message: chainErr.Message,
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("chain REST query failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode chain REST response: %w; body=%s", err, string(body))
	}

	return nil
}

type ChainQueryError struct {
	Code    int
	Message string
}

func (e ChainQueryError) Error() string {
	return fmt.Sprintf("chain query error code=%d message=%q", e.Code, e.Message)
}

func isNotFound(err error) bool {
	chainErr, ok := err.(ChainQueryError)
	if !ok {
		return false
	}

	return chainErr.Code == 5
}

func firstRaw(raw map[string]json.RawMessage, keys ...string) json.RawMessage {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			return value
		}
	}

	return nil
}

func mustMarshalRaw(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}

	return raw
}

func decodeDepositRecord(raw json.RawMessage) (types.DepositRecord, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return types.DepositRecord{}, err
	}

	return types.DepositRecord{
		DepositID:     getString(m, "depositId", "deposit_id"),
		Owner:         getString(m, "owner", "creator"),
		Denom:         getString(m, "denom"),
		Amount:        getString(m, "amount"),
		Processed:     getBool(m, "processed"),
		CreatedHeight: getInt64(m, "createdHeight", "created_height", "height"),
		TxHash:        getString(m, "txHash", "tx_hash"),
	}, nil
}

func decodeWithdrawRecord(raw json.RawMessage) (types.WithdrawRecord, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return types.WithdrawRecord{}, err
	}

	return types.WithdrawRecord{
		WithdrawID:  getString(m, "withdrawId", "withdraw_id"),
		Owner:       getString(m, "owner", "creator"),
		Denom:       getString(m, "denom"),
		Amount:      getString(m, "amount"),
		Destination: getString(m, "destination"),
		Nullifier:   getString(m, "nullifier"),
		Claimed:     getBool(m, "claimed"),
	}, nil
}

func getString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := m[key]
		if !ok {
			continue
		}

		switch typed := value.(type) {
		case string:
			return typed
		case float64:
			return strconv.FormatInt(int64(typed), 10)
		case bool:
			return strconv.FormatBool(typed)
		}
	}

	return ""
}

func getBool(m map[string]any, keys ...string) bool {
	for _, key := range keys {
		value, ok := m[key]
		if !ok {
			continue
		}

		switch typed := value.(type) {
		case bool:
			return typed
		case string:
			return typed == "true"
		}
	}

	return false
}

func getInt64(m map[string]any, keys ...string) int64 {
	for _, key := range keys {
		value, ok := m[key]
		if !ok {
			continue
		}

		switch typed := value.(type) {
		case float64:
			return int64(typed)
		case string:
			parsed, err := strconv.ParseInt(typed, 10, 64)
			if err == nil {
				return parsed
			}
		}
	}

	return 0
}

// parseCosmosCoinString parses simple Cosmos coin strings like:
// ""          -> {}
// "100uusdc" -> {"uusdc":"100"}
// "100uusdc,50uatom" -> {"uusdc":"100","uatom":"50"}
func parseCosmosCoinString(value string) map[string]string {
	result := make(map[string]string)

	value = strings.TrimSpace(value)
	if value == "" {
		return result
	}

	coins := strings.Split(value, ",")
	for _, coin := range coins {
		coin = strings.TrimSpace(coin)
		if coin == "" {
			continue
		}

		i := 0
		for i < len(coin) && coin[i] >= '0' && coin[i] <= '9' {
			i++
		}

		if i == 0 || i == len(coin) {
			continue
		}

		amount := coin[:i]
		denom := coin[i:]
		result[denom] = amount
	}

	return result
}
