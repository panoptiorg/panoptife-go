# Changelog

## [0.3.3](https://github.com/panoptiorg/panoptife-go/compare/v0.3.2...v0.3.3) (2026-10-24)


### Bug Fixes

* keep a zero-arg shell call site for canonicalized pb getters ([#13](https://github.com/panoptiorg/panoptife-go/issues/13)) ([7ef9c0b](https://github.com/panoptiorg/panoptife-go/commit/7ef9c0b2463327672b2d6e3d9c912e64674db18c))

## [0.3.2](https://github.com/panoptiorg/panoptife-go/compare/v0.3.1...v0.3.2) (2026-10-24)


### Bug Fixes

* canonicalize trivial pb getters without an SSA body ([#11](https://github.com/panoptiorg/panoptife-go/issues/11)) ([b55e742](https://github.com/panoptiorg/panoptife-go/commit/b55e742f02747575056a1e83578bd5ca35d9429b))

## [0.3.1](https://github.com/panoptiorg/panoptife-go/compare/v0.3.0...v0.3.1) (2026-10-24)


### Bug Fixes

* clean release version stamp; static calls through once-bound package variables ([#9](https://github.com/panoptiorg/panoptife-go/issues/9)) ([e3587ff](https://github.com/panoptiorg/panoptife-go/commit/e3587ff9a6ec61050d7fc83bbb414b5da28bf813))

## [0.3.0](https://github.com/panoptiorg/panoptife-go/compare/v0.2.0...v0.3.0) (2026-10-18)


### Features

* --version on pc-fe (also -version), -version on cgfdump and cgfstat ([b2ddff1](https://github.com/panoptiorg/panoptife-go/commit/b2ddff14f3247839fa18b15264f414ba2aab80d0))
* a load-warn line names packages with type errors that are left out of the CGF ([b2ddff1](https://github.com/panoptiorg/panoptife-go/commit/b2ddff14f3247839fa18b15264f414ba2aab80d0))
* span paths are relative to the extracted directory, so one checkout gives the same CGF anywhere ([b2ddff1](https://github.com/panoptiorg/panoptife-go/commit/b2ddff14f3247839fa18b15264f414ba2aab80d0))


### Bug Fixes

* callee iids across modules; interface targets in dependencies ([#7](https://github.com/panoptiorg/panoptife-go/issues/7)) ([ed09f02](https://github.com/panoptiorg/panoptife-go/commit/ed09f02d93ba42213712abbd2e5b982d55fedf2f))
* cgfdump --help no longer panics and cgfstat --help prints the usage ([b2ddff1](https://github.com/panoptiorg/panoptife-go/commit/b2ddff14f3247839fa18b15264f414ba2aab80d0))
* clear errors for a missing directory, a file, a directory outside a Go module and a scope that matches nothing; usage errors exit 2 ([b2ddff1](https://github.com/panoptiorg/panoptife-go/commit/b2ddff14f3247839fa18b15264f414ba2aab80d0))
* detect mock files by their generator header ([#8](https://github.com/panoptiorg/panoptife-go/issues/8)) ([d3d449f](https://github.com/panoptiorg/panoptife-go/commit/d3d449f343dfd1214b90a66528df9dc59eea9ae9))

## [0.2.0](https://github.com/panoptiorg/panoptife-go/compare/v0.1.0...v0.2.0) (2026-10-08)


### Features

* HTTP routes, HTTP client calls, request-field sources and Kafka topic cells ([#3](https://github.com/panoptiorg/panoptife-go/issues/3)) ([15f1121](https://github.com/panoptiorg/panoptife-go/commit/15f11210068fa5fd75f4c5730db1ef3573d7ce28))

## 0.1.0 (2026-10-04)


### Features

* initial release ([17075c5](https://github.com/panoptiorg/panoptife-go/commit/17075c5d2a1322a2d1621606221f1dbe556fe688))
