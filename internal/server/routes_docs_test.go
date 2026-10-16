package server

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// TestAPIRoutesMatchDocs 机检「代码路由 ↔ doc/api.md」一致性：
// 每条已注册路由都必须在文档中以「## <METHOD> /api/...」出现；文档中出现的
// /api/ 端点标题也必须都有对应路由（缺失或多余都失败）。
//
// 背景：工程基线要求「改了路由不改文档 = 未完成」，用机检代替肉眼比对。
func TestAPIRoutesMatchDocs(t *testing.T) {
	srv := New(t.TempDir(), Options{})
	code := map[string]bool{}
	for _, rt := range srv.apiRoutes() {
		code[rt.method+" "+rt.pattern] = true
	}

	_, thisFile, _, _ := runtime.Caller(0)
	docPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "doc", "api.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("读取 doc/api.md 失败: %v", err)
	}

	// 文档端点标题形如 "## GET /api/list?path=" 或 "### POST /api/auth/login"；
	// 提取方法 + 路径（去查询串）
	re := regexp.MustCompile(`(?m)^#{2,3}\s+(GET|POST)\s+(/api/[^\s?]*)`)
	doc := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(data), -1) {
		doc[m[1]+" "+m[2]] = true
	}

	for k := range code {
		if !doc[k] {
			t.Errorf("路由 %q 未在 doc/api.md 中登记", k)
		}
	}
	for k := range doc {
		if !code[k] {
			t.Errorf("doc/api.md 中的端点 %q 在代码中不存在（文档多余）", k)
		}
	}
}
