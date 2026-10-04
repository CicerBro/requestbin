# RequestBin

Self-hosted request inspector, in the style of requestbin.net, with an [httpbin](https://httpbin.org)-compatible tool catalog on the same server.

Create a bin, send any HTTP request to its hook URL, and inspect the method, path, headers, query, form, and body. Each bin belongs to the browser that created it.

## Run

Requires Go 1.27.1.

```sh
go run .
```

Open http://127.0.0.1:8080. The process listens on `:8080` and stores bins in `data/bins.json`.

Choose the listen address in this order:

1. `-addr` (host:port, for example `-addr 127.0.0.1:8082`)
2. `-ip` and/or `-port`, when either flag is set
3. `PORT` (`9090` or `127.0.0.1:9090`)
4. `:8080`

`-addr` cannot be combined with `-ip` or `-port`.

```sh
./requestbin -h
```

## Docker

The image is a static binary on `scratch`, published for `linux/amd64` and `linux/arm64`.

```sh
docker pull ghcr.io/cicerbro/requestbin:latest
docker run --rm -p 8080:8080 -v requestbin:/app/data ghcr.io/cicerbro/requestbin:latest
```

Or from this repo:

```sh
docker compose up --build
```

`compose.yml` publishes port 8080 and keeps `data/bins.json` in a volume mounted at `/app/data`.

## Bins

**New bin** creates an id and a secret key. The key is stored in this browser's `localStorage` and removed from the address bar. The sidebar and the captured requests are shown only when that key is present. Anyone can still send requests to the hook URL.

The hook is:

```text
http://<host>/hooks/<id>
```

Optional subpaths are captured too (`/hooks/<id>/anything`).

Give a bin a display name when you create it, or edit the name in the inspector. The sidebar shows the name, with the id underneath. A name can be up to 40 characters. Clearing the field removes it.

Clear and delete also require the key.

## Limits

| Limit | Value |
| --- | --- |
| Bin lifetime | 48 hours after creation (`BIN_TTL`, a Go duration such as `168h`) |
| Bins kept | 200 (oldest dropped; `MAX_BINS`) |
| Requests per bin | 100 (oldest dropped; `MAX_REQUESTS_PER_BIN`) |
| Request body | 1 MiB (`MAX_BODY_BYTES`, bytes; default `1048576`) |

`BIN_TTL` must be a positive duration. `MAX_BINS`, `MAX_REQUESTS_PER_BIN`, and `MAX_BODY_BYTES` must be positive integers. Unset variables keep these defaults.

## HTTP tools

`/tools` is a catalog of the httpbin-compatible endpoints mounted at `/httpbin` (for example `/httpbin/get`, `/httpbin/status/418`, `/httpbin/headers`).

## Todo

Replace `data/bins.json` with a SQLite database in WAL mode (`PRAGMA journal_mode=WAL`). Use `modernc.org/sqlite` so the image stays a static binary on `scratch`. Keep the same caps and TTL. Load one bin at a time instead of the whole file, and keep a global byte budget so a public hook cannot fill the disk up to `MAX_BINS × MAX_REQUESTS_PER_BIN × MAX_BODY_BYTES`.

Two tables: `bins` (`id`, `key`, `name`, `created`) and `requests` (`id`, `bin_id`, `method`, `path`, `timestamp`, `remote_addr`, `content_type`, `content_length`, `headers`, `query`, `body`, `form`). Store `headers`, `query`, and `form` as JSON. Store `body` as a blob.

Indexes:

- `bins(created)` for the TTL sweep and for dropping the oldest bins past `MAX_BINS`.
- `requests(bin_id, timestamp)` for the inspector (one bin, newest first) and for dropping the oldest requests past `MAX_REQUESTS_PER_BIN`.
- `requests(bin_id)` is covered by that composite index; do not add a second one.
