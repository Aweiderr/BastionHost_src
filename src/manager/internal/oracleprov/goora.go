package oracleprov

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	// 导入即注册 database/sql 驱动名 "oracle"。纯 Go，不需要 Instant Client。
	go_ora "github.com/sijms/go-ora/v2"
)

const connectTimeout = 20 * time.Second

// Open 用 go-ora 连接目标库。服务名优先于 SID，与协议网关改写描述符的规则相同。
func Open(ctx context.Context, target Target, admin Admin) (Session, error) {
	dsn, err := BuildDSN(target, admin)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("oracle", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	db.SetConnMaxLifetime(time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &dbSession{db: db}, nil
}

// BuildDSN 组装 go-ora 连接串。口令只放在 URL 用户信息里，调用方不得把结果写入日志。
func BuildDSN(target Target, admin Admin) (string, error) {
	host := strings.TrimSpace(target.Host)
	if host == "" {
		return "", errString("Oracle 地址为空")
	}
	port := target.Port
	if port == 0 {
		port = 1521
	}
	if port < 1 || port > 65535 {
		return "", errString("Oracle 端口非法")
	}
	service := strings.TrimSpace(target.ServiceName)
	sid := strings.TrimSpace(target.SID)
	if service == "" && sid == "" {
		return "", errString("Oracle 服务名和 SID 都为空")
	}
	user := strings.TrimSpace(admin.Username)
	if user == "" || admin.Password == "" {
		return "", errString("Oracle 管理员账号或口令为空")
	}
	opts := map[string]string{
		"CONNECTION TIMEOUT": "20",
		"TIMEOUT":            "20",
	}
	if admin.SysDBA || strings.EqualFold(user, "SYS") {
		opts["DBA PRIVILEGE"] = "SYSDBA"
	}
	serviceArg := service
	if service == "" {
		opts["SID"] = sid
	}
	return go_ora.BuildUrl(host, port, serviceArg, user, admin.Password, opts), nil
}

type dbSession struct {
	db *sql.DB
}

func (s *dbSession) UserExists(ctx context.Context, username string) (bool, error) {
	if err := ValidateUsername(username); err != nil {
		return false, err
	}
	// 用户名已限制为标识符字符，不能把引号带进来。
	q := "SELECT username FROM all_users WHERE username = '" + CanonicalUsername(username) + "'"
	var got string
	err := s.db.QueryRowContext(ctx, q).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return got != "", nil
}

func (s *dbSession) Exec(ctx context.Context, statement string) error {
	_, err := s.db.ExecContext(ctx, statement)
	return err
}

func (s *dbSession) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}
