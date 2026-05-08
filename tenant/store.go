package tenant

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store manages API keys, usage records, and provider configurations.
type Store struct {
	db *sql.DB
	mu sync.RWMutex
	// In-memory key cache for fast auth lookup
	cache map[string]*APIKey // key string → APIKey
	// In-memory provider cache
	providerCache map[string]*ProviderRecord // provider ID → ProviderRecord
}

// NewStore opens (or creates) an SQLite database for tenant management.
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open tenant db: %w", err)
	}

	s := &Store{
		db:            db,
		cache:         make(map[string]*APIKey),
		providerCache: make(map[string]*ProviderRecord),
	}

	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := s.loadCache(); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := s.loadProviderCache(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return s, nil
}

func (s *Store) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS api_keys (
		id          TEXT PRIMARY KEY,
		key         TEXT UNIQUE NOT NULL,
		name        TEXT NOT NULL DEFAULT '',
		enabled     INTEGER NOT NULL DEFAULT 1,
		kiro_accounts     TEXT NOT NULL DEFAULT '[]',
		kiro_default_account TEXT NOT NULL DEFAULT '',
		metadata    TEXT NOT NULL DEFAULT '{}',
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS usage_records (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		key_id        TEXT NOT NULL,
		model         TEXT NOT NULL DEFAULT '',
		provider      TEXT NOT NULL DEFAULT '',
		input_tokens  INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens  INTEGER NOT NULL DEFAULT 0,
		duration_ms   REAL    NOT NULL DEFAULT 0,
		created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (key_id) REFERENCES api_keys(id)
	);

	CREATE INDEX IF NOT EXISTS idx_usage_key_time ON usage_records(key_id, created_at);
	CREATE INDEX IF NOT EXISTS idx_usage_model ON usage_records(model, created_at);

	CREATE TABLE IF NOT EXISTS providers (
		id            TEXT PRIMARY KEY,
		name          TEXT UNIQUE NOT NULL,
		type          TEXT NOT NULL,
		region        TEXT NOT NULL DEFAULT 'us-east-1',
		enabled       INTEGER NOT NULL DEFAULT 1,
		created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS kv_store (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	`
	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("migrate tenant db: %w", err)
	}
	if _, err := s.db.Exec("ALTER TABLE api_keys ADD COLUMN kiro_default_account TEXT NOT NULL DEFAULT ''"); err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("migrate tenant db: %w", err)
	}
	if _, err := s.db.Exec("ALTER TABLE providers ADD COLUMN region TEXT NOT NULL DEFAULT 'us-east-1'"); err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("migrate tenant db: %w", err)
	}
	return nil
}

func (s *Store) loadCache() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query("SELECT id, key, name, enabled, kiro_accounts, kiro_default_account, metadata, created_at, updated_at FROM api_keys")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	s.cache = make(map[string]*APIKey)
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return err
		}
		s.cache[k.Key] = k
	}
	return rows.Err()
}

func scanKey(rows *sql.Rows) (*APIKey, error) {
	var k APIKey
	var accountsJSON, metaJSON string
	var enabled int
	err := rows.Scan(&k.ID, &k.Key, &k.Name, &enabled, &accountsJSON, &k.KiroDefaultAccount, &metaJSON, &k.CreatedAt, &k.UpdatedAt)
	if err != nil {
		return nil, err
	}
	k.Enabled = enabled == 1
	_ = json.Unmarshal([]byte(accountsJSON), &k.KiroAccounts)
	_ = json.Unmarshal([]byte(metaJSON), &k.Metadata)
	if k.KiroAccounts == nil {
		k.KiroAccounts = []string{}
	}
	if k.KiroDefaultAccount == "" && len(k.KiroAccounts) > 0 {
		k.KiroDefaultAccount = k.KiroAccounts[0]
	}
	if k.Metadata == nil {
		k.Metadata = make(map[string]string)
	}
	return &k, nil
}

func cloneAPIKey(k *APIKey) *APIKey {
	clone := *k
	clone.KiroAccounts = append([]string(nil), k.KiroAccounts...)
	clone.Metadata = make(map[string]string, len(k.Metadata))
	for key, value := range k.Metadata {
		clone.Metadata[key] = value
	}
	return &clone
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// ============================================================
// Key CRUD
// ============================================================

// CreateKey creates a new API key and returns it.
func (s *Store) CreateKey(name string, opts ...KeyOption) (*APIKey, error) {
	k := &APIKey{
		ID:        generateID(),
		Key:       generateAPIKey(),
		Name:      name,
		Enabled:   true,
		Metadata:  make(map[string]string),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	for _, opt := range opts {
		opt(k)
	}
	if len(k.KiroAccounts) == 0 {
		return nil, fmt.Errorf("create key: kiro_accounts is required")
	}
	if k.KiroDefaultAccount == "" {
		k.KiroDefaultAccount = k.KiroAccounts[0]
	}
	if !containsString(k.KiroAccounts, k.KiroDefaultAccount) {
		return nil, fmt.Errorf("create key: kiro_default_account must be in kiro_accounts")
	}

	accountsJSON, _ := json.Marshal(k.KiroAccounts)
	metaJSON, _ := json.Marshal(k.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO api_keys (id, key, name, enabled, kiro_accounts, kiro_default_account, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		k.ID, k.Key, k.Name, boolToInt(k.Enabled),
		string(accountsJSON), k.KiroDefaultAccount, string(metaJSON),
		k.CreatedAt, k.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create key: %w", err)
	}

	s.mu.Lock()
	s.cache[k.Key] = k
	s.mu.Unlock()
	return k, nil
}

// GetKeyByToken looks up an API key by its token string (for auth).
func (s *Store) GetKeyByToken(token string) (*APIKey, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k, ok := s.cache[token]
	return k, ok
}

// GetKeyByID looks up an API key by its ID.
func (s *Store) GetKeyByID(id string) (*APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.cache {
		if k.ID == id {
			return k, nil
		}
	}
	return nil, fmt.Errorf("key not found: %s", id)
}

// ListKeys returns all API keys sorted by name.
func (s *Store) ListKeys() []*APIKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]*APIKey, 0, len(s.cache))
	for _, k := range s.cache {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Name < keys[j].Name })
	return keys
}

// UpdateKey updates an existing API key.
func (s *Store) UpdateKey(id string, opts ...KeyOption) (*APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find in cache
	var target *APIKey
	for _, k := range s.cache {
		if k.ID == id {
			target = k
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("key not found: %s", id)
	}

	oldToken := target.Key
	updated := cloneAPIKey(target)
	for _, opt := range opts {
		opt(updated)
	}
	if len(updated.KiroAccounts) == 0 {
		return nil, fmt.Errorf("update key: kiro_accounts is required")
	}
	if updated.KiroDefaultAccount == "" {
		updated.KiroDefaultAccount = updated.KiroAccounts[0]
	}
	if !containsString(updated.KiroAccounts, updated.KiroDefaultAccount) {
		return nil, fmt.Errorf("update key: kiro_default_account must be in kiro_accounts")
	}
	updated.UpdatedAt = time.Now().UTC()

	accountsJSON, _ := json.Marshal(updated.KiroAccounts)
	metaJSON, _ := json.Marshal(updated.Metadata)

	_, err := s.db.Exec(
		`UPDATE api_keys SET name=?, enabled=?, kiro_accounts=?, kiro_default_account=?, metadata=?, updated_at=? WHERE id=?`,
		updated.Name, boolToInt(updated.Enabled),
		string(accountsJSON), updated.KiroDefaultAccount, string(metaJSON),
		updated.UpdatedAt, updated.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("update key: %w", err)
	}

	// Update cache (key token doesn't change, but re-index just in case)
	if oldToken != updated.Key {
		delete(s.cache, oldToken)
	}
	s.cache[updated.Key] = updated
	return updated, nil
}

// DeleteKey removes an API key.
func (s *Store) DeleteKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var token string
	for _, k := range s.cache {
		if k.ID == id {
			token = k.Key
			break
		}
	}
	if token == "" {
		return fmt.Errorf("key not found: %s", id)
	}

	_, err := s.db.Exec("DELETE FROM api_keys WHERE id=?", id)
	if err != nil {
		return fmt.Errorf("delete key: %w", err)
	}
	delete(s.cache, token)
	return nil
}

// ============================================================
// Usage Recording + Query
// ============================================================

// RecordUsage inserts a usage record.
func (s *Store) RecordUsage(r *UsageRecord) error {
	_, err := s.db.Exec(
		`INSERT INTO usage_records (key_id, model, provider, input_tokens, output_tokens, total_tokens, duration_ms, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.KeyID, r.Model, r.Provider, r.InputTokens, r.OutputTokens, r.TotalTokens, r.Duration, r.CreatedAt,
	)
	return err
}

// QueryUsage returns aggregated usage summaries with flexible grouping.
func (s *Store) QueryUsage(q UsageQuery) ([]UsageSummary, error) {
	where := []string{"1=1"}
	args := []any{}

	if q.KeyID != "" {
		where = append(where, "u.key_id = ?")
		args = append(args, q.KeyID)
	}
	if !q.From.IsZero() {
		where = append(where, "u.created_at >= ?")
		args = append(args, q.From)
	}
	if !q.To.IsZero() {
		where = append(where, "u.created_at <= ?")
		args = append(args, q.To)
	}
	if q.Model != "" {
		where = append(where, "u.model = ?")
		args = append(args, q.Model)
	}
	if q.Provider != "" {
		where = append(where, "u.provider = ?")
		args = append(args, q.Provider)
	}

	// Determine grouping based on GroupBy parameter
	var selectFields, groupBy string
	switch q.GroupBy {
	case "model":
		selectFields = "'' as key_id, '' as key_name, u.model, '' as provider"
		groupBy = "u.model"
	case "provider":
		selectFields = "'' as key_id, '' as key_name, '' as model, u.provider"
		groupBy = "u.provider"
	case "key_model":
		selectFields = "u.key_id, COALESCE(k.name, ''), u.model, '' as provider"
		groupBy = "u.key_id, u.model"
	case "key_provider":
		selectFields = "u.key_id, COALESCE(k.name, ''), '' as model, u.provider"
		groupBy = "u.key_id, u.provider"
	default: // "key" or empty
		selectFields = "u.key_id, COALESCE(k.name, ''), '' as model, '' as provider"
		groupBy = "u.key_id"
	}

	query := fmt.Sprintf(`
		SELECT %s, COUNT(*) as total_requests,
		       SUM(u.input_tokens), SUM(u.output_tokens), SUM(u.total_tokens)
		FROM usage_records u
		LEFT JOIN api_keys k ON u.key_id = k.id
		WHERE %s
		GROUP BY %s
		ORDER BY total_requests DESC
	`, selectFields, strings.Join(where, " AND "), groupBy)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []UsageSummary
	for rows.Next() {
		var us UsageSummary
		if err := rows.Scan(&us.KeyID, &us.KeyName, &us.Model, &us.Provider, &us.TotalRequests, &us.InputTokens, &us.OutputTokens, &us.TotalTokens); err != nil {
			return nil, err
		}
		results = append(results, us)
	}
	return results, rows.Err()
}

// ============================================================
// Key options (functional options pattern)
// ============================================================

// KeyOption is a functional option for configuring an API key.
type KeyOption func(*APIKey)

func WithEnabled(enabled bool) KeyOption {
	return func(k *APIKey) { k.Enabled = enabled }
}

func WithName(name string) KeyOption {
	return func(k *APIKey) { k.Name = name }
}

func WithKiroAccounts(accounts []string) KeyOption {
	return func(k *APIKey) { k.KiroAccounts = normalizeStringList(accounts) }
}

func WithKiroDefaultAccount(account string) KeyOption {
	return func(k *APIKey) { k.KiroDefaultAccount = strings.TrimSpace(account) }
}

func normalizeStringList(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		result = append(result, v)
	}
	return result
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

// ============================================================
// Provider persistence
// ============================================================

func (s *Store) loadProviderCache() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query("SELECT id, name, type, region, enabled, created_at, updated_at FROM providers")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	s.providerCache = make(map[string]*ProviderRecord)
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return err
		}
		s.providerCache[p.ID] = p
	}
	return rows.Err()
}

func scanProvider(rows *sql.Rows) (*ProviderRecord, error) {
	var p ProviderRecord
	var enabled int
	err := rows.Scan(&p.ID, &p.Name, &p.Type, &p.Region, &enabled, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if p.Type == "" {
		p.Type = "kiro"
	}
	if p.Region == "" {
		p.Region = "us-east-1"
	}
	p.Enabled = enabled == 1
	return &p, nil
}

// CreateProvider persists a new provider configuration.
func (s *Store) CreateProvider(name, typ string, opts ...ProviderOption) (*ProviderRecord, error) {
	p := &ProviderRecord{
		ID:        generateID(),
		Name:      name,
		Type:      typ,
		Region:    "us-east-1",
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	for _, opt := range opts {
		opt(p)
	}

	_, err := s.db.Exec(
		`INSERT INTO providers (id, name, type, region, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Type, p.Region, boolToInt(p.Enabled),
		p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create provider: %w", err)
	}

	s.mu.Lock()
	s.providerCache[p.ID] = p
	s.mu.Unlock()
	return p, nil
}

// GetProvider returns a provider by ID.
func (s *Store) GetProvider(id string) (*ProviderRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.providerCache[id]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("provider not found: %s", id)
}

// GetProviderByName returns a provider by name.
func (s *Store) GetProviderByName(name string) (*ProviderRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.providerCache {
		if p.Name == name {
			return p, true
		}
	}
	return nil, false
}

// ListProviderRecords returns all persisted provider configurations sorted by name.
func (s *Store) ListProviderRecords() []*ProviderRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*ProviderRecord, 0, len(s.providerCache))
	for _, p := range s.providerCache {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// UpdateProvider updates an existing provider configuration.
func (s *Store) UpdateProvider(id string, opts ...ProviderOption) (*ProviderRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	target, ok := s.providerCache[id]
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", id)
	}

	for _, opt := range opts {
		opt(target)
	}
	target.UpdatedAt = time.Now().UTC()

	_, err := s.db.Exec(
		`UPDATE providers SET name=?, type=?, region=?, enabled=?, updated_at=? WHERE id=?`,
		target.Name, target.Type, target.Region, boolToInt(target.Enabled),
		target.UpdatedAt, target.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("update provider: %w", err)
	}

	s.providerCache[id] = target
	return target, nil
}

// DeleteProvider removes a provider configuration.
func (s *Store) DeleteProvider(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.providerCache[id]; !ok {
		return fmt.Errorf("provider not found: %s", id)
	}

	_, err := s.db.Exec("DELETE FROM providers WHERE id=?", id)
	if err != nil {
		return fmt.Errorf("delete provider: %w", err)
	}
	delete(s.providerCache, id)
	return nil
}

// ============================================================
// Helpers
// ============================================================

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func generateAPIKey() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "ag-" + hex.EncodeToString(b)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ========================
// KV Store — generic key-value persistence
// ========================

// GetKV retrieves a value by key from the kv_store table.
func (s *Store) GetKV(key string) (string, bool) {
	var val string
	err := s.db.QueryRow("SELECT value FROM kv_store WHERE key = ?", key).Scan(&val)
	if err != nil {
		return "", false
	}
	return val, true
}

// SetKV sets a key-value pair in the kv_store table (upsert).
func (s *Store) SetKV(key, value string) error {
	_, err := s.db.Exec(
		"INSERT INTO kv_store (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP",
		key, value,
	)
	return err
}

// DeleteKV removes a key from the kv_store table.
func (s *Store) DeleteKV(key string) error {
	_, err := s.db.Exec("DELETE FROM kv_store WHERE key = ?", key)
	return err
}
