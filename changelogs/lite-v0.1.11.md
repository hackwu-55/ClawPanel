# ClawPanel Lite v0.1.11

发布时间：2026-03-14

## 通道消息回复修复（核心修复）

- **修复通道保存凭证后无法回复消息**：`SaveChannel` 保存飞书/企业微信等通道凭证时，直接用前端传来的 body 整体替换 `channels[id]`，由于前端保存凭证时不传 `enabled` 字段，导致原来的 `"enabled": true` 被覆盖丢失，通道变为未启用状态，所有消息静默丢弃。现在写入前会自动检查 body 是否含 `enabled`，若无则从现有配置继承。

- **修复 AGENTS.md 等工作区模板缺失导致 agent 无法启动**：打包脚本 `prune_openclaw_runtime` 对 `docs/` 目录执行 `rm -rf`，删掉了 OpenClaw agent 运行时必需的 `docs/reference/templates/AGENTS.md` 等模板文件。OpenClaw 收到消息后初始化 agent 时找不到模板，飞书报 `failed to dispatch message`，QQ Bot 报 `No response within timeout`。现改为只删除 `docs/` 下不需要的内容，保留 `docs/reference/templates/`。

- **修复飞书 plugin entry key 错误**：`SwitchFeishuVariant` 在 Lite 版会写入 `feishu-openclaw-plugin` entry key，但 Lite 版只有 `extensions/feishu/` 目录，导致 OpenClaw 报 plugin id mismatch。现在 Lite 版两种变体都映射到 `feishu` key，并自动清理残留的 `feishu-openclaw-plugin` 脏 entry。

## 安装脚本改进

- **安装脚本不再硬编码兜底版本号**：去除 `DEFAULT_VERSION` 变量，脚本始终从 GitHub / 加速服务器动态获取最新版本；若网络全部失败则明确报错并提示通过 `VERSION=x.y.z` 环境变量手动指定，不再静默安装旧版本。

## 当前说明

- Linux Lite 为当前正式推荐版本
- macOS Lite 继续保持预览验证阶段
- Windows Lite 不再提供，请使用 Pro 版
