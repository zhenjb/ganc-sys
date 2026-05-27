package types

// BatchCommitments là 4 commitment root buộc toàn bộ batch payload vào
// proof public inputs. Theo Agreements, public inputs publicInputs[2..5]
// trỏ đúng vào 4 root này — đổi sang scalar (depositAmount, nullifier
// đơn lẻ) là vi phạm contract.
//
// Mọi root đều là hex string "0x"-prefixed. MVP dùng SHA-256 over bytes
// canonical làm placeholder; khi ZK-02 chốt hash circuit (Poseidon/MiMC)
// sẽ bump domain tag và regenerate vectors.
type BatchCommitments struct {
	DepositsRoot        string `json:"depositsRoot"`
	WithdrawalsRoot     string `json:"withdrawalsRoot"`
	NullifiersRoot      string `json:"nullifiersRoot"`
	WithdrawOutputsRoot string `json:"withdrawOutputsRoot"`
}
