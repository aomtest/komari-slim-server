# 面板内自更新（Panel Self-Update）设计方案

状态：**待实施**（方案已修订，吸收了一轮外部审查意见）

> 本版相对初版的修订：重启方式改为 systemd 受控重启；替换流程改为「先备份后替换」的原子序列；`apply` 改为后台任务 + 任务 ID；明确服务端对 release 来源的权威；修正了二次确认的安全定位。

## 1. 目标

在管理员面板里直接完成 server 本体的版本更新，不需要登录服务器、不需要 SSH。

解决的实际问题：目前升级只能靠 `install-komari-slim.sh` 的菜单（需要终端）或手动替换二进制。

## 2. 范围

### 支持

- **server 本体**的检查 / 更新 / 回滚
- **Linux 裸机 + systemd 管理**的安装
- 当前进程对目标二进制目录有写权限
- 与当前架构匹配的官方 release asset

### 明确拒绝

拒绝时返回明确的 `blocked_reason`，**不得静默降级为不安全的更新路径**。

| 环境 | 拒绝原因 |
|---|---|
| **非稳定通道（snapshot / prerelease）** | **见下方说明** |
| Windows | 运行中的 exe 无法被替换 |
| Docker / 其他容器 | 容器内替换二进制必须落在挂载卷，容器重建即回滚 |
| 非 systemd | 无法确认 unit、无法受控重启 |
| 无法确认二进制路径或权限 | 路径安全校验不通过 |
| asset 缺少 digest（或启用签名后缺少签名） | 校验链不完整 |
| 已有更新/回滚任务在执行 | 全局互斥锁占用 |
| agent 自更新 | 不在本功能范围，继续用 `install-komari-slim.sh` 的菜单项 |

### 为什么明确拒绝非稳定通道

三种构建方式注入的版本号格式不同（见 `.github/workflows/`）：

| 通道 | `CurrentVersion` | 构建来源 |
|---|---|---|
| stable | `v0.1.25` | tag |
| prerelease | `prerelease-<commit hash>` | prerelease 分支 |
| snapshot | `Snapshot-<时间戳>` | main 分支 |

快照版和预发布版**从分支构建，代码比任何正式版都新**。让它们走"更新"通道只会被**降级**到更旧的正式版——用"更新"的 UI 做降级本身就是错的。

早期实现只是让它们"检测不到更新"（`update_available: false`），问题在于用户**看不出为什么**没有更新按钮，而且 `apply` 接口本身没有拦（理论上可以绕过界面直接调用）。

现在改为**明确拒绝**：`can_update: false` 并给出 `blocked_reason`，说明当前通道以及"请用安装脚本切换通道"。切换通道应该走安装脚本——那里有通道选择和确认流程。

## 3. 已确认的决策

| 决策点 | 结论 |
|---|---|
| Windows 支持 | 不支持，接口提示手动更新 |
| Docker 支持 | 不做 |
| 鉴权强度 | `RequireAdmin` + CSRF token + 前端二次确认 |
| 回滚接口 | **做**，与 apply 完全同等鉴权与校验 |
| 前端 UI | 完全自定义，不参考任何外部项目版式 |

## 4. 核心流程

更新被实现为一个**受保护的后台任务**，而不是绑在某个 HTTP 连接上的同步操作。

```
① 检查    服务端固定访问可信仓库的官方 API，拉 latest release 元数据
② 建任务  apply 立即返回任务 ID，实际工作在后台 goroutine 中执行
③ 锁定    获取全局互斥锁，阻止并发的 apply / rollback
④ 校验    重新获取 release 元数据，与 check 阶段比对一致性
⑤ 下载    流式写入同目录随机名临时文件，边下边算 SHA-256
⑥ 验证    校验大小、digest（以及签名，若启用）
⑦ 备份    把当前二进制原子 rename 为备份文件
⑧ 替换    把已验证的临时文件原子 rename 为目标路径
⑨ 重启    systemctl restart <固定 unit>
⑩ 健康检查 确认 unit active、健康接口可访问、版本号已更新
```

任何一步失败都要走**恢复路径**（见 §8），不允许留下"二进制没了、服务起不来"的状态。

### 为什么用 `systemctl restart` 而不是 `syscall.Exec`

初版方案打算用 `syscall.Exec` 原地替换进程映像（PID 不变），理由是"不需要 sudo 权限"。修订后改为 systemd 受控重启：

- 本功能**已经限定只支持 systemd 环境**，用 systemd 管理生命周期是一致的选择
- 服务以 `User=root` 运行（已核实），调用 `systemctl restart` 有权限
- `syscall.Exec` 之后 PID 不变，**systemd 感知不到重启**：unit 状态不刷新、日志不轮转、`Restart=` 策略不生效，等于让服务脱离了 systemd 的管理
- `systemctl restart` 会重新应用 unit 配置，行为可预期

调用方式：`exec.Command("systemctl", "restart", unitName)`，**不经过 shell 拼接**，unit 名取自服务端固定值或严格 allowlist。

注意：`restart` 会终止当前进程自身（我们就是被重启的服务），所以必须异步发起、不等待返回。

### 权限前提（已核实）

```
server unit:  User=root
               ExecStart=/opt/komari/komari server -l 0.0.0.0:<port>
               WorkingDirectory=/opt/komari
               Restart=always
二进制路径:    /opt/komari/komari
```

## 5. 接口设计

| 接口 | 方法 | 鉴权 | 作用 |
|---|---|---|---|
| `/api/admin/update/check` | GET | RequireAdmin + CSRF | 返回当前版本、固定 release 元数据、是否可更新、阻塞原因 |
| `/api/admin/update/apply` | POST | RequireAdmin + CSRF | **创建后台任务**，立即返回任务 ID |
| `/api/admin/update/events` | GET | RequireAdmin + CSRF | 按任务 ID 订阅进度（SSE） |
| `/api/admin/update/status` | GET | RequireAdmin + CSRF | 查询任务状态与重启后的最终结果 |
| `/api/admin/update/rollback` | POST | RequireAdmin + CSRF | 创建回滚任务 |

### check 的响应

```json
{
  "current": "v0.1.25",
  "latest": "v0.1.26",
  "update_available": true,
  "release_id": 12345678,
  "tag": "v0.1.26",
  "asset": {
    "id": 987654,
    "name": "komari-linux-amd64",
    "size": 30093600,
    "digest": "sha256:6b193a2f..."
  },
  "release_notes": "### Fixed\n- ...",
  "published_at": "2026-10-07T...",
  "can_update": true,
  "blocked_reason": null,
  "has_backup": true
}
```

`can_update: false` 时 `blocked_reason` 说明原因（Windows / 非 systemd / 无写权限 / digest 缺失 / 任务进行中 等）。

### apply 的请求与响应

请求体**不接受**版本号、下载 URL、digest 等任何可影响下载目标的字段。服务端自己决定下载什么、从哪下。

```json
{ "task_id": "srv-upd-<服务端生成的不可预测值>", "target_version": "v0.1.26" }
```

任务 ID 必须是服务端生成的不可预测值，**不得**由版本号、URL 或任何用户输入拼接而成，也不能用作文件路径片段。

### events 的 SSE 事件

```
event: stage  data: {"stage":"downloading","progress":0.42,"bytes":12640000,"total":30093600}
event: stage  data: {"stage":"verifying"}
event: stage  data: {"stage":"backing_up"}
event: stage  data: {"stage":"replacing"}
event: stage  data: {"stage":"restarting"}
event: error  data: {"message":"...","stage":"verifying"}
```

**CSRF token 不得放进 SSE 的 query string**——会进入访问日志、代理日志和浏览器历史。应走 header。

### 任务状态机

```
queued → downloading → verifying → backing_up → replacing
       → restarting → health_check → succeeded
                                   ↘ failed
                                   ↘ rolled_back
```

## 6. 后端实现要点

新增 `web/api/admin/update*.go`。

### 全局互斥锁

同一时刻只允许一个 apply / rollback / 清理任务操作二进制。锁被占用时新任务直接拒绝（返回 `blocked_reason`），不排队。

### Release 元数据

- 仓库地址、API 端点、unit 名称全部是服务端固定值
- `check` 与 `apply` 阶段各自获取一次元数据，**apply 时必须比对**：release ID、tag、asset ID 与 check 阶段一致；asset 名与当前 OS/架构匹配
- 版本、tag、asset name 只用于元数据比较和日志，**不能作为文件路径片段**

### 下载

- 临时文件建在目标二进制**同目录**（保证 rename 是同文件系统操作）
- 文件名由服务端随机生成
- 严格的文件 mode（如 `0600`），避免被其他用户读取或替换
- 流式下载并同时计算 SHA-256
- 设置连接超时、总超时、最大文件大小
- 完成后校验：HTTP 状态、实际大小、digest（及签名，若启用）
- 任一校验失败 → 删除临时文件，**不触碰当前运行中的二进制**

### 路径与环境校验

更新前必须确认：

- 当前可执行文件的真实路径（解析符号链接后）
- 目标路径不是不受信任的符号链接
- 目标目录与备份目录在预期位置
- 文件属主与权限符合安装要求
- 当前进程确实由预期 systemd unit 管理
- 当前环境不是容器
- 当前进程对目录有写权限

**路径不能由请求参数传入。**

### 版本比较

- 容错 `v` 前缀、`Snapshot-` 前缀
- 当前版本取自 `utils.CurrentVersion`（构建时注入，见 `utils/version.go`）
- 逐段整数比较，不引入新的 semver 依赖

## 7. 替换流程（原子序列）

**不要**执行「删除旧文件 → rename 新文件」。正确顺序：

```
1. 获取更新锁
2. 再次确认当前文件与 unit 状态
3. rename(当前二进制 → 服务端生成的备份路径)     ← 原子
4. rename(已验证的临时文件 → 目标路径)            ← 原子
5. 设置并验证目标文件的 mode、属主、大小
6. 任一步骤失败 → 删除不完整的新文件，rename(备份 → 目标路径) 恢复
7. 跨设备 rename 直接失败，不做不安全的 copy 回退
```

第 3 步是关键差异：**当前二进制是被 rename 走的，不是被删除的**。这样第 4 步失败时，备份还在原位，可以原子恢复。

（进程在二进制被 rename 后仍能继续运行——inode 还在内存里。）

### 备份

- 文件名由服务端生成：`komari.bak.<版本>.<时间戳>`
- 备份目录固定，不可由请求指定
- 只保留最近 **3** 个有效备份，清理前确认文件属于本服务

## 8. 失败恢复

| 失败阶段 | 恢复动作 |
|---|---|
| 下载 / 校验失败 | 删除临时文件，原二进制未动，任务标记 failed |
| 备份 rename 失败 | 中止，原文件仍在，任务标记 failed |
| 替换 rename 失败 | 把备份 rename 回目标路径，任务标记 failed |
| systemd 重启失败 | 记录错误；尝试回滚到备份并再次重启 |
| 健康检查超时 | 标记为「状态未知」而非直接判定失败，并记录需要人工介入 |
| 回滚后仍无法启动 | 恢复到回滚前的二进制，记录明确错误 |

### 成功判定

必须**同时**满足：

- systemd unit 处于 `active`
- 健康接口可访问
- 当前版本等于目标版本
- 目标二进制可执行
- 启动日志无错误

**不能用「PID 不变」作为成功标准。**

## 9. 前端设计

### 位置与形态

「关于」页（`src/pages/admin/about.tsx`）顶部，用 **`SettingCardCollapse` 折叠卡片**，不新增菜单入口，不用弹窗。

```
┌─ 系统更新 ──────────────────────────────┐
│  当前版本  v0.1.25                       │
│                            [检查更新]     │
└──────────────────────────────────────────┘
```

检出更新后自动展开：

```
┌─ 系统更新 ──────────────────────────────┐
│  当前版本  v0.1.25   →   v0.1.26          │
│                                           │
│  更新内容（markdown 渲染，见下方安全约束）  │
│                                           │
│  [更新到此版本]   [查看 Release]   [回滚]  │
└──────────────────────────────────────────┘
```

进度内联在卡片中；详细日志用 `drawer` 从侧面滑出。

### 交互流程

1. 调 `apply` 拿任务 ID
2. 用任务 ID 订阅 `events`（SSE）
3. 收到 `restarting` 后允许连接断开
4. 用任务 ID + 目标版本轮询 `status`
5. 通过版本号、健康接口、unit 状态确认成功
6. 超时后显示**「状态未知」**，不要直接断言更新失败

### 复用组件

`SettingCardCollapse` / `SettingCard`、`ui/button.tsx`、`ui/drawer.tsx`、`ui/tips.tsx`、`lucide-react`

### 样式约束

- 走主题变量（`text-foreground` / `bg-accent-1` / `border-muted`），不用固定色值
- 自定义类名用 `km-` 前缀
- 文案走 i18n `t()` 体系

### 与其他项目的视觉区分

不采用「顶部菜单入口 + 居中弹窗 + 大字号版本对比」那类版式。我们的形态是「关于页内的设置卡片 + 内联进度」，与现有管理页的视觉语言一致。

### 按钮文案

需明确说明影响：

> 更新将替换服务端程序并重启服务，期间连接会短暂中断。

## 10. 安全设计

这个接口等价于**远程代码执行入口**——能替换服务器二进制，就能让服务器跑任意代码。

| 措施 | 说明 |
|---|---|
| `RequireAdmin` | 复用现有鉴权中间件 |
| CSRF token | apply / rollback 强制校验；**不得放入 URL query** |
| 服务端权威 | 下载目标、来源、校验值全部由服务端决定，请求体不接受 |
| digest 校验 | 下载内容必须与 API 提供的摘要一致 |
| 路径校验 | 路径不由请求传入，逐项校验符号链接与权限 |
| 全局互斥锁 | 阻止并发更新 |
| 同等鉴权 | rollback 与 apply 共用同一套中间件与校验 |
| 审计日志 | 见 §11 |

### 前端二次确认的定位

二次确认**不是安全控制**，只是防误操作的用户体验措施。它不能替代服务端鉴权、CSRF 校验、digest 校验和文件安全校验。

### Release notes 是外部输入

前端渲染必须：

- 禁用 raw HTML
- 使用严格的 Markdown sanitizer
- 限制链接与图片协议，优先只允许 `https` 和可信 GitHub 域名

### 校验链的边界（重要）

本功能的完整性校验**只到 digest 为止**，不包含发布者身份验证：

| 能防住 | 防不住 |
|---|---|
| 传输损坏、截断 | GitHub 仓库被攻破后替换 release |
| 中间人篡改（HTTPS 之外的一层） | 攻击者同时替换二进制与其摘要 |
| 镜像 / CDN 返回错误内容 | 伪造的 release（无签名可辨真伪） |

原因见 §14 ①：本项目不使用发布签名。**这个边界必须在代码注释和面向用户的说明中如实记录**，不能让它看起来像完整的供应链防护。

### 已知残余风险

面板被完全攻破（拿到管理员权限）时，攻击者可通过本接口植入后门二进制。这是功能的**固有代价**，无法通过鉴权设计消除。

因此本功能**提高了对面板自身安全性的要求**：实施后必须保证面板的鉴权、CSRF、XSS 防护没有短板。

## 11. 审计日志

记录：

- 操作者身份、来源 IP、时间
- 操作类型（apply / rollback）
- 原版本、目标版本
- release ID、asset ID
- 任务 ID
- 结果与失败阶段
- 是否执行了恢复或回滚

### 错误分类

必须明确区分，不得返回"成功形状"，也不得静默跳过校验：

环境不支持 / 权限不足 / release 元数据变化 / 下载失败 / digest（或签名）失败 / 文件替换失败 / systemd 重启失败 / 健康检查超时

## 12. 实施顺序

1. 抽象更新任务、全局锁、状态存储
2. 固定 release 元数据获取与一致性校验
3. 下载、digest 校验、临时文件清理
4. 路径 / 权限 / systemd / 容器环境检查
5. 原子备份、替换、失败恢复
6. systemd 重启与健康检查
7. rollback
8. check / apply / events / status API
9. 前端卡片、进度、重启后确认
10. i18n、审计日志、测试

## 13. 必须验证的场景

**环境类**：无更新 / Windows / Docker / 非 systemd / 无写权限 / asset 架构不匹配

**校验类**：digest 缺失 / digest 不匹配 / （启用签名后）签名缺失或无效 / 下载超时 / 超出大小限制

**并发类**：更新期间重复 apply / 更新期间 rollback

**失败恢复类**：rename 失败 / systemd restart 失败 / 新版本启动失败 / 失败后自动恢复 / 回滚成功

**鉴权类**：非管理员调用 / 缺少或错误 CSRF token

**输入类**：release notes 含 HTML、脚本、非 HTTPS 链接

## 14. 未决事项

### ① 发布签名：**已决策不做**

外部审查指出：**digest 只能证明「内容未被修改」，不能证明「发布者身份」，两者不可互相替代。** 这一点在理论上正确，但经评估后决定**本功能不引入签名**，只做 digest 校验。

理由：

- 查证了同类项目（上游 komari、Uptime Kuma、哪吒监控、妙妙屋）的实际情况：**没有任何一个做发布签名**，其中一个提供了 `checksums.txt`（校验和，非签名）。做 digest 校验已经达到这个生态的通常水平。
- 签名需要一套独立的基础设施（密钥对、私钥离线保管、轮换与撤销、CI 签名、程序内置公钥验证），其范围远超本功能。
- 平台自动提供的 `asset.digest` 零成本解决了完整性问题。

**这个限制必须在代码注释和面向用户的说明中如实记录**，不能让它看起来像是完整的供应链防护。具体表述：

> 更新流程校验下载内容与 GitHub 提供的 SHA-256 摘要一致，用于防止传输损坏与镜像篡改。**它不验证发布者身份**——如果 GitHub 仓库本身被攻破，攻击者可以同时替换二进制与其摘要。这与本项目当前不使用发布签名有关。

如果将来引入签名（例如 Sigstore / cosign 的 keyless signing），应作为独立任务，并在此处更新说明。

### ② `asset.digest` 缺失时的策略

GitHub 的 `digest` 字段是较新加入 API 的。缺失时应**拒绝更新**（不降级为"只下载不验证"）。这条已按审查意见定稿，但需要在实现时确认 API 版本兼容性。

### ③ 更新日志的展示范围

按跨版本累积实现：显示从当前版本到目标版本之间**所有版本**的 release notes。跨版本更新时用户需要知道中间发生了什么。
