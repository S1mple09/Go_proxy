# go_proxy - 高性能代理池管理系统

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)]

## 项目介绍

go_proxy 是一个用 Go 语言开发的高性能代理池管理系统，支持自动抓取、验证、筛选和切换代理，提供直观的 GUI 界面进行操作和监控。本项目从 Python 版本重构而来，保留所有核心功能并提升性能和稳定性。

## ✨ 核心功能

- 🕵️‍♂️ **多源代理抓取**：自动从多个网站抓取公开代理
- ✅ **智能验证系统**：检测代理延迟、速度和匿名级别
- 🔄 **动态代理轮换**：支持多种轮换策略，自动避开失效代理
- 🖥️ **现代化 GUI 界面**：使用 Fyne 框架构建，支持中文显示
- 🚀 **高性能转发**：基于 SOCKS5 协议的代理服务
- ⚡ **并发处理**：多线程代理验证和连接管理
- 🌍 **地理位置识别**：显示代理服务器所在地区

## 📋 系统要求

- Go 1.18+ 环境
- Windows/macOS/Linux 操作系统
- 网络连接（用于获取代理和验证）

## 🚀 快速开始

### 1. 安装依赖

```bash
# 克隆仓库
git clone https://github.com/S1mple/go_proxy.git
cd go_proxy

# 初始化模块并安装依赖
go mod init go_proxy
go mod tidy
```

### 2. 字体配置（解决中文显示）

```bash
# 创建主题目录
mkdir -p theme

# 下载中文字体并放入 theme 目录
# 可使用系统字体，例如 Windows 系统的微软雅黑
copy C:\Windows\Fonts\msyh.ttc theme\font.ttf

# 生成字体绑定文件
fyne bundle -pkg theme -o theme/bundled.go theme/font.ttf
```

### 3. 构建与运行

```bash
# 构建应用
go build -o go_proxy.exe main.go

# 运行应用
./go_proxy.exe
```

## 📱 使用界面

应用启动后将显示主界面，包含以下功能区域：

- **代理列表**：显示所有可用代理及其性能指标
- **控制面板**：启动/停止代理服务，设置端口
- **筛选面板**：按延迟、速度、协议等条件筛选代理
- **日志视图**：显示系统运行状态和错误信息

## 📁 项目结构

```
go_proxy/
├── checker/       # 代理验证模块
├── fetcher/       # 代理抓取模块
├── proxy/         # 代理核心数据结构
├── server/        # SOCKS5代理服务
├── ui/            # GUI界面实现
├── theme/         # 主题和资源文件
├── cmd/           # 应用入口
├── go.mod         # 依赖管理
└── README.md      # 项目文档
```

## ⚙️ 配置选项

应用支持通过配置文件 `config.json` 自定义以下参数：

- 代理抓取频率
- 验证超时设置
- 默认监听端口
- 代理筛选条件

## 🤝 贡献指南

1. Fork 本仓库
2. 创建特性分支 (`git checkout -b feature/amazing-feature`)
3. 提交更改 (`git commit -m 'Add some amazing feature'`)
4. 推送到分支 (`git push origin feature/amazing-feature`)
5. 打开 Pull Request

## 📄 许可证

本项目采用 MIT 许可证 - 详情参见 LICENSE 文件

## 📧 联系我们

如有问题或建议，请提交 Issue 或联系: ccav2270@gmail.com

---

*项目状态：活跃开发中*

