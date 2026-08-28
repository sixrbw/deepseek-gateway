package entity

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
)

// AccessLogRow 对应 access_logs 表的一行（轻量元数据）
type AccessLogRow struct {
	ID            string
	UserID        string
	APIKeyID      string
	APIKeyName    string
	APIKeyPrefix  string
	Method        string
	Path          string
	ClientIP      string
	UserAgent     string
	ModelName     string
	Timestamp     time.Time
	StatusCode    int
	RequestBytes  int64
	ResponseBytes int64
	InputTokens   int
	OutputTokens  int
	DurationMs    int64
	HasPayload    bool
}

// AccessLogPayloadRow 对应 access_log_payloads 表（gzip 压缩的 headers/body）
type AccessLogPayloadRow struct {
	AccessLogID     string
	RequestHeaders  map[string]string
	RequestBody     string
	ResponseHeaders map[string]string
	ResponseBody    string
}

// AccessLogStore SQLite 访问日志存储
type AccessLogStore struct {
	db *sql.DB
}

// NewAccessLogStore 创建访问日志存储实例
func NewAccessLogStore(db *sql.DB) *AccessLogStore {
	return &AccessLogStore{db: db}
}

// Insert 写入一条访问日志（元数据 + payload）
func (s *AccessLogStore) Insert(
	userID uuid.UUID,
	apiKeyID uuid.UUID,
	apiKeyName, apiKeyPrefix string,
	method, path, clientIP, userAgent, modelName string,
	statusCode int,
	requestBytes, responseBytes int64,
	requestHeaders map[string]string,
	requestBody string,
	responseHeaders map[string]string,
	responseBody string,
	inputTokens, outputTokens int,
	durationMs int64,
) error {
	id := uuid.New().String()
	ts := time.Now().UTC()

	apiKeyIDStr := ""
	if apiKeyID != uuid.Nil {
		apiKeyIDStr = apiKeyID.String()
	}

	_, err := s.db.Exec(`
		INSERT INTO access_logs
			(id, user_id, api_key_id, api_key_name, api_key_prefix,
			 method, path, client_ip, user_agent, model_name,
			 timestamp, status_code, request_bytes, response_bytes,
			 input_tokens, output_tokens, duration_ms, has_payload)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1)`,
		id, userID.String(), apiKeyIDStr, apiKeyName, apiKeyPrefix,
		method, path, clientIP, userAgent, modelName,
		ts.Format(time.RFC3339Nano), statusCode, requestBytes, responseBytes,
		inputTokens, outputTokens, durationMs,
	)
	if err != nil {
		return fmt.Errorf("insert access_log: %w", err)
	}

	// 压缩并写入 payload
	rhGZ, err := compressJSON(requestHeaders)
	if err != nil {
		return fmt.Errorf("compress request_headers: %w", err)
	}
	rbGZ, err := compressString(requestBody)
	if err != nil {
		return fmt.Errorf("compress request_body: %w", err)
	}
	resHGZ, err := compressJSON(responseHeaders)
	if err != nil {
		return fmt.Errorf("compress response_headers: %w", err)
	}
	resBGZ, err := compressString(responseBody)
	if err != nil {
		return fmt.Errorf("compress response_body: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO access_log_payloads
			(access_log_id, request_headers_gz, request_body_gz, response_headers_gz, response_body_gz, created_at)
		VALUES (?,?,?,?,?,?)`,
		id, rhGZ, rbGZ, resHGZ, resBGZ, ts.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert access_log_payloads: %w", err)
	}

	return nil
}

// QueryOptions 查询选项
type QueryOptions struct {
	UserID    string    // 空表示不过滤
	StartTime time.Time // 零值表示不限制
	EndTime   time.Time // 零值表示不限制
	Limit     int       // 0 表示不限制
	Offset    int
}

// List 查询访问日志元数据列表（不含 payload）
func (s *AccessLogStore) List(opts QueryOptions) ([]AccessLogRow, error) {
	query := `SELECT id, user_id, api_key_id, api_key_name, api_key_prefix,
		method, path, client_ip, user_agent, model_name,
		timestamp, status_code, request_bytes, response_bytes,
		input_tokens, output_tokens, duration_ms, has_payload
	FROM access_logs WHERE 1=1`
	args := []interface{}{}

	if opts.UserID != "" {
		query += " AND user_id = ?"
		args = append(args, opts.UserID)
	}
	if !opts.StartTime.IsZero() {
		query += " AND timestamp >= ?"
		args = append(args, opts.StartTime.UTC().Format(time.RFC3339Nano))
	}
	if !opts.EndTime.IsZero() {
		query += " AND timestamp < ?"
		args = append(args, opts.EndTime.UTC().Format(time.RFC3339Nano))
	}
	query += " ORDER BY timestamp DESC"
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}
	if opts.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", opts.Offset)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query access_logs: %w", err)
	}
	defer rows.Close()

	var result []AccessLogRow
	for rows.Next() {
		var r AccessLogRow
		var tsStr string
		var hasPayload int
		if err := rows.Scan(
			&r.ID, &r.UserID, &r.APIKeyID, &r.APIKeyName, &r.APIKeyPrefix,
			&r.Method, &r.Path, &r.ClientIP, &r.UserAgent, &r.ModelName,
			&tsStr, &r.StatusCode, &r.RequestBytes, &r.ResponseBytes,
			&r.InputTokens, &r.OutputTokens, &r.DurationMs, &hasPayload,
		); err != nil {
			return nil, err
		}
		r.HasPayload = hasPayload == 1
		r.Timestamp, _ = time.Parse(time.RFC3339Nano, tsStr)
		result = append(result, r)
	}
	return result, rows.Err()
}

// GetPayload 获取指定日志的完整 payload（若已过期清理则返回 nil, nil）
func (s *AccessLogStore) GetPayload(accessLogID string) (*AccessLogPayloadRow, error) {
	row := s.db.QueryRow(`
		SELECT access_log_id, request_headers_gz, request_body_gz, response_headers_gz, response_body_gz
		FROM access_log_payloads WHERE access_log_id = ?`, accessLogID)

	var r AccessLogPayloadRow
	var rhGZ, rbGZ, resHGZ, resBGZ []byte
	err := row.Scan(&r.AccessLogID, &rhGZ, &rbGZ, &resHGZ, &resBGZ)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query access_log_payloads: %w", err)
	}

	r.RequestHeaders, _ = decompressJSON(rhGZ)
	r.RequestBody, _ = decompressString(rbGZ)
	r.ResponseHeaders, _ = decompressJSON(resHGZ)
	r.ResponseBody, _ = decompressString(resBGZ)

	return &r, nil
}

// DeleteOldPayloads 删除 cutoff 时间之前的 payload 数据
func (s *AccessLogStore) DeleteOldPayloads(cutoff time.Time) (int64, error) {
	res, err := s.db.Exec(
		`DELETE FROM access_log_payloads WHERE created_at < ?`,
		cutoff.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, fmt.Errorf("delete old payloads: %w", err)
	}
	// 同步更新 has_payload 标记
	_, _ = s.db.Exec(
		`UPDATE access_logs SET has_payload = 0 WHERE id NOT IN (SELECT access_log_id FROM access_log_payloads) AND has_payload = 1`,
	)
	n, _ := res.RowsAffected()
	return n, nil
}

// ─── 压缩工具 ───────────────────────────────────────────────

func compressJSON(v map[string]string) ([]byte, error) {
	if len(v) == 0 {
		return gzipBytes([]byte("{}")), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return gzipBytes(b), nil
}

func compressString(s string) ([]byte, error) {
	return gzipBytes([]byte(s)), nil
}

func gzipBytes(data []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(data)
	_ = w.Close()
	return buf.Bytes()
}

func decompressJSON(gz []byte) (map[string]string, error) {
	if len(gz) == 0 {
		return map[string]string{}, nil
	}
	data, err := gunzipBytes(gz)
	if err != nil {
		return map[string]string{}, nil
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]string{}, nil
	}
	return m, nil
}

func decompressString(gz []byte) (string, error) {
	if len(gz) == 0 {
		return "", nil
	}
	data, err := gunzipBytes(gz)
	if err != nil {
		return "", nil
	}
	return string(data), nil
}

func gunzipBytes(gz []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
