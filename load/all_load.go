package load

import (
	"embed"
	"runtime"
	"strings"
)

// getCurrentPackageName 通过 runtime.Caller 获取调用者所在包的完整导入路径。
// 调用栈深度 3 假设：
//
//	depth 0: runtime.Caller
//	depth 1: getCurrentPackageName
//	depth 2: (*LoadFuncDataInfo).Load*
//	depth 3: (*TgenSql).Load*（或任何直接调用者）
//
// 如需更稳健，可在 LoadFuncDataInfo / LoadFuncDataInfoBytes / LoadFuncDataInfoString 中
// 显式传入 pkgPath 参数（留空时回退到此函数）。
func getCurrentPackageName() string {
	pc, _, _, ok := runtime.Caller(3)
	if !ok {
		return ""
	}
	funcInfo := runtime.FuncForPC(pc)
	if funcInfo == nil {
		return ""
	}
	return extractPkgPath(funcInfo.Name())
}

// extractPkgPath 从 runtime.Func.Name() 返回的完整函数名中提取包路径。
// 处理三种格式：
//
//	"github.com/pkg.Func"            → "github.com/pkg"
//	"github.com/pkg.(*Type).Method"  → "github.com/pkg"
//	"github.com/pkg.Type.Method"     → "github.com/pkg"
func extractPkgPath(fullName string) string {
	// 先去掉最后的 .FuncName / .Method 部分
	if i := strings.LastIndex(fullName, "."); i != -1 {
		fullName = fullName[:i]
	}
	// 现在 fullName 可能是:
	//   "github.com/pkg"           (free func 已处理完)
	//   "github.com/pkg.(*Type)"   (pointer receiver)
	//   "github.com/pkg.Type"      (value receiver)
	if j := strings.LastIndex(fullName, "."); j != -1 {
		rest := fullName[j+1:]
		// receiver 以 "(" 或大写字母开头
		if strings.HasPrefix(rest, "(") || (len(rest) > 0 && rest[0] >= 'A' && rest[0] <= 'Z') {
			fullName = fullName[:j]
		}
	}
	return fullName
}

type LoadFuncDataInfo struct {
	sqlDataInfos map[string][]*SqlDataInfo
}

func NewLoadFuncDataInfo() *LoadFuncDataInfo {
	return &LoadFuncDataInfo{
		sqlDataInfos: make(map[string][]*SqlDataInfo),
	}
}

func (lfi *LoadFuncDataInfo) LoadFuncDataInfoBytes(sqlComments []byte) error {
	pkgName := getCurrentPackageName()
	return lfi.loadAndAppend(pkgName, loadCommentBytes, sqlComments)
}

func (lfi *LoadFuncDataInfo) LoadFuncDataInfoString(sqlComments string) error {
	pkgName := getCurrentPackageName()
	return lfi.loadAndAppend(pkgName, loadCommentBytes, []byte(sqlComments))
}

func (lfi *LoadFuncDataInfo) LoadFuncDataInfo(sqlDir embed.FS) error {
	pkgName := getCurrentPackageName()
	// 遍历 embed.FS 中的 .go 文件（支持一层子目录）
	entries, err := sqlDir.ReadDir(".")
	if err != nil {
		return err
	}
	var goFiles []string
	for _, e := range entries {
		if e.IsDir() {
			sub, err := sqlDir.ReadDir(e.Name())
			if err != nil {
				return err
			}
			for _, subE := range sub {
				if !subE.IsDir() && strings.HasSuffix(subE.Name(), ".go") {
					goFiles = append(goFiles, e.Name()+"/"+subE.Name())
				}
			}
		} else if strings.HasSuffix(e.Name(), ".go") {
			goFiles = append(goFiles, e.Name())
		}
	}
	for _, path := range goFiles {
		bytes, err := sqlDir.ReadFile(path)
		if err != nil {
			return err
		}
		if err := lfi.loadAndAppend(pkgName, loadCommentBytes, bytes); err != nil {
			return err
		}
	}
	return nil
}

// commentLoader 是 loadCommentBytes 或未来可能的其他解析器的函数签名
type commentLoader func(pkg string, bytes []byte) ([]*SqlDataInfo, error)

// loadAndAppend 解析 bytes 中的 SQL 注解并追加到 lfi.sqlDataInfos
func (lfi *LoadFuncDataInfo) loadAndAppend(pkgName string, loader commentLoader, data []byte) error {
	infos, err := loader(pkgName, data)
	if err != nil {
		return err
	}
	for _, v := range infos {
		lfi.sqlDataInfos[v.TypeName] = append(lfi.sqlDataInfos[v.TypeName], v)
	}
	return nil
}

func (lfi *LoadFuncDataInfo) GetSqlDataInfo(typeName string) []*SqlDataInfo {
	return lfi.sqlDataInfos[typeName]
}
