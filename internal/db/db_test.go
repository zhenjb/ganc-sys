package db

import "testing"

func TestDatabaseURLFromEnvHasDefault(t *testing.T) {
	got := DatabaseURLFromEnv()
	if got == "" {
		t.Fatalf("expected default database url")
	}
}
