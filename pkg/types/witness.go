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

	// Denom là micro-denom của (Owner, Denom) mà account này đại diện. APPEND-ONLY
	// + omitempty (TRD-UNIFY): witness cũ (không có denom) serialize byte-identical,
	// nên vector/schema cũ không đổi. Cần cho đường core-as-trade để dựng state cell
	// (owner, denom, oldBalance, delta) đưa vào circuit thống nhất gazk-trade-v1 —
	// thay circuit balance-smoke placeholder. Xem internal/service/core_as_trade_prover.go.
	Denom string `json:"denom,omitempty"`
}

// Witness là batch-shaped private witness P2 prover tiêu thụ theo schema
// chốt trong Agreements. Accounts mang đầy đủ thông tin để ZK-04
// (balance transition) và ZK-05 (nullifier) verify từng account.
// StatePath là tuỳ chọn (MVP dùng simplified state model — ZK-02 sẽ chốt
// commitment scheme).
//
// Trade là phần witness mở rộng cho trade (STATE-T09). APPEND-ONLY + con trỏ
// omitempty: batch core (deposit/withdraw-only) có Trade=nil → JSON witness
// byte-identical với schema cũ. Chỉ batch có trade mới populate.
type Witness struct {
	Accounts  []WitnessAccount `json:"accounts"`
	StatePath []string         `json:"statePath,omitempty"`
	Trade     *TradeWitness    `json:"trade,omitempty"`
}

// TradeWitness là phần private witness cho trade batch (STATE-T09) mà circuit
// P2 cần để: replay matching (Orders + Fills), verify chữ ký order, và verify
// chuyển dịch số dư CÓ khoá/nhả (available + reserved cũ/mới cho mỗi
// buyer/seller/feeAccount theo denom). Thiếu reserved thì circuit không chứng
// minh được việc khoá/nhả — nên cả available lẫn reserved đều bắt buộc.
type TradeWitness struct {
	OldStateRoot string                `json:"oldStateRoot"`
	NewStateRoot string                `json:"newStateRoot"`
	OrdersRoot   string                `json:"ordersRoot"`
	TradesRoot   string                `json:"tradesRoot"`
	Balances     []TradeWitnessBalance `json:"balances"`
	Orders       []TradeWitnessOrder   `json:"orders"`
	Fills        []Fill                `json:"fills"`
}

// TradeWitnessBalance là available/reserved CŨ và MỚI của một (owner, denom).
// Cả bốn giá trị bắt buộc — reserved bind việc khoá collateral vào root.
type TradeWitnessBalance struct {
	Owner        string `json:"owner"`
	Denom        string `json:"denom"`
	OldAvailable string `json:"oldAvailable"`
	OldReserved  string `json:"oldReserved"`
	NewAvailable string `json:"newAvailable"`
	NewReserved  string `json:"newReserved"`
}

// TradeWitnessOrder là dữ liệu private của một order để circuit re-hash canonical
// → orderHash, verify chữ ký ADR-036 và bind orderNullifier. MerklePath tuỳ chọn
// (MVP flat state; ZK-02 chốt merkle scheme).
type TradeWitnessOrder struct {
	OrderHash      string    `json:"orderHash"`
	OrderNullifier string    `json:"orderNullifier"`
	Owner          string    `json:"owner"`
	Market         string    `json:"market"`
	Side           OrderSide `json:"side"`
	Price          string    `json:"price"`
	Qty            string    `json:"qty"`
	Expiry         string    `json:"expiry"`
	Nonce          string    `json:"nonce"`
	Signature      string    `json:"signature"`
	Filled         bool      `json:"filled"`
	Remaining      string    `json:"remaining"`
	MerklePath     []string  `json:"merklePath,omitempty"`
}
