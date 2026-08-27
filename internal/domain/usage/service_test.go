package usage

import (
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	_ "modernc.org/sqlite"
)

// newTestDB 在内存 SQLite 上创建 access_logs 表，返回测试用 DB
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	assert.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS access_logs (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id     TEXT    NOT NULL,
		api_key_id  TEXT    NOT NULL DEFAULT '',
		api_key_name    TEXT NOT NULL DEFAULT '',
		api_key_prefix  TEXT NOT NULL DEFAULT '',
		method      TEXT    NOT NULL DEFAULT '',
		path        TEXT    NOT NULL DEFAULT '',
		client_ip   TEXT    NOT NULL DEFAULT '',
		user_agent  TEXT    NOT NULL DEFAULT '',
		model_name  TEXT    NOT NULL DEFAULT '',
		timestamp   DATETIME NOT NULL,
		status_code INTEGER NOT NULL DEFAULT 0,
		request_bytes  INTEGER NOT NULL DEFAULT 0,
		response_bytes INTEGER NOT NULL DEFAULT 0,
		request_headers  BLOB,
		request_body     BLOB,
		response_headers BLOB,
		response_body    BLOB,
		input_tokens  INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		duration_ms   INTEGER NOT NULL DEFAULT 0
	)`)
	assert.NoError(t, err)
	return db
}

// newTestService 创建带测试 DB 的 Service
func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db := newTestDB(t)
	svc := &Service{db: db}
	return svc, db
}

func TestRecordAccessAndGetRecentAccess(t *testing.T) {
	svc, db := newTestService(t)
	defer db.Close()

	userID := uuid.New()

	// 记录几条日志（稍微错开时间以保证排序稳定）
	svc.RecordAccess(userID, "GET", "/api/v1/user/quota", "192.168.1.1", "TestAgent/1.0", "glm4", 200, 100, 200, 50)
	time.Sleep(2 * time.Millisecond)
	svc.RecordAccess(userID, "POST", "/api/v1/chat/completions", "192.168.1.1", "TestAgent/1.0", "kimi", 200, 1000, 5000, 120)
	time.Sleep(2 * time.Millisecond)
	svc.RecordAccess(userID, "GET", "/api/v1/models", "192.168.1.1", "TestAgent/1.0", "", 200, 50, 1000, 30)

	logs := svc.GetRecentAccess(userID, 20)

	assert.Equal(t, 3, len(logs), "应该有3条访问日志")
	// 倒序：最新在前
	assert.Equal(t, "GET", logs[0].Method)
	assert.Equal(t, "/api/v1/models", logs[0].Path)
	assert.Equal(t, 200, logs[0].StatusCode)
}

func TestGetRecentAccessLimit(t *testing.T) {
	svc, db := newTestService(t)
	defer db.Close()

	userID := uuid.New()

	for i := 0; i < 25; i++ {
		svc.RecordAccess(userID, "GET", "/api/test", "127.0.0.1", "Test", "", 200, 100, 200, 10)
	}

	logs := svc.GetRecentAccess(userID, 10)
	assert.Equal(t, 10, len(logs), "应该只返回10条日志")

	logs5 := svc.GetRecentAccess(userID, 5)
	assert.Equal(t, 5, len(logs5), "应该只返回5条日志")
}

func TestGetRecentAccessEmpty(t *testing.T) {
	svc, db := newTestService(t)
	defer db.Close()

	userID := uuid.New()
	logs := svc.GetRecentAccess(userID, 20)
	assert.Equal(t, 0, len(logs), "应该返回空数组")
}

func TestConcurrentAccess(t *testing.T) {
	// Use file-based temp DB with WAL for concurrent write test
	tmpFile, err := os.CreateTemp("", "usage_test_*.db")
	assert.NoError(t, err)
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	db, err := sql.Open("sqlite", tmpFile.Name())
	assert.NoError(t, err)
	defer db.Close()

	// Serialize writes through a single connection to avoid SQLITE_BUSY
	db.SetMaxOpenConns(1)
	_, _ = db.Exec("PRAGMA journal_mode = WAL;")
	_, _ = db.Exec("PRAGMA busy_timeout = 5000;")
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS access_logs (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id     TEXT    NOT NULL,
		api_key_id  TEXT    NOT NULL DEFAULT '',
		api_key_name    TEXT NOT NULL DEFAULT '',
		api_key_prefix  TEXT NOT NULL DEFAULT '',
		method      TEXT    NOT NULL DEFAULT '',
		path        TEXT    NOT NULL DEFAULT '',
		client_ip   TEXT    NOT NULL DEFAULT '',
		user_agent  TEXT    NOT NULL DEFAULT '',
		model_name  TEXT    NOT NULL DEFAULT '',
		timestamp   DATETIME NOT NULL,
		status_code INTEGER NOT NULL DEFAULT 0,
		request_bytes  INTEGER NOT NULL DEFAULT 0,
		response_bytes INTEGER NOT NULL DEFAULT 0,
		request_headers  BLOB,
		request_body     BLOB,
		response_headers BLOB,
		response_body    BLOB,
		input_tokens  INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		duration_ms   INTEGER NOT NULL DEFAULT 0
	)`)
	assert.NoError(t, err)

	svc := &Service{db: db}
	userID := uuid.New()
	done := make(chan bool)

	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				svc.RecordAccess(userID, "GET", "/api/test", "127.0.0.1", "Test", "", 200, 100, 200, 15)
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// With MaxOpenConns=1, all 1000 writes are serialized and must succeed
	logs := svc.GetRecentAccess(userID, 0)
	assert.Equal(t, 1000, len(logs), "应该保留全部1000条日志")
}

func TestMultipleUsers(t *testing.T) {
	svc, db := newTestService(t)
	defer db.Close()

	user1 := uuid.New()
	user2 := uuid.New()

	for i := 0; i < 5; i++ {
		svc.RecordAccess(user1, "GET", "/api/user1", "127.0.0.1", "Test", "", 200, 100, 200, 5)
	}
	svc.RecordAccess(user2, "POST", "/api/user2", "192.168.1.1", "Test", "", 200, 200, 300, 8)

	logs1 := svc.GetRecentAccess(user1, 20)
	assert.Equal(t, 5, len(logs1))

	logs2 := svc.GetRecentAccess(user2, 20)
	assert.Equal(t, 1, len(logs2))
	assert.Equal(t, "POST", logs2[0].Method)
}

func TestGetAccessLogsByDates(t *testing.T) {
	svc, db := newTestService(t)
	defer db.Close()

	userID := uuid.New()

	// 插入两天的日志
	_, _ = db.Exec(`INSERT INTO access_logs
		(user_id, api_key_id, api_key_name, api_key_prefix, method, path, client_ip, user_agent, model_name, timestamp, status_code, request_bytes, response_bytes, input_tokens, output_tokens, duration_ms)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		userID.String(), uuid.Nil.String(), "", "", "GET", "/test", "1.2.3.4", "Agent", "gpt-4",
		"2026-08-20 10:00:00", 200, 100, 200, 10, 20, 50,
	)
	_, _ = db.Exec(`INSERT INTO access_logs
		(user_id, api_key_id, api_key_name, api_key_prefix, method, path, client_ip, user_agent, model_name, timestamp, status_code, request_bytes, response_bytes, input_tokens, output_tokens, duration_ms)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		userID.String(), uuid.Nil.String(), "", "", "POST", "/chat", "1.2.3.4", "Agent", "gpt-4",
		"2026-08-21 12:00:00", 200, 500, 1000, 30, 40, 100,
	)
	_, _ = db.Exec(`INSERT INTO access_logs
		(user_id, api_key_id, api_key_name, api_key_prefix, method, path, client_ip, user_agent, model_name, timestamp, status_code, request_bytes, response_bytes, input_tokens, output_tokens, duration_ms)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		userID.String(), uuid.Nil.String(), "", "", "GET", "/other", "1.2.3.4", "Agent", "gpt-4",
		"2026-08-25 09:00:00", 200, 50, 100, 5, 10, 30,
	)

	var collected []AccessLog
	err := svc.GetAccessLogsByDates([]string{"2026-08-20", "2026-08-21"}, "", func(l AccessLog) error {
		collected = append(collected, l)
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 2, len(collected), "应该只返回两天的2条日志")

	// 按用户过滤
	var filtered []AccessLog
	err = svc.GetAccessLogsByDates([]string{"2026-08-25"}, userID.String(), func(l AccessLog) error {
		filtered = append(filtered, l)
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, len(filtered))
	assert.Equal(t, "/other", filtered[0].Path)
}

func TestCompressDecompress(t *testing.T) {
	original := "Hello, 世界! This is a test body."
	compressed := compressText(original)
	assert.NotNil(t, compressed)
	assert.True(t, len(compressed) > 0)

	decompressed := decompressText(compressed)
	assert.Equal(t, original, decompressed)
}

func TestCompressDecompressJSON(t *testing.T) {
	m := map[string]string{"Content-Type": "application/json", "X-Request-ID": "abc123"}
	compressed := compressJSON(m)
	assert.NotNil(t, compressed)

	result := decompressJSON(compressed)
	assert.Equal(t, m, result)
}

// TestMain ensures we don't need special setup for tests
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
