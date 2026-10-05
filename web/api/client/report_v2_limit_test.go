package client

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadBounded_AllowsWithinLimit(t *testing.T) {
	data := []byte(`{"jsonrpc":"2.0"}`)
	got, err := readBounded(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("got %q, want %q", got, data)
	}
}

// 超限必须报错而不是截断,否则会把不完整的 JSON 交给调用方解析。
func TestReadBounded_RejectsOversized(t *testing.T) {
	oversized := strings.NewReader(strings.Repeat("a", maxDecompressedBodyBytes+1))
	if _, err := readBounded(oversized); err == nil {
		t.Fatal("expected an error for a body over the limit")
	}
}

func TestReadBounded_AllowsExactlyAtLimit(t *testing.T) {
	atLimit := bytes.Repeat([]byte("a"), maxDecompressedBodyBytes)
	got, err := readBounded(bytes.NewReader(atLimit))
	if err != nil {
		t.Fatalf("a body exactly at the limit should be accepted, got %v", err)
	}
	if len(got) != maxDecompressedBodyBytes {
		t.Fatalf("want %d bytes, got %d", maxDecompressedBodyBytes, len(got))
	}
}

// readMaybeCompressedBody 此前对 gzip 请求体做无上限解压,一个极小的请求
// 就能把内存撑爆。这里构造一个真实的小体积高压缩比请求体验证会被拒绝。
func TestReadMaybeCompressedBody_RejectsGzipBomb(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	chunk := bytes.Repeat([]byte("a"), 1<<20) // 1 MiB
	for i := 0; i < 8; i++ {                  // 解压后 8 MiB,超过 4 MiB 上限
		if _, err := zw.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	compressed := buf.Bytes()

	// 前提:压缩后的体积远小于上限,否则这个测试就不是在验证解压炸弹。
	if len(compressed) >= maxDecompressedBodyBytes {
		t.Fatalf("test bomb should compress far below the limit, got %d bytes", len(compressed))
	}

	req := httptest.NewRequest(http.MethodPost, "/api/clients/v2/rpc", bytes.NewReader(compressed))
	req.Header.Set("Content-Encoding", "gzip")

	if _, err := readMaybeCompressedBody(req); err == nil {
		t.Fatal("expected the gzip bomb to be rejected")
	}
}

func TestReadMaybeCompressedBody_AcceptsNormalGzip(t *testing.T) {
	payload := []byte(`{"jsonrpc":"2.0","method":"rpc.ping"}`)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/clients/v2/rpc", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Encoding", "gzip")

	got, err := readMaybeCompressedBody(req)
	if err != nil {
		t.Fatalf("normal gzip payload should be accepted, got %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}
}

func TestReadMaybeCompressedBody_PlainBody(t *testing.T) {
	payload := []byte(`{"jsonrpc":"2.0"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/clients/v2/rpc", bytes.NewReader(payload))

	got, err := readMaybeCompressedBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}
}
