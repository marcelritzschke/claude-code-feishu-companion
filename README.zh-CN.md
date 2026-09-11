<h1 align="center">Claude Code Feishu Companion</h1>

<p align="center">
  从飞书/Lark 继续正在运行的原生 Claude Code 会话。
  <br>
  原生 Claude Code 飞书 Channel——不启动新 Agent，也不替换 Claude Code runtime。
</p>

<p align="center">
  <a href="https://github.com/marcelritzschke/claude-code-feishu-companion/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/marcelritzschke/claude-code-feishu-companion/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/marcelritzschke/claude-code-feishu-companion/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/marcelritzschke/claude-code-feishu-companion"></a>
  <a href="https://github.com/marcelritzschke/claude-code-feishu-companion/releases"><img alt="Downloads" src="https://img.shields.io/github/downloads/marcelritzschke/claude-code-feishu-companion/total"></a>
  <a href="https://github.com/marcelritzschke/claude-code-feishu-companion/blob/main/go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/marcelritzschke/claude-code-feishu-companion"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/marcelritzschke/claude-code-feishu-companion"></a>
</p>

<p align="center">
  🚀 <a href="#快速开始">快速开始</a> ·
  📖 <a href="docs/setup.md">文档</a> ·
  🔐 <a href="docs/security-and-operations.md">安全</a> ·
  📦 <a href="https://github.com/marcelritzschke/claude-code-feishu-companion/releases">发布</a> ·
  🌐 <a href="README.md">English</a>
</p>

## 原生 Claude Code Channel——不是 CLI bridge

Claude Code Feishu Companion 通过原生 Channel，把飞书/Lark 连接到你已经启动的
Claude Code 进程。原来的终端始终保持活跃并可正常使用；飞书只是一个远程入口。

<table width="100%">
  <tr>
    <td>💡 <strong>把飞书/Lark 接入你已经在运行的 Claude Code 会话——不是另一套 Claude harness。</strong></td>
  </tr>
</table>

Claude Code 始终是实际运行环境，掌管会话生命周期、上下文、工具、身份验证、权限和
原生 TUI。Claude Code Feishu Companion 只把这个运行中的进程连接到飞书/Lark；它不会
启动 `claude -p`，不会引入独立的 Agent SDK harness，也不会创建另一个 Claude 会话。

```mermaid
flowchart LR
    feishu["飞书 / Lark"] <--> daemon["Companion 守护进程"]
    daemon <--> channel["Claude Code Channel"]
    channel <--> session["你正在运行的会话"]
```

## 快速开始

一行命令即可安装程序并打开飞书二维码引导。

在 macOS 或 Linux 上：

```sh
curl -fsSL https://raw.githubusercontent.com/marcelritzschke/claude-code-feishu-companion/main/install.sh | sh
```

在 Windows 上，使用 PowerShell：

```powershell
irm https://raw.githubusercontent.com/marcelritzschke/claude-code-feishu-companion/main/install.ps1 | iex
```

两者都会先用发布版本的 `checksums.txt` 校验下载内容，再进行安装，并把程序加入
`PATH`。

扫描二维码即可连接应用，扫码账号会成为所有者。已有应用凭据、手动设置和配置选项
请参阅[设置与配置（英文）](docs/setup.md)。

需要从飞书远程继续的会话，请这样启动：

```sh
claude --dangerously-load-development-channels server:claude-companion
```

在 Claude Code Channels 仍处于预览阶段时，普通 `claude` 会话可以发送通知，
但不能接收远程消息。

## 功能概览

| 功能 | 使用体验 | 状态 |
| --- | --- | --- |
| 现有 Claude Code 会话 | 继续使用终端里已经打开的会话。 | ✅ |
| 会话发现 | 从飞书/Lark 精确选择正在运行的会话。 | ✅ |
| 通知 | 只有 Claude 需要你或完成任务时才提醒。 | ✅ |
| 远程继续 | 把后续指令发送到选中的现有会话。 | ✅ 预览 |
| 实时查看 | 用一张原地更新的卡片跟随进度。 | ✅ |
| 远程中断 | 只停止当前轮次，不结束会话。 | ✅ macOS/Linux |
| 权限决策 | 从飞书/Lark 批准或拒绝权限请求。 | ✅ 可选 |
| 二维码引导 | 扫码一次，连接飞书应用和本机。 | ✅ |
| macOS / Linux | 使用完整的 Companion 工作流程。 | ✅ |
| Windows | 使用存在平台限制的工作流程。 | 🧪 部分支持 |
| Bedrock / Vertex / Foundry | 可以接收通知，但不能远程继续。 | ❌ Channel 限制 |

## 使用体验

- **默认安静。** 飞书只提示问题、权限请求和完成总结，不推送每次文件读取、命令或工具调用。
- **精确控制会话。** 发送 `sessions` 并选择正在运行的会话；后续消息只会进入该会话，
  已结束的会话不会被悄悄替换。
- **一张实时卡片。** 发送 `watch` 即可原地跟随进度。点击 **Interrupt** 或发送
  `interrupt` 只会停止当前轮次，不会结束会话。

## 工作原理与安全

本地轻量守护进程负责连接飞书并发现 Claude Code 会话。Hooks 提供生命周期和关注事件；
Claude Code Channels 把消息送入选定的运行中会话。

会话、上下文、身份验证和工具始终保留在本地并归用户所有。只有已配置的飞书所有者
可以发送消息，远程权限决策则需单独启用。Companion 只发送必要的卡片，不发送终端流、
完整会话记录或模型推理。

完整信任边界请参阅[安全与运维（英文）](docs/security-and-operations.md)，设计思路请参阅
[产品体验规范（英文）](docs/product-experience-spec.md)。

## 当前限制

- 远程继续需要使用上文的预览标志，因为 Claude Code Channels 仍处于研究预览阶段，
  Companion 尚未进入允许列表。
- Channels 需要通过 claude.ai 或 Console API Key 进行 Anthropic 身份验证，
  目前不支持 Bedrock、Vertex 和 Foundry。Team 与 Enterprise 组织需要集中启用
  Channels。
- Claude Code 的多项选择 `AskUserQuestion` 提示无法通过 Channel 回答，需回到原终端。
- 如需远程继续，电脑、Claude Code 会话、Claude Code Feishu Companion 守护进程和网络连接都必须
  保持运行。
- WSL 与原生 Windows 属于两套独立安装，各自需要单独的程序和 `init`。Windows 不
  支持远程中断。

## 参与贡献

```sh
mise install
mise exec -- go test ./...
mise exec -- go build -o claude-companion .
```

本仓库使用 [mise](https://mise.jdx.dev/) 固定 Go 工具链版本。设置和诊断命令请
参阅[设置与配置（英文）](docs/setup.md)与
[安全与运维（英文）](docs/security-and-operations.md)。
