package entity

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Role 用户角色
type Role string

const (
	RoleAdmin   Role = "admin"
	RoleManager Role = "manager"
	RoleUser    Role = "user"
)

// StringArray 用于 JSON 字符串数组类型
type StringArray []string

func (a StringArray) Value() (driver.Value, error) {
	if a == nil {
		return nil, nil
	}
	return json.Marshal(a)
}

func (a *StringArray) Scan(value interface{}) error {
	if value == nil {
		*a = nil
		return nil
	}

	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, a)
	case string:
		return json.Unmarshal([]byte(v), a)
	default:
		return errors.New("cannot scan type into StringArray")
	}
}

type User struct {
	ID            uuid.UUID   `json:"id"`
	Email         string      `json:"email"`
	PasswordHash  string      `json:"-"`
	Name          string      `json:"name"`
	Role          Role        `json:"role"`
	Department    string      `json:"department"`
	QuotaPolicy   string      `json:"quota_policy"`
	QuotaPolicies StringArray `json:"quota_policies"`
	AuthSource    string      `json:"auth_source"` // local, sso
	Enabled       bool        `json:"enabled"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
	LastLoginAt   *time.Time  `json:"last_login_at,omitempty"`
}

type UserCreateRequest struct {
	Email         string   `json:"email" binding:"required,email"`
	Password      string   `json:"password" binding:"required,min=6"`
	Name          string   `json:"name" binding:"required"`
	Role          Role     `json:"role"`
	Department    string   `json:"department"`
	QuotaPolicy   string   `json:"quota_policy"`
	QuotaPolicies []string `json:"quota_policies"`
}

type UserUpdateRequest struct {
	Name          string   `json:"name"`
	Role          Role     `json:"role"`
	Department    string   `json:"department"`
	QuotaPolicy   string   `json:"quota_policy"`
	QuotaPolicies []string `json:"quota_policies"`
	Enabled       *bool    `json:"enabled"`
}

type UserResponse struct {
	ID            uuid.UUID  `json:"id"`
	Email         string     `json:"email"`
	Name          string     `json:"name"`
	Role          Role       `json:"role"`
	Department    string     `json:"department"`
	QuotaPolicy   string     `json:"quota_policy"`
	QuotaPolicies []string   `json:"quota_policies"`
	AuthSource    string     `json:"auth_source"`
	Enabled       bool       `json:"enabled"`
	CreatedAt     time.Time  `json:"created_at"`
	LastLoginAt   *time.Time `json:"last_login_at,omitempty"`
}

func (u *User) ToResponse() UserResponse {
	return UserResponse{
		ID:            u.ID,
		Email:         u.Email,
		Name:          u.Name,
		Role:          u.Role,
		Department:    u.Department,
		QuotaPolicy:   u.QuotaPolicy,
		QuotaPolicies: []string(u.QuotaPolicies),
		AuthSource:    u.AuthSource,
		Enabled:       u.Enabled,
		CreatedAt:     u.CreatedAt,
		LastLoginAt:   u.LastLoginAt,
	}
}

// UserStore 用户数据访问层
type UserStore struct {
	db *sql.DB
}

func NewUserStore(db *sql.DB) *UserStore {
	return &UserStore{db: db}
}

// scanner interface defines the common Scan method for both sql.Row and sql.Rows
type scanner interface {
	Scan(dest ...any) error
}

func scanUser(s scanner) (*User, error) {
	user := &User{}
	err := s.Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Role,
		&user.Department, &user.QuotaPolicy, &user.QuotaPolicies, &user.AuthSource, &user.Enabled,
		&user.CreatedAt, &user.UpdatedAt, &user.LastLoginAt,
	)
	return user, err
}

func (s *UserStore) Create(user *User) error {
	user.ID = uuid.New()
	if user.AuthSource == "" {
		user.AuthSource = "local"
	}
	query := `
		INSERT INTO users (id, email, password_hash, name, role, department, quota_policy, quota_policies, auth_source, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING created_at, updated_at`

	return s.db.QueryRow(query,
		user.ID.String(), user.Email, user.PasswordHash, user.Name, user.Role, user.Department,
		user.QuotaPolicy, user.QuotaPolicies, user.AuthSource, user.Enabled,
	).Scan(&user.CreatedAt, &user.UpdatedAt)
}

func (s *UserStore) GetByID(id uuid.UUID) (*User, error) {
	query := `
		SELECT id, email, password_hash, name, role, department, quota_policy, quota_policies,
		       auth_source, enabled, created_at, updated_at, last_login_at
		FROM users WHERE id = ?`

	user, err := scanUser(s.db.QueryRow(query, id.String()))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *UserStore) GetByEmail(email string) (*User, error) {
	query := `
		SELECT id, email, password_hash, name, role, department, quota_policy, quota_policies,
		       auth_source, enabled, created_at, updated_at, last_login_at
		FROM users WHERE email = ? AND enabled = 1`

	user, err := scanUser(s.db.QueryRow(query, email))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return user, nil
}

// GetByEmailAll 查询用户（不过滤 enabled 状态），用于注册时检测邮箱是否已被占用
func (s *UserStore) GetByEmailAll(email string) (*User, error) {
	query := `
		SELECT id, email, password_hash, name, role, department, quota_policy, quota_policies,
		       auth_source, enabled, created_at, updated_at, last_login_at
		FROM users WHERE email = ?`

	user, err := scanUser(s.db.QueryRow(query, email))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *UserStore) List(limit, offset int) ([]*User, error) {
	query := `
		SELECT id, email, password_hash, name, role, department, quota_policy, quota_policies,
		       auth_source, enabled, created_at, updated_at, last_login_at
		FROM users ORDER BY created_at DESC LIMIT ? OFFSET ?`

	rows, err := s.db.Query(query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

// validSortColumns 允许排序的列白名单（防 SQL 注入）
var validSortColumns = map[string]string{
	"email":         "email",
	"quota_policy":  "quota_policy",
	"enabled":       "enabled",
	"last_login_at": "last_login_at",
	"created_at":    "created_at",
	"name":          "name",
	"role":          "role",
}

// ListPaginated 分页且可排序的用户列表
func (s *UserStore) ListPaginated(limit, offset int, sortBy, sortOrder string) ([]*User, error) {
	// 安全的排序列
	col, ok := validSortColumns[sortBy]
	if !ok {
		col = "created_at"
	}
	order := "DESC"
	if sortOrder == "asc" {
		order = "ASC"
	}

	query := fmt.Sprintf(`
		SELECT id, email, password_hash, name, role, department, quota_policy, quota_policies,
		       auth_source, enabled, created_at, updated_at, last_login_at
		FROM users ORDER BY %s %s LIMIT ? OFFSET ?`, col, order)

	rows, err := s.db.Query(query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

// Count 返回用户总数
func (s *UserStore) Count() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	return count, err
}

func (s *UserStore) Update(user *User) error {
	query := `
		UPDATE users SET
			email = ?, name = ?, role = ?, department = ?,
			quota_policy = ?, quota_policies = ?, auth_source = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`

	_, err := s.db.Exec(query,
		user.Email, user.Name, user.Role, user.Department,
		user.QuotaPolicy, user.QuotaPolicies, user.AuthSource, user.Enabled, user.ID.String(),
	)
	return err
}

func (s *UserStore) UpdateLastLogin(id uuid.UUID) error {
	_, err := s.db.Exec("UPDATE users SET last_login_at = CURRENT_TIMESTAMP WHERE id = ?", id.String())
	return err
}

func (s *UserStore) Delete(id uuid.UUID) error {
	_, err := s.db.Exec("DELETE FROM users WHERE id = ?", id.String())
	return err
}

func (s *UserStore) UpdatePassword(userID uuid.UUID, passwordHash string) error {
	_, err := s.db.Exec(
		"UPDATE users SET password_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		passwordHash, userID.String(),
	)
	return err
}
