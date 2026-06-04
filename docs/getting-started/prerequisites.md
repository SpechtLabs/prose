---
title: Prerequisites
permalink: /getting-started/prerequisites
createTime: 2026/06/03 12:00:00
---

`prose` sits on top of controller-runtime, so it assumes you already have an operator project and the muscle memory that goes with one. This page is the short checklist before you write your first pipeline.

## What you need

**Go 1.25 or newer.** `prose` uses generics throughout (`For[T]`, `Group[T]`, `Context[T]`), and the public API is built around them. Check your toolchain:

```shell
go version
```

**A controller-runtime project.** A scaffold from [kubebuilder](https://book.kubebuilder.io/) or the [Operator SDK](https://sdk.operatorframework.io/), or a manager you wired up yourself, gives you the pieces `prose` plugs into: a `manager.Manager`, at least one CRD with generated types, and a `SetupWithManager` entry point per controller. If you don't have one yet, scaffold an API and a controller first, then come back.

**The status subresource enabled** on any CRD whose status you write. Most steps in a real reconciler end with a `Status().Update`, and that call fails unless the CRD declares the subresource. Kubebuilder turns it on when you mark the type:

```go
// +kubebuilder:subresource:status
```

**Familiarity with the reconcile loop.** You should know what idempotent and level-triggered mean for a controller, why a requeue isn't an error, and how owner references and server-side Apply behave. `prose` doesn't hide any of that; it gives the loop a cleaner shape.

## Install it

The import path is `github.com/spechtlabs/prose/pkg/prose`. Add it to your module:

```shell
go get github.com/spechtlabs/prose
```

Then import the package wherever your controller lives:

```go
import "github.com/spechtlabs/prose/pkg/prose"
```

Steps return [humane errors](https://github.com/sierrasoftworks/humane-errors-go), so you'll usually want that import too:

```go
import humane "github.com/sierrasoftworks/humane-errors-go"
```

The Gomega adapter behind `prose.Match` pulls in [Gomega](https://github.com/onsi/gomega) only when you write a matcher-based `When` gate. You don't need it for a first pipeline.

::: tip Ready?
Once `go version` reports 1.25+, `go get` succeeded, and your CRD has its status subresource, you have everything. Head to [Your First Reconciler](/getting-started/quick).
:::
