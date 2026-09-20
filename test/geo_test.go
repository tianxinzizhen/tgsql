package test

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/tianxinzizhen/tgsql"
	"github.com/tianxinzizhen/tgsql/sqlval"
)

// =================== GeoPoint 类型定义 ===================

// GeoPoint 表示地理坐标（经度 Lng，纬度 Lat）
// MySQL POINT 字段采用 WKB 格式存储：
//
//	4 字节 SRID(=0) + 1 字节 byte order(01=LE) + 4 字节 type(01000000) + 8 字节 Lng + 8 字节 Lat
type GeoPoint struct {
	Lng float64
	Lat float64
}

// geoToWKB 把 GeoPoint 编码成 MySQL POINT 列的 WKB 字节（25 字节）
func geoToWKB(p GeoPoint) []byte {
	buf := make([]byte, 25)
	// SRID = 0（MySQL 约定前 4 字节存 SRID）
	binary.LittleEndian.PutUint32(buf[0:4], 0)
	// byte order = little endian
	buf[4] = 0x01
	// geometry type = 1 (Point)
	binary.LittleEndian.PutUint32(buf[5:9], 1)
	// X = Lng, Y = Lat
	binary.LittleEndian.PutUint64(buf[9:17], math.Float64bits(p.Lng))
	binary.LittleEndian.PutUint64(buf[17:25], math.Float64bits(p.Lat))
	return buf
}

// wkbToGeo 把 MySQL POINT 的 WKB 字节解码为 GeoPoint
func wkbToGeo(b []byte) (GeoPoint, error) {
	if len(b) < 25 {
		return GeoPoint{}, fmt.Errorf("wkb too short: %d bytes", len(b))
	}
	// 跳过 4 字节 SRID + 1 字节 byte order + 4 字节 type
	lng := math.Float64frombits(binary.LittleEndian.Uint64(b[9:17]))
	lat := math.Float64frombits(binary.LittleEndian.Uint64(b[17:25]))
	return GeoPoint{Lng: lng, Lat: lat}, nil
}

// =================== 参数转换器 sqlval.Convert[GeoPoint] ===================

type geoConverter struct{}

func (g *geoConverter) ConvertValue(v GeoPoint) (any, error) {
	return geoToWKB(v), nil
}
func (g *geoConverter) ConvertValuePtr(v *GeoPoint) (any, error) {
	if v == nil {
		return nil, nil
	}
	return geoToWKB(*v), nil
}

// =================== 结果扫描器 sqlval.ScanVal[GeoPoint] ===================

// geoScanner 同时实现 sql.Scanner + sqlval.ScanVal[GeoPoint]
type geoScanner struct {
	p GeoPoint
}

// Scan 实现 sql.Scanner（value receiver，sql.DB 扫描时通过 *geoScanner 调用）
func (g *geoScanner) Scan(dest any) error {
	switch v := dest.(type) {
	case []byte:
		if len(v) == 0 {
			return errors.New("empty geo wkb")
		}
		p, err := wkbToGeo(v)
		if err != nil {
			return err
		}
		g.p = p
		return nil
	case string:
		// 允许传入十六进制字符串，方便调试
		if len(v) == 0 {
			return errors.New("empty geo string")
		}
		return errors.New("string wkt not supported, use ST_AsText + manual parse")
	}
	return fmt.Errorf("unsupported scan type %T", dest)
}

// ScanValue / ScanValuePtr 使用 value receiver，
// 因为 sqlval.result_scan 里 v 是值类型 geoScanner 而非 *geoScanner
func (g geoScanner) ScanValue() (GeoPoint, error)     { return g.p, nil }
func (g geoScanner) ScanValuePtr() (*GeoPoint, error) { return &g.p, nil }

// =================== Geo DAO（InitDBFunc 模式） ===================

type GeoDao struct {
	//sql INSERT INTO geo_points (name, location) VALUES({.Name}, {.Location})
	Insert func(ctx context.Context, name string, location GeoPoint) (sql.Result, error)

	//sql SELECT * FROM geo_points WHERE id = {.id}
	GetByID func(ctx context.Context, id int64) (*GeoPointRow, error)

	//sql SELECT id, name, location FROM geo_points WHERE 1=1 [AND name = {.name}]
	List func(ctx context.Context, name string) ([]*GeoPointRow, error)

	//sql DELETE FROM geo_points WHERE id = {.id}
	Delete func(ctx context.Context, id int64) error
}

// GeoPointRow 对应 geo_points 表的一行
type GeoPointRow struct {
	Id       int64
	Name     string
	Location GeoPoint // sqlval.RegisterScanVal 注册后自动识别
}

// NewGeoDao 工厂函数 — 注册转换器/扫描器 + 构造 DAO
func NewGeoDao(t *testing.T) *GeoDao {
	t.Helper()

	// 1. 注册参数转换器（GeoPoint → WKB []byte）
	if err := sqlval.RegisterConvert[GeoPoint](&geoConverter{}); err != nil {
		t.Fatalf("RegisterConvert: %v", err)
	}
	// 2. 注册结果扫描器（WKB []byte → GeoPoint）
	if err := sqlval.RegisterScanVal[GeoPoint](&geoScanner{}); err != nil {
		t.Fatalf("RegisterScanVal: %v", err)
	}

	tdb := newTDB(t)
	if err := tdb.LoadFuncDataInfo(testDaoFS); err != nil {
		t.Fatalf("LoadFuncDataInfo: %v", err)
	}

	ret := &GeoDao{}
	if err := tgsql.InitDBFunc(tdb, ret); err != nil {
		t.Fatalf("InitDBFunc: %v", err)
	}
	return ret
}

// =================== 测试辅助 ===================

func setupGeoTable(t *testing.T, db *sql.DB) {
	t.Helper()
	schema := `
DROP TABLE IF EXISTS geo_points;
CREATE TABLE geo_points (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  name VARCHAR(100) NOT NULL,
  location POINT NOT NULL,
  create_time DATETIME DEFAULT CURRENT_TIMESTAMP
)`
	for _, stmt := range splitSchema(schema) {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("setup schema %q: %v", stmt, err)
		}
	}
}

// =================== 测试用例 ===================

func TestGeoPoint_CRUD(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	setupGeoTable(t, db)

	dao := NewGeoDao(t)

	// ---- Insert ----
	beijing := GeoPoint{Lng: 116.4074, Lat: 39.9042}
	res, err := dao.Insert(context.Background(), "beijing", beijing)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	id, _ := res.LastInsertId()
	t.Logf("inserted beijing id=%d", id)

	// ---- GetByID — 验证扫描器把 WKB []byte 转回 GeoPoint ----
	row, err := dao.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row.Name != "beijing" {
		t.Fatalf("name want beijing got %s", row.Name)
	}
	if row.Location.Lng != beijing.Lng || row.Location.Lat != beijing.Lat {
		t.Fatalf("location mismatch: want %+v got %+v", beijing, row.Location)
	}
	t.Logf("get back: %+v", row)

	// 用原生 SQL 校验实际存入的值
	var rawWKB []byte
	var wkt sql.NullString
	err = db.QueryRowContext(context.Background(),
		"SELECT location, ST_AsText(location) FROM geo_points WHERE id=?", id).Scan(&rawWKB, &wkt)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("raw WKB hex=%x WKT=%s", rawWKB, wkt.String)
	if wkt.String != "POINT(116.4074 39.9042)" {
		t.Fatalf("unexpected WKT: %s", wkt.String)
	}

	// ---- List — 可选条件 ----
	shanghai := GeoPoint{Lng: 121.4737, Lat: 31.2304}
	dao.Insert(context.Background(), "shanghai", shanghai)

	all, err := dao.List(context.Background(), "") // name="" 不加过滤
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2 rows got %d", len(all))
	}

	filtered, err := dao.List(context.Background(), "beijing")
	if err != nil {
		t.Fatalf("List by name: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Name != "beijing" {
		t.Fatalf("filter failed: %+v", filtered)
	}

	// ---- Delete ----
	if err := dao.Delete(context.Background(), id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

// splitSchema 把分号分隔的 SQL 拆成多条（简单实现，不处理字符串内的分号）
func splitSchema(schema string) []string {
	var stmts []string
	start := 0
	for i := 0; i < len(schema); i++ {
		if schema[i] == ';' {
			stmts = append(stmts, schema[start:i])
			start = i + 1
		}
	}
	if start < len(schema) {
		stmts = append(stmts, schema[start:])
	}
	return stmts
}
