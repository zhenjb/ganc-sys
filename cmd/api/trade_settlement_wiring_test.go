package main

import (
	"testing"

	"github.com/zhenjb/ganc-sys/internal/relayer"
	"github.com/zhenjb/ganc-sys/internal/service"
)

// TRD-V1.0: the wiring matrix. The KEY new row is (remote, chain) — a REAL gazk
// prover paired with the CHAIN submitter (the live-settle combo) — plus the two
// legacy defaults preserved, and safe fallback when no trade client exists.
func TestResolveTradeWiring(t *testing.T) {
	local := relayer.NewLocalClient()

	type want struct {
		proverRemote bool // prover is *RemoteTradeProver (else nil/default stub)
		submitKind   string
		wantWarning  bool
	}
	// submitKind: "chain" | "gazk-verify" | "" (nil/default stub)
	cases := []struct {
		name string
		cfg  tradeWiringConfig
		want want
	}{
		{
			name: "legacy default: local prover -> chain submit",
			cfg:  tradeWiringConfig{proverMode: "local", submitMode: "", gazkURL: "u", tradeClient: local},
			want: want{proverRemote: false, submitKind: "chain"},
		},
		{
			name: "legacy remote: remote prover -> gazk-verify",
			cfg:  tradeWiringConfig{proverMode: "remote", submitMode: "", gazkURL: "u", tradeClient: local},
			want: want{proverRemote: true, submitKind: "gazk-verify"},
		},
		{
			name: "TRD-V1 live: remote prover -> chain submit",
			cfg:  tradeWiringConfig{proverMode: "remote", submitMode: "chain", gazkURL: "u", tradeClient: local},
			want: want{proverRemote: true, submitKind: "chain"},
		},
		{
			name: "chain submit without a trade client -> stub + warning",
			cfg:  tradeWiringConfig{proverMode: "remote", submitMode: "chain", gazkURL: "u", tradeClient: nil},
			want: want{proverRemote: true, submitKind: "", wantWarning: true},
		},
		{
			name: "explicit local submit keeps the stub",
			cfg:  tradeWiringConfig{proverMode: "remote", submitMode: "local", gazkURL: "u", tradeClient: local},
			want: want{proverRemote: true, submitKind: ""},
		},
		{
			name: "unknown submit mode -> stub + warning",
			cfg:  tradeWiringConfig{proverMode: "local", submitMode: "bogus", gazkURL: "u", tradeClient: local},
			want: want{proverRemote: false, submitKind: "", wantWarning: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := resolveTradeWiring(tc.cfg)

			_, isRemote := w.prover.(*service.RemoteTradeProver)
			if isRemote != tc.want.proverRemote {
				t.Fatalf("prover remote = %v, want %v (prover=%T)", isRemote, tc.want.proverRemote, w.prover)
			}

			switch tc.want.submitKind {
			case "chain":
				if _, ok := w.submitter.(*service.RelayerTradeSubmitter); !ok {
					t.Fatalf("want chain submitter, got %T", w.submitter)
				}
			case "gazk-verify":
				if _, ok := w.submitter.(*service.RemoteTradeVerifierSubmitter); !ok {
					t.Fatalf("want gazk-verify submitter, got %T", w.submitter)
				}
			case "":
				if w.submitter != nil {
					t.Fatalf("want nil (default stub) submitter, got %T", w.submitter)
				}
			}

			if gotWarn := len(w.warnings) > 0; gotWarn != tc.want.wantWarning {
				t.Fatalf("warnings=%v, want wantWarning=%v", w.warnings, tc.want.wantWarning)
			}
		})
	}
}
