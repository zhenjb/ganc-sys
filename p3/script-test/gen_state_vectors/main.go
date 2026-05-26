// gen_state_vectors materializes canonical Alice 100/40 vectors dưới
// testvectors/alice_100_40/ cho P3 STATE-02..STATE-09 — phiên bản
// batch-shaped tuân theo Agreements (deposits[], withdrawals[], thêm
// batch_commitments_batch_1.json).
//
// Chạy từ repo root:
//
//	go run ./p3/script-test/gen_state_vectors
//
// Output files được check-in để P2 prover, P1 verifier, P4 backend
// consume mà không phải re-run program.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

const (
	outDir      = "testvectors/alice_100_40"
	aliceAddr   = "cosmos1alice"
	denom       = "uusdc"
	aliceSecret = "alice_secret"
)

type stateSnapshot struct {
	Root     string          `json:"root"`
	Accounts []types.Account `json:"accounts"`
	Note     string          `json:"note,omitempty"`
}

type nullifierVector struct {
	WithdrawID    string `json:"withdrawId"`
	Owner         string `json:"owner"`
	Nonce         string `json:"nonce"`
	UserSecret    string `json:"userSecret"`
	DomainTag     string `json:"domainTag"`
	HashAlgorithm string `json:"hashAlgorithm"`
	Nullifier     string `json:"nullifier"`
	Note          string `json:"note,omitempty"`
}

// destinationHashVector dùng tên trường Agreements (destination /
// destinationHash) thay vì withdrawAddress / withdrawAddressHash.
type destinationHashVector struct {
	WithdrawID      string `json:"withdrawId"`
	Destination     string `json:"destination"`
	DomainTag       string `json:"domainTag"`
	HashAlgorithm   string `json:"hashAlgorithm"`
	DestinationHash string `json:"destinationHash"`
	Note            string `json:"note,omitempty"`
}

// batchCommitmentsVector wrap BatchCommitments + meta để debug/dependent
// roles biết domain tags đã được dùng.
type batchCommitmentsVector struct {
	BatchID          string                 `json:"batchId"`
	Commitments      types.BatchCommitments `json:"commitments"`
	DepositsTag      string                 `json:"depositsDomainTag"`
	WithdrawalsTag   string                 `json:"withdrawalsDomainTag"`
	NullifiersTag    string                 `json:"nullifiersDomainTag"`
	WithdrawOutsTag  string                 `json:"withdrawOutputsDomainTag"`
	HashAlgorithm    string                 `json:"hashAlgorithm"`
	Note             string                 `json:"note,omitempty"`
}

// publicInputsVector materialize STATE-10 — slice public input theo đúng
// thứ tự Agreements + meta để P1/P2/P4 đối chiếu.
//
//   - PublicInputs là slice values, thứ tự khớp Labels (cùng index).
//   - Count = len(PublicInputs) = batch.PublicInputCount.
//   - Labels giúp test/log dễ đọc nhưng PHẢI không được dùng để re-order
//     ở consumer; consumer dùng index const PublicInputIdx*.
type publicInputsVector struct {
	BatchID      string   `json:"batchId"`
	Count        int      `json:"count"`
	Labels       []string `json:"labels"`
	PublicInputs []string `json:"publicInputs"`
	Note         string   `json:"note,omitempty"`
}

func main() {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		die("mkdir: %v", err)
	}

	ls := state.NewLocalState()
	initial := stateSnapshot{
		Root:     ls.Root(),
		Accounts: ls.Snapshot(),
		Note:     "STATE-02 — initial local state, empty accounts. rootA.",
	}
	write("initial_state.json", initial)

	// STATE-09 cần oldBalance = balance TRƯỚC khi batch apply. Capture
	// ngay ở đây để ApplyDeposit / ApplyWithdrawal sau không che mất.
	oldBalance := ls.Account(aliceAddr, denom).Balance

	dep1 := types.DepositRecord{
		DepositID:     "dep-1",
		Owner:         aliceAddr,
		Denom:         denom,
		Amount:        "100",
		Processed:     false,
		CreatedHeight: 12345,
		TxHash:        canonicalTxHash("deposit", aliceAddr, denom, "100", "dep-1"),
	}
	write("deposit_dep_1.json", dep1)

	newRoot, err := ls.ApplyDeposit(dep1)
	if err != nil {
		die("apply deposit: %v", err)
	}
	after := stateSnapshot{
		Root:     newRoot,
		Accounts: ls.Snapshot(),
		Note:     "STATE-03 — after applying dep-1, Alice balance=100. rootB.",
	}
	write("state_after_deposit.json", after)

	// STATE-04 — build canonical Alice withdraw request (40 uusdc).
	wb := state.NewWithdrawRequestBuilder(ls)
	wdReq, err := wb.Build(state.WithdrawIntent{
		Owner:       aliceAddr,
		Denom:       denom,
		Amount:      "40",
		Destination: aliceAddr,
	})
	if err != nil {
		die("build withdraw request: %v", err)
	}
	write("withdraw_request_wd_1.json", wdReq)

	// Re-snapshot để assert STATE-04 không mutate state.
	postBuildRoot := ls.Root()
	if postBuildRoot != newRoot {
		die("STATE-04 mutated root: rootB=%s, after-build=%s", newRoot, postBuildRoot)
	}

	// STATE-06 — derive canonical Alice nullifier.
	nullifier, err := state.NullifierFor(aliceSecret, wdReq.Nonce)
	if err != nil {
		die("nullifier: %v", err)
	}
	write("nullifier_wd_1.json", nullifierVector{
		WithdrawID:    wdReq.WithdrawID,
		Owner:         aliceAddr,
		Nonce:         wdReq.Nonce,
		UserSecret:    aliceSecret,
		DomainTag:     state.NullifierDomainTag(),
		HashAlgorithm: "sha256",
		Nullifier:     nullifier,
		Note:          "STATE-06 — nullifier(domain | userSecret | nonce). Placeholder hash until ZK-02 chốt Poseidon/MiMC; bump domain tag rồi regenerate.",
	})

	// STATE-07 — derive canonical Alice destinationHash. Đặt tên file +
	// field theo Agreements (destination / destinationHash). File legacy
	// withdraw_address_hash_wd_1.json bị thay thế.
	destinationHash, err := state.WithdrawAddressHash(wdReq.Destination)
	if err != nil {
		die("destination hash: %v", err)
	}
	write("destination_hash_wd_1.json", destinationHashVector{
		WithdrawID:      wdReq.WithdrawID,
		Destination:     wdReq.Destination,
		DomainTag:       state.WithdrawAddressDomainTag(),
		HashAlgorithm:   "sha256",
		DestinationHash: destinationHash,
		Note:            "STATE-07 — destinationHash(domain | destination). Tên trường đã đồng bộ Agreements (destination / destinationHash).",
	})

	// STATE-05 — apply canonical Alice withdrawal (40 uusdc).
	oldRoot := ls.Root()
	rootC, err := ls.ApplyWithdrawal(wdReq, nullifier)
	if err != nil {
		die("apply withdrawal: %v", err)
	}
	afterWithdraw := stateSnapshot{
		Root:     rootC,
		Accounts: ls.Snapshot(),
		Note:     "STATE-05 — after applying wd-1, Alice balance=60, nonce=1. rootC.",
	}
	write("state_after_withdrawal.json", afterWithdraw)

	// STATE-08 — assemble batch-shaped SettlementUpdate.
	sub := batch.NewSettlementUpdateBuilder()
	settlementInputs := batch.SettlementInputs{
		OldStateRoot: oldRoot,
		NewStateRoot: rootC,
		Deposits:     []types.DepositRecord{dep1},
		Withdrawals: []batch.WithdrawalInput{
			{
				Request:         wdReq,
				Nullifier:       nullifier,
				DestinationHash: destinationHash,
			},
		},
	}
	upd, err := sub.Build(settlementInputs)
	if err != nil {
		die("build settlement update: %v", err)
	}
	write("settlement_update_batch_1.json", upd)

	// STATE-08 (bổ sung) — compute BatchCommitments cho batch.
	commitments := batch.BuildCommitments(upd)
	depTag, wdTag, nfTag, woTag := batch.CommitmentDomainTags()
	write("batch_commitments_batch_1.json", batchCommitmentsVector{
		BatchID:         upd.BatchID,
		Commitments:     commitments,
		DepositsTag:     depTag,
		WithdrawalsTag:  wdTag,
		NullifiersTag:   nfTag,
		WithdrawOutsTag: woTag,
		HashAlgorithm:   "sha256",
		Note:            "STATE-08 (extension) — 4 commitment root bind batch vào proof public inputs[2..5].",
	})

	// STATE-09 — assemble batch-shaped Witness.
	newBalance := ls.Account(aliceAddr, denom).Balance
	wbuilder := batch.NewWitnessBuilder()
	witness, err := wbuilder.Build(batch.WitnessInputs{
		Settlement: settlementInputs,
		Accounts: []batch.AccountWitnessSecret{
			{
				Owner:      aliceAddr,
				UserSecret: aliceSecret,
				OldBalance: oldBalance,
				NewBalance: newBalance,
			},
		},
	})
	if err != nil {
		die("build witness: %v", err)
	}
	write("witness_batch_1.json", witness)

	// STATE-10 — assemble public input slice cho ProofBundle.
	publicInputs, err := batch.BuildPublicInputs(upd, commitments)
	if err != nil {
		die("build public inputs: %v", err)
	}
	write("public_inputs_batch_1.json", publicInputsVector{
		BatchID:      upd.BatchID,
		Count:        len(publicInputs),
		Labels:       batch.PublicInputLabels(),
		PublicInputs: publicInputs,
		Note:         "STATE-10 — slice public input ordered theo Agreements (publicInputs[0..5]). P1 verifier / P2 circuit / P4 prover client phải đọc đúng thứ tự này.",
	})

	// Sanity: post-STATE-05 invariant.
	acc := ls.Account(aliceAddr, denom)
	if acc.Nonce != wdReq.Nonce {
		die("post-STATE-05 invariant violated: account.Nonce=%s, request.Nonce=%s", acc.Nonce, wdReq.Nonce)
	}
	if acc.Balance != "60" {
		die("post-STATE-05 balance: want 60, got %s", acc.Balance)
	}

	// Xoá vector legacy nếu còn (đổi tên withdraw_address_hash_*).
	legacyHash := filepath.Join(outDir, "withdraw_address_hash_wd_1.json")
	if err := os.Remove(legacyHash); err != nil && !os.IsNotExist(err) {
		die("remove legacy %s: %v", legacyHash, err)
	}

	fmt.Println("rootA:", initial.Root)
	fmt.Println("rootB:", newRoot)
	fmt.Println("rootC:", rootC)
	fmt.Println("withdrawRequest:", wdReq.WithdrawID, "nonce:", wdReq.Nonce)
	fmt.Println("nullifier (placeholder):", nullifier)
	fmt.Println("destinationHash (placeholder):", destinationHash)
	fmt.Println("settlementUpdate:", upd.BatchID,
		"deposits:", len(upd.Deposits), "withdrawals:", len(upd.Withdrawals))
	fmt.Println("batchCommitments:",
		"depositsRoot:", commitments.DepositsRoot,
		"withdrawalsRoot:", commitments.WithdrawalsRoot,
		"nullifiersRoot:", commitments.NullifiersRoot,
		"withdrawOutputsRoot:", commitments.WithdrawOutputsRoot)
	fmt.Println("publicInputs (STATE-10):", len(publicInputs), "entries")
	for i, pi := range publicInputs {
		fmt.Printf("  [%d] %s = %s\n", i, batch.PublicInputLabels()[i], pi)
	}
	if len(witness.Accounts) > 0 {
		fmt.Println("witness account[0]:",
			"owner:", witness.Accounts[0].Owner,
			"oldBalance:", witness.Accounts[0].OldBalance,
			"newBalance:", witness.Accounts[0].NewBalance,
			"nonce:", witness.Accounts[0].Nonce)
	}
	fmt.Println("wrote vectors into", outDir)
}

func write(name string, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		die("marshal %s: %v", name, err)
	}
	path := filepath.Join(outDir, name)
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		die("write %s: %v", path, err)
	}
}

// canonicalTxHash mirrors recipe của P4 chain.MockClient
// (`internal/chain/mock_client.go::mockTxHash`) để static vector khớp
// với mock runtime cho cùng input.
func canonicalTxHash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte("|"))
	}
	return "0x" + strings.ToLower(hex.EncodeToString(h.Sum(nil)))[:32]
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen_state_vectors: "+format+"\n", args...)
	os.Exit(1)
}
