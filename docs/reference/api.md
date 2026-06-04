---
title: API & godoc
permalink: /reference/api
createTime: 2026/06/03 12:00:00
---

These docs and the godoc are two different things, on purpose.

The Ginkgo project draws this line well, and prose follows it. The narrative documentation (the tutorials, the how-to guides, the explanation pages you've been reading) teaches you how the pieces fit and why the library is shaped the way it is. The godoc is the precise, generated reference for every exported symbol: the exact signature, the type constraints, the doc comment on the method. When the two ever seem to disagree, the godoc wins, because it's generated from the code you're actually compiling against.

So this Reference section is a map, not a mirror. It points you at the godoc and gives you the two lookup tables worth having on one page: [the vocabulary](/reference/vocabulary) and [the outcomes](/reference/outcomes). For anything signature-level, go to the source.

## The godoc

The public API lives in one package, documented in full on pkg.go.dev:

::: tip Reference
**[pkg.go.dev/github.com/spechtlabs/prose/pkg/prose](https://pkg.go.dev/github.com/spechtlabs/prose/pkg/prose)** lists every exported type, function, and method, generated from the source.
:::

You import it as:

```go
import "github.com/spechtlabs/prose/pkg/prose"
```

That's the whole surface you build against. Everything you call (`prose.For`, the builder chain, `prose.Context`, the outcomes, the observability options, `prose.Match`) is exported from `pkg/prose`.

## Package layout

prose is split into a thin public facade and two internal packages. You only ever import the facade; the split exists so the internals can move without breaking you.

| Package | Role | You import it? |
| --- | --- | --- |
| `github.com/spechtlabs/prose/pkg/prose` | The public DSL: `For`, `Builder`, `Group`, `Gate`, `Context`, `Outcome`, the observability options, `Match`. | Yes |
| `github.com/spechtlabs/prose/internal/pipeline` | The generic core: the step/group tree, the executor, the typed `Context`, the `Builder`, the controller-runtime runner. | No (internal) |
| `github.com/spechtlabs/prose/internal/observability` | The non-generic telemetry: the sink, the wide-event field accumulator, the per-step metric, humane-error folding. | No (internal) |

The facade re-exports the core types as Go aliases, so a `prose.Builder[T]` is identically a `pipeline.Builder[T]`; there's no wrapper indirection at runtime, just a stable import path.

## Where each thing is documented

| You want… | Look here |
| --- | --- |
| The exact signature of a function or method | [godoc](https://pkg.go.dev/github.com/spechtlabs/prose/pkg/prose) |
| The full builder/group/context vocabulary at a glance | [The Vocabulary](/reference/vocabulary) |
| What each outcome means and how it maps to a `ctrl.Result` | [Outcomes](/reference/outcomes) |
| How to build your first reconciler | [Your First Reconciler](/getting-started/quick) |
| Why the API is shaped this way | [The Mental Model](/understanding/mental-model) |
| How to do a specific task | [How-to Guides](/guides/observability) |

::: info Stability
prose is early and the API surface is still settling. The concepts are stable; signatures may change before a tagged release. The godoc always reflects the version you've pinned in `go.mod`; these pages track the latest design.
:::
