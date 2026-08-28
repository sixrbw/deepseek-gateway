package user

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"modelgate/internal/config"
	"modelgate/internal/domain/usage"
	entity "modelgate/internal/repository"
	_ "modernc.org/sqlite"
)

func TestSystemConfigHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create temporary config file
	tmpFile, err := os.CreateTemp("", "config-test-*.yaml")
	require.NoError(t, err)
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	initialCfg := &config.Config{
		Server: config.ServerConfig{
			Port:            8080,
			Mode:            "release",
			ReadTimeout:     60 * time.Second,
			WriteTimeout:    30 * time.Minute,
			IdleTimeout:     300 * time.Second,
			MaxHeaderBytes:  1048576,
			ShutdownTimeout: 30 * time.Second,
		},
		Database: config.DatabaseConfig{
			Path: "modelgate.db",
		},
		JWT: config.JWTConfig{
			Secret:      "test-secret-change-in-production",
			ExpireHours: 24,
		},
		Logs: config.LogConfig{
			RawDumps: "none",
		},
	}
	cm := config.NewManager(initialCfg, tmpPath)

	h := &Handler{
		cm: cm,
	}

	router := gin.New()
	admin := router.Group("/admin")
	{
		configGrp := admin.Group("/config")
		configGrp.GET("/system", h.GetSystemConfig)
		configGrp.PUT("/system", h.UpdateSystemConfig)
	}

	// 1. Test GET /admin/config/system
	req, _ := http.NewRequest("GET", "/admin/config/system", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusOK, resp.Code)

	var getResponse struct {
		Data RefinedSystemConfigJSON `json:"data"`
	}
	err = json.Unmarshal(resp.Body.Bytes(), &getResponse)
	require.NoError(t, err)
	assert.Equal(t, "1m0s", getResponse.Data.Server.ReadTimeout)
	assert.Equal(t, "30m0s", getResponse.Data.Server.WriteTimeout)
	assert.Equal(t, "5m0s", getResponse.Data.Server.IdleTimeout)

	// 2. Test PUT /admin/config/system (Valid)
	updatePayload := RefinedSystemConfigJSON{
		Server: RefinedServerConfigJSON{
			ReadTimeout:  "15s",
			WriteTimeout: "10m",
			IdleTimeout:  "120s",
		},
		Frontend: config.FrontendConfig{
			FeedbackURL:         "https://feedback.new.com",
			DevManualURL:        "https://docs.new.com",
			RegistrationEnabled: true,
		},
	}
	payloadBytes, _ := json.Marshal(updatePayload)
	req, _ = http.NewRequest("PUT", "/admin/config/system", bytes.NewBuffer(payloadBytes))
	req.Header.Set("Content-Type", "application/json")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusOK, resp.Code)

	// Verify manager state
	currentCfg := cm.GetConfig()
	assert.Equal(t, 15*time.Second, currentCfg.Server.ReadTimeout)
	assert.Equal(t, 10*time.Minute, currentCfg.Server.WriteTimeout)
	assert.Equal(t, 120*time.Second, currentCfg.Server.IdleTimeout)
	assert.Equal(t, "https://feedback.new.com", currentCfg.Frontend.FeedbackURL)
	assert.True(t, currentCfg.Frontend.RegistrationEnabled)

	// 3. Test PUT /admin/config/system (Invalid Timeout string)
	invalidPayload := RefinedSystemConfigJSON{
		Server: RefinedServerConfigJSON{
			ReadTimeout:  "invalid-duration",
			WriteTimeout: "10m",
			IdleTimeout:  "120s",
		},
		Frontend: config.FrontendConfig{
			FeedbackURL: "https://feedback.new.com",
		},
	}
	invalidBytes, _ := json.Marshal(invalidPayload)
	req, _ = http.NewRequest("PUT", "/admin/config/system", bytes.NewBuffer(invalidBytes))
	req.Header.Set("Content-Type", "application/json")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusBadRequest, resp.Code)
	assert.Contains(t, resp.Body.String(), "invalid read_timeout")
}

func TestList_DefaultPageSize50(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newTestUserStore(t, 120)
	h := &Handler{store: store}

	router := gin.New()
	router.GET("/admin/users", h.List)

	req, _ := http.NewRequest("GET", "/admin/users", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)

	var body struct {
		Data     []map[string]any `json:"data"`
		Total    int              `json:"total"`
		Page     int              `json:"page"`
		PageSize int              `json:"page_size"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	assert.Equal(t, 120, body.Total)
	assert.Equal(t, 1, body.Page)
	assert.Equal(t, 50, body.PageSize)
	assert.Len(t, body.Data, 50)
}

func TestList_PageSizeMax500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newTestUserStore(t, 520)
	h := &Handler{store: store}

	router := gin.New()
	router.GET("/admin/users", h.List)

	req, _ := http.NewRequest("GET", "/admin/users?page_size=9999", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)

	var body struct {
		Data     []map[string]any `json:"data"`
		PageSize int              `json:"page_size"`
		Total    int              `json:"total"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	assert.Equal(t, 500, body.PageSize)
	assert.Equal(t, 520, body.Total)
	assert.Len(t, body.Data, 500)
}

func TestExportByDates_IgnoresPaginationParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logs := []usage.AccessLog{
		{Timestamp: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), UserID: uuid.New(), Method: "GET", Path: "/a"},
		{Timestamp: time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC), UserID: uuid.New(), Method: "POST", Path: "/b"},
	}
	h := &Handler{usageService: &mockUsageService{logs: logs}}

	router := gin.New()
	router.GET("/admin/access-logs/export-by-dates", h.ExportAccessLogsByDates)

	req, _ := http.NewRequest(
		"GET",
		"/admin/access-logs/export-by-dates?start_date=2026-08-01&end_date=2026-08-01&page=9&page_size=1",
		nil,
	)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Header().Get("Content-Type"), "text/csv")

	lines := strings.Split(strings.TrimSpace(resp.Body.String()), "\n")
	assert.Len(t, lines, 3) // header + 2 rows
}

func TestExportByDates_RespectsExportMaxRows(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logs := []usage.AccessLog{
		{Timestamp: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), UserID: uuid.New()},
		{Timestamp: time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC), UserID: uuid.New()},
	}
	cm := config.NewManager(&config.Config{
		Logs: config.LogConfig{ExportMaxRows: 1},
	}, "")
	h := &Handler{
		usageService: &mockUsageService{logs: logs},
		cm:           cm,
	}

	router := gin.New()
	router.GET("/admin/access-logs/export-by-dates", h.ExportAccessLogsByDates)

	req, _ := http.NewRequest("GET", "/admin/access-logs/export-by-dates?start=2026-08-01&end=2026-08-01", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, resp.Code)
	assert.Contains(t, resp.Body.String(), "export exceeds maximum rows")
}

func newTestUserStore(t *testing.T, count int) *entity.UserStore {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE users (
			id TEXT PRIMARY KEY,
			email TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			name TEXT NOT NULL,
			role TEXT NOT NULL,
			department TEXT,
			quota_policy TEXT,
			quota_policies TEXT,
			auth_source TEXT DEFAULT 'local',
			enabled INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_login_at DATETIME
		);
	`)
	require.NoError(t, err)

	for i := 0; i < count; i++ {
		_, err = db.Exec(`
			INSERT INTO users (id, email, password_hash, name, role, department, quota_policy, quota_policies, auth_source, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			uuid.New().String(),
			"user"+strconv.Itoa(i)+"@example.com",
			"hash",
			"user-"+strconv.Itoa(i),
			"user",
			"dev",
			"default",
			`["default"]`,
			"local",
			1,
			time.Now().Add(time.Duration(-i)*time.Minute),
			time.Now().Add(time.Duration(-i)*time.Minute),
		)
		require.NoError(t, err)
	}

	return entity.NewUserStore(db)
}

type mockUsageService struct {
	logs []usage.AccessLog
}

func (m *mockUsageService) GetRecentAccess(userID uuid.UUID, limit int) []usage.AccessLog {
	return nil
}

func (m *mockUsageService) GetAllRecentAccess(limit int) []usage.AccessLog {
	return nil
}

func (m *mockUsageService) GetAccessLogsByDateRange(userID string, start, end time.Time, withPayload bool) []usage.AccessLog {
	return nil
}

func (m *mockUsageService) GetAllAccessLogsByDateRange(start, end time.Time, withPayload bool) []usage.AccessLog {
	return nil
}

func (m *mockUsageService) GetAccessLogsByDateRangeLimited(userID string, start, end time.Time, withPayload bool, limit int) []usage.AccessLog {
	return m.logs
}

func (m *mockUsageService) GetAllAccessLogsByDateRangeLimited(start, end time.Time, withPayload bool, limit int) []usage.AccessLog {
	return m.logs
}

func (m *mockUsageService) CleanupOldPayloads(retentionDays int) (int64, error) {
	return 0, nil
}

func (m *mockUsageService) GetAccessLogsByDates(dates []string, userID string, fn func(usage.AccessLog) error) error {
	return nil
}
