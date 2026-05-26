// gen_state_vectors materializes canonical Alice 100/40 vectors dưới
// testvectors/alice_100_40/ cho P3 STATE-02..STATE-11 — phiên bản
// batch-shaped tuân theo Agreements (deposits[], withdrawals[], thêm
// batch_commitments_batch_1.json, public_inputs_batch_1.json, và
// MANIFEST.json cho STATE-11 canonical folder).
//
// Chạy từ repo root:
//
//	go run ./p3/script-test/gen_state_vectors
//
// Output files được check-in để P1 verifier / P2 prover / P4 backend /
// P5 UI consume mà không phải re-run program. MANIFEST.json bind tất cả
// file vào một index machine-readable + SHA-256 — bất kỳ ai đụng tay
// vào folder mà không qua generator sẽ khiến manifest mismatch và
// determinism test (pkg/testvectors) fail.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

const (
	outDir       = "testvectors/alice_100_40"
	scenarioName = "alice_100_40"
	aliceAddr    = "cosmos1alice"
	denom        = "uusdc"
	aliceSecret  = "alice_secret"

	// vectorVersion bump khi format file (hash circuit / domain tag /
	// schema) thay đổi. STATE-11 chốt v0 baseline. ZK-02 sẽ bump v1.
	vectorVersion = "v0"

	manifestName = "MANIFEST.json"
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
	BatchID         string                 `json:"batchId"`
	Commitments     types.BatchCommitments `json:"commitments"`
	DepositsTag     string                 `json:"depositsDomainTag"`
	WithdrawalsTag  string                 `json:"withdrawalsDomainTag"`
	NullifiersTag   string                 `json:"nullifiersDomainTag"`
	WithdrawOutsTag string                 `json:"withdrawOutputsDomainTag"`
	HashAlgorithm   string                 `json:"hashAlgorithm"`
	Note            string                 `json:"note,omitempty"`
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

// manifestEntry mô tả một file vector trong folder. SHA256 chốt nội
// dung file (gồm trailing newline đúng như write() phát ra) — anyone
// edit tay file sẽ làm SHA256 lệch và determinism test fail.
type manifestEntry struct {
	Name      string   `json:"name"`
	StateTask string   `json:"stateTask"`
	Schema    string   `json:"schema"`
	Consumers []string `json:"consumers"`
	SHA256    string   `json:"sha256"`
	Note      string   `json:"note,omitempty"`
}

// manifestRoots tóm tắt 3 root chính của scenario — giúp consumer
// (đặc biệt P1 verifier / P5 UI) đối chiếu nhanh mà không phải mở 3
// file riêng.
type manifestRoots struct {
	RootA string `json:"rootA"`
	RootB string `json:"rootB"`
	RootC string `json:"rootC"`
}

type manifestAlice struct {
	Address          string `json:"address"`
	Denom            string `json:"denom"`
	DepositAmount    string `json:"depositAmount"`
	WithdrawAmount   string `json:"withdrawAmount"`
	StartingBalance  string `json:"startingBalance"`
	FinalBalance     string `json:"finalBalance"`
	ModuleAccountEnd string `json:"moduleAccountFinalBalance"`
}

type manifestDomainTags struct {
	Nullifier           string `json:"nullifier"`
	WithdrawAddress     string `json:"withdrawAddress"`
	DepositsRoot        string `json:"depositsRoot"`
	WithdrawalsRoot     string `json:"withdrawalsRoot"`
	NullifiersRoot      string `json:"nullifiersRoot"`
	WithdrawOutputsRoot string `json:"withdrawOutputsRoot"`
}

type manifest struct {
	Scenario      string             `json:"scenario"`
	Description   string             `json:"description"`
	VectorVersion string             `json:"vectorVersion"`
	HashAlgorithm string             `json:"hashAlgorithm"`
	Generator     string             `json:"generator"`
	Alice         manifestAlice      `json:"alice"`
	Roots         manifestRoots      `json:"roots"`
	DomainTags    manifestDomainTags `json:"domainTags"`
	Files         []manifestEntry    `json:"files"`
	Note          string             `json:"note,omitempty"`
}

// fileMeta là descriptor STATE-11 dùng để build manifest. Phải khai báo
// trước khi write() để tránh thiếu metadata cho file mới.
type fileMeta struct {
	stateTask string
	schema    string
	consumers []string
	note      string
}

// writer collect output files theo đúng thứ tự generation + metadata
// để build MANIFEST.json sau cùng.
type writer struct {
	outDir   string
	files    []manifestEntry
	seen     map[string]bool
}

func newWriter(dir string) *writer {
	return &writer{outDir: dir, seen: make(map[string]bool)}
}

// write serialize v ra <outDir>/name, đồng thời append entry vào
// manifest tracker với SHA-256 nội dung đã ghi. Yêu cầu meta non-empty
// — file không metadata không được publish ra folder canonical.
func (w *writer) write(name string, v any, meta fileMeta) {
	if w.seen[name] {
		die("duplicate file: %s", name)
	}
	if meta.stateTask == "" || meta.schema == "" {
		die("manifest meta required for %s", name)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		die("marshal %s: %v", name, err)
	}
	content := append(b, '\n')
	path := filepath.Join(w.outDir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		die("write %s: %v", path, err)
	}
	digest := sha256.Sum256(content)
	w.files = append(w.files, manifestEntry{
		Name:      name,
		StateTask: meta.stateTask,
		Schema:    meta.schema,
		Consumers: append([]string(nil), meta.consumers...),
		SHA256:    "0x" + hex.EncodeToString(digest[:]),
		Note:      meta.note,
	})
	w.seen[name] = true
}

// emitManifest write MANIFEST.json cuối cùng. Files được sort
// alphabetical để diff giữa các lần run dễ đọc; metadata kèm theo bao
// gồm domain tags hiện hành + 3 root canonical để consumer nắm tổng
// thể mà không cần mở từng file.
func (w *writer) emitManifest(m manifest) {
	files := append([]manifestEntry(nil), w.files...)
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].Name < files[j].Name
	})
	m.Files = files
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		die("marshal manifest: %v", err)
	}
	path := filepath.Join(w.outDir, manifestName)
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		die("write manifest: %v", err)
	}
}

func main() {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		die("mkdir: %v", err)
	}
	w := newWriter(outDir)

	ls := state.NewLocalState()
	rootA := ls.Root()
	initial := stateSnapshot{
		Root:     rootA,
		Accounts: ls.Snapshot(),
		Note:     "STATE-02 — initial local state, empty accounts. rootA.",
	}
	w.write("initial_state.json", initial, fileMeta{
		stateTask: "STATE-02",
		schema:    "LocalStateSnapshot",
		consumers: []string{"P1", "P2", "P3", "P4"},
		note:      "Baseline rootA = root of empty account set.",
	})

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
	w.write("deposit_dep_1.json", dep1, fileMeta{
		stateTask: "STATE-03 (input)",
		schema:    "types.DepositRecord",
		consumers: []string{"P1", "P3", "P4"},
		note:      "Canonical dep-1 record. TxHash khớp chain.MockClient determinism recipe.",
	})

	rootB, err := ls.ApplyDeposit(dep1)
	if err != nil {
		die("apply deposit: %v", err)
	}
	after := stateSnapshot{
		Root:     rootB,
		Accounts: ls.Snapshot(),
		Note:     "STATE-03 — after applying dep-1, Alice balance=100. rootB.",
	}
	w.write("state_after_deposit.json", after, fileMeta{
		stateTask: "STATE-03",
		schema:    "LocalStateSnapshot",
		consumers: []string{"P2", "P3", "P4"},
		note:      "Pending off-chain state sau khi deposit credit. rootB = settlementUpdate.oldStateRoot.",
	})

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
	w.write("withdraw_request_wd_1.json", wdReq, fileMeta{
		stateTask: "STATE-04",
		schema:    "types.WithdrawRequest",
		consumers: []string{"P3", "P4", "P5"},
		note:      "Canonical wd-1 request. Nonce=1 ăn khớp post-state nonce.",
	})

	// Re-snapshot để assert STATE-04 không mutate state.
	postBuildRoot := ls.Root()
	if postBuildRoot != rootB {
		die("STATE-04 mutated root: rootB=%s, after-build=%s", rootB, postBuildRoot)
	}

	// STATE-06 — derive canonical Alice nullifier.
	nullifier, err := state.NullifierFor(aliceSecret, wdReq.Nonce)
	if err != nil {
		die("nullifier: %v", err)
	}
	w.write("nullifier_wd_1.json", nullifierVector{
		WithdrawID:    wdReq.WithdrawID,
		Owner:         aliceAddr,
		Nonce:         wdReq.Nonce,
		UserSecret:    aliceSecret,
		DomainTag:     state.NullifierDomainTag(),
		HashAlgorithm: "sha256",
		Nullifier:     nullifier,
		Note:          "STATE-06 — nullifier(domain | userSecret | nonce). Placeholder hash until ZK-02 chốt Poseidon/MiMC; bump domain tag rồi regenerate.",
	}, fileMeta{
		stateTask: "STATE-06",
		schema:    "NullifierVector",
		consumers: []string{"P1", "P2"},
		note:      "Demo-only userSecret = 'alice_secret'. KHÔNG bao giờ commit secret thật.",
	})

	// STATE-07 — derive canonical Alice destinationHash. Đặt tên file +
	// field theo Agreements (destination / destinationHash). File legacy
	// withdraw_address_hash_wd_1.json bị thay thế.
	destinationHash, err := state.WithdrawAddressHash(wdReq.Destination)
	if err != nil {
		die("destination hash: %v", err)
	}
	w.write("destination_hash_wd_1.json", destinationHashVector{
		WithdrawID:      wdReq.WithdrawID,
		Destination:     wdReq.Destination,
		DomainTag:       state.WithdrawAddressDomainTag(),
		HashAlgorithm:   "sha256",
		DestinationHash: destinationHash,
		Note:            "STATE-07 — destinationHash(domain | destination). Tên trường đã đồng bộ Agreements (destination / destinationHash).",
	}, fileMeta{
		stateTask: "STATE-07",
		schema:    "DestinationHashVector",
		consumers: []string{"P1", "P2"},
		note:      "Domain-separated khỏi nullifier để chặn cross-domain collision.",
	})

	// STATE-05 — apply canonical Alice withdrawal (40 uusdc).
	rootC, err := ls.ApplyWithdrawal(wdReq, nullifier)
	if err != nil {
		die("apply withdrawal: %v", err)
	}
	afterWithdraw := stateSnapshot{
		Root:     rootC,
		Accounts: ls.Snapshot(),
		Note:     "STATE-05 — after applying wd-1, Alice balance=60, nonce=1. rootC.",
	}
	w.write("state_after_withdrawal.json", afterWithdraw, fileMeta{
		stateTask: "STATE-05",
		schema:    "LocalStateSnapshot",
		consumers: []string{"P2", "P3", "P4"},
		note:      "Post-batch state. rootC = settlementUpdate.newStateRoot.",
	})

	// STATE-08 — assemble batch-shaped SettlementUpdate.
	sub := batch.NewSettlementUpdateBuilder()
	settlementInputs := batch.SettlementInputs{
		OldStateRoot: rootB,
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
	w.write("settlement_update_batch_1.json", upd, fileMeta{
		stateTask: "STATE-08",
		schema:    "types.SettlementUpdate",
		consumers: []string{"P1", "P2", "P4", "P5"},
		note:      "Batch-shaped: deposits[] + withdrawals[]. Schema không bao giờ rớt về scalar form.",
	})

	// STATE-08 (bổ sung) — compute BatchCommitments cho batch.
	commitments := batch.BuildCommitments(upd)
	depTag, wdTag, nfTag, woTag := batch.CommitmentDomainTags()
	w.write("batch_commitments_batch_1.json", batchCommitmentsVector{
		BatchID:         upd.BatchID,
		Commitments:     commitments,
		DepositsTag:     depTag,
		WithdrawalsTag:  wdTag,
		NullifiersTag:   nfTag,
		WithdrawOutsTag: woTag,
		HashAlgorithm:   "sha256",
		Note:            "STATE-08 (extension) — 4 commitment root bind batch vào proof public inputs[2..5].",
	}, fileMeta{
		stateTask: "STATE-08-ext",
		schema:    "BatchCommitmentsVector",
		consumers: []string{"P1", "P2", "P4"},
		note:      "4 root đóng vai trò public inputs[2..5].",
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
	w.write("witness_batch_1.json", witness, fileMeta{
		stateTask: "STATE-09",
		schema:    "types.Witness",
		consumers: []string{"P2"},
		note:      "Static vector: userSecret='alice_secret'. KHÁC runtime fallback 'mock-user-secret' (xem README).",
	})

	// STATE-10 — assemble public input slice cho ProofBundle.
	publicInputs, err := batch.BuildPublicInputs(upd, commitments)
	if err != nil {
		die("build public inputs: %v", err)
	}
	w.write("public_inputs_batch_1.json", publicInputsVector{
		BatchID:      upd.BatchID,
		Count:        len(publicInputs),
		Labels:       batch.PublicInputLabels(),
		PublicInputs: publicInputs,
		Note:         "STATE-10 — slice public input ordered theo Agreements (publicInputs[0..5]). P1 verifier / P2 circuit / P4 prover client phải đọc đúng thứ tự này.",
	}, fileMeta{
		stateTask: "STATE-10",
		schema:    "PublicInputsVector",
		consumers: []string{"P1", "P2", "P4"},
		note:      "publicInputs[0..5] = (oldStateRoot, newStateRoot, depositsRoot, withdrawalsRoot, nullifiersRoot, withdrawOutputsRoot).",
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

	// STATE-11 — emit MANIFEST.json.
	w.emitManifest(manifest{
		Scenario:      scenarioName,
		Description:   "Alice 100/40 canonical scenario: deposit 100 uusdc, withdraw 40 uusdc, final user balance = 60.",
		VectorVersion: vectorVersion,
		HashAlgorithm: "sha256",
		Generator:     "p3/script-test/gen_state_vectors",
		Alice: manifestAlice{
			Address:          aliceAddr,
			Denom:            denom,
			DepositAmount:    "100",
			WithdrawAmount:   "40",
			StartingBalance:  "1000",
			FinalBalance:     "60",
			ModuleAccountEnd: "60",
		},
		Roots: manifestRoots{
			RootA: rootA,
			RootB: rootB,
			RootC: rootC,
		},
		DomainTags: manifestDomainTags{
			Nullifier:           state.NullifierDomainTag(),
			WithdrawAddress:     state.WithdrawAddressDomainTag(),
			DepositsRoot:        depTag,
			WithdrawalsRoot:     wdTag,
			NullifiersRoot:      nfTag,
			WithdrawOutputsRoot: woTag,
		},
		Note: "STATE-11 — file inventory + SHA-256. Bump vectorVersion khi ZK-02 chốt hash circuit. KHÔNG hand-edit; chạy `go run ./p3/script-test/gen_state_vectors` để regenerate.",
	})

	fmt.Println("rootA:", rootA)
	fmt.Println("rootB:", rootB)
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
	fmt.Printf("manifest (STATE-11): %s (%d files, version %s)\n",
		manifestName, len(w.files), vectorVersion)
	fmt.Println("wrote vectors into", outDir)
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
