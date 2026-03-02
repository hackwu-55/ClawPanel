#!/bin/bash
# ============================================================
# ClawPanel QQ NapCat 插件诊断与修复脚本 (Linux / macOS)
# 用法:
#   curl -sSO https://raw.githubusercontent.com/zhaoxinyi02/ClawPanel/main/scripts/fix-qq-napcat.sh
#   sudo bash fix-qq-napcat.sh
# ============================================================

set -e

RED='\033[31m'; GREEN='\033[32m'; YELLOW='\033[33m'
CYAN='\033[36m'; BOLD='\033[1m'; NC='\033[0m'

ok()   { echo -e "${GREEN}[✓]${NC} $1"; }
warn() { echo -e "${YELLOW}[!]${NC} $1"; }
err()  { echo -e "${RED}[✗]${NC} $1"; }
info() { echo -e "${CYAN}[→]${NC} $1"; }
title(){ echo -e "\n${BOLD}$1${NC}"; echo "$(echo "$1" | sed 's/./-/g')"; }

echo ""
echo -e "${BOLD}╔══════════════════════════════════════════════╗${NC}"
echo -e "${BOLD}║   ClawPanel QQ NapCat 插件诊断与修复工具    ║${NC}"
echo -e "${BOLD}╚══════════════════════════════════════════════╝${NC}"
echo ""

OPENCLAW_DIR="${OPENCLAW_DIR:-$HOME/openclaw/config}"
OPENCLAW_JSON="$OPENCLAW_DIR/openclaw.json"
QQ_EXT_DIR=""

# ── 1. 检测 Node.js ──────────────────────────────────────────
title "1. 检测 Node.js"
if command -v node &>/dev/null; then
    NODE_VER=$(node --version)
    NODE_MAJOR=$(echo "$NODE_VER" | sed 's/^v//' | cut -d. -f1)
    if [ "${NODE_MAJOR:-0}" -ge 20 ]; then
        ok "Node.js $NODE_VER"
    else
        warn "Node.js $NODE_VER 版本过低 (需要 >= 20)"
        info "正在升级 Node.js..."
        if [ "$(uname)" = "Darwin" ]; then
            brew install node@22 || brew upgrade node@22 || true
            brew link --overwrite node@22 || true
        else
            curl -fsSL https://deb.nodesource.com/setup_22.x | bash - 2>/dev/null && apt-get install -y nodejs 2>/dev/null || \
            curl -fsSL https://rpm.nodesource.com/setup_22.x | bash - 2>/dev/null && (dnf install -y nodejs 2>/dev/null || yum install -y nodejs 2>/dev/null) || \
            { warn "自动升级失败，请手动安装 Node.js 22+: https://nodejs.org"; }
        fi
    fi
else
    err "Node.js 未安装"
    info "请先安装 Node.js 22+: https://nodejs.org 或通过 ClawPanel 软件环境页面一键安装"
    exit 1
fi

# ── 2. 检测 openclaw CLI ──────────────────────────────────────
title "2. 检测 OpenClaw"
if command -v openclaw &>/dev/null; then
    OC_VER=$(openclaw --version 2>/dev/null || echo "unknown")
    ok "openclaw $OC_VER"
else
    err "openclaw 命令未找到"
    info "正在安装 OpenClaw..."
    npm install -g openclaw@latest --registry=https://registry.npmmirror.com
    ok "openclaw 已安装: $(openclaw --version 2>/dev/null)"
fi

# ── 3. 定位 OpenClaw 配置目录 ────────────────────────────────
title "3. 定位 OpenClaw 配置"
if [ ! -f "$OPENCLAW_JSON" ]; then
    # 尝试常见路径
    for candidate in \
        "$HOME/openclaw/config/openclaw.json" \
        "/root/openclaw/config/openclaw.json" \
        "$HOME/.openclaw/openclaw.json" \
        "$(openclaw config path 2>/dev/null)/openclaw.json"; do
        if [ -f "$candidate" ]; then
            OPENCLAW_JSON="$candidate"
            OPENCLAW_DIR="$(dirname "$candidate")"
            break
        fi
    done
fi
if [ -f "$OPENCLAW_JSON" ]; then
    ok "openclaw.json: $OPENCLAW_JSON"
else
    err "openclaw.json 未找到，尝试初始化..."
    openclaw init 2>/dev/null || true
    OPENCLAW_JSON="$HOME/openclaw/config/openclaw.json"
    OPENCLAW_DIR="$HOME/openclaw/config"
    if [ ! -f "$OPENCLAW_JSON" ]; then
        err "初始化失败，请手动运行 'openclaw init'"
        exit 1
    fi
    ok "openclaw.json 已初始化: $OPENCLAW_JSON"
fi

# ── 4. 检测 QQ 插件目录 ──────────────────────────────────────
title "4. 检测 QQ 扩展插件"
# openclaw 扩展目录 = openclaw 全局安装目录/extensions/qq
OPENCLAW_GLOBAL=$(npm root -g 2>/dev/null)/openclaw
QQ_EXT_DIR="$OPENCLAW_GLOBAL/extensions/qq"

if [ -d "$QQ_EXT_DIR" ]; then
    ok "QQ 扩展目录: $QQ_EXT_DIR"
    # 检查文件权限（需要 root 所有权）
    OWNER=$(stat -c '%U' "$QQ_EXT_DIR" 2>/dev/null || stat -f '%Su' "$QQ_EXT_DIR" 2>/dev/null)
    if [ "$OWNER" != "root" ]; then
        warn "QQ 扩展目录所有者为 $OWNER（应为 root），正在修复权限..."
        chown -R root:root "$QQ_EXT_DIR"
        ok "权限已修复 (root:root)"
    else
        ok "文件权限正常 (root:root)"
    fi
else
    warn "QQ 扩展目录不存在: $QQ_EXT_DIR"
    info "QQ/NapCat 通道依赖 openclaw 内置的 qq 扩展，尝试重装 openclaw..."
    npm install -g openclaw@latest --registry=https://registry.npmmirror.com
    if [ -d "$QQ_EXT_DIR" ]; then
        ok "QQ 扩展已随 openclaw 安装"
        chown -R root:root "$QQ_EXT_DIR" 2>/dev/null || true
    else
        warn "QQ 扩展在此版本的 openclaw 中不存在，可能已内置或路径不同"
    fi
fi

# ── 5. 检测 NapCat Docker 容器 ───────────────────────────────
title "5. 检测 NapCat 容器"
if ! command -v docker &>/dev/null; then
    err "Docker 未安装，无法运行 NapCat"
    info "请先安装 Docker: https://docs.docker.com/engine/install/ 或通过 ClawPanel 软件环境页面一键安装"
else
    CONTAINER_STATUS=$(docker inspect --format '{{.State.Status}}' openclaw-qq 2>/dev/null || echo "not_found")
    case "$CONTAINER_STATUS" in
        running)
            ok "NapCat 容器 (openclaw-qq) 运行中"
            # 检测 WebSocket 端口
            if nc -z 127.0.0.1 3001 2>/dev/null; then
                ok "OneBot WS 端口 3001 可达"
            else
                warn "OneBot WS 端口 3001 不可达（容器刚启动或配置问题）"
            fi
            ;;
        exited|stopped)
            warn "NapCat 容器已停止，正在启动..."
            docker start openclaw-qq
            sleep 5
            ok "NapCat 容器已启动"
            ;;
        not_found)
            err "NapCat 容器 (openclaw-qq) 不存在"
            info "请通过 ClawPanel → NapCat 管理页面安装并配置 NapCat"
            info "或参考文档: https://github.com/zhaoxinyi02/ClawPanel/blob/main/docs/qq-napcat-guide.md"
            ;;
        *)
            warn "容器状态: $CONTAINER_STATUS"
            ;;
    esac
fi

# ── 6. 检查并修复 openclaw.json 中的 QQ 通道配置 ─────────────
title "6. 检查 openclaw.json QQ 通道配置"
if command -v python3 &>/dev/null; then
    CHANNELS_QQ=$(python3 -c "
import json, sys
try:
    d = json.load(open('$OPENCLAW_JSON'))
    qq = d.get('channels', {}).get('qq', {})
    print('enabled=' + str(qq.get('enabled', False)).lower())
    print('wsUrl=' + str(qq.get('wsUrl', '')))
except Exception as e:
    print('error=' + str(e))
" 2>/dev/null)
    ENABLED=$(echo "$CHANNELS_QQ" | grep enabled | cut -d= -f2)
    WS_URL=$(echo "$CHANNELS_QQ" | grep wsUrl | cut -d= -f2)

    if [ "$ENABLED" = "true" ]; then
        ok "channels.qq.enabled = true"
    else
        warn "channels.qq.enabled = false 或未配置"
        info "如需使用 QQ 通道，请在 ClawPanel 通道配置页面启用"
    fi

    if [ "$WS_URL" = "ws://127.0.0.1:3001" ]; then
        ok "channels.qq.wsUrl = ws://127.0.0.1:3001"
    elif [ -n "$WS_URL" ] && [ "$WS_URL" != "None" ]; then
        ok "channels.qq.wsUrl = $WS_URL"
    else
        warn "channels.qq.wsUrl 未配置，建议值: ws://127.0.0.1:3001"
        info "可在 ClawPanel 通道配置中修改"
    fi
else
    warn "无法解析 openclaw.json（需要 python3）"
fi

# ── 7. 重启 OpenClaw 网关 ────────────────────────────────────
title "7. 重启 OpenClaw 网关"
if command -v systemctl &>/dev/null && systemctl is-active --quiet clawpanel 2>/dev/null; then
    info "通过 ClawPanel 服务重启 OpenClaw..."
    # 写入重启信号文件
    RESTART_SIGNAL_DIR="$OPENCLAW_DIR"
    echo '{"reason":"fix-qq-napcat script","time":"'"$(date -Iseconds)"'"}' > "$RESTART_SIGNAL_DIR/restart-gateway-signal.json" 2>/dev/null || true
    ok "重启信号已发送，ClawPanel 将在 5 秒内重启 OpenClaw 网关"
else
    info "请在 ClawPanel 面板中手动重启 OpenClaw 网关，或重启 clawpanel 服务:"
    info "  systemctl restart clawpanel"
fi

echo ""
echo -e "${BOLD}${GREEN}══════════════════════════════════════════════${NC}"
echo -e "${BOLD}${GREEN}  诊断完成！如仍有问题，请提交 Issue:${NC}"
echo -e "${BOLD}${GREEN}  https://github.com/zhaoxinyi02/ClawPanel/issues${NC}"
echo -e "${BOLD}${GREEN}══════════════════════════════════════════════${NC}"
echo ""
