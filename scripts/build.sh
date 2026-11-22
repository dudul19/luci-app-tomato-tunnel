#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION=$(cat VERSION)
REL=1
PKG=luci-app-tomato-tunnel
ARCHS="${ARCHS:-aarch64_generic aarch64_cortex-a53 aarch64_cortex-a72}"
DEPENDS="libc kmod-tun ip-full ca-bundle luci-base"
HEV=third_party/hev-socks5-tunnel/hev-socks5-tunnel-linux-arm64
DESC="Tomato Tunnel: SSH tunnel (Direct, Payload, SSL/SNI, WebSocket, WebSocket-TLS, SlowDNS, Enhanced payload) and ZiVPN (UDP) client with LuCI page. Made by @dudulrealnofek."
URL="https://github.com/dudul19/tomato-tunnel"
APK="${APK:-$(ls ../king3399-immortalwrt/immortalwrt-imagebuilder-*/staging_dir/host/bin/apk 2>/dev/null | head -1)}"

OUT=dist
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$OUT"

echo "== go test"
go test ./internal/... >/dev/null

echo "== go build (linux/arm64, static)"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
	-ldflags "-s -w -buildid= -X main.version=$VERSION" \
	-o "$WORK/tomato-tunnel" ./cmd/tomato-tunnel

ROOT="$WORK/root"
mkdir -p "$ROOT"
cp -a root/. "$ROOT/"
install -Dm755 "$WORK/tomato-tunnel" "$ROOT/usr/bin/tomato-tunnel"
echo "$(cat third_party/hev-socks5-tunnel/SHA256)  $HEV" | sha256sum -c --quiet
install -Dm755 "$HEV" "$ROOT/usr/libexec/tomato-tunnel/hev-socks5-tunnel"
find "$ROOT" -type d -exec chmod 755 {} +
find "$ROOT" -type f -exec chmod 644 {} +
chmod 755 "$ROOT/usr/bin/tomato-tunnel" "$ROOT/etc/init.d/tomato-tunnel" \
	"$ROOT/usr/libexec/tomato-tunnel/net.sh" "$ROOT/usr/libexec/tomato-tunnel/ctl" \
	"$ROOT/usr/libexec/tomato-tunnel/hev-socks5-tunnel" \
	"$ROOT/etc/uci-defaults/90-tomato-tunnel"
SIZE=$(du -sb "$ROOT" | cut -f1)

rm -f "$OUT/${PKG}_${VERSION}_"* "$OUT/sha256sums.txt"

for ARCH in $ARCHS; do
	I="$WORK/ipk-$ARCH"
	mkdir -p "$I/control"
	cat > "$I/control/control" <<EOF
Package: $PKG
Version: $VERSION-r$REL
Depends: $(echo $DEPENDS | sed 's/ /, /g')
Source: $URL
SourceName: $PKG
License: GPL-3.0-only
Section: luci
Maintainer: @dudulrealnofek <$URL>
Architecture: $ARCH
Installed-Size: $SIZE
Description: $DESC
EOF
	cp pkg/postinst "$I/control/postinst"
	cp pkg/prerm "$I/control/prerm"
	cp pkg/postrm "$I/control/postrm"
	chmod 755 "$I/control/"post* "$I/control/prerm"
	echo "2.0" > "$I/debian-binary"
	fakeroot -- tar --format=gnu --sort=name --mtime=@0 --numeric-owner --owner=0 --group=0 -czf "$I/control.tar.gz" -C "$I/control" .
	fakeroot -- tar --format=gnu --sort=name --mtime=@0 --numeric-owner --owner=0 --group=0 -czf "$I/data.tar.gz" -C "$ROOT" .
	fakeroot -- tar --format=gnu --mtime=@0 --numeric-owner --owner=0 --group=0 -czf "$OUT/${PKG}_${VERSION}_${ARCH}.ipk" -C "$I" ./debian-binary ./data.tar.gz ./control.tar.gz
	echo "   $OUT/${PKG}_${VERSION}_${ARCH}.ipk"

	if [ -n "$APK" ] && [ -x "$APK" ]; then
		unshare -r -- "$APK" mkpkg \
			--info "name:$PKG" --info "version:$VERSION-r$REL" --info "arch:$ARCH" \
			--info "description:$DESC" --info "license:GPL-3.0-only" --info "url:$URL" \
			--info "maintainer:@dudulrealnofek" --info "origin:$PKG" \
			--info "depends:$DEPENDS" \
			--script "post-install:pkg/postinst" --script "post-upgrade:pkg/postinst" \
			--script "pre-upgrade:pkg/preupg" --script "pre-deinstall:pkg/prerm" \
			--script "post-deinstall:pkg/postrm" \
			--files "$ROOT" --output "$OUT/${PKG}_${VERSION}_${ARCH}.apk"
		echo "   $OUT/${PKG}_${VERSION}_${ARCH}.apk"
	else
		echo "   (skipping .apk: set APK=/path/to/apk-tools-3)"
	fi
done

(cd "$OUT" && sha256sum ${PKG}_${VERSION}_* > sha256sums.txt)
echo "== done"
cat "$OUT/sha256sums.txt"
