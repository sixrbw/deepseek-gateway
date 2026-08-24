package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

func New(dbPath string) (*DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// SQLite optimizations with WAL mode enabled
	db.SetMaxOpenConns(10) // Allow concurrent readers in WAL mode
	db.SetMaxIdleConns(5)

	// Enable foreign keys, WAL mode, and busy timeout
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode = WAL;"); err != nil {
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout = 5000;"); err != nil {
		return nil, fmt.Errorf("failed to set busy timeout: %w", err)
	}

	return &DB{db}, nil
}

func (db *DB) Migrate() error {
	// 内嵌的数据库 schema，无需外部 migrations 目录
	// 注意：模型配置、后端配置、配额策略 已迁移到 config.yaml
	// 保留的表：users, api_keys, quota_usage_daily（运行时数据）
	schema := `
-- 用户表
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    name TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'user',
    department TEXT,
    quota_policy TEXT DEFAULT 'default',
    auth_source TEXT DEFAULT 'local', -- 用户来源: local, sso
    enabled BOOLEAN DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_login_at DATETIME
);

-- API Key 表
CREATE TABLE IF NOT EXISTS api_keys (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    key_hash TEXT UNIQUE NOT NULL,
    key_prefix TEXT NOT NULL,
    enabled BOOLEAN DEFAULT 1,
    expires_at DATETIME,
    last_used_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    total_tokens_used INTEGER DEFAULT 0,
    plain_key TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- 配额使用统计表（运行时数据，保留在数据库中）
CREATE TABLE IF NOT EXISTS quota_usage_daily (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    date DATE NOT NULL,
    model_id TEXT NOT NULL,
    request_count INTEGER DEFAULT 0,
    input_tokens INTEGER DEFAULT 0,
    output_tokens INTEGER DEFAULT 0,
    UNIQUE(user_id, date, model_id),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- 创建索引
CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON api_keys(user_id);
CREATE INDEX IF NOT EXISTS idx_quota_usage_user_id ON quota_usage_daily(user_id);
CREATE INDEX IF NOT EXISTS idx_quota_usage_date ON quota_usage_daily(date);

-- 注意：以下表已弃用，配置数据现在存储在 config.yaml 中
-- 保留这些注释以便于理解数据库演进历史
-- DEPRECATED: models 表 -> 迁移到 config.yaml
-- DEPRECATED: backends 表 -> 迁移到 config.yaml
-- DEPRECATED: quota_policies 表 -> 迁移到 config.yaml
`

	_, err := db.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to execute schema: %w", err)
	}

	// 增量迁移脚本：为现有表添加 Token 统计字段，忽略重复列导致的错误
	migrations := []string{
		"ALTER TABLE api_keys ADD COLUMN total_tokens_used INTEGER DEFAULT 0;",
		"ALTER TABLE quota_usage_daily ADD COLUMN input_tokens INTEGER DEFAULT 0;",
		"ALTER TABLE quota_usage_daily ADD COLUMN output_tokens INTEGER DEFAULT 0;",
		"ALTER TABLE api_keys ADD COLUMN plain_key TEXT;",
		"ALTER TABLE users ADD COLUMN quota_policies TEXT DEFAULT '[]';",
	}

	for _, query := range migrations {
		_, _ = db.Exec(query) // 忽略错误，如果列已存在则会报错但安全忽略
	}

	// 注意：quota_policies 表已弃用，相关迁移代码已移除
	// 配置数据现在存储在 config.yaml 中

	return nil
}

