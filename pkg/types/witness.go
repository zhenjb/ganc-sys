package types

// WitnessAccount là phần private witness cho một account tham gia batch.
// Owner trùng với owner trong các entry Deposit/Withdrawal mà account này
// tạo ra ở SettlementUpdate. UserSecret là material bí mật KHÔNG bao giờ
// được publish ra ngoài witness file.
type WitnessAccount struct {
	Owner      string `json:"owner"`
	UserSecret string `json:"userSecret"`
	Nonce      string `json:"nonce"`
	OldBalance string `json:"oldBalance"`
	NewBalance string `json:"newBalance"`
}

// Witness là batch-shaped private witness P2 prover tiêu thụ theo schema
// chốt trong Agreements. Accounts mang đầy đủ thông tin để ZK-04
// (balance transition) và ZK-05 (nullifier) verify từng account.
// StatePath là tuỳ chọn (MVP dùng simplified state model — ZK-02 sẽ chốt
// commitment scheme).
type Witness struct {
	Accounts  []WitnessAccount `json:"accounts"`
	StatePath []string         `json:"statePath,omitempty"`
}
