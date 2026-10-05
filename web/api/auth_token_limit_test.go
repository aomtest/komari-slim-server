package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newBodyTestContext(method, target string, body []byte) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

// extractClientToken 对非 GET 请求会去 body 里找 token。这条路径匿名请求也会走到,
// 此前是无上限 io.ReadAll,超大 POST 可以直接打爆内存。
func TestExtractClientToken_QueryTakesPrecedence(t *testing.T) {
	c := newBodyTestContext(http.MethodPost, "/api/clients/v2/rpc?token=from-query", nil)
	if got := extractClientToken(c); got != "from-query" {
		t.Fatalf("want from-query, got %q", got)
	}
}

func TestExtractClientToken_AuthorizationQuery(t *testing.T) {
	// rpc2 约定:agent 经 ?Authorization=<token> 传入 client token。
	c := newBodyTestContext(http.MethodPost, "/api/rpc2?Authorization=from-auth-query", nil)
	if got := extractClientToken(c); got != "from-auth-query" {
		t.Fatalf("want from-auth-query, got %q", got)
	}
}

func TestExtractClientToken_BodyTokenWithinLimit(t *testing.T) {
	body, err := json.Marshal(map[string]string{"token": "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	c := newBodyTestContext(http.MethodPost, "/api/clients/v2/rpc", body)
	if got := extractClientToken(c); got != "abc123" {
		t.Fatalf("want abc123, got %q", got)
	}
}

func TestExtractClientToken_GetNeverReadsBody(t *testing.T) {
	body, err := json.Marshal(map[string]string{"token": "from-body"})
	if err != nil {
		t.Fatal(err)
	}
	c := newBodyTestContext(http.MethodGet, "/api/clients", body)
	if got := extractClientToken(c); got != "" {
		t.Fatalf("GET must not read a body token, got %q", got)
	}
}

// 超过扫描上限的请求体:不再尝试提取 token,但必须把完整请求体还给下游。
// 这一点很关键——如果只是截断,agent 的大上报体会被破坏。
func TestExtractClientToken_OversizedBodySkipsTokenButPreservesBody(t *testing.T) {
	pad := strings.Repeat("x", maxTokenScanBytes+4096)
	body, err := json.Marshal(map[string]string{"token": "must-not-be-read", "pad": pad})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) <= maxTokenScanBytes {
		t.Fatalf("test payload should exceed the scan limit, got %d bytes", len(body))
	}

	c := newBodyTestContext(http.MethodPost, "/api/clients/v2/rpc", body)

	if got := extractClientToken(c); got != "" {
		t.Fatalf("oversized body must not yield a token, got %q", got)
	}

	restored, err := io.ReadAll(c.Request.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if !bytes.Equal(restored, body) {
		t.Fatalf("body was not preserved: got %d bytes, want %d", len(restored), len(body))
	}

	// 内容也要完好,能解析回原始字段。
	var decoded map[string]string
	if err := json.Unmarshal(restored, &decoded); err != nil {
		t.Fatalf("restored body is not valid JSON: %v", err)
	}
	if decoded["token"] != "must-not-be-read" {
		t.Fatalf("restored body lost the token field")
	}
	if len(decoded["pad"]) != len(pad) {
		t.Fatalf("restored body pad truncated: got %d, want %d", len(decoded["pad"]), len(pad))
	}
}

// 恰好等于上限的请求体应正常提取 token(边界值)。
func TestExtractClientToken_BodyExactlyAtLimit(t *testing.T) {
	body, err := json.Marshal(map[string]string{"token": "edge"})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > maxTokenScanBytes {
		t.Fatalf("unexpected: small payload exceeds limit")
	}
	c := newBodyTestContext(http.MethodPost, "/api/clients/v2/rpc", body)
	if got := extractClientToken(c); got != "edge" {
		t.Fatalf("want edge, got %q", got)
	}
}
