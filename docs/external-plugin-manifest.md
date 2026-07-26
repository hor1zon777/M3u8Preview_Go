# 外部插件 manifest 格式（schemaVersion 1）

插件中心支持导入**声明式外部插件**：一份 `manifest.json` 描述一个外部服务集成（名称、图标、说明，及可选的健康检查地址）。导入后在插件中心显示卡片，可启停、删除。

> **安全边界**：manifest 是纯元数据，**不包含也不会执行任何代码**。Go 是编译型语言，本项目的插件机制不做动态加载（见 `internal/plugin` 包文档）；"外部插件"用于把外部服务（自建 worker、旁路服务、监控目标等）纳入插件中心统一展示与管理。

## 导入方式

管理面板 → 插件中心 → 「导入插件」→ 选择 `manifest.json`（≤64KB）→ 预览确认。

- 同 ID 的外部插件已存在时会询问是否**覆盖升级**（更新元数据、保留启用状态）。
- 对应 API：`POST /api/v1/admin/plugins/import`（multipart `file` 字段，`?overwrite=true` 升级）；`DELETE /api/v1/admin/plugins/:id` 删除（内置插件受保护）。

## 完整示例

```json
{
  "schemaVersion": 1,
  "id": "jellyfin-bridge",
  "name": "Jellyfin 桥接",
  "description": "外部 Jellyfin 服务的健康监控卡片。",
  "version": "1.0.0",
  "icon": "server",
  "category": "外部服务",
  "healthUrl": "https://jellyfin.example.com/health",
  "homepage": "https://github.com/example/jellyfin-bridge"
}
```

## 字段说明

| 字段 | 必填 | 约束 | 说明 |
|---|---|---|---|
| `schemaVersion` | ✓ | 必须为 `1` | 格式版本；未知字段会被忽略（前向兼容） |
| `id` | ✓ | 3-64 字符 kebab-case（`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`） | 全局唯一，与内置插件共用命名空间（冲突拒绝） |
| `name` | ✓ | ≤50 字符 | 卡片显示名 |
| `version` | ✓ | ≤32 字符 | 插件自身版本标识 |
| `description` | | ≤500 字符 | 卡片描述 |
| `icon` | | 小写 lucide 图标名 | 前端白名单映射：`server` `database` `globe` `cloud` `activity` `shield` `bot` `webhook` `hard-drive` `monitor` `link` `plug` `radio` `cpu` `box` `subtitles`；未知名称兜底为拼图图标 |
| `category` | | ≤20 字符 | 分类展示；空则默认"外部服务" |
| `healthUrl` | | http/https，禁 userinfo | 可选健康检查地址：GET 探测（3s 超时、30s 缓存），2xx/3xx 视为健康，失败时卡片显示告警。**默认拒绝内网/保留地址** |
| `homepage` | | http/https，禁 userinfo | 仅展示用外链，服务端不请求 |

## healthUrl 与 SSRF 防护

- 默认策略：导入时拒绝指向内网/保留地址的 `healthUrl`（字面量校验），探测阶段走 SafeFetch（DNS 解析结果与实际连接 IP 绑定，防 DNS rebinding；重定向逐跳复检）。响应体不读取、不回显。
- 自托管场景健康检查内网服务很常见：在 `.env` 设 **`PLUGIN_HEALTH_ALLOW_PRIVATE=1`** 可放开内网地址（此时探测退化为普通 HTTP 客户端，属管理员显式选择）。改动后需重启生效。

## 启停语义

外部插件的"停用"只影响本系统侧：停止健康探测、卡片显示已停用；**不会对外部服务做任何操作**。
