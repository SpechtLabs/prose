---
title: Outcomes
permalink: /reference/outcomes
createTime: 2026/06/03 12:00:00
---

Every step returns a `prose.Outcome` alongside its error. The outcome says what should happen to the pipeline; the error says whether something went wrong. They're independent, and keeping them independent is the point: "come back in 30s" is a normal reconcile result, never an error.

## The four outcomes

| Outcome | Meaning | Stops the pipeline? |
| --- | --- | --- |
| `prose.Continue` | The step succeeded; run the next step. | No |
| `prose.Requeue` | Come back immediately, paired with the controller's backoff. | Yes |
| `prose.RequeueAfter(d)` | Come back after duration `d`. | Yes |
| `prose.Done` | The reconcile is complete; stop successfully. | Yes |

`Continue` is the only outcome that proceeds. The other three end the pipeline: `Done` because there's nothing left to do, `Requeue`/`RequeueAfter` because the object isn't settled yet and the reconcile should run again.

The zero value of `Outcome` is `Continue`, so a bare `return prose.Continue, nil` and an accidental zero value read the same way: success, proceed.

## How outcomes become a `ctrl.Result`

prose translates the `(Outcome, error)` your step returns into the `(ctrl.Result, error)` controller-runtime expects. This is the one place that depends on controller-runtime's requeue representation.

| Returned by step | `ctrl.Result` | error returned |
| --- | --- | --- |
| `Continue`, `nil` | `{}` | `nil` |
| `Done`, `nil` | `{}` | `nil` |
| `Requeue`, `nil` | `{Requeue: true}` | `nil` |
| `RequeueAfter(d)`, `nil` | `{RequeueAfter: d}` | `nil` |
| any, `err` | `{}` (or `{RequeueAfter: d}` if paired with `RequeueAfter`) | `err` |

A returned error always propagates to controller-runtime as the root cause, which drives a rate-limited requeue on its own. If a step returns an error together with `RequeueAfter(d)`, prose honors the explicit duration; otherwise the error alone governs the backoff.

## Error is not requeue

A step asking to be requeued and a step reporting a failure are different things, and prose keeps them in different return values:

```go
// "Not settled yet, come back in 30s." No error; this is normal.
return prose.RequeueAfter(30 * time.Second), nil

// "Something went wrong." The error propagates; the wide event records it.
return prose.Requeue, humane.Wrap(err, "apply deployment",
    "verify the controller's ServiceAccount can create Deployments")
```

The first is a converged step that simply wants to poll again. The second failed, and the framework folds the error into the wide event as `<step>.error`, `<step>.cause`, and `<step>.advice`, then returns it to controller-runtime.

## The result field in the wide event

Each reconcile's wide event carries a top-level `result` field, derived from the final outcome and error:

| Situation | `result` |
| --- | --- |
| Pipeline finished on `Continue` | `continue` |
| A step returned `Done` | `done` |
| A step returned `Requeue` / `RequeueAfter` | `requeue` / `requeue_after` |
| A step returned an error | `error` |
| The reconcile context was canceled (manager shutting down) | `aborted` |

That last row is prose handling shutdown gracefully. When the manager is stopping, an in-flight step's client call fails with `context canceled`; that's not a business failure, so prose reports the reconcile as `aborted` rather than `error` and doesn't surface it to controller-runtime. Your observability won't fill with errors every time the operator restarts. See [Wire Up Observability](/guides/observability) for how to read these fields.

## Each step's outcome, too

Beyond the reconcile-level `result`, every step records its own outcome under its dotted path: `dependencies.deployment.outcome=continue`, `status.outcome=requeue`. That per-step outcome is also the third label on the `prose_step_duration_seconds` metric, so you can see a single step flapping between `continue` and `requeue` without grepping logs.
