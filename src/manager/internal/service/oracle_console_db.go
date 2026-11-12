package service

import (
	"context"
	"database/sql"
	"time"

	"bastion.local/manager/internal/oracleprov"
)

// openOracleConn 用托管账号拨到资产库。
//
// 驱动由 oracleprov 引入 go-ora 时注册。调用方负责丢掉口令，这里也不返回 DSN。
func openOracleConn(ctx context.Context, target oracleprov.Target, username, password string) (OracleConn, error) {
	dsn, err := oracleprov.BuildDSN(target, oracleprov.Admin{Username: username, Password: password})
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("oracle", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &oracleDB{db: db}, nil
}

type oracleDB struct {
	db *sql.DB
}

func (o *oracleDB) Query(ctx context.Context, statement string, maxRows int, args ...any) (OracleTable, error) {
	if maxRows <= 0 || maxRows > maxOracleMetaRows {
		maxRows = maxOracleMetaRows
	}
	rows, err := o.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return OracleTable{}, err
	}
	defer rows.Close()
	all, err := rows.Columns()
	if err != nil {
		return OracleTable{}, err
	}
	truncated := false
	width := len(all)
	if width > maxOracleCols {
		width = maxOracleCols
		truncated = true
	}
	table := OracleTable{Columns: append([]string(nil), all[:width]...)}
	for rows.Next() {
		if len(table.Rows) >= maxRows {
			truncated = true
			break
		}
		raw := make([]any, len(all))
		dest := make([]any, len(all))
		for i := range raw {
			dest[i] = &raw[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return OracleTable{}, err
		}
		line := make([]*string, width)
		for i := 0; i < width; i++ {
			line[i] = formatOracleCell(raw[i])
		}
		table.Rows = append(table.Rows, line)
	}
	if err := rows.Err(); err != nil {
		return OracleTable{}, err
	}
	table.Truncated = truncated
	return table, nil
}

func (o *oracleDB) Exec(ctx context.Context, statement string) (int64, error) {
	res, err := o.db.ExecContext(ctx, statement)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}

func (o *oracleDB) Close() error {
	if o == nil || o.db == nil {
		return nil
	}
	return o.db.Close()
}
