package usage

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"io"
	"sort"
	"time"

	"github.com/google/uuid"
	"modelgate/internal/infra/constants"
	"modelgate/internal/infra/logger"
	"modelgate/internal/infra/utils"
)

// AccessLog 访问日志结构
type AccessLog struct {
	ID              int64             `json:"id"`
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
	RequestHeaders  map[string]string `json:"request_headers"`
	RequestBody     string            `json:"request_body"`
	ResponseHeaders map[string]string `json:"response_headers"`
	ResponseBody    string            `json:"response_body"`
	InputTokens     int               `json:"input_tokens"`
	OutputTokens    int               `json:"output_tokens"`
	DurationMs      int64             `json:"duration_ms"`
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

// Service 使用记录服务
type Service struct {
	logger *logger.UserLogger
	db     *sql.DB
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

// NewService 创建使用记录服务（内存模式，向后兼容）
func NewService(logger *logger.UserLogger) *Service {
	return &Service{
		logger: logger,
	}
}

// NewServiceWithDB 创建带数据库持久化的使用记录服务
func NewServiceWithDB(logger *logger.UserLogger, db *sql.DB) *Service {
	return &Service{
		logger: logger,
		db:     db,
	}
}

// RecordUsageDetailed 记录详细的使用信息（写文件日志）
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

// CleanupOldRecords 清理旧记录（由 logger 自动处理）
func (s *Service) CleanupOldRecords() error {
	return s.logger.CleanupOldLogs()
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

// Flush 保留以兼容旧代码
func (s *Service) Flush() {}

// RecordAccess 记录用户访问日志（简单版）
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
	if s.db == nil {
		return
	}

	reqHeadersBlob := compressJSON(requestHeaders)
	respHeadersBlob := compressJSON(responseHeaders)
	reqBodyBlob := compressText(truncateString(requestBody, constants.MaxLogRequestBodySize))
	respBodyBlob := compressText(truncateString(responseBody, constants.MaxLogResponseBodySize))

	_, _ = s.db.Exec(`
		INSERT INTO access_logs
			(user_id, api_key_id, api_key_name, api_key_prefix,
			 method, path, client_ip, user_agent, model_name,
			 timestamp, status_code, request_bytes, response_bytes,
			 request_headers, request_body, response_headers, response_body,
			 input_tokens, output_tokens, duration_ms)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		userID.String(), apiKeyID.String(), apiKeyName, apiKeyPrefix,
		method, path, clientIP, userAgent, modelName,
		time.Now().UTC(), statusCode, requestBytes, responseBytes,
		reqHeadersBlob, reqBodyBlob, respHeadersBlob, respBodyBlob,
		inputTokens, outputTokens, durationMs,
	)
}

// truncateString 截断字符串到指定长度
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n[truncated...]"
}

// compressJSON 将 map 序列化并 gzip 压缩
func compressJSON(v map[string]string) []byte {
	if len(v) == 0 {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return gzipCompress(data)
}

// compressText 将字符串 gzip 压缩
func compressText(s string) []byte {
	if s == "" {
		return nil
	}
	return gzipCompress([]byte(s))
}

// gzipCompress 执行 gzip 压缩
func gzipCompress(data []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return data // fallback to raw
	}
	if err := w.Close(); err != nil {
		return data
	}
	return buf.Bytes()
}

// gzipDecompress 执行 gzip 解压
func gzipDecompress(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		// 不是 gzip，当作原始数据
		return data, nil
	}
	defer r.Close()
	return io.ReadAll(r)
}

// decompressText 解压文本 blob
func decompressText(blob []byte) string {
	if len(blob) == 0 {
		return ""
	}
	data, err := gzipDecompress(blob)
	if err != nil {
		return string(blob)
	}
	return string(data)
}

// decompressJSON 解压 JSON blob 为 map
func decompressJSON(blob []byte) map[string]string {
	if len(blob) == 0 {
		return nil
	}
	data, err := gzipDecompress(blob)
	if err != nil {
		data = blob
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

// scanAccessLog 从 SQL 行扫描 AccessLog
func scanAccessLog(rows *sql.Rows) (AccessLog, error) {
	var log AccessLog
	var userIDStr, apiKeyIDStr string
	var reqHeadersBlob, reqBodyBlob, respHeadersBlob, respBodyBlob []byte
	err := rows.Scan(
		&log.ID,
		&userIDStr, &apiKeyIDStr, &log.APIKeyName, &log.APIKeyPrefix,
		&log.Method, &log.Path, &log.ClientIP, &log.UserAgent, &log.ModelName,
		&log.Timestamp, &log.StatusCode, &log.RequestBytes, &log.ResponseBytes,
		&reqHeadersBlob, &reqBodyBlob, &respHeadersBlob, &respBodyBlob,
		&log.InputTokens, &log.OutputTokens, &log.DurationMs,
	)
	if err != nil {
		return log, err
	}
	log.UserID, _ = uuid.Parse(userIDStr)
	log.APIKeyID, _ = uuid.Parse(apiKeyIDStr)
	log.RequestHeaders = decompressJSON(reqHeadersBlob)
	log.RequestBody = decompressText(reqBodyBlob)
	log.ResponseHeaders = decompressJSON(respHeadersBlob)
	log.ResponseBody = decompressText(respBodyBlob)
	return log, nil
}

const selectAccessLogCols = `
	id,
	user_id, api_key_id, api_key_name, api_key_prefix,
	method, path, client_ip, user_agent, model_name,
	timestamp, status_code, request_bytes, response_bytes,
	request_headers, request_body, response_headers, response_body,
	input_tokens, output_tokens, duration_ms
`

// GetRecentAccess 获取用户最近访问记录（按时间倒序）
// limit <= 0 表示不限制条数
func (s *Service) GetRecentAccess(userID uuid.UUID, limit int) []AccessLog {
	if s.db == nil {
		return []AccessLog{}
	}
	var (
		rows *sql.Rows
		err  error
	)
	if limit > 0 {
		rows, err = s.db.Query(
			`SELECT `+selectAccessLogCols+` FROM access_logs WHERE user_id = ? ORDER BY timestamp DESC LIMIT ?`,
			userID.String(), limit,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT `+selectAccessLogCols+` FROM access_logs WHERE user_id = ? ORDER BY timestamp DESC`,
			userID.String(),
		)
	}
	if err != nil {
		return []AccessLog{}
	}
	defer rows.Close()
	return collectRows(rows)
}

// GetAllRecentAccess 获取所有用户最近的访问记录（按时间倒序）
func (s *Service) GetAllRecentAccess(limit int) []AccessLog {
	if s.db == nil {
		return []AccessLog{}
	}
	query := `SELECT ` + selectAccessLogCols + ` FROM access_logs ORDER BY timestamp DESC`
	args := []interface{}{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return []AccessLog{}
	}
	defer rows.Close()
	return collectRows(rows)
}

// GetAccessLogsByDates 获取指定日期集合内的访问记录（流式，通过回调处理）
func (s *Service) GetAccessLogsByDates(dates []string, userID string, fn func(AccessLog) error) error {
	if s.db == nil || len(dates) == 0 {
		return nil
	}

	// Build IN clause
	placeholders := make([]string, len(dates))
	args := make([]interface{}, len(dates))
	for i, d := range dates {
		placeholders[i] = "DATE(timestamp) = ?"
		args[i] = d
	}

	whereClause := "(" + joinStrings(placeholders, " OR ") + ")"
	if userID != "" {
		whereClause = "user_id = ? AND " + whereClause
		args = append([]interface{}{userID}, args...)
	}

	rows, err := s.db.Query(
		`SELECT `+selectAccessLogCols+` FROM access_logs WHERE `+whereClause+` ORDER BY timestamp ASC`,
		args...,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		log, err := scanAccessLog(rows)
		if err != nil {
			continue
		}
		if err := fn(log); err != nil {
			return err
		}
	}
	return rows.Err()
}

func collectRows(rows *sql.Rows) []AccessLog {
	var logs []AccessLog
	for rows.Next() {
		log, err := scanAccessLog(rows)
		if err != nil {
			continue
		}
		logs = append(logs, log)
	}
	// already sorted by DB, but ensure
	sort.SliceStable(logs, func(i, j int) bool {
		return logs[i].Timestamp.After(logs[j].Timestamp)
	})
	return logs
}

func joinStrings(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
