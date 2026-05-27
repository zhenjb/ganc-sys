// Package testvectors là loader canonical cho folder
// testvectors/alice_100_40/ (STATE-11). Mọi consumer (P1 verifier /
// P2 prover / P4 backend / P5 UI) PHẢI dùng package này để đọc test
// vector thay vì tự bash path string. Lý do:
//
//  1. Folder vị trí ổn định theo go.mod (FindRepoRoot walk up tìm
//     go.mod) — caller không lệ thuộc cwd.
//  2. MANIFEST.json là source of truth: VerifyManifest re-hash SHA-256
//     từng file và assert khớp manifest entry — đảm bảo file checked-in
//     không drift khỏi generator output.
//  3. AliceScenario là typed snapshot toàn bộ scenario; consumer không
//     phải parse JSON từng file rồi tự ráp lại invariant.
//
// Khi ZK-02 chốt hash circuit thật (Poseidon/MiMC), bump
// gen_state_vectors `vectorVersion` v0 → v1, regenerate folder, và
// update ExpectedVectorVersion ở đây — test sẽ tự bắt drift.
package testvectors
