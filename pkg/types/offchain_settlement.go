package types

type CancelOffchainSettlementBatchRequestBody struct {
	Reason string `json:"reason"`
}

type CancelOffchainSettlementBatchResponse struct {
	BatchID string `json:"batchId"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
}
