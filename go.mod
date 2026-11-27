module github.com/dudul19/tomato-tunnel

go 1.27.4

require (
	github.com/apernet/hysteria/core/v2 v2.13.0
	github.com/xtaci/kcp-go/v5 v5.6.8
	github.com/xtaci/smux v1.5.24
	golang.org/x/crypto v0.59.0
	golang.org/x/net v0.63.0
	www.bamsoftware.com/git/dnstt.git v1.20260501.0
)

require (
	github.com/andybalholm/brotli v1.2.7 // indirect
	github.com/apernet/quic-go v0.63.1-0.20261004180939-a10df75c260c // indirect
	github.com/flynn/noise v1.0.0 // indirect
	github.com/klauspost/compress v1.20.2 // indirect
	github.com/klauspost/cpuid/v2 v2.2.6 // indirect
	github.com/klauspost/reedsolomon v1.12.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/refraction-networking/utls v1.8.2 // indirect
	github.com/templexxx/cpu v0.1.0 // indirect
	github.com/templexxx/xorsimd v0.4.2 // indirect
	github.com/tjfoc/gmsm v1.4.1 // indirect
	golang.org/x/exp v0.0.0-20240506185415-9bf2ced13842 // indirect
	golang.org/x/sys v0.50.0 // indirect
	golang.org/x/text v0.44.0 // indirect
)

replace github.com/apernet/hysteria/core/v2 => ./third_party/hysteria-core

replace github.com/xtaci/kcp-go/v5 => ./third_party/kcp-go
