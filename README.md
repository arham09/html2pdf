# html2pdf

A pure-Go library for rendering **static HTML/CSS business documents into searchable, selectable PDF**: invoices, statements, receipts, and financial reports. The supported profile includes multipage tables, PNG/JPEG images, embedded fonts, bounded Flexbox, and explicit-column CSS Grid.

Rendering runs inside your Go process. It does not launch a browser or another binary, execute JavaScript, discover system fonts, or fetch external resources automatically.

html2pdf implements a defined print profile, not full browser compatibility. The public API follows semantic versioning.

## Installation

html2pdf requires **Go 1.27 or newer** and builds with `CGO_ENABLED=0`.

```sh
go get github.com/arham09/html2pdf
```

```go
import "github.com/arham09/html2pdf"
```

## Quick start

This complete program renders an invoice, reserves space for a page footer, and saves the PDF only after rendering succeeds:

```go
package main

import (
    "bytes"
    "context"
    "log"
    "os"
    "time"

    "github.com/arham09/html2pdf"
)

func main() {
    if err := run(); err != nil {
        log.Fatal(err)
    }
}

func run() error {
    page := html2pdf.A4()
    margins := html2pdf.MillimeterMargins(15, 15, 15, 15)
    engine, err := html2pdf.New(html2pdf.Options{
        PageSize:  &page,
        Margins:   &margins,
        StrictCSS: true,
        Footer: html2pdf.HeaderFooter{
            HTML:     `<div style="font-size:8pt;line-height:12pt">Page {page} of {pages}</div>`,
            HeightPt: 18,
        },
        Limits: html2pdf.Limits{
            MaxPages:       100,
            MaxOutputBytes: 16 << 20,
        },
    })
    if err != nil {
        return err
    }

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    var output bytes.Buffer
    result, err := engine.Render(ctx, html2pdf.Document{
        HTML: `<h1>Invoice INV-0001</h1>
<p>PT Contoh Indonesia · café é</p>
<table>
  <thead><tr><th>Description</th><th class="amount">Amount</th></tr></thead>
  <tbody><tr><td>Service</td><td class="amount">Rp 1.234,56</td></tr></tbody>
</table>`,
        Stylesheets: []string{`
            body { font-size:10pt; line-height:14pt }
            h1 { font-size:18pt; line-height:22pt; margin:0 0 12pt }
            table { width:100%; table-layout:fixed; border-collapse:collapse }
            th, td { padding:4pt; border:1pt solid #808080 }
            .amount { text-align:right }
        `},
    }, &output)
    // Diagnostics are available even when preparation/layout returns an error.
    for _, warning := range result.Warnings {
        log.Printf("PDF warning: %s (source %d): %s",
            warning.Code, warning.SourceID, warning.Message)
    }
    if err != nil {
        return err // Discard the buffer on any render error.
    }
    if err := os.WriteFile("invoice.pdf", output.Bytes(), 0644); err != nil {
        return err
    }
    log.Printf("Rendered %d page(s)", result.Pages)
    return nil
}
```

For default options and HTML only, the convenience function returns bytes instead:

```go
pdfBytes, err := html2pdf.Render(ctx, `<p>Hello Indonesia · Rp 1.234,56</p>`)
```

It returns `nil` bytes on failure. Use `New` and `Engine.Render` when you need stylesheets, options, page counts, or warnings.

## What your application supplies

The library accepts the final static HTML, ordered CSS strings, and any registered fonts or asset resolver. Your application owns template generation, escaping, business calculations, currency/date formatting, storage, and HTTP responses. For Go templates, `html/template` can generate the HTML before rendering; scripts cannot populate rows or calculate totals inside the engine.

| Capability | Current behavior |
|---|---|
| Text | Embedded searchable fonts; mixed regular/bold/italic styles; Latin/Indonesian text, currency and supported punctuation |
| Blocks and inline content | Paragraph wrapping, lists, inline-blocks, solid backgrounds/borders, PNG/JPEG images |
| Tables | Fixed and limited-auto columns, wrapping, colspan, separate/collapsed borders, repeated `thead` |
| Pagination | Paragraph line boundaries, table row boundaries, explicit page breaks, reserved headers/footers and page counters |
| Flexbox | Row/column, basis, grow/shrink, gaps, alignment and simple row wrap; complete containers remain atomic |
| Grid | Explicit length/%/fr columns, basic placement/spans, item alignment and row-based pagination |
| HTML/scripts | Static normalization; script/template/iframe/embedded active content is excluded; scripts never execute |

Unsupported features include browser-complete HTML/CSS, JavaScript, HTML table `rowspan`, nested tables, nested/mixed Flex/Grid, SVG/GIF/WebP, arbitrary positioning/transforms, and PDF signing/encryption/forms. Grid has its own supported row spans; these do not imply HTML table `rowspan` support.

## Images and external resources

Use `<img>` with **PNG or JPEG** bytes. PNG transparency is supported. Two resource routes are available:

1. A bounded `data:` URI, using base64 or percent encoding; no resolver is needed.
2. An exact reference handled by your `AssetResolver`, including application asset names, URLs, or file references.

A URL or local path in HTML does **not** trigger an automatic HTTP request or file read. Without a resolver, external images fail. Linked stylesheets, CSS imports, remote font URLs, and system-font discovery are not asset-loading shortcuts: provide CSS through `Document.Stylesheets` and custom fonts through `Options.Fonts`.

### Image sizing and layout

```html
<div style="padding:6pt;border:1pt solid #808080">
  <img src="asset:logo" style="width:90pt">
</div>
```

- Natural size uses 96 CSS pixels per inch: one pixel is `0.75pt`. HTML `width`/`height` attributes use pixels; CSS dimensions override them.
- Set one dimension to preserve the image's aspect ratio. Setting both dimensions scales to that size; it does not crop.
- Percentage image heights are unsupported. Oversized images fail with `ErrUnbreakableContent` instead of being silently fitted or clipped.
- Put padding, margins, borders, and backgrounds on a block wrapper, rather than on the image itself.
- In Flexbox or Grid, make that wrapper the item; a direct image item is outside the supported profile.

### An allowlisted in-memory resolver

This file can be added beside the quick-start program. It serves only preloaded assets and performs no network/filesystem access during rendering:

```go
package main

import (
    "bytes"
    "context"
    "io"

    "github.com/arham09/html2pdf"
)

type MemoryAssets map[string][]byte

func (assets MemoryAssets) Resolve(ctx context.Context, reference string) (io.ReadCloser, error) {
    if err := ctx.Err(); err != nil {
        return nil, err
    }
    data, ok := assets[reference]
    if !ok {
        return nil, html2pdf.ErrMissingResource
    }
    return io.NopCloser(bytes.NewReader(data)), nil
}

func engineWithLogo(logoPNG []byte) (*html2pdf.Engine, error) {
    return html2pdf.New(html2pdf.Options{
        StrictCSS: true,
        Resolver: MemoryAssets{
            "asset:logo": bytes.Clone(logoPNG),
        },
    })
}
```

Load the trusted logo bytes at application startup, or embed them with Go's `embed` package, then use `engineWithLogo` and the HTML above. Keep resolver maps and their byte slices immutable while renders run. The engine owns and closes the readers returned by the resolver; your application owns and closes the output writer.

If you implement network/file resolution, your resolver must enforce its own scheme/host/path allowlist, obey context cancellation, and support concurrent calls. Encoded references, decoded images, pixels, fonts, and repeated/trial work share per-render budgets. Base64 has encoding overhead; both the encoded reference and decoded payload consume asset budget.

## CSS, fonts, and page behavior

Author CSS is applied in this order: embedded stylesheets in document order, then `Document.Stylesheets` in slice order. Inline declarations have higher specificity within the same importance level; `!important`, selector specificity, and source order still apply. Only the documented static selectors and print rules are supported.

`StrictCSS: true` rejects unsupported declarations/selectors instead of silently accepting a template that depends on them. The default permissive mode warns and discards safely ignorable unsupported CSS. Critical layout features still fail in either mode, including unsupported positioning, display values, Grid/Flex sizing/placement, and other documented combinations. Inspect `Result.Warnings`; permissive mode is not full CSS support.

Bundled Noto Sans includes real regular, bold, italic, and bold-italic faces. No font installation is required. Register custom static TrueType faces through `Options.Fonts`, including each weight/style you use. The engine copies those bytes at construction; font registration is frozen. Missing faces/glyphs return errors. General complex-script shaping/fallback, variable/color/CFF fonts, and URL-based font loading are outside the current profile.

Page configuration resolves as **explicit API options → supported CSS `@page` → defaults**. Defaults are A4 with 15 mm margins. Nil `PageSize`/`Margins` pointers mean omitted; `&html2pdf.Margins{}` explicitly requests zero margins. Units use points internally (`1pt = 1/72 inch`, `1px = 0.75pt`).

Header/footer `HeightPt` reserves space inside the page margins and reduces the usable body. Nonempty decoration HTML requires a positive reserved height; overflow fails rather than overlapping body content. `{page}` and `{pages}` are substituted in decoration text nodes after pagination. Do not use `position:fixed` to create running content; use `Options.Header`/`Footer`.

Some deliberate print-profile differences matter when authoring templates: margins do not collapse; negative margins and fixed ordinary block heights are rejected; line boxes expand to fit actual font metrics; unsupported layout does not become browser-equivalent just because HTML/CSS parses.

## Pagination and overflow contract

| Content | Legal behavior |
|---|---|
| Paragraph | Breaks between measured lines |
| Table | Breaks between complete rows; `thead` repeats on continuation pages |
| Image / inline-block | Moves intact; oversized atomic content fails |
| Flex container | Entire container moves intact, including all wrapped lines and items |
| Grid | Breaks between rows; rows connected by a spanning item form one indivisible group |
| Header/footer | Must fit the reserved fixed region |

`break-before:page` and `break-after:page` work on supported non-atomic body blocks. Breaks inside atomic items are unsupported. `break-inside:avoid` is a preference when a region can fit a fresh page; an oversized avoid region may be relaxed at legal boundaries with a warning. It never authorizes splitting an indivisible row/item/group.

An indivisible footprint larger than an empty usable page returns `ErrUnbreakableContent`. The engine does not silently drop text, split oversized table rows, crop images, or shrink the entire document. Explicit Grid placement can intentionally change visual/page order; keep logical source order consistent with your intended reading order for predictable copy/extraction behavior.

## Errors, output ownership, and concurrency

Use `errors.Is` for error categories, including wrapped errors:

| Error | Meaning / caller action |
|---|---|
| `ErrInvalidOptions` | Correct engine/page/font configuration |
| `ErrInvalidInput` | Correct malformed/invalid input or geometry |
| `ErrUnsupportedFeature` | Change the template to the supported profile |
| `ErrMissingResource` | Supply a permitted asset, font face, or supported glyph |
| `ErrUnbreakableContent` | Reduce/restructure the indivisible content or increase usable page space |
| `ErrLimitExceeded` | Inspect the quota and reduce the input, or deliberately adjust limits |
| Context / writer errors | Handle cancellation/deadline or the original output I/O failure |

For quota details, use `errors.As` with a pointer to `*html2pdf.LimitError`:

```go
var limit *html2pdf.LimitError
if errors.As(err, &limit) {
    log.Printf("PDF quota %s: limit=%d used=%d requested=%d",
        limit.Resource, limit.Limit, limit.Used, limit.Requested)
}
```

Input, resource, layout, page, decoration, and exact serialized-size failures are preflighted before PDF writes. **Writer errors or cancellation during output can still leave partial bytes.** Buffer output and discard it on error before publishing a file or sending successful HTTP headers, as in the quick start. Filesystem/HTTP write failures remain the caller's responsibility. The byte-returning convenience API exposes bytes only on success.

Reuse one engine concurrently; construction copies page settings and custom font data, and each render owns its mutable state and quotas. A supplied resolver must be concurrency-safe. Do not mutate caller stylesheet slices, resolver maps, or asset bytes during a render. Context cancellation is cooperative: the engine cannot forcibly interrupt a resolver/writer that blocks while ignoring context, or every instruction inside dependencies.

With pinned toolchain/dependencies, identical HTML/CSS, options, fonts, and deterministic resolver bytes produce identical PDF bytes. Upgrading versions may change output; this is not cross-version PDF canonicalization.

## Resource limits

`Limits{}` and zero-valued individual fields select defaults; zero does not mean unlimited. Negative/unsafe limits fail construction. Selected defaults:

| Budget | Default |
|---|---:|
| HTML / aggregate CSS | 1 MiB / 1 MiB |
| DOM nodes / depth | 200,000 / 128 |
| Layout work / glyphs | 10,000,000 / 2,000,000 |
| Pages / output | 1,000 / 64 MiB |
| Per-asset bytes / aggregate asset bytes | 8 MiB / 32 MiB |
| Aggregate image pixels / decoded image reservation | 8,000,000 / 64 MiB |

Budgets include trial shaping, intermediate measurements, repeated page decorations, and resource accounting, not only final visible output. Grid additionally has a profile bound of 64 explicit columns and 1,000 content rows. Limits are not a total process-memory or hard execution-time guarantee; bound concurrent large renders at the application/service layer. `html2pdf.DefaultLimits()` returns every default.

## Examples

From the repository root:

| Command | Output |
|---|---|
| `go run ./examples/basic` | `document.pdf`: the smallest program, using `html2pdf.Render` |
| `go run ./examples/invoice` | `invoice.pdf`: Flexbox masthead, 201-row table with repeated header, page-number footer |
| `go run ./examples/report` | `report.pdf`: 60 CSS Grid cards that paginate between grid rows |
| `go run ./examples/webserver` | HTML editor with PDF preview and an HTTP API at <http://localhost:8080> |

The invoice and report examples generate their HTML with `html/template` and accept `-out` to change the output file. The webserver is a local demo, not an authenticated production deployment; see its [README](examples/webserver/README.md) and [OpenAPI contract](examples/webserver/openapi.json).

## Development

```sh
make verify   # build, vet and test with CGO_ENABLED=0
make race     # tests under the race detector
make fuzz     # 15 seconds per fuzz target
make bench    # render benchmarks
```

## License

html2pdf is released under the [MIT License](LICENSE). The bundled Noto Sans fonts use the [SIL Open Font License 1.1](internal/font/assets/OFL.txt).
