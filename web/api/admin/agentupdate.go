package admin

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aomtest/komari-slim-server/database/auditlog"
	"github.com/aomtest/komari-slim-server/database/clients"
	"github.com/aomtest/komari-slim-server/internal/config"
	api "github.com/aomtest/komari-slim-server/web/api"
	"github.com/gin-gonic/gin"
)

const (
	// 连接 agent 触发端口的超时。这是个内网小包通信,5 秒足够 ——
	// 太长会让"面板卡住"变成常见体验。
	agentUpdateDialTimeout = 5 * time.Second

	// 单次最多触发的 agent 数量。批量触发时每台一个 goroutine,
	// 没有上限的话一次请求就能把连接数打满。
	agentUpdateMaxTargets = 50
)

type agentUpdateResult struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Version string `json:"version"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// TriggerAgentUpdate 让选中的 agent 去更新自己。
//
// 面板侧只做一件事:向 agent 的触发端口发一个共享令牌。它不发送任何指令、
// 不传二进制、不传参数 —— agent 收到匹配的令牌后自己去 GitHub 拉最新版。
// 所以即使面板被攻破,这个接口能给攻击者的也只是"让 agent 更新到最新版"。
//
// POST /api/admin/self-update/agent
func TriggerAgentUpdate(c *gin.Context) {
	var req struct {
		UUIDs []string `json:"uuids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, 400, "invalid request body")
		return
	}
	if len(req.UUIDs) == 0 {
		api.RespondError(c, 400, "no agent selected")
		return
	}
	if len(req.UUIDs) > agentUpdateMaxTargets {
		api.RespondError(c, 400, fmt.Sprintf("too many agents in one request (max %d)", agentUpdateMaxTargets))
		return
	}

	token, _ := config.GetAs[string](config.AgentUpdateTokenKey, "")
	if strings.TrimSpace(token) == "" {
		// 没配令牌就什么都不做。空令牌意味着功能未启用,而不是"用空字符串去触发"。
		api.RespondError(c, 400,
			"agent update is not configured: set a shared token in the settings first")
		return
	}
	port, _ := config.GetAs[int](config.AgentUpdatePortKey, 25775)
	if port <= 0 || port > 65535 {
		api.RespondError(c, 400, "invalid agent update port in the settings")
		return
	}

	results := make([]agentUpdateResult, len(req.UUIDs))
	var wg sync.WaitGroup
	for i, uuid := range req.UUIDs {
		wg.Add(1)
		go func(i int, uuid string) {
			defer wg.Done()
			results[i] = triggerOneAgent(uuid, token, port)
		}(i, uuid)
	}
	wg.Wait()

	okCount := 0
	for _, r := range results {
		if r.OK {
			okCount++
		}
	}

	uuid, _ := c.Get("uuid")
	operator, _ := uuid.(string)
	auditlog.Log(c.ClientIP(), operator,
		fmt.Sprintf("agent self-update triggered: %d/%d succeeded", okCount, len(results)),
		"info")

	c.JSON(200, gin.H{
		"status":  "success",
		"message": "",
		"data": gin.H{
			"succeeded": okCount,
			"total":     len(results),
			"results":   results,
		},
	})
}

// triggerOneAgent 向单个 agent 发送触发令牌。
func triggerOneAgent(uuid, token string, port int) agentUpdateResult {
	client, err := clients.GetClientByUUID(uuid)
	if err != nil {
		return agentUpdateResult{UUID: uuid, OK: false, Message: "unknown agent"}
	}

	res := agentUpdateResult{
		UUID:    uuid,
		Name:    client.Name,
		Version: client.Version,
	}

	// 优先用 IPv4:agent 侧的触发端口通常只监听在 IPv4 上。
	host := strings.TrimSpace(client.IPv4)
	if host == "" {
		host = strings.TrimSpace(client.IPv6)
	}
	if host == "" {
		res.Message = "no address on record"
		return res
	}

	// JoinHostPort 会为 IPv6 自动加方括号。
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	conn, err := net.DialTimeout("tcp", addr, agentUpdateDialTimeout)
	if err != nil {
		// 最常见的原因是 agent 在内网/NAT 后面,面板连不进去。
		// 这不是 bug,是这类方案的固有限制,所以错误信息里点出来。
		res.Message = fmt.Sprintf("cannot reach %s: %v (the agent may be behind NAT)", addr, err)
		return res
	}
	defer conn.Close()

	_ = conn.SetWriteDeadline(time.Now().Add(agentUpdateDialTimeout))
	// 只发令牌本身,不加任何其他内容。agent 侧逐字节比较。
	if _, err := conn.Write([]byte(token + "\n")); err != nil {
		res.Message = fmt.Sprintf("failed to send the trigger: %v", err)
		return res
	}

	res.OK = true
	res.Message = "trigger sent"
	return res
}
