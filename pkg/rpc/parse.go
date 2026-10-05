package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MaxBatchSize 是单个 JSON-RPC 批量请求允许的最大子请求数。
// 此前批量数组没有数量上限,而且分发是串行的:一个请求塞进上千条重查询
// (例如 getRecords)即可把一次 HTTP 请求放大成上千倍的服务端负载。
// 目前前端 batchCall 没有任何调用点,该上限不影响现有功能。
const MaxBatchSize = 20

// ParseRequest 解析单个 JSON-RPC 请求。返回请求与错误（解析层面）。
func ParseRequest(data []byte) (*JsonRpcRequest, *JsonRpcError) {
	requests, err := ParseRequests(data)
	if err != nil {
		return nil, err
	}
	if len(requests) == 0 {
		return nil, &JsonRpcError{Code: InvalidRequest, Message: "no requests found"}
	}
	return requests[0], nil
}

// ParseRequests 解析单个或批量 JSON-RPC 请求。返回请求切片与错误（解析层面），
// 若是批量空数组则返回 InvalidRequest 错误（协议要求）。
func ParseRequests(data []byte) ([]*JsonRpcRequest, *JsonRpcError) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, &JsonRpcError{Code: ParseError, Message: "empty body"}
	}
	first := data[0]
	if first == '{' { // 单个
		var r JsonRpcRequest
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, &JsonRpcError{Code: ParseError, Message: "invalid json", Data: err.Error()}
		}
		if e := r.Validate(); e != nil {
			return nil, e
		}
		return []*JsonRpcRequest{&r}, nil
	}
	if first == '[' { // 批量
		var arr []JsonRpcRequest
		if err := json.Unmarshal(data, &arr); err != nil {
			return nil, &JsonRpcError{Code: ParseError, Message: "invalid json", Data: err.Error()}
		}
		if len(arr) == 0 {
			return nil, &JsonRpcError{Code: InvalidRequest, Message: "empty batch"}
		}
		if len(arr) > MaxBatchSize {
			return nil, &JsonRpcError{
				Code:    InvalidRequest,
				Message: fmt.Sprintf("batch too large: %d requests (max %d)", len(arr), MaxBatchSize),
			}
		}
		res := make([]*JsonRpcRequest, 0, len(arr))
		for i := range arr {
			rr := arr[i]
			if e := rr.Validate(); e != nil {
				return nil, e
			}
			res = append(res, &rr)
		}
		return res, nil
	}
	return nil, &JsonRpcError{Code: ParseError, Message: "invalid json: not object/array"}
}
