package endpoint

import (
	"net/url"
	"testing"
)

func TestGeminiPath(t *testing.T) {
	// 1. 模型映射、路径转义和原查询参数一起传入生成端点。
	path, err := GeminiPath("/custom/models/{model}:{action}?region=global", "models/gemini?key=not-a-key", StreamGenerateContent)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(path)
	if err != nil || u.Query().Get("region") != "global" || u.Query().Has("key") || u.Path != "/custom/models/gemini?key=not-a-key:streamGenerateContent" {
		t.Fatalf("path=%q err=%v", path, err)
	}
	// 2. 默认端点不强制流式格式，由调用方选择 SSE 或原生 JSON。
	path, err = GeminiPath("", "gemini-flash", GenerateContent)
	if err != nil || path != "/v1beta/models/gemini-flash:generateContent" {
		t.Fatalf("path=%q err=%v", path, err)
	}
	for _, tc := range []struct{ path, model, action string }{
		{"https://other.test/{model}", "gemini", GenerateContent},
		{"//other.test/{model}", "gemini", GenerateContent},
		{"", "../private", GenerateContent},
		{"", "gemini", "delete"},
	} {
		if _, err := GeminiPath(tc.path, tc.model, tc.action); err == nil {
			t.Errorf("unsafe endpoint accepted: %+v", tc)
		}
	}
}

func TestParseGeminiPath(t *testing.T) {
	// 1. 两种版本的生成路径共享模型与动作识别。
	for _, tc := range []struct {
		path, model string
		stream, ok  bool
	}{
		{"/v1beta/models/gemini:generateContent", "gemini", false, true},
		{"/v1/models/gemini:streamGenerateContent", "gemini", true, true},
		{"/v1/models", "", false, false},
		{"/v1beta/models/gemini:countTokens", "", false, false},
		{"/v1beta/models/a/b:generateContent", "", false, false},
	} {
		model, stream, ok := ParseGeminiPath(tc.path)
		if model != tc.model || stream != tc.stream || ok != tc.ok {
			t.Errorf("%s: got (%q,%v,%v)", tc.path, model, stream, ok)
		}
	}
}
