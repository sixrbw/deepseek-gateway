package entity

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"modelgate/internal/config"
)

// TimeRange 可用时间段
type TimeRange struct {
	Start string `json:"start"` // "HH:MM" 格式
	End   string `json:"end"`   // "HH:MM" 格式
}

type QuotaPolicy struct {
	Name                string      `json:"name"`
	RateLimit           int         `json:"rate_limit"`
	RateLimitWindow     int         `json:"rate_limit_window"`
	RequestQuotaDaily   int         `json:"request_quota_daily"`
	AvailableTimeRanges []TimeRange `json:"available_time_ranges"`
	Models              StringArray `json:"models"`
	Description         string      `json:"description"`
	DefaultModel        string      `json:"default_model"`
	CreatedAt           time.Time   `json:"created_at"`
	UpdatedAt           time.Time   `json:"updated_at"`
}

type QuotaUsageDaily struct {
	ID           uuid.UUID `json:"id"`
	UserID       uuid.UUID `json:"user_id"`
	Date         time.Time `json:"date"`
	ModelID      string    `json:"model_id"`
	RequestCount int       `json:"request_count"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
}

type QuotaCheckResult struct {
	Allowed           bool   `json:"allowed"`
	Reason            string `json:"reason,omitempty"`
	DailyRequests     int    `json:"daily_requests"`
	DailyRequestLimit int    `json:"daily_request_limit"`
	RateRemaining     int    `json:"rate_remaining"`
	RateLimit         int    `json:"rate_limit"`
	RateLimitWindow   int    `json:"rate_limit_window"`
	DefaultModel      string `json:"default_model,omitempty"`
}

type UsageStats struct {
	TotalRequests int `json:"total_requests"`
	AvgLatencyMs  int `json:"avg_latency_ms"`
	ErrorCount    int `json:"error_count"`
}

// QuotaStore 配额数据访问层
// 策略配置从 ConfigManager 读取
// 使用统计从数据库读取
type QuotaStore struct {
	cm *config.ConfigManager
	db *sql.DB
}

func NewQuotaStore(cm *config.ConfigManager, db *sql.DB) *QuotaStore {
	return &QuotaStore{cm: cm, db: db}
}

// configToPolicy 将配置策略转换为数据策略
func (s *QuotaStore) configToPolicy(cfg config.PolicyConfig) *QuotaPolicy {
	// 转换时间段
	var timeRanges []TimeRange
	for _, tr := range cfg.AvailableTimeRanges {
		timeRanges = append(timeRanges, TimeRange{Start: tr.Start, End: tr.End})
	}

	return &QuotaPolicy{
		Name:                cfg.Name,
		RateLimit:           cfg.RateLimit,
		RateLimitWindow:     cfg.RateLimitWindow,
		RequestQuotaDaily:   cfg.RequestQuotaDaily,
		AvailableTimeRanges: timeRanges,
		Models:              cfg.Models,
		Description:         cfg.Description,
		DefaultModel:        cfg.DefaultModel,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}
}

// policyToConfig 将数据策略转换为配置策略
func (s *QuotaStore) policyToConfig(policy *QuotaPolicy) config.PolicyConfig {
	// 转换时间段
	var timeRanges []config.TimeRangeConfig
	for _, tr := range policy.AvailableTimeRanges {
		timeRanges = append(timeRanges, config.TimeRangeConfig{Start: tr.Start, End: tr.End})
	}

	return config.PolicyConfig{
		Name:                policy.Name,
		RateLimit:           policy.RateLimit,
		RateLimitWindow:     policy.RateLimitWindow,
		RequestQuotaDaily:   policy.RequestQuotaDaily,
		AvailableTimeRanges: timeRanges,
		Models:              policy.Models,
		Description:         policy.Description,
		DefaultModel:        policy.DefaultModel,
	}
}

// GetPolicy retrieves a policy by name from config
func (s *QuotaStore) GetPolicy(name string) (*QuotaPolicy, error) {
	cfg := s.cm.GetPolicyByName(name)
	if cfg == nil {
		return nil, nil
	}
	return s.configToPolicy(*cfg), nil
}

// ListPolicies retrieves all policies from config
func (s *QuotaStore) ListPolicies() ([]*QuotaPolicy, error) {
	configs := s.cm.GetPolicies()
	policies := make([]*QuotaPolicy, len(configs))
	for i, cfg := range configs {
		policies[i] = s.configToPolicy(cfg)
	}
	return policies, nil
}

// CreateOrUpdatePolicy creates or updates a policy in config
func (s *QuotaStore) CreateOrUpdatePolicy(policy *QuotaPolicy) error {
	cfg := s.policyToConfig(policy)

	// Check if exists
	existing := s.cm.GetPolicyByName(policy.Name)
	if existing == nil {
		// Create
		if err := s.cm.AddPolicy(cfg); err != nil {
			return err
		}
	} else {
		// Update
		if err := s.cm.UpdatePolicy(cfg); err != nil {
			return err
		}
	}

	policy.UpdatedAt = time.Now()
	if existing == nil {
		policy.CreatedAt = time.Now()
	}
	return nil
}

// DeletePolicy deletes a policy from config
func (s *QuotaStore) DeletePolicy(name string) error {
	return s.cm.DeletePolicy(name)
}

// GetDailyRequestCount 获取用户当天的请求次数
func (s *QuotaStore) GetDailyRequestCount(userID uuid.UUID, date time.Time) (int, error) {
	var total int
	query := `
		SELECT COALESCE(SUM(request_count), 0)
		FROM quota_usage_daily
		WHERE user_id = ? AND date = ?`

	err := s.db.QueryRow(query, userID.String(), date.Format("2006-01-02")).Scan(&total)
	return total, err
}

// IncrementRequestCount 增加请求计数
func (s *QuotaStore) IncrementRequestCount(userID uuid.UUID, modelID string) error {
	return s.IncrementUsage(userID, modelID, 0, 0)
}

// IncrementUsage 增加请求计数和 Token 消耗
func (s *QuotaStore) IncrementUsage(userID uuid.UUID, modelID string, inputTokens, outputTokens int) error {
	query := `
		INSERT INTO quota_usage_daily (id, user_id, date, model_id, request_count, input_tokens, output_tokens)
		VALUES (?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(user_id, date, model_id) DO UPDATE SET
			request_count = request_count + 1,
			input_tokens = input_tokens + ?,
			output_tokens = output_tokens + ?`

	id := uuid.New().String()
	_, err := s.db.Exec(query, id, userID.String(), time.Now().Format("2006-01-02"), modelID, inputTokens, outputTokens, inputTokens, outputTokens)
	return err
}

// GetUsageStats 获取使用统计
func (s *QuotaStore) GetUsageStats(userID uuid.UUID, startDate, endDate time.Time) (*UsageStats, error) {
	stats := &UsageStats{}
	query := `
		SELECT
			COALESCE(SUM(request_count), 0)
		FROM quota_usage_daily
		WHERE user_id = ? AND date BETWEEN ? AND ?`

	err := s.db.QueryRow(query, userID.String(), startDate.Format("2006-01-02"), endDate.Format("2006-01-02")).Scan(
		&stats.TotalRequests,
	)
	return stats, err
}

// GetDailyUsageList 获取用户指定日期范围内的每日使用列表
func (s *QuotaStore) GetDailyUsageList(userID uuid.UUID, startDate, endDate time.Time) ([]*QuotaUsageDaily, error) {
	query := `
		SELECT id, user_id, date, model_id, request_count, input_tokens, output_tokens
		FROM quota_usage_daily
		WHERE user_id = ? AND date BETWEEN ? AND ?
		ORDER BY date DESC, model_id`

	rows, err := s.db.Query(query, userID.String(), startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var usages []*QuotaUsageDaily
	for rows.Next() {
		usage := &QuotaUsageDaily{}
		var idStr, userIDStr string
		err := rows.Scan(
			&idStr, &userIDStr, &usage.Date, &usage.ModelID,
			&usage.RequestCount, &usage.InputTokens, &usage.OutputTokens,
		)
		if err != nil {
			return nil, err
		}
		usage.ID = uuid.MustParse(idStr)
		usage.UserID = uuid.MustParse(userIDStr)
		usages = append(usages, usage)
	}
	return usages, rows.Err()
}

// GetAllDailyUsageList 获取所有用户指定日期范围内的每日使用列表
func (s *QuotaStore) GetAllDailyUsageList(startDate, endDate time.Time) ([]*QuotaUsageDaily, error) {
	query := `
		SELECT id, user_id, date, model_id, request_count, input_tokens, output_tokens
		FROM quota_usage_daily
		WHERE date BETWEEN ? AND ?
		ORDER BY date DESC, user_id, model_id`

	rows, err := s.db.Query(query, startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var usages []*QuotaUsageDaily
	for rows.Next() {
		usage := &QuotaUsageDaily{}
		var idStr, userIDStr string
		err := rows.Scan(
			&idStr, &userIDStr, &usage.Date, &usage.ModelID,
			&usage.RequestCount, &usage.InputTokens, &usage.OutputTokens,
		)
		if err != nil {
			return nil, err
		}
		usage.ID = uuid.MustParse(idStr)
		usage.UserID = uuid.MustParse(userIDStr)
		usages = append(usages, usage)
	}
	return usages, rows.Err()
}

// GetRecentUsageRecords 获取最近的使用记录（按天汇总）
func (s *QuotaStore) GetRecentUsageRecords(userID uuid.UUID, days int) ([]map[string]interface{}, error) {
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -days+1)

	query := `
		SELECT
			date,
			COALESCE(SUM(request_count), 0) as requests,
			COALESCE(SUM(input_tokens), 0) as input_tokens,
			COALESCE(SUM(output_tokens), 0) as output_tokens
		FROM quota_usage_daily
		WHERE user_id = ? AND date >= ?
		GROUP BY date
		ORDER BY date DESC`

	rows, err := s.db.Query(query, userID.String(), startDate.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []map[string]interface{}
	for rows.Next() {
		var date time.Time
		var requests, inputTokens, outputTokens int
		err := rows.Scan(&date, &requests, &inputTokens, &outputTokens)
		if err != nil {
			return nil, err
		}
		records = append(records, map[string]interface{}{
			"date":          date.Format("2006-01-02"),
			"requests":      requests,
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		})
	}
	return records, rows.Err()
}

// GetPolicyForModel 从多个策略名中找到第一个包含该 modelID 的策略
func (s *QuotaStore) GetPolicyForModel(policyNames []string, modelID string) (*QuotaPolicy, error) {
	for _, name := range policyNames {
		policy, err := s.GetPolicy(name)
		if err != nil || policy == nil {
			continue
		}
		for _, m := range policy.Models {
			if m == "*" || m == modelID {
				return policy, nil
			}
		}
	}
	return nil, nil
}

// ValidatePoliciesNoConflict 校验多个策略的模型列表不冲突
// 每个模型只能出现在一个策略里；包含 * 的策略不能与其他策略共存
func (s *QuotaStore) ValidatePoliciesNoConflict(policyNames []string) error {
	seen := map[string]string{}
	for _, name := range policyNames {
		policy, _ := s.GetPolicy(name)
		if policy == nil {
			continue
		}
		for _, m := range policy.Models {
			if m == "*" {
				return fmt.Errorf("策略 %s 包含通配符 *，不能与其他策略同时绑定给同一用户", name)
			}
			if prev, exists := seen[m]; exists {
				return fmt.Errorf("模型 %s 在策略 %s 和 %s 中重复，每个模型只能属于一个策略", m, prev, name)
			}
			seen[m] = name
		}
	}
	return nil
}
