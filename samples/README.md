# Samples

These samples implement the **same** Kubernetes operator twice, so you can read the two
side by side and see how `prose` compares to writing a controller-runtime reconciler by hand.

The operator is the classic `Memcached` example: given a `Memcached` custom resource, it
reconciles a `Deployment`, a `Service`, and reports the running pod names back into
`Memcached.Status.Nodes`.

| Directory | Framework | What it shows |
|-----------|-----------|---------------|
| [`operator-sdk/memcached-operator`](./operator-sdk/memcached-operator) | [Operator SDK](https://sdk.operatorframework.io/) / plain controller-runtime | The reconcile loop written by hand: the `Get` prologue, manual requeue plumbing, scattered logging, and explicit `SetupWithManager` wiring. |
| [`prose/memcached-operator`](./prose/memcached-operator) | [`prose`](../) | The same behavior expressed as a linear sequence of named steps, with tracing, wide-event logging, metrics, and events handled at the framework boundary. |

Read [`operator-sdk/memcached-operator/internal/controller/memcached_controller.go`](./operator-sdk/memcached-operator/internal/controller/memcached_controller.go)
and the [corresponding file under `prose/`](./prose/memcached-operator/internal/controller/memcached_controller.go)
next to each other to see the difference in the reconcile body.

## Credits

The Memcached sample was originally taken from
[**99cloud/operator-sdk-samples**](https://github.com/99cloud/operator-sdk-samples/tree/master/memcached-operator)
and modified to reflect the current Operator SDK / Kubebuilder project layout. All credit for the
original implementation goes to the original authors. It is distributed under the Apache License
2.0, the same license as this repository.

The `prose/memcached-operator` sample starts from that same code and rewrites the reconcile body
to use the `prose` framework.
