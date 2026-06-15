# OpenClaw Supervisor

OpenClaw 的 Windows 桌面管理工具，基于 Go + [Fyne](https://fyne.io/) 构建。提供图形化的安装向导、服务启停、保活监控、系统托盘等功能。

## 功能

- **安装向导** — 自动检测并安装 Node.js / OpenClaw CLI，下载 MSI 让用户手动安装，注入 PATH 避免重启
- **供应商配置** — 下拉选择常见供应商（DeepSeek、OpenAI、Anthropic 等），自动填入 ID / 地址 / 默认模型，支持从 API 拉取模型列表
- **网关管理** — 一键启动 / 停止 OpenClaw Gateway，保活守护线程自动检测端口并拉起
- **系统托盘** — 关闭窗口最小化到托盘，开机自启时自动隐藏
- **访问链接** — 启动后自动显示网关 URL（可点击打开），Token 已写配置
- **更新管理** — 检查 OpenClaw 更新并一键升级

## 构建

```bash
# 开发调试
go build -o debug_test.exe .

# 打包发布
fyne package -os windows -icon icon.png
```

## 配置

- `supervisor_config.json` — 放在 exe 同目录，自动生成
- OpenClaw 配置通过 `openclaw config patch --stdin` 写入 `~/.openclaw/openclaw.json`

## 使用

1. 启动后进入「安装向导」标签，点击「开始准备环境」
2. 如果未安装 Node.js，会自动下载 MSI 并打开，手动完成安装后点击确认
3. 选择供应商，填写 API Key，点击完成
4. 切换到「运行 / 停止 OpenClaw」标签，点击启动

## 开机自启

在「配置」标签中勾选「开机自动启动」即可。启动后窗口自动隐藏到系统托盘。

## 依赖

- Go 1.21+
- Fyne v2
- Node.js (安装向导会自动安装)
- OpenClaw CLI (`npm install -g openclaw`)

## 项目结构

```
├── main.go          # 入口，UI 布局，系统托盘
├── supervisor.go    # 保活守护线程，进程管理
├── install.go       # 安装向导，环境检测，Node.js 安装，供应商配置
├── update.go        # 更新检查，one-click update
├── config.go        # supervisor_config.json 读写，注册表自启
├── helpers.go       # safelog，safeGo
├── ipc.go           # 单实例互斥
├── icon.png         # 应用图标
└── build.bat        # 构建脚本
```
