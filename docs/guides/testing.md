---
title: Test a reconciler
permalink: /guides/testing
createTime: 2026/06/03 12:00:00
---

You want to test your reconciler, and your instinct from controller-runtime is to construct the reconciler, call its `Reconcile` method against a fake client, and assert on the result. With `prose` you can't do that, and the reason isn't an oversight: a `prose` pipeline has no exported `Reconcile` method. There's no `MemcachedReconciler` struct carrying a client, no entry point to call. The builder chain in `SetupWithManager` *is* the controller. So you test it the way it actually runs: registered on a real manager, driven by watch events, with [envtest](https://book.kubebuilder.io/reference/envtest) providing a real API server underneath.

This is a feature once you adjust to it. A fake-client unit test asserts on what your `Reconcile` returns; an envtest spec asserts on what the cluster *converges to*, which is the thing you actually care about. You stop testing the function and start testing the behavior.

## The shape of the suite

The sample operator uses Ginkgo plus envtest, scaffolded the way Kubebuilder lays it out. `BeforeSuite` starts a real API server, registers your pipeline on a manager, and starts the manager in a goroutine:

```go
var _ = BeforeSuite(func() {
    ctx, cancel = context.WithCancel(context.TODO())

    Expect(cachev1alpa1.AddToScheme(scheme.Scheme)).To(Succeed())

    By("bootstrapping test environment")
    testEnv = &envtest.Environment{
        CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
        ErrorIfCRDPathMissing: true,
    }
    cfg, err := testEnv.Start()
    Expect(err).NotTo(HaveOccurred())

    k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
    Expect(err).NotTo(HaveOccurred())

    // Run the prose pipeline inside a real manager. There is no Reconcile method
    // to call directly, so the controller is exercised the way it runs in
    // production: registered on a manager and driven by watch events.
    mgr, err := ctrl.NewManager(cfg, ctrl.Options{
        Scheme:  scheme.Scheme,
        Metrics: metricsserver.Options{BindAddress: "0"},
    })
    Expect(err).NotTo(HaveOccurred())

    Expect(SetupWithManager(mgr)).To(Succeed())

    go func() {
        defer GinkgoRecover()
        Expect(mgr.Start(ctx)).To(Succeed(), "failed to run manager")
    }()
})
```

The load-bearing line is `SetupWithManager(mgr)`. That's the same function your `main.go` calls in production, with the same builder chain, the same observability sink, the same watches. You're not building a stripped-down test harness around the reconciler; you're running the real one against a real control plane. `Metrics.BindAddress: "0"` just keeps two parallel suites from fighting over a metrics port.

`AfterSuite` cancels the context and stops the environment, which tears the API server down cleanly.

## Driving it: mutate the cluster, assert convergence

Because the manager is running and watching, you don't call anything to make a reconcile happen. You create or change an object, and the watch fires the pipeline. The spec then waits for the cluster to reach the state you expect:

```go
var _ = Describe("Memcached Controller", func() {
    Context("When reconciling a Memcached resource", func() {
        const (
            resourceName = "test-resource"
            namespace    = "default"
            replicas     = int32(3)
            timeout      = 10 * time.Second
            interval     = 250 * time.Millisecond
        )
        key := types.NamespacedName{Name: resourceName, Namespace: namespace}

        BeforeEach(func() {
            By("creating the Memcached custom resource")
            Expect(k8sClient.Create(ctx, &cachev1alpa1.Memcached{
                ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: namespace},
                Spec:       cachev1alpa1.MemcachedSpec{Size: replicas, Image: image},
            })).To(Succeed())
        })

        It("creates the Deployment with the desired size", func() {
            Eventually(func(g Gomega) {
                deployment := &appsv1.Deployment{}
                g.Expect(k8sClient.Get(ctx, key, deployment)).To(Succeed())
                g.Expect(deployment.Spec.Replicas).NotTo(BeNil())
                g.Expect(*deployment.Spec.Replicas).To(Equal(replicas))
            }, timeout, interval).Should(Succeed())
        })
    })
})
```

`Eventually` is doing the real work. Reconciliation is asynchronous: the create returns, the watch enqueues, the pipeline runs, the deployment appears, all on the manager's schedule rather than yours. So you poll until the cluster converges or the timeout fires. Don't reach for a fixed `time.Sleep`; it's either flaky because it's too short or slow because it's too long, and `Eventually` with a sensible timeout and interval is the idiom that handles both.

::: tip Assert on cluster state, not on return values
There's no `(ctrl.Result, error)` to inspect, and that's fine, because it was never the interesting thing. Assert that the Deployment exists with the right replica count, that the Service is there, that `status.Nodes` reflects the pods. Those are the outcomes a user of your operator observes, and they're what convergence means.
:::

## Setting up the world the reconcile reads

Some steps read state your test has to provide. `syncStatus` lists pods by label and reflects them into `status.Nodes`, so the spec creates the pods the Deployment would have produced before asserting the status converges:

```go
BeforeEach(func() {
    By("creating the pods the Deployment would produce")
    for _, name := range podNames {
        pod := &corev1.Pod{
            ObjectMeta: metav1.ObjectMeta{
                Name:      name,
                Namespace: namespace,
                Labels:    labelsForMemcached(resourceName),
            },
            Spec: corev1.PodSpec{
                Containers: []corev1.Container{{Name: "memcached", Image: image}},
            },
        }
        Expect(k8sClient.Create(ctx, pod)).To(Succeed())
    }
})

It("reflects the backing pods into status.Nodes", func() {
    Eventually(func(g Gomega) {
        m := &cachev1alpa1.Memcached{}
        g.Expect(k8sClient.Get(ctx, key, m)).To(Succeed())
        g.Expect(m.Status.Nodes).To(ConsistOf(podNames))
    }, timeout, interval).Should(Succeed())
})
```

envtest runs the API server and etcd, not the kubelet or controller-manager, so a Deployment you create won't actually spawn pods. You create the pods the reconcile expects to find, with the labels the step selects on, and then assert the convergence. That's the one thing to internalize about envtest: it's a real API surface with no workload controllers behind it, so anything those controllers would have produced, your test stands in for.

`AfterEach` deletes the CR, waits for it to disappear, then cleans up the owned objects and pods, so specs don't bleed state into each other.

## Running it

The sample's CRDs and envtest binaries have to be in place. The framework's own envtest suite is wired through mise:

::: terminal Run the framework's envtest suite

```text
$ mise run setup-envtest   # download the control-plane binaries once
$ mise run test-envtest
```

:::

`test-envtest` resolves `KUBEBUILDER_ASSETS` to the downloaded binaries and runs `go test -tags envtest ./...`. For a sample operator scaffolded by Kubebuilder, the equivalent is its `make test` target, which downloads the same `setup-envtest` binaries and points `KUBEBUILDER_ASSETS` at them before running the suite. The suite locates the binaries itself when you run from an IDE, falling back to `bin/k8s` if `KUBEBUILDER_ASSETS` isn't set, so a green run from the command line and a green run from your editor exercise the same control plane.

::: warning Pin the Kubernetes version
envtest behavior tracks the API server version it downloads. The framework pins `1.33.0` in `mise.toml`; pin yours too, so a contributor on a different machine and CI are testing against the same API server rather than whatever `setup-envtest` happened to fetch last.
:::

## Where to go next

[When the DSL isn't enough](/guides/escape-hatch) covers the watch and predicate wiring your suite exercises through `SetupWithManager`. [Add teardown with Finalize](/guides/finalizers) is worth a dedicated spec: create the CR, delete it, and assert the object leaves `Terminating` only after teardown ran. For Ginkgo and envtest themselves, the [Ginkgo docs](https://onsi.github.io/ginkgo/) and the [Kubebuilder envtest reference](https://book.kubebuilder.io/reference/envtest) are the upstream sources.
