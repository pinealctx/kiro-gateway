package tenant

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "tenant.db"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStoreKeyCRUDWithKiroAccounts(t *testing.T) {
	s := newTestStore(t)

	key, err := s.CreateKey("test-app", WithKiroAccounts([]string{"kiro-a", "kiro-b"}))
	if err != nil {
		t.Fatalf("CreateKey() error = %v", err)
	}
	if len(key.KiroAccounts) != 2 || key.KiroAccounts[0] != "kiro-a" || key.KiroAccounts[1] != "kiro-b" {
		t.Fatalf("KiroAccounts = %+v, want [kiro-a kiro-b]", key.KiroAccounts)
	}
	if key.KiroDefaultAccount != "kiro-a" {
		t.Fatalf("KiroDefaultAccount = %q, want kiro-a", key.KiroDefaultAccount)
	}

	byToken, ok := s.GetKeyByToken(key.Key)
	if !ok {
		t.Fatal("GetKeyByToken() did not find key")
	}
	if len(byToken.KiroAccounts) != 2 || byToken.KiroAccounts[0] != "kiro-a" {
		t.Fatalf("cached KiroAccounts = %+v, want [kiro-a kiro-b]", byToken.KiroAccounts)
	}

	updated, err := s.UpdateKey(key.ID, WithName("renamed"), WithEnabled(false), WithKiroAccounts([]string{"kiro-c"}), WithKiroDefaultAccount("kiro-c"))
	if err != nil {
		t.Fatalf("UpdateKey() error = %v", err)
	}
	if updated.Name != "renamed" || updated.Enabled || len(updated.KiroAccounts) != 1 || updated.KiroAccounts[0] != "kiro-c" {
		t.Fatalf("updated key = %+v", updated)
	}
	if updated.KiroDefaultAccount != "kiro-c" {
		t.Fatalf("updated default = %q, want kiro-c", updated.KiroDefaultAccount)
	}
	if _, err := s.UpdateKey(key.ID, WithKiroDefaultAccount("kiro-x")); err == nil {
		t.Fatal("UpdateKey() with default outside KiroAccounts should fail")
	}
	if _, err := s.UpdateKey(key.ID, WithKiroAccounts([]string{})); err == nil {
		t.Fatal("UpdateKey() with empty KiroAccounts should fail")
	}
	afterFailedUpdate, err := s.GetKeyByID(key.ID)
	if err != nil {
		t.Fatalf("GetKeyByID() error = %v", err)
	}
	if len(afterFailedUpdate.KiroAccounts) != 1 || afterFailedUpdate.KiroAccounts[0] != "kiro-c" {
		t.Fatalf("failed update mutated cache: %+v", afterFailedUpdate.KiroAccounts)
	}

	if err := s.DeleteKey(key.ID); err != nil {
		t.Fatalf("DeleteKey() error = %v", err)
	}
	if _, ok := s.GetKeyByToken(key.Key); ok {
		t.Fatal("deleted key still exists in cache")
	}
}

func TestStoreSuppressReasoning(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tenant.db")
	s, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	// Defaults to false when not set.
	key, err := s.CreateKey("test-app", WithKiroAccounts([]string{"kiro-a"}))
	if err != nil {
		t.Fatalf("CreateKey() error = %v", err)
	}
	if key.SuppressReasoning {
		t.Fatal("SuppressReasoning should default to false")
	}

	// Create with the flag enabled.
	on, err := s.CreateKey("quiet-app", WithKiroAccounts([]string{"kiro-a"}), WithSuppressReasoning(true))
	if err != nil {
		t.Fatalf("CreateKey() error = %v", err)
	}
	if !on.SuppressReasoning {
		t.Fatal("SuppressReasoning should be true after WithSuppressReasoning(true)")
	}

	// Toggle via update.
	updated, err := s.UpdateKey(key.ID, WithSuppressReasoning(true))
	if err != nil {
		t.Fatalf("UpdateKey() error = %v", err)
	}
	if !updated.SuppressReasoning {
		t.Fatal("SuppressReasoning should be true after update")
	}

	// Persisted across a store reload (column round-trips through SQLite).
	_ = s.Close()
	s2, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("reopen NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	reloaded, err := s2.GetKeyByID(key.ID)
	if err != nil {
		t.Fatalf("GetKeyByID() error = %v", err)
	}
	if !reloaded.SuppressReasoning {
		t.Fatal("SuppressReasoning should persist across reload")
	}
}

func TestStoreProviderCRUDKiroOnlyRecord(t *testing.T) {
	s := newTestStore(t)

	rec, err := s.CreateProvider("kiro-a", "kiro")
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	if rec.Type != "kiro" {
		t.Fatalf("Type = %q, want kiro", rec.Type)
	}

	updated, err := s.UpdateProvider(rec.ID, WithProviderEnabled(false))
	if err != nil {
		t.Fatalf("UpdateProvider() error = %v", err)
	}
	if updated.Enabled {
		t.Fatalf("updated provider = %+v", updated)
	}

	list := s.ListProviderRecords()
	if len(list) != 1 || list[0].Name != "kiro-a" {
		t.Fatalf("ListProviderRecords() = %+v", list)
	}

	if err := s.DeleteProvider(rec.ID); err != nil {
		t.Fatalf("DeleteProvider() error = %v", err)
	}
}

func TestStoreUsageQuery(t *testing.T) {
	s := newTestStore(t)
	key, err := s.CreateKey("test-app", WithKiroAccounts([]string{"kiro-a"}))
	if err != nil {
		t.Fatalf("CreateKey() error = %v", err)
	}

	err = s.RecordUsage(&UsageRecord{
		KeyID:        key.ID,
		Model:        "claude-sonnet-4.5",
		Provider:     "kiro-a",
		InputTokens:  10,
		OutputTokens: 20,
		TotalTokens:  30,
		Duration:     12.5,
		CreatedAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}

	summaries, err := s.QueryUsage(UsageQuery{KeyID: key.ID})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if len(summaries) != 1 || summaries[0].TotalRequests != 1 || summaries[0].TotalTokens != 30 {
		t.Fatalf("usage summaries = %+v", summaries)
	}
}
