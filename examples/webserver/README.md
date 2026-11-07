# Webserver example

A small HTTP server that uses the public `html2pdf` package. Embedded assets provide an HTML editor and a PDF preview. The engine does not run document scripts or fetch external assets, and this example provides no asset resolver.

From the repository root:

```sh
go run ./examples/webserver
```

Open <http://localhost:8080>. The server listens on `:8080`; use `-addr=127.0.0.1:8080` to listen on loopback only.

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `:8080` | HTTP listen address |
| `-max-concurrent` | `1` | Maximum concurrent renders (1..16) |
| `-render-timeout` | `30s` | Render deadline (up to 5m) |

This is a local demonstration server. It has no authentication. A public deployment needs its own access control, TLS and admission limits.

## HTTP contract

[openapi.json](openapi.json) describes the API.

`GET /healthz` returns the runtime identity, for example `{"status":"ok","os":"linux","arch":"arm64","go":"go1.27.0"}`.

`POST /render` accepts UTF-8 `application/json` with a required `html` string and an optional ordered `stylesheets` string array. Unknown fields and trailing JSON values are rejected. Empty HTML is valid and renders a blank page.

```sh
curl -sS http://127.0.0.1:8080/render \
  -H 'Content-Type: application/json' \
  --data '{"html":"<h1>Hello Indonesia</h1><p>Rp 1.234,56 · café</p>"}' \
  -o document.pdf
```

Success is `application/pdf` with `Content-Disposition: inline; filename="document.pdf"` and the `X-PDF-Pages` / `X-PDF-Warnings` count headers. The server buffers the complete PDF before it sends it, so a render error never sends partial PDF bytes.

Errors have the shape `{"error":{"code":"...","message":"..."}}`:

| Status | Cause |
|---|---|
| 400 | Invalid JSON or missing required field |
| 404 / 405 | Unknown route or method |
| 408 | Request cancelled |
| 413 | Request body or render limit exceeded |
| 415 | Wrong media type or encoding |
| 422 | Invalid, unsupported or unbreakable document, or missing font/asset |
| 503 | All render slots busy (`Retry-After: 1`) |
| 504 | Render deadline exceeded |
| 500 | Unexpected failure |

Messages do not echo the submitted HTML or internal error details.

## Limits

The request body is capped at 2 MiB. The render limits are smaller than the library defaults: HTML 256 KiB, aggregate CSS 128 KiB, 100 pages, 16 MiB output, 20,000 DOM nodes with depth 64, 5,000 table rows, 25,000 cells, 200,000 glyphs and paint operations, 2,000,000 layout work units, and bounded images and assets.

Server timeouts: read header 5s, read request 15s, idle 60s, and maximum header size 16 KiB. SIGTERM or an interrupt cancels active renders and shuts the server down.
