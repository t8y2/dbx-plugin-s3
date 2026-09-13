# S3 Browser

S3-compatible object storage plugin for DBX.

The Go Sidecar owns S3 connection lifecycle, credentials, and object operations; the Svelte workbench owns the object browser UI.

## Features

- AWS S3 and S3-compatible endpoints such as MinIO, R2, and Ceph.
- Automatic, path-style, and virtual-hosted bucket addressing.
- Separate endpoint protocol and host fields, plus an optional base path for providers such as UFile.
- Long-lived access keys plus optional STS session tokens.
- Root and nested object listing with cursor pagination, image/video/audio/Markdown/Word previews, writes, directory markers, recursive deletes, and rename.
- Optimistic write protection with ETags and a 4 MiB inline payload limit enforced by the DBX filesystem contract.

Object contents are transferred through DBX's bounded filesystem API. The plugin is not a general-purpose multipart upload client; large-object transfer requires a future DBX streaming capability.

For UFile S3-compatible storage, configure the endpoint protocol as `https`, endpoint host as `s3-cn-bj.ufileos.com`, bucket as `cogagent`, region as `cn-bj`, and base path as `/cogagent_annotation/data`. The base path is an object prefix: DBX exposes it as the connected filesystem root and never includes it in the DBX-facing `s3:` URIs.

Keep access keys and secret keys in DBX's connection secret storage. Do not put them in this repository, manifests, issue descriptions, logs, or test fixtures.

## Develop

Edit `src/App.svelte`, then build the Svelte workbench. The browser development host is available from the current DBX plugin SDK checkout:

```bash
cd /path/to/dbx
npm ci --prefix plugins/sdk/dev-host
npm run build --prefix plugins/sdk/dev-host
cd /path/to/dbx-plugin-s3
npm install
npm run build
DBX_PLUGIN_SDK_ROOT=/path/to/dbx \
  DBX_PLUGIN_DEV_RUNTIME=/path/to/dbx/plugins/sdk/dev-host/dist/runtime.mjs \
  cargo run --manifest-path plugins/sdk/cli/Cargo.toml -- dev --path /path/to/dbx-plugin-s3 --port 5190
```

The browser host does not start the Go Sidecar. Use the real DBX host for connection lifecycle and final integration testing. `.dbx-dev/` stores local development data and must not be committed or packaged.

The Go backend uses the DBX Go SDK from the plugin framework branch. If that SDK is only available in a local DBX checkout, point the CLI at that checkout while developing or packaging:

```bash
DBX_PLUGIN_SDK_ROOT=/path/to/dbx dbx-plugin package .
```

Backend-only checks can run without DBX:

```bash
cd backend
go test ./...
go vet ./...
```

Then package the plugin:

```bash
dbx-plugin package .
```

The command builds the Go Sidecar, stages `manifest.json`, `assets/`, and the compiled Svelte `ui/`, then writes a target-specific `.dbxp` candidate plus matching `.artifact.json` into `dist/`.

## Release

1. Update the plugin version, commit it, and publish a GitHub Release with a new immutable tag.
2. The `dbx-store` catalog automation discovers the published release and opens or updates a catalog PR automatically.
3. After review, a DBX Store maintainer runs the protected signing workflow; it writes the signed catalog data back to that PR.
4. A maintainer merges the PR after all checks pass.

The plugin repository does not need marketplace automation secrets. A `dbx-store` maintainer registers the public plugin repository in the store automation registry; the store-side GitHub App discovers releases and creates catalog PRs.

An optional `.dbx-store.json` can provide marketplace-only fields such as the icon, tags, license, and localizations for a first submission or an intentional listing update. Existing plugin version updates do not require this file.

Source code and the unsigned candidate stay in this repository. DBX users install the DBX Store-signed asset exposed by the official catalog.

Do not submit ordinary plugin source to `t8y2/dbx`; that repository accepts plugin host, SDK, CLI, schema, documentation, and official-example changes.
