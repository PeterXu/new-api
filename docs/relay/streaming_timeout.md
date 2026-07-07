# Streaming Timeout 详解

## 架构上下文

```
客户端 → [New-API] → sub2api → proxy → Ollama
           ↑
           这个 TCP 连接断了
           c.Request.Context() 才会 cancel
```

`context canceled` 错误 100% 是 New-API 关闭了与上游的 TCP 连接导致的。上游服务（sub2api、Ollama）不会主动触发此错误。

## 所有可能导致连接中断的超时源

### 第一优先级 — 主要超时配置

| 超时配置 | 默认值 | 环境变量 | 作用位置 |
|---|---|---|---|
| **`RELAY_TIMEOUT`** | 0（无限） | `RELAY_TIMEOUT` | `service/http_client.go` — 设置 `http.Client.Timeout` |
| **`STREAMING_TIMEOUT`** | 300 秒（5 分钟） | `STREAMING_TIMEOUT` | `relay/helper/stream_scanner.go` — 流式数据接收超时 |

### 第二优先级 — 其他超时

| 超时 | 默认值 | 作用位置 | 说明 |
|---|---|---|---|
| `IdleConnTimeout` | 90 秒 | `service/http_client.go` | HTTP Transport 空闲连接超时（不影响活跃连接） |
| Ping 发送超时 | 10 秒 | `relay/helper/stream_scanner.go` | ping 写操作阻塞超时 |
| Ping goroutine 最大时长 | 30 分钟 | `relay/helper/stream_scanner.go` | ping 协程保护 |
| Docker 健康检查 | 30 秒间隔 / 10 秒超时 | `docker-compose.yml` | 容器重启会中断活跃连接 |

## 超时触发流程

### STREAMING_TIMEOUT 触发流程

代码位置：`relay/helper/stream_scanner.go:53-273`

```
1. ticker := time.NewTicker(streamingTimeout)        // 默认 300s
2. 每收到一条数据 → ticker.Reset(streamingTimeout)    // 重置计时器
3. 如果 streamingTimeout 内没有新数据到达：
   → ticker.C 触发
   → SetEndReason(Timeout)
   → 进入 defer 清理，关闭 resp.Body
   → New-API 与上游的 TCP 连接断开
   → 上游服务看到 context canceled
```

**关键点**：如果 Ollama 模型加载或推理超过 5 分钟才出首字，或者两次 token 间隔超过 5 分钟，就会触发此超时。

### RELAY_TIMEOUT 触发流程

代码位置：`service/http_client.go:56-67`

```
1. 如果 RELAY_TIMEOUT > 0 → http.Client.Timeout = RELAY_TIMEOUT 秒
2. Go 的 http.Client.Timeout 是整个请求的总超时（包括连接、重定向、读 body）
3. 超时 → context deadline exceeded → 连接关闭
```

当 `RELAY_TIMEOUT=0`（默认）时，HTTP 客户端没有超时限制。

## 日志诊断

通过 `StreamEndReason` 判断超时来源：

| EndReason | 含义 | 排查方向 |
|---|---|---|
| `StreamEndReasonTimeout` | `STREAMING_TIMEOUT` 触发 | 增大 `STREAMING_TIMEOUT` |
| `StreamEndReasonClientGone` | 上游客户端（或反向代理）先断了 | 检查反向代理超时配置 |
| `StreamEndReasonDone` | 正常收到 `[DONE]` 结束 | 无需处理 |
| `StreamEndReasonEOF` | 上游关闭连接 | 检查上游服务状态 |

## 配置建议

### Ollama 等大模型（首字可能很慢）

```bash
# 20 分钟，适合需要长时间加载模型或推理的场景
STREAMING_TIMEOUT=1200

# 无限制（按需设置）
RELAY_TIMEOUT=0
```

### 反向代理（Nginx）

New-API 前面的 Nginx 需要针对 SSE 流式场景进行配置。以下是经过验证的参考配置：

```nginx
location / {
    proxy_pass http://new-api:3000;

    proxy_set_header X-Real-Ip $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Host $host;

    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_buffering off;
    gzip off;

    proxy_connect_timeout 60s;
    proxy_send_timeout 300s;
    proxy_read_timeout 3600s;
    send_timeout 300s;

    client_max_body_size 100m;
}
```

**配置要点说明：**

| 配置 | 值 | 为什么 |
|---|---|---|
| `proxy_buffering off` | — | SSE 必须，否则 Nginx 会缓冲响应直到完成才发送 |
| `gzip off` | — | 避免 gzip 缓冲导致数据延迟推送 |
| `proxy_http_version 1.1` + `Connection ""` | — | 保持与上游的长连接 |
| `proxy_read_timeout 3600s` | 1 小时 | Nginx 等待 New-API 响应数据的间隔超时，必须 ≥ `STREAMING_TIMEOUT` |
| `proxy_send_timeout 300s` | 5 分钟 | Nginx 向 New-API 发送请求体的间隔超时 |
| `send_timeout 300s` | 5 分钟 | Nginx 向客户端发送响应的间隔超时 |
| `client_max_body_size 100m` | 100MB | 允许较大的请求体（如文件上传、长对话） |

**超时链路对照：**

```
客户端 ← send_timeout → [Nginx] ← proxy_read_timeout → [New-API] ← STREAMING_TIMEOUT → [sub2api → Ollama]
                              ← proxy_send_timeout →         ← RELAY_TIMEOUT →
```

整条链路的超时应该从后往前递增：`STREAMING_TIMEOUT ≤ proxy_read_timeout`。

### 反向代理（Cloudflare）

Cloudflare 默认 100 秒超时，企业版可自定义。如果请求可能超过 100 秒，需要：
- 使用 Enterprise 计划设置更长的超时
- 或者绕过 Cloudflare 直接访问

## Gin 服务器配置

当前 `main.go` 中 Gin 服务器没有设置 `ReadTimeout` / `WriteTimeout`：

```go
err = server.Run(":" + port)
```

这意味着 Go 使用默认的 HTTP 服务器行为，对于长连接 SSE 场景通常不会造成问题。但如果有需要，可以通过 `http.Server` 显式配置：

```go
srv := &http.Server{
    Addr:         ":" + port,
    Handler:      router,
    ReadTimeout:  0,             // 0 = 无限制
    WriteTimeout: 0,             // 0 = 无限制，SSE 场景必须为 0
    IdleTimeout:  120 * time.Second,
}
```
