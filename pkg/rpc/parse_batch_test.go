package rpc

import (
	"strconv"
	"strings"
	"testing"
)

func buildBatch(n int) []byte {
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"jsonrpc":"2.0","method":"rpc.ping","id":`)
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString("}")
	}
	sb.WriteString("]")
	return []byte(sb.String())
}

// 批量数组此前没有数量上限,而分发是串行的:一个请求塞上千条重查询即可
// 把负载放大上千倍。
func TestParseRequests_RejectsOversizedBatch(t *testing.T) {
	_, e := ParseRequests(buildBatch(MaxBatchSize + 1))
	if e == nil {
		t.Fatalf("expected an error for a batch larger than %d", MaxBatchSize)
	}
	if e.Code != InvalidRequest {
		t.Fatalf("want InvalidRequest, got code %d (%s)", e.Code, e.Message)
	}
}

// 边界值:恰好等于上限应当通过。
func TestParseRequests_AllowsBatchAtLimit(t *testing.T) {
	reqs, e := ParseRequests(buildBatch(MaxBatchSize))
	if e != nil {
		t.Fatalf("batch of exactly %d should be accepted, got %v", MaxBatchSize, e)
	}
	if len(reqs) != MaxBatchSize {
		t.Fatalf("want %d requests, got %d", MaxBatchSize, len(reqs))
	}
}

// 单个请求不受批量上限影响。
func TestParseRequests_SingleRequestUnaffected(t *testing.T) {
	req, e := ParseRequest([]byte(`{"jsonrpc":"2.0","method":"rpc.ping","id":1}`))
	if e != nil {
		t.Fatalf("unexpected error: %v", e)
	}
	if req.Method != "rpc.ping" {
		t.Fatalf("want rpc.ping, got %q", req.Method)
	}
}

// 空批量仍然按协议报错(保持原有行为)。
func TestParseRequests_EmptyBatchStillRejected(t *testing.T) {
	_, e := ParseRequests([]byte("[]"))
	if e == nil {
		t.Fatal("expected empty batch to be rejected")
	}
	if e.Message != "empty batch" {
		t.Fatalf("want the original empty-batch error, got %q", e.Message)
	}
}
