# Changelog

## [0.3.0](https://github.com/panoptiorg/panoptife-go/compare/v0.2.0...v0.3.0) (2026-10-14)


### Features

* --version on pc-fe (also -version), -version on cgfdump and cgfstat ([b2ddff1](https://github.com/panoptiorg/panoptife-go/commit/b2ddff14f3247839fa18b15264f414ba2aab80d0))
* a load-warn line names packages with type errors that are left out of the CGF ([b2ddff1](https://github.com/panoptiorg/panoptife-go/commit/b2ddff14f3247839fa18b15264f414ba2aab80d0))
* span paths are relative to the extracted directory, so one checkout gives the same CGF anywhere ([b2ddff1](https://github.com/panoptiorg/panoptife-go/commit/b2ddff14f3247839fa18b15264f414ba2aab80d0))


### Bug Fixes

* cgfdump --help no longer panics and cgfstat --help prints the usage ([b2ddff1](https://github.com/panoptiorg/panoptife-go/commit/b2ddff14f3247839fa18b15264f414ba2aab80d0))
* clear errors for a missing directory, a file, a directory outside a Go module and a scope that matches nothing; usage errors exit 2 ([b2ddff1](https://github.com/panoptiorg/panoptife-go/commit/b2ddff14f3247839fa18b15264f414ba2aab80d0))

## [0.2.0](https://github.com/panoptiorg/panoptife-go/compare/v0.1.0...v0.2.0) (2026-10-08)


### Features

* HTTP routes, HTTP client calls, request-field sources and Kafka topic cells ([#3](https://github.com/panoptiorg/panoptife-go/issues/3)) ([15f1121](https://github.com/panoptiorg/panoptife-go/commit/15f11210068fa5fd75f4c5730db1ef3573d7ce28))

## 0.1.0 (2026-10-04)


### Features

* initial release ([17075c5](https://github.com/panoptiorg/panoptife-go/commit/17075c5d2a1322a2d1621606221f1dbe556fe688))
