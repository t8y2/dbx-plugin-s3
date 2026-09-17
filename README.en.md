# S3 Browser

English | [简体中文](README.md)

S3 Browser is a plugin for [DBX](https://github.com/t8y2/dbx), the open-source database workbench. It turns S3-compatible object storage — AWS S3, MinIO, Cloudflare R2, UFile, Ceph, and more — into a first-class filesystem inside DBX: browse buckets and folders, stream previews, upload and download large objects, package folders as ZIP archives, and share files with presigned links.

[![CI](https://github.com/t8y2/dbx-plugin-s3/actions/workflows/ci.yml/badge.svg)](https://github.com/t8y2/dbx-plugin-s3/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/t8y2/dbx-plugin-s3)](https://github.com/t8y2/dbx-plugin-s3/releases)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

## Features

**Connections**

- AWS S3 and S3-compatible endpoints: MinIO, Cloudflare R2, UFile, Ceph, and anything that speaks the S3 API.
- Automatic, path-style, and virtual-hosted bucket addressing.
- Optional region and base path: mount an object prefix such as `/project/assets` as the filesystem root, or `/` for the whole bucket.
- Long-lived access keys plus optional STS session tokens.
- Leave the bucket field empty to discover and list all buckets after connecting.

**Browsing**

- Folder tree sidebar, breadcrumbs, and directory-first object listings.
- Cursor pagination for large buckets.
- Directory markers for folder creation and renames.

**Transfers**

- Streaming previews over the DBX plugin stream API (256 KiB chunks) — the UI never buffers an entire object in one JSON-RPC response.
- Large uploads through the framed DBX binary channel, written as S3 multipart uploads; the inline write method stays capped at 4 MiB.
- Uploads use the actual file size, a bounded sending window, and backend acknowledgements. Browse other folders or cancel without changing the upload destination or connection. The UI uses binary transfers above 256 KiB.
- Upload entire folders while preserving the selected root folder, nested paths, Unicode names, and zero-byte files. Browser directory selection omits empty folders. An error stops the remaining uploads; completed files are retained.
- ZIP downloads for folders and multi-object selections, with transfer progress.
- Optimistic write protection with ETags.

**Object versions**

- Same-name uploads create a new version when bucket versioning is enabled. Unversioned buckets, suspended versioning, and unverified versioning configurations continue to reject duplicate uploads.
- Use **Versions** in the file preview toolbar or context menu to inspect version IDs, timestamps, sizes, the latest version, and delete markers. Listings are limited to the first 1,000 versions with an explicit truncation notice; restoring or deleting historical versions is not included.
- Duplicate uploads require permission to read bucket versioning; version history requires permission to list object versions. The plugin never enables or changes bucket versioning automatically and conservatively rejects automatic duplicate uploads when versioning exclusions are configured.

**Sharing**

- Presigned links for any file — valid for 1 hour, 24 hours, or 7 days. Signing happens locally in the sidecar and never touches bucket ACLs.

**Previews**

- Text and Markdown up to 2 MiB, Word documents and audio/video up to 4 MiB, spreadsheets up to 2 MiB. Preview limits only gate what the workbench renders; they never truncate or modify remote objects.

## Requirements

- DBX 0.6.12 or later (the first release with the plugin marketplace).
- macOS (Apple Silicon or Intel), Windows, or Linux — releases are built for all six targets.
- An S3-compatible account with an access key pair.

## Install

Install from the DBX plugin marketplace: open DBX, browse plugins, and install **S3 Browser**. DBX users install the DBX Store-signed asset published by the official catalog; nothing needs to be built manually.

## Configure a connection

| Field | Required | Description |
| --- | --- | --- |
| Connection name | yes | Display name in DBX. |
| Endpoint protocol | yes | `https` or `http`. |
| Endpoint host | yes | Host name only, no path (e.g. `s3.amazonaws.com`). |
| Region | no | Empty falls back to `us-east-1`; MinIO accepts any value. |
| Base path | no | Object prefix mapped to the filesystem root; `/` uses the whole bucket. |
| Addressing style | yes | Automatic, path-style, or virtual-hosted. |
| Bucket | no | Leave empty to list all buckets after connecting. |
| Access key / Secret key | yes | Stored in DBX connection secret storage. |
| Session token | no | Only for STS or temporary IAM role credentials; leave empty otherwise. |

Provider quick reference:

| Provider | Protocol | Endpoint host | Region | Addressing |
| --- | --- | --- | --- | --- |
| AWS S3 | `https` | `s3.amazonaws.com` | e.g. `us-west-2` | auto |
| MinIO | `http`/`https` | `localhost:9000` | any | path |
| Cloudflare R2 | `https` | `<account>.r2.cloudflarestorage.com` | `auto` | path |
| UFile | `https` | e.g. `s3-cn-bj.ufileos.com` | e.g. `cn-bj` | path |

> **Cloudflare R2 note:** set the region to `auto`. An empty region or `us-east-1` results in a 400 error from R2.

For UFile-style providers, combine the endpoint with a base path such as `/project/assets`; DBX exposes that prefix as the connected root and never includes it in the `s3:` URIs it shows.

## Architecture

- **Go sidecar** (`backend/`) — owns the S3 connection lifecycle, credentials, and object operations. It speaks stdio-framed JSON-RPC with the DBX host and streams object contents in bounded 256 KiB `host.stream.*` events.
- **Svelte workbench** (`src/`, built into `ui/`) — the object browser UI: folder tree, listings, previews, transfers, and sharing.

Other plugin authors can reuse the streaming pattern: after declaring the `host.events` permission, `window.dbxPlugin.stream()` returns `{ stream, metadata }` where `stream` is a browser `ReadableStream<Uint8Array>`; cancelling the reader closes the stream via the conventional `filesystem/stream/close` method (override with `options.closeMethod`).

## Development

Prerequisites: Go 1.22+, Node.js 22, and pnpm.

```bash
npm install
npm test             # Upload paths, flow control, and cancellation
npm run build        # build the Svelte workbench into ui/

cd backend
go test ./...
go vet ./...
```

`TestLocalS3UploadLifecycle` is an opt-in real S3 check restricted to an isolated local MinIO server (`127.0.0.1:port`). Set `DBX_S3_LOCAL_ENDPOINT`, `DBX_S3_LOCAL_ACCESS_KEY`, and `DBX_S3_LOCAL_SECRET_KEY`, then run `go test -run TestLocalS3UploadLifecycle -v`. It creates and cleans up a dedicated bucket. `DBX_S3_LARGE_TEST_BYTES` overrides the default approximately 36 MiB upload size.

For the full loop with hot reload, run the dev host from a DBX SDK checkout:

```bash
cd /path/to/dbx
npm ci --prefix plugins/sdk/dev-host
npm run build --prefix plugins/sdk/dev-host
cd /path/to/dbx-plugin-s3
DBX_PLUGIN_SDK_ROOT=/path/to/dbx \
  DBX_PLUGIN_DEV_RUNTIME=/path/to/dbx/plugins/sdk/dev-host/dist/runtime.mjs \
  cargo run --manifest-path plugins/sdk/cli/Cargo.toml -- dev --path /path/to/dbx-plugin-s3 --port 5190
```

The browser dev host does not start the Go sidecar; use the real DBX host for connection lifecycle and final integration testing. `.dbx-dev/` holds local development data and must not be committed or packaged.

Package a candidate `.dbxp` (plus matching `.artifact.json`) into `dist/`:

```bash
dbx-plugin package .
```

If the DBX Go SDK in `backend/go.mod` is only available in a local DBX checkout, point the CLI at it with `DBX_PLUGIN_SDK_ROOT=/path/to/dbx dbx-plugin package .`.

## Release (maintainers)

1. Update the plugin version (kept in sync between `manifest.json` and `backend/main.go`; CI rejects mismatches), commit, and publish a GitHub Release with a new immutable tag.
2. The release workflow builds all six platform targets and attaches the artifacts.
3. The `dbx-store` catalog automation discovers the release and opens or updates a catalog PR.
4. After review, a DBX Store maintainer runs the protected signing workflow and merges the PR.

The plugin repository needs no marketplace automation secrets. An optional `.dbx-store.json` can carry marketplace-only fields (icon, tags, license, localizations) for a first submission or an intentional listing update; ordinary version updates do not need it.

## Contributing

Bug reports and pull requests for this plugin belong in [t8y2/dbx-plugin-s3](https://github.com/t8y2/dbx-plugin-s3). Do not submit plugin source to [t8y2/dbx](https://github.com/t8y2/dbx); that repository accepts plugin host, SDK, CLI, schema, documentation, and official-example changes only.

## Security

Access keys, secret keys, and session tokens live in DBX's connection secret storage. Never put them in this repository, manifests, issue reports, logs, or test fixtures.

## License

[Apache-2.0](LICENSE). Source code and unsigned build candidates stay in this repository; DBX users install the DBX Store-signed assets from the official catalog.
