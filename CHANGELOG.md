# Changelog

## 0.0.1 (2026-06-04)


### Features

* implement the prose pipeline framework and wormhole-operator demo ([21cd3bf](https://github.com/SpechtLabs/prose/commit/21cd3bf9de63e343c2944ca7cd61649acdfd82ce))
* **tracing:** name the reconcile root span per controller ([bdbab7a](https://github.com/SpechtLabs/prose/commit/bdbab7aa502ca92afa8aff8305107514fcac70b1))
* **wormhole:** close the relay saturation loop ([8a2db81](https://github.com/SpechtLabs/prose/commit/8a2db816f4bb8edafbf7666e6d650879102e5c61))


### Bug Fixes

* **observability:** set service.namespace and put outcome on spans ([47c2cfc](https://github.com/SpechtLabs/prose/commit/47c2cfcc83d485d8b3557d64277a19e55b92775a))
* **tracing:** mark the reconcile root span SERVER for per-service RED ([43e00cc](https://github.com/SpechtLabs/prose/commit/43e00cc4420d59bb78b82e9f7557c9b7ec0d3147))
* **tracing:** wrap each reconcile in a single root span ([afb1fb4](https://github.com/SpechtLabs/prose/commit/afb1fb46b10847115122dc1a6fe815a7a564d906))
