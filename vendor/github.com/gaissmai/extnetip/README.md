[![Go Reference](https://pkg.go.dev/badge/github.com/gaissmai/extnetip.svg)](https://pkg.go.dev/github.com/gaissmai/extnetip#section-documentation)
![GitHub release (latest SemVer)](https://img.shields.io/github/v/release/gaissmai/extnetip)
[![CI](https://github.com/gaissmai/extnetip/actions/workflows/go.yml/badge.svg)](https://github.com/gaissmai/extnetip/actions/workflows/go.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/gaissmai/extnetip)](https://goreportcard.com/report/github.com/gaissmai/extnetip)
[![Coverage Status](https://coveralls.io/repos/github/gaissmai/extnetip/badge.svg?branch=master)](https://coveralls.io/github/gaissmai/extnetip?branch=master)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Stand With Ukraine](https://raw.githubusercontent.com/vshymanskyy/StandWithUkraine/main/badges/StandWithUkraine.svg)](https://stand-with-ukraine.pp.ua)

# package extnetip

Package `extnetip` is an extension to the Go standard library package `net/netip`, providing
a set of important auxiliary functions **currently missing** from `netip` for
converting IP prefixes to IP ranges and vice versa.

With these extensions, it becomes straightforward to build third-party IP-range
libraries based on `net/netip`.

## API

```go
import "github.com/gaissmai/extnetip"

func Range(p netip.Prefix) (first, last netip.Addr)
func Prefix(first, last netip.Addr) (prefix netip.Prefix, ok bool)
func CommonPrefix(pfx1, pfx2 netip.Prefix) (pfx netip.Prefix)
func All(first, last netip.Addr) iter.Seq[netip.Prefix]
```

## Unsafe Mode

This package supports two modes of operation for converting between `netip.Addr` and
a custom internal `uint128` representation:

- When built with the `unsafe` build tag (e.g., `go build -tags=unsafe`), conversions use
  `unsafe.Pointer` to perform zero-copy, direct memory reinterpretation. This method
  is **significantly faster**.

- Without the `unsafe` build tag, conversions are performed safely by using
  `binary.ByteOrder`-based byte slice manipulations, avoiding use of the `unsafe` package.
  This is the default mode and is suitable when importing unsafe modules is prohibited.

### Performance Benchmark

Below is a benchmark comparing the safe (default) and unsafe conversion methods:

```
goos: linux
goarch: amd64
pkg: github.com/gaissmai/extnetip
cpu: Intel(R) Core(TM) i5-8250U CPU @ 1.60GHz
                  │   safe.bm    │             unsafe.bm              │
                  │    sec/op    │   sec/op     vs base               │
Range/v4-8          12.435n ± 0%   5.099n ± 1%  -58.99% (p=0.002 n=6)
Range/v6-8          25.855n ± 0%   5.399n ± 0%  -79.12% (p=0.002 n=6)
Prefix/v4-8          16.31n ± 0%   12.07n ± 1%  -25.99% (p=0.002 n=6)
Prefix/v6-8          17.38n ± 1%   10.88n ± 1%  -37.45% (p=0.002 n=6)
CommonPrefix/v4-8    26.79n ± 0%   21.58n ± 1%  -19.43% (p=0.002 n=6)
CommonPrefix/v6-8    28.25n ± 0%   21.00n ± 1%  -25.63% (p=0.002 n=6)
geomean              20.25n        10.86n       -46.38%
```

## Future Work

It is hoped that these frequently needed helper functions will be added to the Go standard
library's `netip` package at some point in the future by the maintainers.

Until then, `extnetip` provides a robust and efficient alternative.
