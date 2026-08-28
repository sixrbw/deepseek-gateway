package usage

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	_ "modernc.org/sqlite"
	entity "modelgate/internal/repository"
)

// newTestStore creates an in-memory SQLite access log store for testing.
func newTestStore(t *testing.T) *entity.AccessLogStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// Enable WAL mode and busy_timeout for concurrent write tests
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA busy_timeout = 5000;",
	} {
		if _, err := db.Exec(pragma); err != nil {
			t.Logf("pragma: %v", err)
		}
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS access_logs (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			api_key_id TEXT NOT NULL DEFAULT '',
			api_key_name TEXT NOT NULL DEFAULT '',
			api_key_prefix TEXT NOT NULL DEFAULT '',
			method TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '',
			client_ip TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT '',
			model_name TEXT NOT NULL DEFAULT '',
			timestamp DATETIME NOT NULL,
			status_code INTEGER NOT NULL DEFAULT 0,
			request_bytes INTEGER NOT NULL DEFAULT 0,
			response_bytes INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			has_payload INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS access_log_payloads (
			access_log_id TEXT PRIMARY KEY,
			request_headers_gz BLOB,
			request_body_gz BLOB,
			response_headers_gz BLOB,
			response_body_gz BLOB,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return entity.NewAccessLogStore(db)
}

// newTestService creates a usage service backed by an in-memory SQLite store.
func newTestService(t *testing.T) *Service {
	t.Helper()
	store := newTestStore(t)
	return NewServiceWithStore(nil, store)
}

func TestRecordAccessAndGetRecentAccess(t *testing.T) {
	service := newTestService(t)
	userID := uuid.New()

	// 记录几条日志（加小间隔确保时间戳有序）
	service.RecordAccess(userID, "GET", "/api/v1/user/quota", "192.168.1.1", "TestAgent/1.0", "glm4", 200, 100, 200, 50)
	time.Sleep(2 * time.Millisecond)
	service.RecordAccess(userID, "POST", "/api/v1/chat/completions", "192.168.1.1", "TestAgent/1.0", "kimi", 200, 1000, 5000, 120)
	time.Sleep(2 * time.Millisecond)
	service.RecordAccess(userID, "GET", "/api/v1/models", "192.168.1.1", "TestAgent/1.0", "", 200, 50, 1000, 30)

	logs := service.GetRecentAccess(userID, 20)

	assert.Equal(t, 3, len(logs), "应该有3条访问日志")

	// 验证倒序排列（最新的在前）
	assert.Equal(t, "GET", logs[0].Method, "第一条应该是最新的 GET 请求")
	assert.Equal(t, "POST", logs[1].Method, "第二条应该是 POST 请求")

	assert.Equal(t, "/api/v1/models", logs[0].Path)
	assert.Equal(t, 200, logs[0].StatusCode)
	assert.Equal(t, int64(50), logs[0].RequestBytes)
	assert.Equal(t, int64(1000), logs[0].ResponseBytes)
}

func TestGetRecentAccessLimit(t *testing.T) {
	service := newTestService(t)
	userID := uuid.New()

	for i := 0; i < 25; i++ {
		service.RecordAccess(userID, "GET", "/api/test", "127.0.0.1", "Test", "", 200, 100, 200, 10)
	}

	// limit=20
	logs := service.GetRecentAccess(userID, 20)
	assert.Equal(t, 20, len(logs), "应该只返回20条日志")

	// limit=5
	logs = service.GetRecentAccess(userID, 5)
	assert.Equal(t, 5, len(logs), "应该只返回5条日志")
}

func TestGetRecentAccessEmpty(t *testing.T) {
	service := newTestService(t)
	userID := uuid.New()

	logs := service.GetRecentAccess(userID, 20)
	assert.Equal(t, 0, len(logs), "应该返回空数组")
}

func TestConcurrentAccess(t *testing.T) {
	service := newTestService(t)
	userID := uuid.New()
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				service.RecordAccess(userID, "GET", "/api/test", "127.0.0.1", "Test", "", 200, 100, 200, 15)
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// With persistent SQLite store, all 100 records should be available
	logs := service.GetRecentAccess(userID, 200)
	assert.Equal(t, 100, len(logs), "应该有100条日志")
}

func TestMultipleUsers(t *testing.T) {
	service := newTestService(t)
	user1 := uuid.New()
	user2 := uuid.New()

	for i := 0; i < 5; i++ {
		service.RecordAccess(user1, "GET", "/api/user1", "127.0.0.1", "Test", "", 200, 100, 200, 5)
	}
	service.RecordAccess(user2, "POST", "/api/user2", "192.168.1.1", "Test", "", 200, 200, 300, 8)

	logs1 := service.GetRecentAccess(user1, 20)
	assert.Equal(t, 5, len(logs1))
	assert.Equal(t, "GET", logs1[0].Method)

	logs2 := service.GetRecentAccess(user2, 20)
	assert.Equal(t, 1, len(logs2))
	assert.Equal(t, "POST", logs2[0].Method)
}

func TestCleanupOldPayloads(t *testing.T) {
	service := newTestService(t)
	userID := uuid.New()

	service.RecordAccess(userID, "GET", "/api/test", "127.0.0.1", "Test", "", 200, 100, 200, 10)

	// Use retentionDays=-1 so cutoff is tomorrow — deletes all existing payloads
	deleted, err := service.CleanupOldPayloads(-1)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
}

func TestGetAccessLogsByDateRange(t *testing.T) {
	service := newTestService(t)
	userID := uuid.New()

	service.RecordAccess(userID, "GET", "/api/test", "127.0.0.1", "Test", "", 200, 100, 200, 10)

	start := time.Now().Add(-time.Hour)
	end := time.Now().Add(time.Hour)
	logs := service.GetAccessLogsByDateRange(userID.String(), start, end, false)
	assert.Equal(t, 1, len(logs))
}

func TestGetAccessLogsByDates(t *testing.T) {
	service := newTestService(t)
	userID := uuid.New()

	// 记录两条日志（均在 "today"，将通过日期过滤）
	service.RecordAccess(userID, "GET", "/path-a", "1.2.3.4", "Agent", "gpt-4", 200, 100, 200, 10)
	service.RecordAccess(userID, "POST", "/path-b", "1.2.3.4", "Agent", "gpt-4", 200, 500, 1000, 30)

	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	var collected []AccessLog
	err := service.GetAccessLogsByDates([]string{today}, userID.String(), func(l AccessLog) error {
		collected = append(collected, l)
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 2, len(collected), "今天的日志应该返回2条")

	// 昨天应返回0条
	var yesterday0 []AccessLog
	err = service.GetAccessLogsByDates([]string{yesterday}, userID.String(), func(l AccessLog) error {
		yesterday0 = append(yesterday0, l)
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 0, len(yesterday0), "昨天应该没有日志")

	// 两天选择，结果只来自今天
	var multi []AccessLog
	err = service.GetAccessLogsByDates([]string{yesterday, today}, userID.String(), func(l AccessLog) error {
		multi = append(multi, l)
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 2, len(multi), "多日期时只有今天的2条")
}
