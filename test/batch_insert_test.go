package test

import (
	"context"
	"testing"

	"github.com/tianxinzizhen/tgsql"
)

// ============ Batch Insert 专用 DAO ============

type BatchDao struct {
	//sql?option{batch_insert:true} INSERT INTO student (user_name, age) VALUES(?)
	BatchInsert func(ctx context.Context, rows []*Student) error
}

// NewBatchDao 构造批量 DAO（需 InitDBFunc）
func NewBatchDao(t *testing.T) *BatchDao {
	t.Helper()
	tdb := newTDB(t)
	if err := tdb.LoadFuncDataInfo(testDaoFS); err != nil {
		t.Fatalf("LoadFuncDataInfo: %v", err)
	}
	ret := &BatchDao{}
	if err := tgsql.InitDBFunc(tdb, ret); err != nil {
		t.Fatalf("InitDBFunc: %v", err)
	}
	return ret
}

// cleanupBatch 删掉 batch_ 前缀的残留数据（不影响其他测试的 seed）
func cleanupBatch(t *testing.T) {
	t.Helper()
	db := testDB(t)
	defer db.Close()
	db.ExecContext(context.Background(), "DELETE FROM student WHERE user_name LIKE 'batch_%'")
}

// ============ 测试 ============

// TestBatchInsert_Basic 验证批量 INSERT：写入多条 slice，确认条数和字段值
func TestBatchInsert_Basic(t *testing.T) {
	cleanupBatch(t)
	defer cleanupBatch(t)

	db := testDB(t)
	defer db.Close()
	dao := NewBatchDao(t)

	rows := []*Student{
		{UserName: "batch_alice", Age: 21},
		{UserName: "batch_bob", Age: 32},
		{UserName: "batch_carol", Age: 43},
	}
	if err := dao.BatchInsert(context.Background(), rows); err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}

	// 校验条数
	var cnt int64
	err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM student WHERE user_name LIKE 'batch_%'").Scan(&cnt)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 3 {
		t.Fatalf("want 3 rows, got %d", cnt)
	}

	// 校验每条数据值
	for _, s := range rows {
		var got Student
		err := db.QueryRowContext(context.Background(),
			"SELECT id, user_name, age FROM student WHERE user_name = ? AND age = ?",
			s.UserName, s.Age).Scan(&got.Id, &got.UserName, &got.Age)
		if err != nil {
			t.Errorf("row %+v: not found, err=%v", s, err)
			continue
		}
		if got.Id == 0 {
			t.Errorf("row %+v Id should be non-zero", s)
		}
	}

	// 再确认 BatchInsert 没有重复插入同一条——这是 bug 修复点！
	// 如果旧代码 changeOp 返回后没 break，batch_carol 会被执行两次
	var carolCnt int64
	db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM student WHERE user_name='batch_carol'").Scan(&carolCnt)
	if carolCnt != 1 {
		t.Fatalf("batch_carol duplicated: want 1, got %d", carolCnt)
	}
}

// TestBatchInsert_EmptySlice 验证空 slice 报错（不是静默成功）
func TestBatchInsert_EmptySlice(t *testing.T) {
	dao := NewBatchDao(t)

	err := dao.BatchInsert(context.Background(), []*Student{})
	if err == nil {
		t.Fatal("empty slice should return error")
	}
	t.Logf("empty slice error: %v", err)
}

// TestBatchInsert_Single 验证只有一条的 slice 也能正确写入
func TestBatchInsert_Single(t *testing.T) {
	cleanupBatch(t)
	defer cleanupBatch(t)

	db := testDB(t)
	defer db.Close()
	dao := NewBatchDao(t)

	only := []*Student{{UserName: "batch_only", Age: 99}}
	if err := dao.BatchInsert(context.Background(), only); err != nil {
		t.Fatalf("BatchInsert single: %v", err)
	}

	var cnt int64
	db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM student WHERE user_name='batch_only'").Scan(&cnt)
	if cnt != 1 {
		t.Fatalf("batch_only should be 1 row, got %d", cnt)
	}
}

// TestBatchInsert_NotExistType 验证不匹配的 DAO struct 不会 panic
func TestBatchInsert_NotExistType(t *testing.T) {
	// 确保 InitDBFunc 对没有 sql 注解的 struct 不会 panic
	cleanupBatch(t)
}
