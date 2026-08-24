package quota

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"modelgate/internal/repository"
)

// RateCounter 内存速率计数器
type RateCounter struct {
	counts    map[string]int
	windows   map[string]time.Time
	windowSec int // 窗口大小（秒）
	mu        sync.RWMutex
}

func NewRateCounter() *RateCounter {
	rc := &RateCounter{
		counts:    make(map[string]int),
		windows:   make(map[string]time.Time),
		windowSec: 60, // 默认 60 秒窗口
	}
	// 启动每分钟清理任务
	go rc.cleanupLoop()
	return rc
}

// cleanupLoop 每分钟清理过期的计数器
func (rc *RateCounter) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rc.cleanup()
	}
}

// cleanup 清理过期的计数器条目
func (rc *RateCounter) cleanup() {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	now := time.Now()
	for key, window := range rc.windows {
		if now.Sub(window) >= time.Duration(rc.windowSec)*time.Second {
			delete(rc.counts, key)
			delete(rc.windows, key)
		}
	}
}

// getWindowKey 获取当前窗口的 key
// 使用窗口起始时间作为 key，确保同一窗口内的请求使用相同的 key
func (rc *RateCounter) getWindowKey(userID string, window int) string {
	now := time.Now()
	// 计算窗口起始时间
	windowStart := now.Truncate(time.Duration(window) * time.Second)
	return fmt.Sprintf("%s:%d", userID, windowStart.Unix())
}

// Increment 增加计数并返回当前计数
func (rc *RateCounter) Increment(userID string, window int) int {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	key := rc.getWindowKey(userID, window)

	// 检查是否需要重置窗口
	if lastWindow, exists := rc.windows[key]; !exists || time.Since(lastWindow) >= time.Duration(window)*time.Second {
		rc.counts[key] = 0
	}

	rc.windows[key] = time.Now()
	rc.counts[key]++
	return rc.counts[key]
}

// GetCount 获取当前计数
func (rc *RateCounter) GetCount(userID string, window int) int {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	key := rc.getWindowKey(userID, window)
	return rc.counts[key]
}

// DashboardRecorder 用于记录仪表板统计的接口
type DashboardRecorder interface {
	RecordHourlyStat(userID string, modelID string, backendID string, inputTokens, outputTokens int, durationMs int64)
}

type Service struct {
	store             *entity.QuotaStore
	modelStore        *entity.ModelStore
	apiKeyStore       *entity.APIKeyStore // 增加 APIKeyStore 依赖以便记录 Token 消耗
	rateCounter       *RateCounter
	dailyRequestCache *DailyRequestCounter
	dashboardRecorder DashboardRecorder
}

// DailyRequestCounter 每日请求数内存计数器
type DailyRequestCounter struct {
	counts map[string]int // key: user_id:date
	mu     sync.RWMutex
}

func NewDailyRequestCounter() *DailyRequestCounter {
	return &DailyRequestCounter{
		counts: make(map[string]int),
	}
}

// Get 获取用户当日请求数
func (c *DailyRequestCounter) Get(userID string, date time.Time) (int, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := fmt.Sprintf("%s:%s", userID, date.Format("2006-01-02"))
	val, exists := c.counts[key]
	return val, exists
}

// Add 增加用户当日请求数
func (c *DailyRequestCounter) Add(userID string, date time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := fmt.Sprintf("%s:%s", userID, date.Format("2006-01-02"))
	c.counts[key]++
}

// Set 设置用户当日请求数
func (c *DailyRequestCounter) Set(userID string, date time.Time, count int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := fmt.Sprintf("%s:%s", userID, date.Format("2006-01-02"))
	c.counts[key] = count
}

// CleanupExpired 清理过期缓存（非当日）
func (c *DailyRequestCounter) CleanupExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	for key := range c.counts {
		// key 格式: user_id:date，检查 date 部分
		parts := strings.Split(key, ":")
		if len(parts) == 2 && parts[1] != today {
			delete(c.counts, key)
		}
	}
}

func NewService(store *entity.QuotaStore, modelStore *entity.ModelStore, apiKeyStore *entity.APIKeyStore, dashboardRecorder DashboardRecorder) *Service {
	s := &Service{
		store:             store,
		modelStore:        modelStore,
		apiKeyStore:       apiKeyStore,
		rateCounter:       NewRateCounter(),
		dailyRequestCache: NewDailyRequestCounter(),
		dashboardRecorder: dashboardRecorder,
	}
	// 启动每日清理任务
	go s.dailyCleanupLoop()
	return s
}

// dailyCleanupLoop 每日清理过期缓存
func (s *Service) dailyCleanupLoop() {
	// 每小时检查一次
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		s.dailyRequestCache.CleanupExpired()
	}
}

// CheckQuota 检查用户配额
func (s *Service) CheckQuota(userID uuid.UUID, policyName string, modelID string) (*entity.QuotaCheckResult, error) {
	result := &entity.QuotaCheckResult{
		Allowed: true,
	}

	// 如果未指定策略，使用 default
	if policyName == "" {
		policyName = "default"
	}

	// 获取用户配额策略
	policy, err := s.store.GetPolicy(policyName)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		return nil, fmt.Errorf("policy not found: %s", policyName)
	}

	// 检查模型权限
	hasModelAccess := false
	for _, m := range policy.Models {
		if m == "*" || m == modelID {
			hasModelAccess = true
			break
		}
	}

	result.DefaultModel = policy.DefaultModel

	if !hasModelAccess {
		result.Allowed = false
		result.Reason = "model not allowed"
		return result, nil
	}

	// 检查可用时间段
	if !isWithinAvailableTime(policy.AvailableTimeRanges, time.Now()) {
		result.Allowed = false
		result.Reason = "outside available time range"
		return result, nil
	}

	// 检查速率限制（0 表示无限制）
	current := s.rateCounter.GetCount(userID.String(), policy.RateLimitWindow)

	if policy.RateLimit > 0 && current >= policy.RateLimit {
		result.Allowed = false
		result.Reason = "rate limit exceeded"
		result.RateLimit = policy.RateLimit
		result.RateRemaining = 0
		result.RateLimitWindow = policy.RateLimitWindow
		return result, nil
	}

	result.RateLimit = policy.RateLimit
	result.RateRemaining = policy.RateLimit - current - 1
	result.RateLimitWindow = policy.RateLimitWindow

	// 检查请求数配额
	today := time.Now()
	dailyRequests, err := s.getDailyRequestCountWithCache(userID, today)
	if err != nil {
		return nil, err
	}

	result.DailyRequests = dailyRequests
	result.DailyRequestLimit = policy.RequestQuotaDaily

	// 如果 RequestQuotaDaily 为 0，表示无限制（防止配置错误导致无法使用）
	if policy.RequestQuotaDaily > 0 && dailyRequests >= policy.RequestQuotaDaily {
		result.Allowed = false
		result.Reason = "daily request quota exceeded"
		return result, nil
	}

	return result, nil
}

// isWithinAvailableTime 检查指定时间是否在可用时间段内
// 空列表表示全天可用
func isWithinAvailableTime(ranges []entity.TimeRange, now time.Time) bool {
	if len(ranges) == 0 {
		return true
	}

	currentMinutes := now.Hour()*60 + now.Minute()

	for _, tr := range ranges {
		startH, startM, err1 := parseTimeOfDay(tr.Start)
		endH, endM, err2 := parseTimeOfDay(tr.End)
		if err1 != nil || err2 != nil {
			continue // 忽略无效的时间段配置
		}

		startMinutes := startH*60 + startM
		endMinutes := endH*60 + endM

		if startMinutes <= endMinutes {
			// 非跨午夜：如 08:00-18:00
			if currentMinutes >= startMinutes && currentMinutes < endMinutes {
				return true
			}
		} else {
			// 跨午夜：如 22:00-06:00 → [22:00, 24:00) ∪ [00:00, 06:00)
			if currentMinutes >= startMinutes || currentMinutes < endMinutes {
				return true
			}
		}
	}

	return false
}

// parseTimeOfDay 解析 "HH:MM" 格式的时间字符串
// 支持 "24:00" 表示一天结束
func parseTimeOfDay(s string) (int, int, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, fmt.Errorf("invalid time format: %s", s)
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	if h < 0 || h > 24 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("invalid time: %s", s)
	}
	if h == 24 && m != 0 {
		return 0, 0, fmt.Errorf("invalid time: %s (only 24:00 is allowed)", s)
	}
	return h, m, nil
}

// getDailyRequestCountWithCache 获取每日请求数（带缓存）
func (s *Service) getDailyRequestCountWithCache(userID uuid.UUID, date time.Time) (int, error) {
	// 1. 先查缓存
	if cached, exists := s.dailyRequestCache.Get(userID.String(), date); exists {
		return cached, nil
	}

	// 2. 缓存未命中，查数据库
	dailyRequests, err := s.store.GetDailyRequestCount(userID, date)
	if err != nil {
		return 0, err
	}

	// 3. 写入缓存
	s.dailyRequestCache.Set(userID.String(), date, dailyRequests)
	return dailyRequests, nil
}

// IncrementRate 增加速率计数
func (s *Service) IncrementRate(userID uuid.UUID, window int) error {
	s.rateCounter.Increment(userID.String(), window)
	return nil
}

// RecordRequest 记录一次请求（增加请求计数）
func (s *Service) RecordRequest(userID uuid.UUID, modelID string, backendID string, durationMs int64) error {
	// 增加请求计数
	err := s.store.IncrementRequestCount(userID, modelID)
	if err != nil {
		return err
	}

	// 更新内存缓存
	s.dailyRequestCache.Add(userID.String(), time.Now())

	// 记录小时级统计（用于仪表板）
	if s.dashboardRecorder != nil {
		s.dashboardRecorder.RecordHourlyStat(userID.String(), modelID, backendID, 0, 0, durationMs)
	}

	return nil
}

// RecordRequestTokens 记录一次请求并记录消耗的 Token
func (s *Service) RecordRequestTokens(userID uuid.UUID, modelID string, apiKeyID uuid.UUID, backendID string, inputTokens, outputTokens int, durationMs int64) error {
	// 增加请求计数及 Token (每日配额)
	err := s.store.IncrementUsage(userID, modelID, inputTokens, outputTokens)
	if err != nil {
		return err
	}

	// 增加 API Key Token 消耗
	if apiKeyID != uuid.Nil && s.apiKeyStore != nil {
		_ = s.apiKeyStore.AddTokensUsed(apiKeyID, inputTokens+outputTokens)
	}

	// 更新内存缓存
	s.dailyRequestCache.Add(userID.String(), time.Now())

	// 记录小时级统计（用于仪表板）
	if s.dashboardRecorder != nil {
		s.dashboardRecorder.RecordHourlyStat(userID.String(), modelID, backendID, inputTokens, outputTokens, durationMs)
	}

	return nil
}

// GetQuotaStats 获取配额统计
func (s *Service) GetQuotaStats(userID uuid.UUID, policyName string) (map[string]interface{}, error) {
	// 如果未指定策略，使用 default
	if policyName == "" {
		policyName = "default"
	}

	policy, err := s.store.GetPolicy(policyName)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		return nil, fmt.Errorf("policy not found: %s", policyName)
	}

	// 使用缓存获取每日请求数
	dailyRequests, err := s.getDailyRequestCountWithCache(userID, time.Now())
	if err != nil {
		return nil, err
	}

	// 处理 models_allowed：如果包含 *，返回所有模型名称列表
	var modelsAllowed []string
	if len(policy.Models) == 1 && policy.Models[0] == "*" {
		// 获取所有启用的模型
		models, err := s.modelStore.ListEnabled()
		if err != nil {
			return nil, err
		}
		for _, m := range models {
			modelsAllowed = append(modelsAllowed, m.ID)
		}
	} else {
		modelsAllowed = policy.Models
	}

	return map[string]interface{}{
		"daily_requests_used":  dailyRequests,
		"daily_requests_limit": policy.RequestQuotaDaily,
		"rate_limit":           policy.RateLimit,
		"rate_window":          policy.RateLimitWindow,
		"models_allowed":       modelsAllowed,
		"reset_time":           "00:00",
	}, nil
}

// CheckQuotaMulti 多策略版本：从 policyNames 中找匹配 modelID 的策略执行配额检查
// 如果没有匹配的策略，fallback 到名为 "default" 的策略
func (s *Service) CheckQuotaMulti(userID uuid.UUID, policyNames []string, modelID string) (*entity.QuotaCheckResult, error) {
	result := &entity.QuotaCheckResult{Allowed: true}

	// 1. 找到匹配该模型的策略
	policy, err := s.store.GetPolicyForModel(policyNames, modelID)
	if err != nil {
		return nil, err
	}
	// 2. 没找到匹配策略，fallback 到 "default"
	if policy == nil {
		policy, err = s.store.GetPolicy("default")
		if err != nil {
			return nil, err
		}
	}
	if policy == nil {
		return &entity.QuotaCheckResult{Allowed: false, Reason: "no matching policy found"}, nil
	}

	// 3. 检查模型权限
	hasModelAccess := false
	for _, m := range policy.Models {
		if m == "*" || m == modelID {
			hasModelAccess = true
			break
		}
	}
	result.DefaultModel = policy.DefaultModel
	if !hasModelAccess {
		result.Allowed = false
		result.Reason = "model not allowed"
		return result, nil
	}

	// 4. 检查可用时间段
	if !isWithinAvailableTime(policy.AvailableTimeRanges, time.Now()) {
		result.Allowed = false
		result.Reason = "outside available time range"
		return result, nil
	}

	// 5. 检查速率限制
	current := s.rateCounter.GetCount(userID.String(), policy.RateLimitWindow)
	if policy.RateLimit > 0 && current >= policy.RateLimit {
		result.Allowed = false
		result.Reason = "rate limit exceeded"
		result.RateLimit = policy.RateLimit
		result.RateRemaining = 0
		result.RateLimitWindow = policy.RateLimitWindow
		return result, nil
	}
	result.RateLimit = policy.RateLimit
	result.RateRemaining = policy.RateLimit - current - 1
	result.RateLimitWindow = policy.RateLimitWindow

	// 6. 检查每日配额
	today := time.Now()
	dailyRequests, err := s.getDailyRequestCountWithCache(userID, today)
	if err != nil {
		return nil, err
	}
	result.DailyRequests = dailyRequests
	result.DailyRequestLimit = policy.RequestQuotaDaily
	if policy.RequestQuotaDaily > 0 && dailyRequests >= policy.RequestQuotaDaily {
		result.Allowed = false
		result.Reason = "daily request quota exceeded"
		return result, nil
	}

	return result, nil
}
