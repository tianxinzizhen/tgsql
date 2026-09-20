package test

import (
	"context"
	"fmt"
	"testing"
)

// TestDaoCRUD 完整 CRUD 链路测试
func TestDaoCRUD(t *testing.T) {
	dao := NewStudentDao(t)
	ctx := context.Background()

	t.Run("insert", func(t *testing.T) {
		res, err := dao.Insert(ctx, &Student{UserName: "Alice", Age: 20})
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
		id, _ := res.LastInsertId()
		t.Logf("Insert Alice: id=%d", id)
	})

	t.Run("get_by_id", func(t *testing.T) {
		s, err := dao.GetByID(ctx, 1)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if s.Id != 1 || s.UserName != "Alice" {
			t.Errorf("Expected Alice id=1, got %+v", s)
		}
		t.Logf("GetByID(1): %+v", s)
	})

	t.Run("get_by_ids", func(t *testing.T) {
		list, err := dao.GetByIds(ctx, []int64{1, 2, 3})
		if err != nil {
			t.Fatalf("GetByIds: %v", err)
		}
		t.Logf("GetByIds([1,2,3]): %d rows", len(list))
		if len(list) != 3 {
			t.Errorf("Expected 3 rows, got %d", len(list))
		}
	})

	t.Run("update", func(t *testing.T) {
		err := dao.Update(ctx, &Student{Id: 1, UserName: "Alice_Updated", Age: 21})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		s, _ := dao.GetByID(ctx, 1)
		if s.UserName != "Alice_Updated" || s.Age != 21 {
			t.Errorf("Expected Alice_Updated/21, got %s/%d", s.UserName, s.Age)
		}
		t.Logf("Update id=1 OK: %+v", s)

		// 恢复原始值
		dao.Update(ctx, &Student{Id: 1, UserName: "Alice", Age: 20})
	})

	t.Run("delete", func(t *testing.T) {
		res, _ := dao.Insert(ctx, &Student{UserName: "ToDelete", Age: 99})
		id, _ := res.LastInsertId()
		t.Logf("Insert ToDelete: id=%d", id)

		err := dao.Delete(ctx, id)
		if err != nil {
			t.Fatalf("Delete: %v", err)
		}
		t.Logf("Delete id=%d OK", id)

		s, err := dao.GetByID(ctx, id)
		if err == nil && s != nil {
			t.Errorf("Expected nil after delete, got %+v", s)
		}
	})
}

// TestDaoOptionalCondition 可选条件列表查询
func TestDaoOptionalCondition(t *testing.T) {
	dao := NewStudentDao(t)
	ctx := context.Background()

	t.Run("only_age", func(t *testing.T) {
		list, err := dao.List(ctx, 25, "")
		if err != nil {
			t.Fatalf("List(age>25): %v", err)
		}
		t.Logf("List(age>25): %d rows", len(list))
		for _, s := range list {
			fmt.Printf("  id=%d age=%d name=%s\n", s.Id, s.Age, s.UserName)
		}
	})

	t.Run("only_name", func(t *testing.T) {
		list, err := dao.List(ctx, 0, "a")
		if err != nil {
			t.Fatalf("List(name like 'a'): %v", err)
		}
		t.Logf("List(name like 'a'): %d rows", len(list))
		for _, s := range list {
			fmt.Printf("  id=%d age=%d name=%s\n", s.Id, s.Age, s.UserName)
		}
	})

	t.Run("both", func(t *testing.T) {
		list, err := dao.List(ctx, 25, "a")
		if err != nil {
			t.Fatalf("List(both): %v", err)
		}
		t.Logf("List(age>25, name like 'a'): %d rows", len(list))
		for _, s := range list {
			fmt.Printf("  id=%d age=%d name=%s\n", s.Id, s.Age, s.UserName)
		}
	})

	t.Run("none", func(t *testing.T) {
		list, err := dao.List(ctx, 0, "")
		if err != nil {
			t.Fatalf("List(none): %v", err)
		}
		t.Logf("List(none): %d rows", len(list))
	})
}
