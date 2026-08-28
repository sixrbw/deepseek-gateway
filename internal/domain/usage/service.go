package usage

import (
	"time"

	"github.com/google/uuid"
	"modelgate/internal/infra/constants"
	"modelgate/internal/infra/logger"
	"modelgate/internal/infra/utils"
	entity "modelgate/internal/repository"
)

// AccessLog 访问日志结构（用于 API 响应）
type AccessLog struct {
	UserID          uuid.UUID         `json:"user_id"`
	APIKeyID        uuid.UUID         `json:"api_key_id"`
	APIKeyName      string            `json:"api_key_name"`
	APIKeyPrefix    string            `json:"api_key_prefix"`
	Method          string            `json:"method"`
	Path            string            `json:"path"`
	ClientIP        string            `json:"client_ip"`
	UserAgent       string            `json:"user_agent"`
	ModelName       string            `json:"model_name"`
	Timestamp       time.Time         `json:"timestamp"`
	StatusCode      int               `json:"status_code"`
	RequestBytes    int64             `json:"request_bytes"`
	ResponseBytes   int64             `json:"response_bytes"`
	RequestHeaders  map[string]string `json:"request_headers,omitempty"`
	RequestBody     string            `json:"request_body,omitempty"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`
	ResponseBody    string            `json:"response_body,omitempty"`
	InputTokens     int               `json:"input_tokens"`
	OutputTokens    int               `json:"output_tokens"`
	DurationMs      int64             `json:"duration_ms"`
	// PayloadExpired 为 true 时表示 payload 已超过保留期被清理
	PayloadExpired bool `json:"payload_expired,omitempty"`
}

// SimpleAccessLog 简化版的访问日志（用于列表显示）
type SimpleAccessLog struct {
	UserID        string    `json:"user_id"`
	Method        string    `json:"method"`
	Path          string    `json:"path"`
	ClientIP      string    `json:"client_ip"`
	UserAgent     string    `json:"user_agent"`
	ModelName     string    `json:"model_name"`
	Timestamp     time.Time `json:"timestamp"`
	StatusCode    int       `json:"status_code"`
	RequestBytes  int64     `json:"request_bytes"`
	ResponseBytes int64     `json:"response_bytes"`
}

// Beautify 对 User-Agent 进行脱敏/美化处理
func (log *AccessLog) Beautify() {
	log.UserAgent = utils.FormatUserAgentForDisplay(log.UserAgent, log.RequestHeaders["Referer"])
}

// ToSimple 转换为简化版访问日志
func (log *AccessLog) ToSimple() SimpleAccessLog {
	return SimpleAccessLog{
		UserID:        log.UserID.String(),
		Method:        log.Method,
		Path:          log.Path,
		ClientIP:      log.ClientIP,
		UserAgent:     utils.FormatUserAgentForDisplay(log.UserAgent, log.RequestHeaders["Referer"]),
		ModelName:     log.ModelName,
		Timestamp:     log.Timestamp,
		StatusCode:    log.StatusCode,
		RequestBytes:  log.RequestBytes,
		ResponseBytes: log.ResponseBytes,
	}
}

// rowToAccessLog 将 AccessLogRow 转为 AccessLog（不含 payload）
func rowToAccessLog(r entity.AccessLogRow) AccessLog {
	uid, _ := uuid.Parse(r.UserID)
	akid, _ := uuid.Parse(r.APIKeyID)
	log := AccessLog{
		UserID:        uid,
		APIKeyID:      akid,
		APIKeyName:    r.APIKeyName,
		APIKeyPrefix:  r.APIKeyPrefix,
		Method:        r.Method,
		Path:          r.Path,
		ClientIP:      r.ClientIP,
		UserAgent:     r.UserAgent,
		ModelName:     r.ModelName,
		Timestamp:     r.Timestamp,
		StatusCode:    r.StatusCode,
		RequestBytes:  r.RequestBytes,
		ResponseBytes: r.ResponseBytes,
		InputTokens:   r.InputTokens,
		OutputTokens:  r.OutputTokens,
		DurationMs:    r.DurationMs,
	}
	// 若原本有 payload 但 has_payload=false，说明已被清理
	if !r.HasPayload {
		log.PayloadExpired = false // has_payload=0 可能根本没有过 payload（老数据迁移兼容），不标记
	}
	return log
}

// Record 使用记录
type Record struct {
	UserID          uuid.UUID
	UserName        string
	UserEmail       string
	ModelID         string
	LatencyMs       int
	ClientIP        string
	UserAgent       string
	StatusCode      int
	Error           string
	BackendID       string
	InputTokens     int
	OutputTokens    int
	TraceID         string
	RequestPayload  map[string]interface{}
	ResponsePayload string
	TTFTMs          int64
}

// Service 使用记录服务
type Service struct {
	logger   *logger.UserLogger
	logStore *entity.AccessLogStore
}

// NewService 创建使用记录服务（无 SQLite 依赖，保持向后兼容）
func NewService(logger *logger.UserLogger) *Service {
	return &Service{
		logger: logger,
	}
}

// NewServiceWithStore 创建使用记录服务（带 SQLite 日志存储）
func NewServiceWithStore(lg *logger.UserLogger, logStore *entity.AccessLogStore) *Service {
	return &Service{
		logger:   lg,
		logStore: logStore,
	}
}

// RecordUsageDetailed 记录详细的使用信息
func (s *Service) RecordUsageDetailed(record *Record) {
	s.logger.LogUsageWithDetails(record.UserID.String(), logger.UsageLogEntry{
		Time:            time.Now().Format(time.RFC3339),
		UserName:        record.UserName,
		UserEmail:       record.UserEmail,
		Model:           record.ModelID,
		LatencyMs:       record.LatencyMs,
		ClientIP:        record.ClientIP,
		ClientType:      utils.ParseClientType(record.UserAgent),
		StatusCode:      record.StatusCode,
		Error:           record.Error,
		BackendID:       record.BackendID,
		InputTokens:     record.InputTokens,
		OutputTokens:    record.OutputTokens,
		TraceID:         record.TraceID,
		RequestPayload:  record.RequestPayload,
		ResponsePayload: record.ResponsePayload,
		OriginalTTFTMs:  record.TTFTMs,
	})
}

// CleanupOldRecords 清理旧记录
func (s *Service) CleanupOldRecords() error {
	return s.logger.CleanupOldLogs()
}

// CleanupOldPayloads 清理超过保留期的 payload（由后台任务定期调用）
func (s *Service) CleanupOldPayloads(retentionDays int) (int64, error) {
	if s.logStore == nil {
		return 0, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	return s.logStore.DeleteOldPayloads(cutoff)
}

// GetUsageStats 获取使用统计
func (s *Service) GetUsageStats(userID string, startDate, endDate time.Time) (map[string]interface{}, error) {
	return map[string]interface{}{
		"user_id":    userID,
		"start_date": startDate.Format("2006-01-02"),
		"end_date":   endDate.Format("2006-01-02"),
		"note":       "Stats from file logs not yet implemented",
	}, nil
}

// Flush 刷新日志（兼容旧代码）
func (s *Service) Flush() {}

// RecordAccess 记录用户访问日志（简化版）
func (s *Service) RecordAccess(userID uuid.UUID, method, path, clientIP, userAgent string, modelName string, statusCode int, requestBytes, responseBytes int64, durationMs int64) {
	s.RecordAccessDetailed(userID, uuid.Nil, "", "", method, path, clientIP, userAgent, modelName, statusCode, requestBytes, responseBytes, nil, "", nil, "", 0, 0, durationMs)
}

// RecordAccessDetailed 记录用户访问日志（包含详细信息）
func (s *Service) RecordAccessDetailed(
	userID uuid.UUID,
	apiKeyID uuid.UUID,
	apiKeyName string,
	apiKeyPrefix string,
	method, path, clientIP, userAgent string, modelName string,
	statusCode int,
	requestBytes, responseBytes int64,
	requestHeaders map[string]string,
	requestBody string,
	responseHeaders map[string]string,
	responseBody string,
	inputTokens int,
	outputTokens int,
	durationMs int64,
) {
	if s.logStore == nil {
		return
	}

	// 截断大内容
	requestBody = truncateString(requestBody, constants.MaxLogRequestBodySize)
	responseBody = truncateString(responseBody, constants.MaxLogResponseBodySize)

	if err := s.logStore.Insert(
		userID, apiKeyID, apiKeyName, apiKeyPrefix,
		method, path, clientIP, userAgent, modelName,
		statusCode, requestBytes, responseBytes,
		requestHeaders, requestBody, responseHeaders, responseBody,
		inputTokens, outputTokens, durationMs,
	); err != nil {
		// 非致命错误，仅记录
		_ = err
	}
}

// GetRecentAccess 获取用户最近访问记录（按时间倒序）
func (s *Service) GetRecentAccess(userID uuid.UUID, limit int) []AccessLog {
	if s.logStore == nil {
		return []AccessLog{}
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.logStore.List(entity.QueryOptions{
		UserID: userID.String(),
		Limit:  limit,
	})
	if err != nil {
		return []AccessLog{}
	}
	logs := make([]AccessLog, 0, len(rows))
	for _, r := range rows {
		logs = append(logs, rowToAccessLog(r))
	}
	return logs
}

// GetAllRecentAccess 获取所有用户最近的访问记录（按时间倒序）
func (s *Service) GetAllRecentAccess(limit int) []AccessLog {
	if s.logStore == nil {
		return []AccessLog{}
	}
	rows, err := s.logStore.List(entity.QueryOptions{Limit: limit})
	if err != nil {
		return []AccessLog{}
	}
	logs := make([]AccessLog, 0, len(rows))
	for _, r := range rows {
		logs = append(logs, rowToAccessLog(r))
	}
	return logs
}

// GetAccessLogsByDateRange 获取指定时间范围内的访问记录（含 payload，若已过期则标记）
func (s *Service) GetAccessLogsByDateRange(userID string, start, end time.Time, withPayload bool) []AccessLog {
	if s.logStore == nil {
		return []AccessLog{}
	}
	opts := entity.QueryOptions{
		UserID:    userID,
		StartTime: start,
		EndTime:   end,
	}
	rows, err := s.logStore.List(opts)
	if err != nil {
		return []AccessLog{}
	}

	logs := make([]AccessLog, 0, len(rows))
	for _, r := range rows {
		log := rowToAccessLog(r)
		if withPayload && r.HasPayload {
			payload, _ := s.logStore.GetPayload(r.ID)
			if payload != nil {
				log.RequestHeaders = payload.RequestHeaders
				log.RequestBody = payload.RequestBody
				log.ResponseHeaders = payload.ResponseHeaders
				log.ResponseBody = payload.ResponseBody
			} else {
				// payload 已被清理
				log.PayloadExpired = true
			}
		} else if r.HasPayload && !withPayload {
			// 不需要 payload，不标记
		}
		logs = append(logs, log)
	}
	return logs
}

// GetAllAccessLogsByDateRange 获取所有用户指定时间范围内的访问记录
func (s *Service) GetAllAccessLogsByDateRange(start, end time.Time, withPayload bool) []AccessLog {
	return s.GetAccessLogsByDateRange("", start, end, withPayload)
}

// truncateString 截断字符串到指定长度
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n[truncated...]"
}

// GetAccessLogsByDates 按多个具体日期（YYYY-MM-DD）流式迭代访问日志
// 对每条记录调用 fn；若 fn 返回错误则中止迭代。
func (s *Service) GetAccessLogsByDates(dates []string, userID string, fn func(AccessLog) error) error {
	if s.logStore == nil || len(dates) == 0 {
		return nil
	}
	for _, d := range dates {
		start, err := time.Parse("2006-01-02", d)
		if err != nil {
			continue
		}
		end := start.Add(24 * time.Hour)
		rows, err := s.logStore.List(entity.QueryOptions{
			UserID:    userID,
			StartTime: start.UTC(),
			EndTime:   end.UTC(),
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			log := rowToAccessLog(r)
			if r.HasPayload {
				payload, _ := s.logStore.GetPayload(r.ID)
				if payload != nil {
					log.RequestHeaders = payload.RequestHeaders
					log.RequestBody = payload.RequestBody
					log.ResponseHeaders = payload.ResponseHeaders
					log.ResponseBody = payload.ResponseBody
				} else {
					log.PayloadExpired = true
				}
			}
			if err := fn(log); err != nil {
				return err
			}
		}
	}
	return nil
}
