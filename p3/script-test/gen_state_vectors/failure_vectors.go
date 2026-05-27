package main

// STATE-12 — failure vectors generator.
//
// File này extend gen_state_vectors để emit subfolder
// testvectors/<scenario>/failure_vectors/. Sản phẩm là 4 JSON canonical:
//
//   - over_withdraw.json
//   - wrong_root.json
//   - duplicate_nullifier.json
//   - tampered_destination.json
//
// + MANIFEST.json riêng cho subfolder. Schema struct match 1:1 với
// pkg/testvectors/failure_vectors.go — generator import struct từ đó
// thay vì redeclare (giảm drift so với happy-path nơi struct được mirror
// thủ công).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/testvectors"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// failureWriter collect failure file metadata để build MANIFEST.json
// riêng cho subfolder. Tương tự writer ở main.go nhưng schema entry
// dùng `case` thay vì `stateTask`.
type failureWriter struct {
	outDir string
	files  []testvectors.FailureManifestEntry
	seen   map[string]bool
}

func newFailureWriter(dir string) *failureWriter {
	return &failureWriter{outDir: dir, seen: make(map[string]bool)}
}

type failureFileMeta struct {
	caseName  string
	schema    string
	consumers []string
	note      string
}

// write serialize v + đính entry vào tracker. SHA-256 nội dung file
// (gồm trailing newline đúng như write phát ra) → fingerprint quyết
// định nội dung.
func (w *failureWriter) write(name string, v any, meta failureFileMeta) {
	if w.seen[name] {
		die("duplicate failure file: %s", name)
	}
	if meta.caseName == "" || meta.schema == "" {
		die("failure manifest meta required for %s", name)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		die("marshal failure %s: %v", name, err)
	}
	content := append(b, '\n')
	path := filepath.Join(w.outDir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		die("write failure %s: %v", path, err)
	}
	digest := sha256.Sum256(content)
	w.files = append(w.files, testvectors.FailureManifestEntry{
		Name:      name,
		Case:      meta.caseName,
		Schema:    meta.schema,
		Consumers: append([]string(nil), meta.consumers...),
		SHA256:    "0x" + hex.EncodeToString(digest[:]),
		Note:      meta.note,
	})
	w.seen[name] = true
}

// emitManifest ghi failure_vectors/MANIFEST.json. Files sort alphabetical
// để diff cross-run ổn định.
func (w *failureWriter) emitManifest(m testvectors.FailureManifest) {
	files := append([]testvectors.FailureManifestEntry(nil), w.files...)
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].Name < files[j].Name
	})
	m.Files = files
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		die("marshal failure manifest: %v", err)
	}
	path := filepath.Join(w.outDir, testvectors.FailureManifestFile)
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		die("write failure manifest: %v", err)
	}
}

// happyPathArtifacts là cụm dữ liệu happy path mà generator chính đã
// dựng xong và bây giờ chuyển cho failure generator để clone/mutate.
// Giữ trong struct riêng để main.go không phải truyền 10 tham số.
type happyPathArtifacts struct {
	RootA, RootB, RootC string
	Deposit             types.DepositRecord
	WithdrawRequest     types.WithdrawRequest
	Nullifier           string
	DestinationHash     string
	Settlement          types.SettlementUpdate
	Commitments         types.BatchCommitments
}

// emitFailureVectors là entry point được main.go gọi sau happy path.
// Idempotent: chạy lại sẽ overwrite cả 4 file + MANIFEST.json.
func emitFailureVectors(scenarioDir string, hp happyPathArtifacts) {
	subDir := filepath.Join(scenarioDir, testvectors.FailureVectorsSubdir)
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		die("mkdir failure subdir: %v", err)
	}
	w := newFailureWriter(subDir)

	emitOverWithdraw(w, hp)
	emitWrongRoot(w, hp)
	emitDuplicateNullifier(w, hp)
	emitTamperedDestination(w, hp)

	w.emitManifest(testvectors.FailureManifest{
		Scenario:      scenarioName,
		Subfolder:     testvectors.FailureVectorsSubdir,
		Description:   "STATE-12 — bốn negative vector canonical cho luồng Alice 100/40. Mỗi file là một input PHẢI bị reject ở stage chỉ định.",
		VectorVersion: vectorVersion,
		HashAlgorithm: "sha256",
		Generator:     "p3/script-test/gen_state_vectors",
		HappyPathRoots: testvectors.ManifestRoots{
			RootA: hp.RootA,
			RootB: hp.RootB,
			RootC: hp.RootC,
		},
		Note: "STATE-12 — file inventory + SHA-256 cho failure_vectors. KHÔNG hand-edit; chạy `go run ./p3/script-test/gen_state_vectors` để regenerate.",
	})
}

// -------------------- per-case emitters --------------------

// emitOverWithdraw — Alice mới deposit 100 (balance=100), giả vờ withdraw
// 200. Builder STATE-04 PHẢI trả về state.ErrInsufficientBalance.
//
// Account snapshot: lấy chính LocalState sau khi ApplyDeposit nhưng
// TRƯỚC khi withdraw apply — đây là moment realistic mà P4 BatchService
// hứng intent của user.
func emitOverWithdraw(w *failureWriter, hp happyPathArtifacts) {
	// Re-derive account snapshot post-deposit: LocalState mới + apply
	// dep1 lại. Tránh phụ thuộc ls.Snapshot() ở caller (đã advance qua
	// rootC).
	ls := state.NewLocalState()
	if _, err := ls.ApplyDeposit(hp.Deposit); err != nil {
		die("over_withdraw: re-apply deposit: %v", err)
	}
	acc := ls.Account(hp.WithdrawRequest.Owner, hp.WithdrawRequest.Denom)

	vec := testvectors.OverWithdrawVector{
		FailureMeta: testvectors.FailureMeta{
			Case:               testvectors.FailureCaseOverWithdraw,
			Title:              "Over-withdraw — Alice cố rút vượt balance",
			Description:        "Alice vừa deposit 100, balance off-chain = 100, nhưng intent ghi amount=200. Bất kỳ pipeline nào dẫn intent qua STATE-04 builder hoặc trực tiếp STATE-05 đều phải reject ngay tại stage validate balance.",
			ViolatedInvariant:  "off-chain balance ≥ withdraw amount (state.ErrInsufficientBalance)",
			HappyPathReference: testvectors.ScenarioName,
			Mutation:           "Intent.Amount = '200' (vượt balance=100). Mọi field khác giữ y hệt withdraw_request_wd_1 happy path.",
			Rejection: testvectors.FailureRejection{
				Stage:           testvectors.FailureStageState04Builder,
				Sentinel:        "state.ErrInsufficientBalance",
				MessageContains: "insufficient balance",
				Note:            "STATE-05 ApplyWithdrawal cũng reject với cùng sentinel nếu pipeline skip STATE-04 và đẩy thẳng request giả mạo (amount=200) vào ApplyWithdrawal.",
			},
			Consumers: []string{"P3", "P4"},
		},
		AccountSnapshot: acc,
		Intent: testvectors.WithdrawIntentVector{
			Owner:       hp.WithdrawRequest.Owner,
			Denom:       hp.WithdrawRequest.Denom,
			Amount:      "200",
			Destination: hp.WithdrawRequest.Destination,
		},
	}
	w.write(testvectors.FileOverWithdraw, vec, failureFileMeta{
		caseName:  testvectors.FailureCaseOverWithdraw,
		schema:    "OverWithdrawVector",
		consumers: []string{"P3", "P4"},
		note:      "STATE-04 builder reject ngay balance check, không cần invoke STATE-05.",
	})
}

// emitWrongRoot — clone happy path SettlementUpdate nhưng đặt
// OldStateRoot lệch hẳn so với rootB. Chain MsgSubmitBatchProof phải
// reject ở step 8 (check oldStateRoot == currentStateRoot).
//
// Cả BatchCommitments và PublicInputs ĐƯỢC tái derive từ tampered
// settlement: vì OldStateRoot không đi vào commitments slice (chỉ ảnh
// hưởng publicInputs[0]), commitments thực ra trùng happy path —
// publicInputs[0] thì khác.
func emitWrongRoot(w *failureWriter, hp happyPathArtifacts) {
	tampered := cloneSettlement(hp.Settlement)
	// Placeholder root deterministic ≠ rootB. Dùng SHA-256 của chuỗi
	// const để mỗi lần regenerate cho ra cùng một giá trị (KHÔNG random).
	tampered.OldStateRoot = "0x" + sha256Hex32("STATE-12/wrong_root/oldStateRoot")

	com := batch.BuildCommitments(tampered)
	pi, err := batch.BuildPublicInputs(tampered, com)
	if err != nil {
		die("wrong_root: build public inputs: %v", err)
	}

	vec := testvectors.WrongRootVector{
		FailureMeta: testvectors.FailureMeta{
			Case:               testvectors.FailureCaseWrongRoot,
			Title:              "Wrong root — relayer submit SettlementUpdate.OldStateRoot lệch",
			Description:        "Settlement update giữ nguyên deposits/withdrawals của batch-1 happy path nhưng OldStateRoot bị thay bằng giá trị placeholder (KHÔNG khớp currentStateRoot=rootB của chain). Chain verifier phải reject TRƯỚC khi gọi VerifyProof.",
			ViolatedInvariant:  "SettlementUpdate.OldStateRoot == x/zkdex.currentStateRoot",
			HappyPathReference: testvectors.ScenarioName,
			Mutation:           "tamperedSettlement.OldStateRoot = SHA256('STATE-12/wrong_root/oldStateRoot') (deterministic, ≠ rootB).",
			Rejection: testvectors.FailureRejection{
				Stage:           testvectors.FailureStageChainVerifier,
				Sentinel:        "x/zkdex: ErrStaleStateRoot (P1 ONCHAIN-08)",
				MessageContains: "oldStateRoot",
				Note:            "Verifier reject ở ONCHAIN-08 trước khi proof verify chạy. STATE-10 PublicInputBuilder vẫn build được publicInputs[] vì validateHex pass — sai sót về SEMANTIC root chỉ chain mới đối chiếu được.",
			},
			Consumers: []string{"P1", "P4"},
		},
		CorrectOldStateRoot: hp.RootB,
		TamperedSettlement:  tampered,
		BatchCommitments:    com,
		PublicInputs:        pi,
	}
	w.write(testvectors.FileWrongRoot, vec, failureFileMeta{
		caseName:  testvectors.FailureCaseWrongRoot,
		schema:    "WrongRootVector",
		consumers: []string{"P1", "P4"},
		note:      "Reject ở ONCHAIN-08 (chain), không phải ở P3 builder.",
	})
}

// emitDuplicateNullifier — clone happy path settlement, append entry
// thứ hai 'wd-2-replay' với CÙNG nullifier như wd-1. Intra-batch dup
// check trên chain (hoặc STATE-05 nếu apply tuần tự cùng nullifier hai
// lần trên cùng LocalState).
//
// Amount của wd-2-replay = "20" để không trùng phương vị wd-1 (Alice
// thực tế chỉ withdraw được 60 nhưng vector này không cần balance khả
// thi — verifier reject TRƯỚC khi đụng balance check).
func emitDuplicateNullifier(w *failureWriter, hp happyPathArtifacts) {
	tampered := cloneSettlement(hp.Settlement)
	replay := tampered.Withdrawals[0] // shallow copy struct
	replay.WithdrawID = "wd-2-replay"
	replay.Amount = "20"
	// nullifier + destinationHash giữ y hệt wd-1 → intra-batch replay.
	tampered.Withdrawals = append(tampered.Withdrawals, replay)

	com := batch.BuildCommitments(tampered)
	pi, err := batch.BuildPublicInputs(tampered, com)
	if err != nil {
		die("duplicate_nullifier: build public inputs: %v", err)
	}

	vec := testvectors.DuplicateNullifierVector{
		FailureMeta: testvectors.FailureMeta{
			Case:               testvectors.FailureCaseDuplicateNullifier,
			Title:              "Duplicate nullifier — withdrawal thứ hai reuse nullifier wd-1 trong cùng batch",
			Description:        "Settlement update có 2 withdrawals[]: wd-1 (happy path) và wd-2-replay (clone nullifier + destinationHash của wd-1). Chain phải reject vì nullifier xuất hiện 2 lần trong cùng batch — cross-batch replay (nullifierUsed[n] đã true) thuộc cùng family invariant.",
			ViolatedInvariant:  "Trong batch và xuyên batch, mỗi nullifier chỉ được sử dụng tối đa một lần (nullifierUsed[nullifier] == false trước khi apply).",
			HappyPathReference: testvectors.ScenarioName,
			Mutation:           "Append withdrawals[1] = {withdrawId:'wd-2-replay', amount:'20', nullifier: <happy path>, destinationHash: <happy path>}; mọi field khác giữ nguyên wd-1.",
			Rejection: testvectors.FailureRejection{
				Stage:           testvectors.FailureStageChainVerifier,
				Sentinel:        "x/zkdex: ErrNullifierReplay (P1 ONCHAIN-08)",
				MessageContains: "nullifier",
				Note:            "STATE-05 cũng reject với state.ErrWithdrawAlreadyApplied nếu pipeline thử apply tuần tự — sentinel state-side khác chain-side, nhưng cùng family invariant. P3 batch.LocalBuilder.Build hiện reject ở STATE-05 với ErrInvalidBuildInput wrap.",
			},
			Consumers: []string{"P1", "P3", "P4"},
		},
		Nullifier:        hp.Nullifier,
		SettlementUpdate: tampered,
		BatchCommitments: com,
		PublicInputs:     pi,
	}
	w.write(testvectors.FileDuplicateNullifier, vec, failureFileMeta{
		caseName:  testvectors.FailureCaseDuplicateNullifier,
		schema:    "DuplicateNullifierVector",
		consumers: []string{"P1", "P3", "P4"},
		note:      "Reject ở chain dup check (ONCHAIN-08) HOẶC state.ErrWithdrawAlreadyApplied khi apply tuần tự.",
	})
}

// emitTamperedDestination — clone happy path, đổi withdrawals[0].Destination
// sang attacker address nhưng GIỮ destinationHash của Alice. Verifier ZK
// (ZK-07) hoặc P1 recompute H(destination) PHẢI thấy mismatch.
func emitTamperedDestination(w *failureWriter, hp happyPathArtifacts) {
	const attackerAddr = "cosmos1attacker0000000000000000000000xxxxx"

	tampered := cloneSettlement(hp.Settlement)
	tampered.Withdrawals[0].Destination = attackerAddr
	// DestinationHash giữ nguyên = hash của Alice (hp.DestinationHash).

	com := batch.BuildCommitments(tampered)
	pi, err := batch.BuildPublicInputs(tampered, com)
	if err != nil {
		die("tampered_destination: build public inputs: %v", err)
	}

	vec := testvectors.TamperedDestinationVector{
		FailureMeta: testvectors.FailureMeta{
			Case:               testvectors.FailureCaseTamperedDestination,
			Title:              "Tampered destination — relayer đổi destination nhưng KHÔNG đổi destinationHash",
			Description:        "Relayer cố redirect 40 uusdc của Alice sang attacker bằng cách đổi withdrawals[0].Destination, nhưng quên / không thể recompute destinationHash (vì cần userSecret + circuit hash). Verifier phải recompute H(destination) và đối chiếu DestinationHash để bắt mismatch.",
			ViolatedInvariant:  "destinationHash == H(domain | destination) trong settlement (ZK-07 binding).",
			HappyPathReference: testvectors.ScenarioName,
			Mutation:           "tamperedSettlement.Withdrawals[0].Destination = '" + attackerAddr + "', DestinationHash giữ nguyên = hash của 'cosmos1alice'.",
			Rejection: testvectors.FailureRejection{
				Stage:           testvectors.FailureStageCircuitBinding,
				Sentinel:        "P2 circuit: destinationHash binding (ZK-07)",
				MessageContains: "destinationHash",
				Note:            "P1 verifier có thể recompute H(destination) on-chain để fail-fast TRƯỚC proof verify — STATE-12 cung cấp đủ payload cho cả hai loại check.",
			},
			Consumers: []string{"P1", "P2", "P4"},
		},
		OriginalDestination:     hp.WithdrawRequest.Destination,
		TamperedDestination:     attackerAddr,
		OriginalDestinationHash: hp.DestinationHash,
		TamperedSettlement:      tampered,
		BatchCommitments:        com,
		PublicInputs:            pi,
	}
	w.write(testvectors.FileTamperedDestination, vec, failureFileMeta{
		caseName:  testvectors.FailureCaseTamperedDestination,
		schema:    "TamperedDestinationVector",
		consumers: []string{"P1", "P2", "P4"},
		note:      "Reject ở ZK circuit binding (ZK-07) hoặc P1 verifier on-chain recompute hash.",
	})
}

// cloneSettlement deep-copy một SettlementUpdate. Cần thiết vì các
// emit* nhánh mutate Withdrawals[] (append entry, đổi Destination) —
// nếu share slice, các vector sẽ overwrite nhau.
func cloneSettlement(src types.SettlementUpdate) types.SettlementUpdate {
	out := src
	out.Deposits = append([]types.SettlementDeposit(nil), src.Deposits...)
	out.Withdrawals = append([]types.SettlementWithdrawal(nil), src.Withdrawals...)
	return out
}

// sha256Hex32 trả về 64-char hex (32 byte) của input string, dùng làm
// placeholder root deterministic. KHÔNG đụng pkg/hash để tránh thừa
// "0x" prefix — caller tự prepend.
func sha256Hex32(in string) string {
	h := sha256.Sum256([]byte(in))
	return hex.EncodeToString(h[:])
}
