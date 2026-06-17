package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pinealctx/kiro-gateway/config"
	"github.com/pinealctx/kiro-gateway/core/providers"
	kiroProvider "github.com/pinealctx/kiro-gateway/providers/kiro"
	"go.uber.org/zap"
)

const (
	quotaStateKey          = "notifications:teams:quota_state"
	quotaEnabledOverrideKV = "notifications:teams:enabled"
)

type kvStore interface {
	GetKV(key string) (string, bool)
	SetKV(key, value string) error
}

type QuotaNotifier struct {
	cfg      config.TeamsNotificationConfig
	registry *providers.Registry
	store    kvStore
	logger   *zap.Logger
	client   *http.Client
	stopCh   chan struct{}
	doneCh   chan struct{}
	once     sync.Once
}

type quotaState struct {
	Accounts      map[string]float64 `json:"accounts"`
	Total         float64            `json:"total"`
	DailySentKeys map[string]bool    `json:"daily_sent_keys"`
}

type quotaSnapshot struct {
	Account     string
	Email       string
	Tier        string
	Used        float64
	Limit       float64
	Remaining   float64
	PercentUsed float64
	FetchedAt   time.Time
}

type teamsPayload struct {
	Type        string            `json:"type"`
	Attachments []teamsAttachment `json:"attachments"`
}

type teamsAttachment struct {
	ContentType string        `json:"contentType"`
	ContentURL  *string       `json:"contentUrl"`
	Content     teamsCardBody `json:"content"`
}

type teamsCardBody struct {
	Schema  string           `json:"$schema"`
	Type    string           `json:"type"`
	Version string           `json:"version"`
	Body    []teamsTextBlock `json:"body"`
}

type teamsTextBlock struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Wrap   bool   `json:"wrap"`
	Weight string `json:"weight,omitempty"`
	Size   string `json:"size,omitempty"`
}

func NewQuotaNotifier(cfg config.TeamsNotificationConfig, registry *providers.Registry, store kvStore, logger *zap.Logger) *QuotaNotifier {
	return &QuotaNotifier{
		cfg:      cfg,
		registry: registry,
		store:    store,
		logger:   logger,
		client:   &http.Client{Timeout: 10 * time.Second},
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

func (n *QuotaNotifier) Enabled() bool {
	return n != nil && n.cfg.Enabled && strings.TrimSpace(n.cfg.WebhookURL) != "" && n.registry != nil
}

func (n *QuotaNotifier) Start() {
	if !n.Enabled() {
		return
	}
	go n.loop()
}

func (n *QuotaNotifier) Stop() {
	if n == nil {
		return
	}
	n.once.Do(func() {
		close(n.stopCh)
		<-n.doneCh
	})
}

func (n *QuotaNotifier) loop() {
	defer close(n.doneCh)
	interval := time.Duration(n.cfg.CheckIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 10 * time.Minute
	}

	n.runCheck(false)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			n.runCheck(false)
			n.runDailyIfDue(time.Now())
		case <-n.stopCh:
			return
		}
	}
}

func (n *QuotaNotifier) runCheck(forceDaily bool) {
	if !RuntimeEnabled(n.store, n.cfg) {
		return
	}
	snapshots := n.collectSnapshots()
	if len(snapshots) == 0 {
		return
	}
	state := n.loadState()
	total := aggregateQuota(snapshots)
	accountEvents := make([]thresholdEvent, 0)

	for _, snap := range snapshots {
		threshold := crossedThreshold(state.Accounts[snap.Account], snap.PercentUsed, n.cfg.AccountThresholds)
		if threshold > 0 {
			accountEvents = append(accountEvents, thresholdEvent{Snapshot: snap, Threshold: threshold})
		}
		state.Accounts[snap.Account] = highestReached(snap.PercentUsed, n.cfg.AccountThresholds)
	}

	threshold := crossedThreshold(state.Total, total.PercentUsed, n.cfg.TotalThresholds)
	if len(accountEvents) > 0 || threshold > 0 {
		n.sendThresholdSummary(accountEvents, total, threshold, len(snapshots))
	}
	state.Total = highestReached(total.PercentUsed, n.cfg.TotalThresholds)

	if forceDaily {
		n.sendDaily(snapshots, total)
	}
	n.saveState(state)
}

type thresholdEvent struct {
	Snapshot  quotaSnapshot
	Threshold float64
}

func (n *QuotaNotifier) sendThresholdSummary(events []thresholdEvent, total quotaSnapshot, totalThreshold float64, accountCount int) {
	sort.Slice(events, func(i, j int) bool {
		return events[i].Snapshot.PercentUsed > events[j].Snapshot.PercentUsed
	})
	lines := []string{
		"**Kiro 额度阈值提醒**",
		"",
		fmt.Sprintf("总额度 %s **%.1f%%**，%s / %s，剩余 %s。", batteryBar(total.PercentUsed), total.PercentUsed, formatNumber(total.Used), formatNumber(total.Limit), formatNumber(total.Remaining)),
	}
	if totalThreshold > 0 {
		lines = append(lines, fmt.Sprintf("总额度已达到 %.0f%% 阈值。", totalThreshold))
	}
	lines = append(lines, "", fmt.Sprintf("触发账号：%d / %d", len(events), accountCount))
	for _, event := range events {
		snap := event.Snapshot
		lines = append(lines, fmt.Sprintf("- `%s` %s **%.1f%%**，达到 %.0f%%，剩余 %s", snap.Account, batteryBar(snap.PercentUsed), snap.PercentUsed, event.Threshold, formatNumber(snap.Remaining)))
	}
	n.send("quota threshold summary", strings.Join(lines, "\n"))
}

func (n *QuotaNotifier) runDailyIfDue(now time.Time) {
	loc := time.Local
	if n.cfg.Timezone != "" && n.cfg.Timezone != "Local" {
		if loaded, err := time.LoadLocation(n.cfg.Timezone); err == nil {
			loc = loaded
		}
	}
	now = now.In(loc)
	for _, scheduled := range n.cfg.DailyTimes {
		scheduledTime, ok := scheduledToday(now, scheduled)
		if !ok || now.Before(scheduledTime) {
			continue
		}
		key := now.Format("2006-01-02") + " " + strings.TrimSpace(scheduled)
		state := n.loadState()
		if state.DailySentKeys[key] {
			continue
		}
		n.runCheck(true)
		state = n.loadState()
		state.DailySentKeys[key] = true
		n.saveState(state)
		return
	}
}

func scheduledToday(now time.Time, value string) (time.Time, bool) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(now.Year(), now.Month(), now.Day(), parsed.Hour(), parsed.Minute(), 0, 0, now.Location()), true
}

func (n *QuotaNotifier) sendDaily(snapshots []quotaSnapshot, total quotaSnapshot) {
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].PercentUsed > snapshots[j].PercentUsed
	})
	lines := []string{
		"**Kiro 额度日报**",
		"",
		fmt.Sprintf("总额度：%s **%.1f%%**，%s / %s，剩余 %s。", batteryBar(total.PercentUsed), total.PercentUsed, formatNumber(total.Used), formatNumber(total.Limit), formatNumber(total.Remaining)),
		"",
		"账号明细：",
	}
	for _, snap := range snapshots {
		lines = append(lines, fmt.Sprintf("- `%s` %s %.1f%%，%s / %s，剩余 %s", snap.Account, batteryBar(snap.PercentUsed), snap.PercentUsed, formatNumber(snap.Used), formatNumber(snap.Limit), formatNumber(snap.Remaining)))
	}
	n.send("daily quota summary", strings.Join(lines, "\n"))
}

func (n *QuotaNotifier) collectSnapshots() []quotaSnapshot {
	accounts := n.registry.All()
	snapshots := make([]quotaSnapshot, 0, len(accounts))
	for name, provider := range accounts {
		kp, ok := provider.(*kiroProvider.Provider)
		if !ok {
			continue
		}
		limits, ok := kp.GetCachedUsageLimits()
		if !ok || limits.Usage.Limit == 0 && limits.Usage.LimitPrecise == 0 {
			continue
		}
		used := firstPositive(limits.Usage.UsedPrecise, limits.Usage.Used)
		limit := firstPositive(limits.Usage.LimitPrecise, limits.Usage.Limit)
		remaining := firstPositive(limits.Usage.RemainingPrecise, limits.Usage.Remaining)
		percent := limits.Usage.PercentUsed
		if percent == 0 && limit > 0 {
			percent = used / limit * 100
		}
		account := limits.Account
		if account == "" {
			account = name
		}
		snapshots = append(snapshots, quotaSnapshot{
			Account:     account,
			Email:       limits.Email,
			Tier:        limits.Tier,
			Used:        used,
			Limit:       limit,
			Remaining:   remaining,
			PercentUsed: percent,
			FetchedAt:   limits.FetchedAt,
		})
	}
	return snapshots
}

func (n *QuotaNotifier) send(event, text string) {
	if !RuntimeEnabled(n.store, n.cfg) {
		return
	}
	body, err := json.Marshal(newTeamsPayload(text))
	if err != nil {
		n.logger.Error("failed to marshal teams notification", zap.Error(err))
		return
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, n.cfg.WebhookURL, bytes.NewReader(body))
	if err != nil {
		n.logger.Error("failed to build teams notification request", zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		n.logger.Warn("failed to send teams notification", zap.String("event", event), zap.Error(err))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		n.logger.Warn("teams notification returned non-success status", zap.String("event", event), zap.Int("status", resp.StatusCode))
	}
}

func newTeamsPayload(text string) teamsPayload {
	lines := strings.SplitN(text, "\n", 2)
	title := strings.TrimSpace(lines[0])
	body := ""
	if len(lines) > 1 {
		body = strings.TrimSpace(lines[1])
	}
	if body == "" {
		body = title
		title = "Kiro Gateway"
	}
	return teamsPayload{
		Type: "message",
		Attachments: []teamsAttachment{
			{
				ContentType: "application/vnd.microsoft.card.adaptive",
				ContentURL:  nil,
				Content: teamsCardBody{
					Schema:  "http://adaptivecards.io/schemas/adaptive-card.json",
					Type:    "AdaptiveCard",
					Version: "1.2",
					Body: []teamsTextBlock{
						{
							Type:   "TextBlock",
							Text:   strings.Trim(title, "*"),
							Wrap:   true,
							Weight: "Bolder",
							Size:   "Medium",
						},
						{
							Type: "TextBlock",
							Text: body,
							Wrap: true,
						},
					},
				},
			},
		},
	}
}

func (n *QuotaNotifier) loadState() quotaState {
	state := quotaState{
		Accounts:      map[string]float64{},
		DailySentKeys: map[string]bool{},
	}
	if n.store == nil {
		return state
	}
	raw, ok := n.store.GetKV(quotaStateKey)
	if !ok || strings.TrimSpace(raw) == "" {
		return state
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		n.logger.Warn("failed to load quota notification state", zap.Error(err))
		return quotaState{Accounts: map[string]float64{}, DailySentKeys: map[string]bool{}}
	}
	if state.Accounts == nil {
		state.Accounts = map[string]float64{}
	}
	if state.DailySentKeys == nil {
		state.DailySentKeys = map[string]bool{}
	}
	return state
}

func (n *QuotaNotifier) saveState(state quotaState) {
	if n.store == nil {
		return
	}
	data, err := json.Marshal(state)
	if err != nil {
		n.logger.Warn("failed to marshal quota notification state", zap.Error(err))
		return
	}
	if err := n.store.SetKV(quotaStateKey, string(data)); err != nil {
		n.logger.Warn("failed to save quota notification state", zap.Error(err))
	}
}

func crossedThreshold(previous, current float64, thresholds []float64) float64 {
	crossed := 0.0
	for _, threshold := range thresholds {
		if current >= threshold && previous < threshold {
			crossed = threshold
		}
	}
	return crossed
}

func highestReached(current float64, thresholds []float64) float64 {
	reached := 0.0
	for _, threshold := range thresholds {
		if current >= threshold {
			reached = threshold
		}
	}
	return reached
}

func aggregateQuota(snapshots []quotaSnapshot) quotaSnapshot {
	total := quotaSnapshot{Account: "total"}
	for _, snap := range snapshots {
		total.Used += snap.Used
		total.Limit += snap.Limit
		total.Remaining += snap.Remaining
	}
	if total.Limit > 0 {
		total.PercentUsed = total.Used / total.Limit * 100
	}
	return total
}

func firstPositive(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func formatNumber(value float64) string {
	if math.Abs(value-math.Round(value)) < 0.005 {
		return fmt.Sprintf("%.0f", value)
	}
	return fmt.Sprintf("%.2f", value)
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func batteryBar(percent float64) string {
	filled := int(math.Round(maxFloat(0, minFloat(100, percent)) / 10))
	return strings.Repeat("▰", filled) + strings.Repeat("▱", 10-filled)
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

type RuntimeStatus struct {
	Configured bool `json:"configured"`
	Enabled    bool `json:"enabled"`
}

func GetRuntimeStatus(store kvStore, cfg config.TeamsNotificationConfig) RuntimeStatus {
	return RuntimeStatus{
		Configured: cfg.Enabled && strings.TrimSpace(cfg.WebhookURL) != "",
		Enabled:    RuntimeEnabled(store, cfg),
	}
}

func RuntimeEnabled(store kvStore, cfg config.TeamsNotificationConfig) bool {
	if !cfg.Enabled || strings.TrimSpace(cfg.WebhookURL) == "" {
		return false
	}
	if store == nil {
		return true
	}
	value, ok := store.GetKV(quotaEnabledOverrideKV)
	if !ok || strings.TrimSpace(value) == "" {
		return true
	}
	return strings.TrimSpace(value) == "true" || strings.TrimSpace(value) == "1"
}

func SetRuntimeEnabled(store kvStore, enabled bool) error {
	if store == nil {
		return nil
	}
	if enabled {
		return store.SetKV(quotaEnabledOverrideKV, "true")
	}
	return store.SetKV(quotaEnabledOverrideKV, "false")
}
