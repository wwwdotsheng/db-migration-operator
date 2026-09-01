package test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

func TestDBConn(t *testing.T) {
	dsn := "root:root@tcp(mysql.example.com:30306)/testdb?timeout=5s"
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		fmt.Println("配置错误：", err)
		return
	}
	defer db.Close()

	err = db.Ping()
	if err != nil {
		fmt.Println("连接数据库失败 ", err)
	}

	fmt.Println("连接 MySQL 成功！")

	ctx := context.Background()
	conn, err := db.Conn(ctx)
	defer conn.Close()
	if err != nil {
		fmt.Println("获取连接出现错误：", err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		fmt.Println("创建事务出现错误：", err)
	}
	_, err = tx.Exec("alter table test_data add `no` varchar(20) not null")
	if err != nil {
		fmt.Println("执行Sql语句出现错误", err)
	}

	err = tx.Commit()
	if err != nil {
		fmt.Println("提交事务出现错误", err)
	}

	fmt.Println("运行成功！！！")
}
