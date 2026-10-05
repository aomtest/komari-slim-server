package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/aomtest/komari-slim-server/internal/config"
	"github.com/aomtest/komari-slim-server/web/connection"
	"github.com/aomtest/komari-slim-server/web/security"
)

type WebSocketUpgradeOption func(*websocket.Upgrader)

// DefaultWSReadLimit 是 WebSocket 单帧读取上限的默认值。
// gorilla/websocket 默认不限制单帧大小,恶意客户端可以发送超大帧耗尽内存;
// 此前三个 WebSocket 端点(/api/clients、/api/rpc2、/api/clients/v2/rpc)
// 都没有调用 SetReadLimit。
//
// 取 1 MiB 是因为 /api/rpc2 承载后台 RPC,管理员设置里的 custom_head /
// custom_body 等 HTML 字段可能相当大;agent 的 v2 上报体也在同一量级。
// 匿名可访问的公开端点另行收紧,见 PublicWSReadLimit。
const DefaultWSReadLimit = 1 << 20

// PublicWSReadLimit 是匿名可访问端点(/api/clients)的单帧上限。
// 该端点只接受 "get" 之类的短指令,64 KiB 绰绰有余。
const PublicWSReadLimit = 64 << 10

func IsWebSocketUpgrade(c *gin.Context) bool {
	return websocket.IsWebSocketUpgrade(c.Request)
}

func EnableWebSocketCompression(upgrader *websocket.Upgrader) {
	upgrader.EnableCompression = true
}

func UpgradeWebSocket(c *gin.Context, options ...WebSocketUpgradeOption) (*websocket.Conn, error) {
	if !IsWebSocketUpgrade(c) {
		return nil, fmt.Errorf("require websocket upgrade")
	}
	upgrader := websocket.Upgrader{
		CheckOrigin: CheckWebSocketOrigin,
	}
	for _, option := range options {
		option(&upgrader)
	}
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return nil, err
	}
	// 统一施加单帧上限。公开端点会在升级后自行收紧到 PublicWSReadLimit。
	conn.SetReadLimit(DefaultWSReadLimit)
	return conn, nil
}

// UpgradeSafeConn upgrades the request to a WebSocket and attaches the
// process-wide plugin frame interceptor (when one is wired). A plugin
// wsConnect hook may deny the connection: the peer receives a
// policy-violation close frame and the caller gets an error.
func UpgradeSafeConn(c *gin.Context, options ...WebSocketUpgradeOption) (*connection.SafeConn, error) {
	unsafeConn, err := UpgradeWebSocket(c, options...)
	if err != nil {
		return nil, err
	}
	interceptor := connection.Interceptor()
	if interceptor == nil {
		return connection.NewSafeConn(unsafeConn), nil
	}
	sc := connection.NewSafeConn(unsafeConn)
	info := &connection.ConnInfo{
		ID:        sc.ID,
		Path:      c.Request.URL.Path,
		RemoteIP:  c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
	}
	if clientUUID, ok := c.Get("client_uuid"); ok {
		if s, ok := clientUUID.(string); ok {
			info.ClientUUID = s
		}
	}
	if deny, reason := interceptor.OnConnect(info); deny {
		_ = unsafeConn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, reason),
			time.Now().Add(time.Second))
		_ = unsafeConn.Close()
		return nil, errors.New("websocket connection denied by plugin")
	}
	sc.SetInterceptor(info, interceptor)
	return sc, nil
}

func CheckWebSocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if strings.EqualFold(os.Getenv("KOMARI_WS_DISABLE_ORIGIN"), "true") {
		return true
	}
	if security.IsAPIKeyRequest(r) {
		return true
	}
	if origin == "" && r.URL.Query().Get("token") != "" {
		return true
	}
	enabled, _ := config.GetAs[bool](config.WsOriginCheckEnabledKey, true)
	if !enabled {
		return true
	}
	if origin == "" {
		return false
	}
	if security.OriginMatchesHost(origin, r.Host) {
		return true
	}
	allowlist, _ := config.GetAs[string](config.WsAllowedOriginsKey, "")
	return security.OriginInAllowlist(origin, allowlist)
}
