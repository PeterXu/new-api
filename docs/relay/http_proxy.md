# New-API 渠道商 SOCKS5 代理请求流程分析

## 1. 用户请求格式

用户向 new-api 发送 OpenAI 兼容请求：

**请求示例：**
```http
POST /v1/chat/completions HTTP/1.1
Host: your-new-api-server.com
Authorization: Bearer sk-user-token-here
Content-Type: application/json

{
  "model": "gpt-4o",
  "messages": [{"role": "user", "content": "Hello"}],
  "stream": true
}
```

**关键字段：**
| 元素 | 说明 |
|------|------|
| URL | `/v1/chat/completions` (定义在 `router/relay-router.go:69-166`) |
| Authorization | `Bearer sk-XXXX` 格式的用户 API Key |
| Content-Type | `application/json` |

**Token 验证流程** (`middleware/auth.go:276-407`)：
1. 从 `Authorization` header 提取 token
2. 去除 `Bearer ` 前缀和 `sk-` 前缀
3. 调用 `model.ValidateUserToken()` 验证
4. 将用户 ID、分组、配额限制存入 context

---

## 2. 请求转换流程

### 2.1 渠道选择 (`middleware/distributor.go:30-164`)

`Distribute()` 中间件根据以下条件选择渠道：
- 请求中的 model 名称
- 用户分组 / token 分组
- 渠道优先级和亲和性

### 2.2 Context 设置 (`middleware/distributor.go:345-407`)

选中的渠道信息存入 context：
```go
common.SetContextKey(c, constant.ContextKeyChannelId, channel.Id)
common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, channel.GetBaseURL())
common.SetContextKey(c, constant.ContextKeyChannelKey, key)           // 渠道的 API Key
common.SetContextKey(c, constant.ContextKeyChannelSetting, channel.GetSetting())  // 包含 Proxy 配置!
```

### 2.3 URL 转换 (`relay/channel/openai/adaptor.go:97-173`)

| 渠道类型 | URL 构造方式 |
|----------|-------------|
| 标准 OpenAI | `baseURL + /v1/chat/completions` |
| Azure | `baseURL + /deployments/{model}/chat/completions?api-version=...` |
| Cloudflare | 去除 `/v1` 前缀 |
| 自定义模板 | 支持 `{model}` 占位符替换 |

### 2.4 Header 转换 (`relay/channel/openai/adaptor.go:175-227`)

```go
// 关键变化：用户 token 替换为渠道 token
header.Set("Authorization", "Bearer "+info.ApiKey)  // info.ApiKey = channel.Key

// Azure 特殊处理
header.Set("api-key", info.ApiKey)

// OpenRouter 特殊处理
header.Set("HTTP-Referer", "...")
header.Set("X-Title", "...")
```

**转换前后对比：**

| 元素 | 用户请求 | 转发请求 |
|------|----------|----------|
| URL | `http://new-api/v1/chat/completions` | `https://api.openai.com/v1/chat/completions` |
| Authorization | `Bearer sk-user-token` | `Bearer sk-channel-key` |
| 其他 Header | 保留 Content-Type, Accept 等 | 基本保留，部分渠道有特殊处理 |

---

## 3. SOCKS5 代理转发

### 3.1 代理配置 (`dto/channel_settings.go`)

```go
type ChannelSettings struct {
    Proxy                  string `json:"proxy"`
    InjectUserIdInProxyURL bool   `json:"inject_user_id_in_proxy_url,omitempty"`
    // ...
}
```

**配置示例：**
```json
{
  "proxy": "socks5://user:password@proxy-server:1080",
  "inject_user_id_in_proxy_url": true
}
```

**支持的代理格式：**
- `socks5://host:port` — SOCKS5 代理
- `socks5://user:pass@host:port` — 带认证的 SOCKS5
- `socks5h://host:port` — SOCKS5 + DNS 通过代理解析
- `http://host:port` — HTTP 代理

### 3.2 HTTP 客户端创建 (`service/http_client.go`)

```go
func NewProxyHttpClient(proxyURL string) (*http.Client, error) {
    // 解析 URL scheme
    switch scheme {
    case "socks5", "socks5h":
        // 提取认证信息
        var auth *proxy.Auth
        if parsedURL.User != nil {
            auth = &proxy.Auth{
                User:     parsedURL.User.Username(),
                Password: password,
            }
        }

        // 创建 SOCKS5 dialer (使用 golang.org/x/net/proxy)
        dialer, _ := proxy.SOCKS5("tcp", parsedURL.Host, auth, proxy.Direct)

        // 自定义 Transport 使用 SOCKS5 dialer
        transport := &http.Transport{
            IdleConnTimeout:     90 * time.Second,  // 空闲连接 90 秒后回收
            DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
                return dialer.Dial(network, addr)
            },
        }

        return &http.Client{Transport: transport}, nil
    }
}
```

### 3.3 请求执行 (`relay/channel/api_request.go`)

```go
func doRequest(c *gin.Context, req *http.Request, info *common.RelayInfo) (*http.Response, error) {
    var client *http.Client

    if info.ChannelSetting.Proxy != "" {
        proxyURL := info.ChannelSetting.Proxy
        // 当启用用户 ID 注入时，将用户 ID 嵌入代理 URL 的用户名字段
        if info.ChannelSetting.InjectUserIdInProxyURL {
            proxyURL = common.InjectUserIdInProxyURL(proxyURL, true, info.UserId)
        }
        client, _ = service.NewProxyHttpClient(proxyURL)
    } else {
        client = service.GetHttpClient()
    }

    resp, _ := client.Do(req)
    return resp, nil
}
```

### 3.4 用户 ID 注入 (`common/proxy_user_id.go`)

当渠道设置 `inject_user_id_in_proxy_url` 为 `true` 时，系统将当前请求用户的 ID 注入到代理 URL 的用户名字段中，使代理服务器能够识别每个请求的用户身份。

**转换规则：**

| 输入 | 用户 ID | 输出 |
|------|---------|------|
| `socks5://user:pass@host:port` | 42 | `socks5://user@42:pass@host:port` |
| `socks5://user@host:port` | 42 | `socks5://user@42@host:port` |

**代理服务器解析：**
代理服务器在收到 SOCKS5 认证时，将用户名按第一个 `@` 分割：
- 前半部分 = 原始用户名 (`user`)
- 后半部分 = 用户 ID (`42`)

**连接生命周期：**
- 每个唯一的代理 URL（含注入的用户 ID）对应一个独立的 HTTP 客户端和连接池
- 客户端按 URL 缓存，活跃用户的连接可复用（keep-alive）
- 空闲连接在 90 秒后自动回收（`IdleConnTimeout`）

### 3.5 代理客户端缓存 (`service/http_client.go`)

代理客户端按 URL 缓存，避免每次请求重复创建 SOCKS5 dialer。当启用用户 ID 注入时，每个用户对应一个缓存条目。

---

## 4. 完整请求流程图

```
用户请求                    New-API 处理                      上游渠道
   │                           │                               │
   │  POST /v1/chat/completions│                               │
   │  Auth: Bearer sk-user     │                               │
   ├──────────────────────────►│                               │
   │                           │                               │
   │                   [TokenAuth] 验证用户 token               │
   │                           │  • 提取 userId=42             │
   │                           │                               │
   │                   [Distribute] 选择渠道                   │
   │                           │  • 加载 channel.Setting      │
   │                           │  • Setting.Proxy = "socks5://..." │
   │                           │  • Setting.InjectUserIdInProxyURL │
   │                           │                               │
   │                   [Relay] 构建上游请求                    │
   │                           │  • URL: channel.BaseURL + path │
   │                           │  • Header: 替换 Authorization │
   │                           │                               │
   │                   [InjectUserId] (如果启用)               │
   │                           │  • socks5://user:pass@host    │
   │                           │  → socks5://user@42:pass@host │
   │                           │                               │
   │                           │         [SOCKS5 连接]         │
   │                           │              │                │
   │                           │              ▼                │
   │                           │    ┌─────────────────┐        │
   │                           │    │  SOCKS5 Proxy  │        │
   │                           │    │  Auth: user@42  │        │
   │                           │    │  proxy:1080    │        │
   │                           │    └────────┬────────┘       │
   │                           │             │                │
   │                           │ POST /v1/chat/completions    │
   │                           │ Auth: Bearer sk-channel-key  │
   │                           ├─────────────────────────────►│
   │                           │             │                │
   │                           │             │ Response       │
   │                           │◄────────────┴────────────────┤
   │                           │                              │
   │          Response         │                              │
   │◄──────────────────────────┤                              │
```

---

## 5. 关键代码位置汇总

| 功能 | 文件路径 |
|------|----------|
| 代理 URL 配置结构 | `dto/channel_settings.go` |
| 用户 ID 注入逻辑 | `common/proxy_user_id.go` |
| SOCKS5/HTTP 代理客户端创建 | `service/http_client.go` |
| 代理使用与用户 ID 注入调用 | `relay/channel/api_request.go` |
| 渠道设置加载 | `relay/common/relay_info.go` |
| Token 认证 | `middleware/auth.go` |
| 渠道选择 | `middleware/distributor.go` |
| URL 构造 | `relay/channel/openai/adaptor.go` |
| Header 设置 | `relay/channel/openai/adaptor.go` |
| Header Override | `relay/channel/api_request.go` |

---

## 6. 通过 SOCKS5 转发后的实际请求

当请求通过 SOCKS5 代理转发时，**HTTP 层面的请求内容不变**，变化的是 TCP 连接路径：

```
TCP 连接路径:
用户 → New-API 服务器 → SOCKS5 代理 → 上游渠道服务器

HTTP 请求内容（代理前后相同）:
POST /v1/chat/completions HTTP/1.1
Host: api.openai.com
Authorization: Bearer sk-channel-api-key
Content-Type: application/json

{"model":"gpt-4o","messages":[...]}
```

**SOCKS5 握手过程（在 TCP 层）：**
1. New-API 连接 SOCKS5 代理 (如 `proxy:1080`)
2. 发送 SOCKS5 握手，携带认证信息：
   - 用户名: `user@42`（原始用户名 + `@` + 用户 ID，仅在启用 `inject_user_id_in_proxy_url` 时）
   - 密码: 原始密码不变
3. 代理验证认证，提取用户名中的用户 ID 用于身份识别
4. 发送目标地址 (`api.openai.com:443`)
5. 代理建立到目标的 TCP 连接
6. 后续 HTTP 请求通过代理隧道传输

**连接复用与回收：**
- 同一用户的后续请求复用已建立的 SOCKS5 连接（HTTP keep-alive）
- 空闲连接在 90 秒后自动关闭（`IdleConnTimeout`）
- 每个用户对应独立的连接池，不同用户的连接互不干扰