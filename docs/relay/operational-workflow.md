# new-api 完整操作流程（从零到 API 调用）

## Step 1: 添加渠道（Channel）

**路径**: Admin → Channels → 点击"添加渠道"

配置内容：
- **名称**: 渠道标识，如 "OpenAI 主渠道"
- **类型**: 选择供应商（OpenAI / Anthropic / Azure / AWS Bedrock 等 40+ 种）
- **API Key**: 供应商的密钥（支持多 key 轮询）
- **Base URL**: 供应商接口地址（部分类型自动填充）
- **模型列表**: 选择或手动输入该渠道支持的模型（如 `gpt-4o`, `claude-sonnet-4-20250514`）
- **分组**: 将渠道分配到哪些组（如 `default`, `vip`）
- **优先级/权重**: 多渠道负载均衡时的路由策略
- **模型映射**（可选）: 客户端模型名 → 上游模型名的映射

> 添加渠道后，系统自动根据"模型列表"生成 `abilities` 记录，模型即可被路由到。

## Step 2: 配置模型定价（可选但推荐）

**路径**: Admin → System Settings → Models & Routing → Model Pricing

配置内容：
- **Model Ratio**: 模型倍率（影响输入 token 计费）
- **Completion Ratio**: 补全倍率
- **Cache Ratio**: 缓存命中倍率
- **Billing Mode**: 计费模式（按 token、按次、按表达式等）

**路径**: Admin → System Settings → Models & Routing → Group Pricing

配置内容：
- **Group Ratio**: 分组倍率（不同用户组使用不同价格，如 VIP 打折）
- **User Selectable Groups**: 用户创建 Key 时可选的分组

## Step 3: 配置速率限制（可选）

**路径**: Admin → System Settings → Security & Limits → Rate Limiting

配置内容：
- **启用开关**: 开启/关闭模型请求限速
- **限速周期**: 时间窗口（分钟）
- **最大请求数**: 周期内总请求上限（含失败）
- **最大成功数**: 周期内成功请求上限
- **分组覆盖**: 为特定组设置独立限额，如 `{"vip": [0, 10000], "free": [100, 50]}`

## Step 4: 创建用户

**路径**: Admin → Users → 点击"添加用户"

配置内容：
- **用户名** + **密码**
- **角色**: 普通用户 / 管理员
- **额度**: 设置初始余额

## Step 5: 创建 API Key

**路径**: General → API Keys → 点击"创建 Key"

配置内容：
- **名称**: Key 的备注名
- **分组**: 选择 Key 所属分组（决定使用哪些渠道和价格倍率）
- **有效期**: 永不过期 / 1小时 / 1天 / 1月 / 自定义
- **额度限制**: 可设置独立额度上限
- **模型限制**: 可限制该 Key 只能访问特定模型
- **IP 白名单**: 可选 CIDR 格式限制

创建后获得 `sk-xxx` 格式的 API Key。

## Step 6: 用户通过 API 调用

```bash
# OpenAI 兼容格式
curl https://your-domain/v1/chat/completions \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello"}]
  }'

# Anthropic 兼容格式
curl https://your-domain/anthropic/v1/messages \
  -H "x-api-key: sk-xxx" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-20250514",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

## 请求路由流程

```
用户请求 → middleware/distributor.go
  → 验证 API Key → 检查额度/限速/模型权限
  → service/channel_select.go
    → 查询 abilities 表（匹配 model + group）
    → 按优先级/权重选择渠道
  → relay/ 转发到上游供应商
  → 返回响应 → 扣费/记录日志
```

## 核心关系总结

| 概念 | 作用 | 是否必须 |
|------|------|---------|
| Channel | 提供模型的上游渠道 | **必须** |
| Channel.Models | 定义渠道支持哪些模型 | **必须** |
| Group | 渠道分组，控制访问和定价 | 可选（有 default） |
| API Key | 用户认证凭证 | **必须** |
| Model Pricing | 模型计费倍率 | 可选（默认倍率） |
| Group Pricing | 分组价格倍率 | 可选 |
| Models 表 | 模型元数据/描述 | 可选（不影响可用性） |
| Rate Limiting | 全局/分组限速 | 可选 |

