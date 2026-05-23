//go:build contract

package contract

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// pathRE 匹配 web/src/lib/api/*.ts 中以下形式的路径字面量:
//
//	api.get<T>(`/path/foo?...`, ...)
//	api.post<T>(`/path/bar`, body, ...)
//
// 捕获组 1 = method (lowercase),组 2 = path 直到 ? / ${ / 反引号。
// 不处理动态拼接路径 (那些 review 时人工看)。
//
// 多行写法同样能匹配:RE2 里 \s* 默认匹配换行符,所以
//
//	queryFn: () =>
//	  api.get<T>(
//	    `/path/foo`,
//	  )
//
// 中 `(\s*` 能跨越 `(\n    ` 这段空白,path 同样会被正确抽出。
var pathRE = regexp.MustCompile("api\\.(get|post|put|delete|patch)<[^>]*>\\(\\s*`(/[^`${?]+)")

type apiCall struct {
	Method string // upper case
	Path   string
}

func extractAPICallsFromTS(t *testing.T, root string) []apiCall {
	t.Helper()
	var calls []apiCall
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(p) != ".ts" {
			return err
		}
		body, err := os.ReadFile(p)
		require.NoError(t, err)
		for _, m := range pathRE.FindAllSubmatch(body, -1) {
			calls = append(calls, apiCall{
				Method: strings.ToUpper(string(m[1])),
				Path:   strings.TrimSpace(string(m[2])),
			})
		}
		return nil
	})
	require.NoError(t, err)
	return calls
}

// TestFrontendAPIPaths_NoNotFound: success path. 扫前端所有 api.{method} path,
// 按实际 method 发请求,断言不是 404。允许 401/403/400/4xx,只防 "后端没这条路由"。
func TestFrontendAPIPaths_NoNotFound(t *testing.T) {
	base := os.Getenv("AIGW_MASTER_URL")
	if base == "" {
		base = "http://localhost:8140"
	}
	token := os.Getenv("AIGW_ADMIN_TOKEN")
	require.NotEmpty(t, token, "set AIGW_ADMIN_TOKEN env (login + copy from localStorage)")

	repoRoot := os.Getenv("AIGW_REPO_ROOT")
	if repoRoot == "" {
		repoRoot = "../.." // test/contract/ → repo root
	}
	calls := extractAPICallsFromTS(t, filepath.Join(repoRoot, "web/src/lib/api"))
	require.NotEmpty(t, calls, "extracted 0 api calls from web/src/lib/api/*.ts — regex broken")

	seen := map[string]bool{}
	var skipped int
	for _, c := range calls {
		key := c.Method + " " + c.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		// 尾斜杠 = regex 在 ${...} 处截断的参数路径 (e.g. /admin/agent-routes/${id}
		// → /admin/agent-routes/); 后端实际是 /resource/:id, 不能直接 GET, 跳过。
		if strings.HasSuffix(c.Path, "/") {
			t.Logf("skip parametric path: %s %s (truncated at ${...})", c.Method, c.Path)
			skipped++
			continue
		}
		req, _ := http.NewRequest(c.Method, base+"/api"+c.Path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err, "call=%s %s", c.Method, c.Path)
		resp.Body.Close()
		require.NotEqual(t, http.StatusNotFound, resp.StatusCode,
			"frontend call %s %q returns 404 — backend route missing or path typo", c.Method, c.Path)
	}
	t.Logf("checked %d unique static paths, skipped %d parametric", len(seen)-skipped, skipped)
}

// failure case: regex 遇到完全动态拼接的路径 (整个第一段就是 ${var}) 应跳过,
// 防止把 `${baseUrl}/foo` 这种误抽成 path。
func TestFrontendAPIPaths_DynamicLeadingSegment_NotMatched(t *testing.T) {
	src := []byte("api.get<X>(`${baseUrl}/foo`, opts)")
	matches := pathRE.FindAllSubmatch(src, -1)
	require.Empty(t, matches, "纯动态前缀的路径不应被 regex 抽出")
}

// boundary: 路径包含 query string → regex 应只抽出问号前的部分;同时验证 method 捕获。
func TestFrontendAPIPaths_QueryStringStripped(t *testing.T) {
	src := []byte("api.post<X>(`/stats/dashboard?start=${a}&end=${b}`, opts)")
	matches := pathRE.FindAllSubmatch(src, -1)
	require.Len(t, matches, 1)
	require.Equal(t, "post", string(matches[0][1]), "method 应被捕获")
	require.Equal(t, "/stats/dashboard", string(matches[0][2]), "path 应在问号前截止")
}
