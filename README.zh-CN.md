**简体中文** | [English](README.md)

# Wirelark

**继续用 Claude Code。离开电脑后，也能在飞书里继续同一个会话。**

Wirelark 是 Claude Code 的低调远程伴侣。它不会替你运行 Claude Code，也不会
取代 Claude Code TUI。它只是把飞书连接到你在自己电脑上启动并拥有的会话。

```text
Claude Code TUI  ←────  Wirelark  ────→  飞书
     主工作区            隐形桥梁          临时远程入口
```

Wirelark 默默运行在后台：Claude Code 始终是你的工作区，飞书只在你离开电脑时
成为一个临时远程窗口。会话需要你时，在飞书里响应；回到电脑后，在原来的终端里
继续同一个会话。

## 为什么选择 Wirelark？

### Claude Code 仍是主工作区

你仍然按照平时的方式工作：

```sh
cd my-project
claude
```

无需创建 Wirelark 工作区，无需迁移到另一套 Agent 运行时或 Web UI，也不需要
终端镜像或由桥接程序托管的会话。原来的终端始终可以正常使用。

### 只提醒需要关注的事，不制造噪音

> **需要你时通知，完成后总结，始终继续原来的会话。**

Wirelark 不会把每次文件读取、搜索、Shell 命令或工具调用都发送到飞书。它会
提示权限请求和 Claude 提出的问题，总结已完成的工作；长任务则可选择在同一张
卡片上更新进度。

当你离开电脑时，Wirelark 只需要回答两个问题：

- Claude 需要我吗？
- 我不在时发生了什么？

### 远程继续同一会话，而不是创建第二个对话

向飞书机器人发送 `sessions`，然后从本机正在运行的 Claude Code 会话中选择
一个：

```text
payments-api
Working · Remote ready

frontend
Waiting for permission · Remote ready

wirelark
Idle · Notifications only
```

点击会话或回复编号。后续消息只会进入这个指定会话，不会发送到其他会话。如果
会话已经结束，Wirelark 会清除选择，不会悄悄把消息转发到别处。

## 使用流程

```text
照常启动 Claude
        ↓
Wirelark 自动发现会话
        ↓
离开电脑
        ↓
Claude 需要你时，飞书发出通知
        ↓
选中对应的运行中会话
        ↓
远程继续该会话
        ↓
回到终端
        ↓
继续同一个原生 Claude Code 会话
```

Wirelark 只在需要关注和任务完成时通知，并提供本地会话概览、向指定会话发送
后续指令，以及可选的远程权限批准与拒绝。卡片按钮只是快捷方式，并非必需；
输入会话编号和明确的权限回复同样有效。

## 与 Agent 网关有何不同

许多 Agent 网关会把桥接程序或平台作为工作的起点：

```text
飞书
  ↓
桥接程序 / Agent 平台
  ↓
由桥接程序启动或恢复 Claude
```

Wirelark 采用不同的产品边界：

```text
Claude Code TUI ← Wirelark → 飞书
```

如果聊天工具是主要工作区，网关是很自然的入口。Wirelark 面向另一种需求：让
Claude Code 始终作为主工作区，只在离开电脑时，通过一座隐形桥梁临时连接进去。

Wirelark 不拥有、不启动、也不恢复你的 Claude Code 会话。它不是通用 Agent
平台，不是以飞书为中心的编程环境，也不是 Claude Code 的替代界面。

## 安装与设置

在 macOS 或 Linux 上运行：

```sh
curl -fsSL https://raw.githubusercontent.com/marcelritzschke/wirelark/main/install.sh | sh
wirelark init
```

安装程序会从 [GitHub Releases](https://github.com/marcelritzschke/wirelark/releases)
下载适合当前平台的单一二进制文件，校验其校验和，并安装到 `~/.local/bin`。
无需维护运行时环境、容器或服务管理器。

`wirelark init` 会启动基于二维码的飞书引导流程：

```text
wirelark init
    ↓
扫描飞书二维码
    ↓
批准授权
    ↓
完成
```

扫码所用的账号会成为该 Wirelark 安装的所有者。在管理员统一管理的环境中，也
可以改用已有的 App ID 和 App Secret。

Windows 发布包、手动配置飞书应用、所需权限范围、配置选项和其他安装路径，请
参阅[设置与配置（英文）](docs/setup.md)。

## 工作原理

Wirelark 在本地运行一个轻量守护进程，负责维持飞书连接并识别本机上的 Claude
Code 会话。Claude Code Hooks 提供会话生命周期、需要关注和完成等事件；Claude
Channels 则通过受支持的连接方式，把飞书消息送入已经运行的会话。

```text
                          飞书
                           ↕
                  本地 Wirelark 守护进程
                       ↗    ↖
               Claude 会话  Claude 会话
                Hook + Channel  Hook + Channel
```

最重要的结果很简单：

> **Claude 会话始终保留在本地并归你所有。Wirelark 只连接会话，不拥有会话。**

Wirelark 不是终端模拟器，也不是云端托管的 Claude 运行时。进程细节、本地存储、
Hook 行为和运维操作，请参阅[安全与运维（英文）](docs/security-and-operations.md)。

## 安全与透明

通常由本地 Wirelark 守护进程负责与飞书通信；必要时，Hook 可直接发送通知作为
后备。Wirelark 只发送所配置功能需要的卡片和消息，例如会话标识、Claude 回复
摘录、验证结果和权限详情；不会发送终端数据流或完整的会话记录。

Wirelark 只接受来自已配置所有者的消息。远程消息只会发送到明确选定的会话。
远程权限批准是一项独立设置，因为它会向该飞书身份授予真实的操作权限。

Wirelark 不会启动或停止 Claude Code，不会接管终端，不会自行编辑文件，也不会
控制 Claude 的身份验证。完整的信任边界和本地数据处理方式记录在
[安全与运维（英文）](docs/security-and-operations.md)中。

## 当前限制

- **远程继续功能目前需要预览标志。** Claude Code Channels 仍处于研究预览阶段，
  Wirelark 尚未进入 Anthropic 的 Channel 允许列表。需要从飞书继续的会话目前
  必须这样启动：

  ```sh
  claude --dangerously-load-development-channels server:wirelark
  ```

  使用普通 `claude` 启动的会话仍会被发现并发送通知，但在飞书中显示为
  **Notifications only**。

- Channels 需要通过 claude.ai 或 Console API Key 进行 Anthropic 身份验证，
  目前不支持 Bedrock、Vertex 和 Foundry。Team 与 Enterprise 组织需要集中启用
  Channels。
- Claude Code 的多项选择 `AskUserQuestion` 提示目前无法通过 Channel 回答。Wirelark
  会通知你，但仍需回到原终端作答。
- 如需远程继续，电脑、Claude Code 会话、Wirelark 守护进程和网络连接都必须
  保持运行。
- WSL 与原生 Windows 属于两套独立安装。在 Windows 上，第一次消息确认会话的
  Channel 之前，远程状态会显示为 **Remote untested**。

## 项目状态

按需通知、完成总结、本地会话发现、指定会话的远程继续、可选的权限批准与拒绝、
二进制发布包安装和二维码引导均已实现。

下一步的产品方向是为选定会话提供可选的精简实时视图——不是终端流式传输，也不是
另一套 Claude Code 界面。完整设计思路请参阅
[产品体验规范（英文）](docs/product-experience-spec.md)。

## 参与贡献

```sh
mise install
mise exec -- go test ./...
mise exec -- go build -o wirelark .
```

本仓库使用 [mise](https://mise.jdx.dev/) 固定 Go 工具链版本。设置和诊断命令请
参阅[设置与配置（英文）](docs/setup.md)与
[安全与运维（英文）](docs/security-and-operations.md)。
