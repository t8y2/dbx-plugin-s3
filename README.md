# S3 Browser

[English](README.en.md) | 简体中文

S3 Browser 是 [DBX](https://github.com/t8y2/dbx)（开源数据库工作台）的插件。它把 S3 兼容的对象存储——AWS S3、MinIO、Cloudflare R2、UFile、Ceph 等——变成 DBX 里的一等文件系统：浏览存储桶和目录、流式预览、上传下载大文件、目录打包 ZIP 下载、用预签名链接分享文件。

[![CI](https://github.com/t8y2/dbx-plugin-s3/actions/workflows/ci.yml/badge.svg)](https://github.com/t8y2/dbx-plugin-s3/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/t8y2/dbx-plugin-s3)](https://github.com/t8y2/dbx-plugin-s3/releases)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

## 功能

**连接**

- AWS S3 及各类 S3 兼容端点：MinIO、Cloudflare R2、UFile、Ceph 等。
- 自动、路径风格、虚拟主机三种寻址方式。
- 区域和基础路径均可选：可把 `/project/assets` 这样的对象前缀挂载为文件系统根，`/` 表示整个存储桶。
- 长期访问密钥，外加可选的 STS 会话令牌。
- 存储桶留空时，连接后自动发现并列出所有存储桶。

**浏览**

- 目录树侧边栏、面包屑导航、目录优先的对象列表。
- 大存储桶游标分页。
- 目录标记，支持创建目录和重命名。

**传输**

- 通过 DBX 插件流式 API 做预览（256 KiB 分块）——UI 从不会把整个对象塞进一次 JSON-RPC 响应。
- 大文件上传走 DBX 分帧二进制通道，由后端以 S3 分段上传写入；内联写方法保持 4 MiB 上限。
- 目录和多选对象可打包 ZIP 下载，带传输进度。
- 基于 ETag 的乐观写保护。

**分享**

- 任意文件可生成预签名链接——有效期 1 小时、24 小时或 7 天。签名在本地边车进程内完成，绝不改动存储桶 ACL。

**预览**

- 文本和 Markdown 最大 2 MiB，Word 文档和音视频最大 4 MiB，电子表格最大 2 MiB。预览限制只约束工作台的渲染，不会截断或修改远端对象。

## 环境要求

- DBX 0.6.12 或更高版本（首个内置插件市场的版本）。
- macOS（Apple Silicon 或 Intel）、Windows 或 Linux——发布产物覆盖全部六个平台目标。
- 一套带有访问密钥对的 S3 兼容账号。

## 安装

从 DBX 插件市场安装：打开 DBX，浏览插件列表，安装 **S3 Browser**。用户安装的是官方目录发布的、经 DBX Store 签名的产物，无需手动构建。

## 配置连接

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| 连接名称 | 是 | 在 DBX 中显示的名称。 |
| 端点协议 | 是 | `https` 或 `http`。 |
| 端点主机 | 是 | 只填主机名，不含路径（如 `s3.amazonaws.com`）。 |
| 区域 | 否 | 留空时回退到 `us-east-1`；MinIO 接受任意值。 |
| 基础路径 | 否 | 映射为文件系统根的对象前缀；`/` 表示整个存储桶。 |
| 寻址方式 | 是 | 自动、路径风格或虚拟主机。 |
| 存储桶 | 否 | 留空时连接后列出所有存储桶。 |
| 访问密钥 / 秘密密钥 | 是 | 保存在 DBX 连接密钥存储中。 |
| 会话令牌 | 否 | 仅用于 STS 或临时 IAM 角色凭据；其他情况留空。 |

各服务商速查：

| 服务商 | 协议 | 端点主机 | 区域 | 寻址 |
| --- | --- | --- | --- | --- |
| AWS S3 | `https` | `s3.amazonaws.com` | 如 `us-west-2` | 自动 |
| MinIO | `http`/`https` | `localhost:9000` | 任意 | 路径 |
| Cloudflare R2 | `https` | `<account>.r2.cloudflarestorage.com` | `auto` | 路径 |
| UFile | `https` | 如 `s3-cn-bj.ufileos.com` | 如 `cn-bj` | 路径 |

> **Cloudflare R2 注意：** 区域必须填 `auto`。留空或填 `us-east-1` 会导致 R2 返回 400 错误。

UFile 这类服务商可以配合基础路径使用，例如把端点和 `/project/assets` 前缀组合；DBX 会把该前缀暴露为连接后的根，且不会把它计入面向用户的 `s3:` URI。

## 架构

- **Go 边车进程**（`backend/`）——负责 S3 连接生命周期、凭据和对象操作。它与 DBX 宿主通过 stdio 分帧 JSON-RPC 通信，以 256 KiB 有界的 `host.stream.*` 事件流式输出对象内容。
- **Svelte 工作台**（`src/`，构建产物在 `ui/`）——对象浏览器 UI：目录树、列表、预览、传输和分享。

其他插件作者可以复用这套流式方案：声明 `host.events` 权限后，`window.dbxPlugin.stream()` 返回 `{ stream, metadata }`，其中 `stream` 是浏览器的 `ReadableStream<Uint8Array>`；取消读取器时会按约定调用 `filesystem/stream/close` 关闭流（可用 `options.closeMethod` 覆盖）。

## 开发

前置条件：Go 1.22+、Node.js 22 和 pnpm。

```bash
npm install
npm run build        # 把 Svelte 工作台构建到 ui/

cd backend
go test ./...
go vet ./...
```

完整调试环（带热重载），从 DBX SDK 检出目录启动开发宿主：

```bash
cd /path/to/dbx
npm ci --prefix plugins/sdk/dev-host
npm run build --prefix plugins/sdk/dev-host
cd /path/to/dbx-plugin-s3
DBX_PLUGIN_SDK_ROOT=/path/to/dbx \
  DBX_PLUGIN_DEV_RUNTIME=/path/to/dbx/plugins/sdk/dev-host/dist/runtime.mjs \
  cargo run --manifest-path plugins/sdk/cli/Cargo.toml -- dev --path /path/to/dbx-plugin-s3 --port 5190
```

浏览器开发宿主不会启动 Go 边车进程；连接生命周期和最终集成测试请使用真实的 DBX 宿主。`.dbx-dev/` 存放本地开发数据，不得提交或打包。

打包候选 `.dbxp`（及配套 `.artifact.json`）到 `dist/`：

```bash
dbx-plugin package .
```

如果 `backend/go.mod` 里的 DBX Go SDK 只在本地 DBX 检出中可用，打包时用 `DBX_PLUGIN_SDK_ROOT=/path/to/dbx dbx-plugin package .` 把 CLI 指向该检出。

## 发布（维护者）

1. 更新插件版本（`manifest.json` 与 `backend/main.go` 保持同步，CI 会拒绝不一致），提交后以新的不可变标签发布 GitHub Release。
2. 发布流程构建全部六个平台目标并附上产物。
3. `dbx-store` 目录自动化会发现该 Release 并创建或更新目录 PR。
4. 审核通过后，由 DBX Store 维护者运行受保护的签名流程并合并 PR。

插件仓库本身不需要插件市场自动化密钥。可选的 `.dbx-store.json` 可以携带仅用于市场的字段（图标、标签、许可证、本地化），仅在首次提交或有意识地更新市场条目时需要；日常版本更新不需要。

## 参与贡献

本插件的缺陷报告和拉取请求请提交到 [t8y2/dbx-plugin-s3](https://github.com/t8y2/dbx-plugin-s3)。不要把插件源码提交到 [t8y2/dbx](https://github.com/t8y2/dbx)；那个仓库只接受插件宿主、SDK、CLI、schema、文档和官方示例的变更。

## 安全

访问密钥、秘密密钥和会话令牌都保存在 DBX 的连接密钥存储中。绝不要把它们放进本仓库、manifest、缺陷报告、日志或测试夹具。

## 许可证

[Apache-2.0](LICENSE)。源代码和未签名构建产物保留在本仓库；DBX 用户安装的是官方目录发布的、经 DBX Store 签名的产物。
