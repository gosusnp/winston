# Changelog

## [0.1.8](https://github.com/gosusnp/winston/compare/v0.1.7...v0.1.8) (2026-03-19)


### Features

* report usage for pods with no limit or requests ([e68a254](https://github.com/gosusnp/winston/commit/e68a25495744a1da7cfeaf4364bb20d3e988e7a2))

## [0.1.7](https://github.com/gosusnp/winston/compare/v0.1.6...v0.1.7) (2026-03-19)


### Features

* add a configurable misconfig window ([515da22](https://github.com/gosusnp/winston/commit/515da220c302cdcc6c528f3255aab25b96583e40))
* apply active windown to aggregated stats ([9852e65](https://github.com/gosusnp/winston/commit/9852e65f628680d3a277d848a14c8e27aebc4da4))
* unify config POD_TTL_S for TTL using both aggregate and config ([5326939](https://github.com/gosusnp/winston/commit/532693972993173a61597f91192496d7a4de514f))

## [0.1.6](https://github.com/gosusnp/winston/compare/v0.1.5...v0.1.6) (2026-03-19)


### Features

* **ui:** more consistent coloring and add links ([2f747f2](https://github.com/gosusnp/winston/commit/2f747f2e2d12fafc795661f0bcbb2f20ef1483c6))


### Bug Fixes

* **helm:** pass the correct image version ([0fb0441](https://github.com/gosusnp/winston/commit/0fb04418c51d7d3a57256bed6e84ecfda440129c))

## [0.1.5](https://github.com/gosusnp/winston/compare/v0.1.4...v0.1.5) (2026-03-19)


### Features

* add no limit and no request to the UI ([ca2f6a4](https://github.com/gosusnp/winston/commit/ca2f6a45fbe11bf5ea2614548bed6f040d50477a))
* better grouping for the reporting ([3c35dd6](https://github.com/gosusnp/winston/commit/3c35dd6ed3c5741a6318364aee52ec4637d9c33d))


### Bug Fixes

* address group by on nullable values ([556fb8d](https://github.com/gosusnp/winston/commit/556fb8d2287692f197f19109cf836b604114ccdf))

## [0.1.4](https://github.com/gosusnp/winston/compare/v0.1.3...v0.1.4) (2026-03-19)


### Features

* flag no limit and no request earlier ([7181d7d](https://github.com/gosusnp/winston/commit/7181d7d268648d2bb0cef40e31d6397ddcddd6f1))
* flag pods without request or limit ([2bc348d](https://github.com/gosusnp/winston/commit/2bc348d178c1350e62bd16d7d6916bd535a0ff8c))
* start flagging all exuberant pods after 1h of data ([0bfadfd](https://github.com/gosusnp/winston/commit/0bfadfd74de33c60ea7eb84f5f50c0bf17465300))

## [0.1.3](https://github.com/gosusnp/winston/compare/v0.1.2...v0.1.3) (2026-03-18)


### Bug Fixes

* fix error logging ([7e2738f](https://github.com/gosusnp/winston/commit/7e2738f828ed2707b2bd86fe3069fe15be153029))

## [0.1.2](https://github.com/gosusnp/winston/compare/v0.1.1...v0.1.2) (2026-03-18)


### Features

* **helm:** add charts ([#4](https://github.com/gosusnp/winston/issues/4)) ([86abaac](https://github.com/gosusnp/winston/commit/86abaacb16975e33a460e62f7e8fc4f833c1e0c2))

## [0.1.1](https://github.com/gosusnp/winston/compare/v0.1.0...v0.1.1) (2026-03-18)


### Features

* embedded ui ([93735ce](https://github.com/gosusnp/winston/commit/93735cea47b5469015b4baddebdd8daf18ed2901))
* implement analyzer ([bc46790](https://github.com/gosusnp/winston/commit/bc46790ee8ce834d1e855160febf7de8cf034022))
* implement api and report ([885c033](https://github.com/gosusnp/winston/commit/885c03311ff6adda428a31513660ede8539fd7c2))
* implement cli report ([0d86d5f](https://github.com/gosusnp/winston/commit/0d86d5f33f0a04f3c20c5680c3762acd42d1aa94))
* implement compaction ([9b42314](https://github.com/gosusnp/winston/commit/9b4231421c9baf644ac7f4c58dd473b29bb329c0))
* implement metric collector ([39f1426](https://github.com/gosusnp/winston/commit/39f14262ea2c780f38d7e979d1255f0981c58aa0))
* improve transaction patterns ([83db0e0](https://github.com/gosusnp/winston/commit/83db0e0a1fe06f61b078dbdd1e4610ce004506bb))
* initial project setup ([aea3bcc](https://github.com/gosusnp/winston/commit/aea3bcc40e3d28ab2d51b000d342e68bf366a69c))


### Bug Fixes

* improve tests and address floating point issues ([6784d76](https://github.com/gosusnp/winston/commit/6784d76c4bcb805ceb9b8bfec8b071d0e4cb0908))
