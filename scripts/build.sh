#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION=$(cat VERSION)
REL=1
PKG=luci-app-tomato-tunnel
ARCHS="${ARCHS:-aarch64_generic aarch64_cortex-a53 aarch64_cortex-a72 arm_cortex-a7 arm_cortex-a7_neon-vfpv4 arm_cortex-a9 arm_cortex-a9_vfpv3-d16 arm_cortex-a15_neon-vfpv4 mipsel_24kc mips_24kc x86_64}"
DEPENDS="libc kmod-tun ip-full ca-bundle luci-base"
HEVDIR=third_party/hev-socks5-tunnel

target() {
	case "$1" in
		aarch64_*) echo "arm64 - linux-arm64" ;;
		arm_cortex-a7|arm_cortex-a9) echo "arm 5 linux-arm32v7" ;;
		arm_*) echo "arm 7 linux-arm32v7" ;;
		mipsel_*) echo "mipsle softfloat linux-mips32elsf" ;;
		mips_*) echo "mips softfloat linux-mips32sf" ;;
		x86_64) echo "amd64 - linux-x86_64" ;;
		*) echo "unknown arch $1" >&2; exit 1 ;;
	esac
}
DESC="Tomato Tunnel: SSH tunnel (Direct, Payload, SSL/SNI, WebSocket, WebSocket-TLS, SlowDNS, Enhanced payload), ZiVPN and Xray client with LuCI page. Made by @dudulrealnofek."
XPKG=tomato-tunnel-xray
XTAG=v26.11.9
XVER=${XTAG#v}
XREL=2 # 1.7.2: ttxray -ctl (shared Xray for Balance)
XSRC=third_party/xray-src
XDESC="Xray core $XVER for Tomato Tunnel (VLESS, VMess, Trojan; TCP, WebSocket, HTTPUpgrade, gRPC, XHTTP; TLS, REALITY). Client-only build with allowInsecure kept. Optional add-on for luci-app-tomato-tunnel."
WITH_XRAY=${WITH_XRAY:-1}
URL="https://github.com/dudul19/luci-app-tomato-tunnel"
APK="${APK:-$(ls ../king3399-immortalwrt/immortalwrt-imagebuilder-*/staging_dir/host/bin/apk 2>/dev/null | head -1)}"

OUT=dist
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$OUT"

echo "== go test"
go test ./internal/... >/dev/null

(cd "$HEVDIR" && sha256sum -c --quiet SHA256SUMS)

xray_src() {
	if [ ! -d "$XSRC/core/.git" ]; then
		echo "== clone Xray-core $XTAG"
		git clone -q --depth 1 --branch "$XTAG" https://github.com/XTLS/Xray-core.git "$XSRC/core"
		(cd "$XSRC/core" && git apply ../tomato.patch)
	fi
	mkdir -p "$XSRC/core/main/ttxray"
	cp "$XSRC/ttxray/main.go.in" "$XSRC/core/main/ttxray/main.go"
}
[ "$WITH_XRAY" = 1 ] && xray_src

stage() {
	local arch=$1 goarch sub hev key
	read -r goarch sub hev <<<"$(target "$arch")"
	key="$goarch-$sub"
	ROOT="$WORK/root-$key"
	[ -d "$ROOT" ] && return 0
	echo "== go build ($goarch $sub)"
	local env=(CGO_ENABLED=0 GOOS=linux GOARCH=$goarch)
	[ "$goarch" = arm ] && env+=(GOARM=$sub)
	case "$goarch" in mips|mipsle) env+=(GOMIPS=$sub) ;; esac
	env "${env[@]}" go build -trimpath -ldflags "-s -w -buildid= -X main.version=$VERSION" -o "$WORK/bin-$key" ./cmd/tomato-tunnel
	mkdir -p "$ROOT"
	cp -a root/. "$ROOT/"
	install -Dm755 "$WORK/bin-$key" "$ROOT/usr/bin/tomato-tunnel"
	install -Dm755 "$HEVDIR/hev-socks5-tunnel-$hev" "$ROOT/usr/libexec/tomato-tunnel/hev-socks5-tunnel"
	find "$ROOT" -type d -exec chmod 755 {} +
	find "$ROOT" -type f -exec chmod 644 {} +
	chmod 755 "$ROOT/usr/bin/tomato-tunnel" "$ROOT/etc/init.d/tomato-tunnel" "$ROOT/etc/init.d/tomato-tunnel-bot" \
		"$ROOT/usr/libexec/tomato-tunnel/net.sh" "$ROOT/usr/libexec/tomato-tunnel/ctl" \
		"$ROOT/usr/libexec/tomato-tunnel/hev-socks5-tunnel" \
		"$ROOT/etc/uci-defaults/90-tomato-tunnel"
}

rm -f "$OUT/${PKG}_${VERSION}_"* "$OUT/sha256sums.txt"
[ "$WITH_XRAY" = 1 ] && rm -f "$OUT/${XPKG}_${XVER}_"*

xray_pkg() {
	local arch=$1 goarch sub hev key XR XI XSIZE
	read -r goarch sub hev <<<"$(target "$arch")"
	key="$goarch-$sub"
	XR="$WORK/xroot-$key"
	if [ ! -d "$XR" ]; then
		echo "== xray build ($goarch $sub)"
		local env=(CGO_ENABLED=0 GOOS=linux GOARCH=$goarch)
		[ "$goarch" = arm ] && env+=(GOARM=$sub)
		case "$goarch" in mips|mipsle) env+=(GOMIPS=$sub) ;; esac
		(cd "$XSRC/core" && env "${env[@]}" go build -trimpath -ldflags "-s -w -buildid=" -o "$WORK/xray-$key" ./main/ttxray)
		install -Dm755 "$WORK/xray-$key" "$XR/usr/libexec/tomato-tunnel/xray"
		install -Dm644 "$XSRC/LICENSE.xray" "$XR/usr/share/doc/$XPKG/LICENSE"
		find "$XR" -type d -exec chmod 755 {} +
	fi
	XSIZE=$(du -sb "$XR" | cut -f1)
	XI="$WORK/xipk-$arch"
	mkdir -p "$XI/control"
	cat > "$XI/control/control" <<EOF
Package: $XPKG
Version: $XVER-r$XREL
Depends: $PKG
Source: https://github.com/XTLS/Xray-core
SourceName: $XPKG
License: MPL-2.0
Section: net
Maintainer: @dudulrealnofek <$URL>
Architecture: $arch
Installed-Size: $XSIZE
Description: $XDESC
EOF
	echo "2.0" > "$XI/debian-binary"
	fakeroot -- tar --format=gnu --sort=name --mtime=@0 --numeric-owner --owner=0 --group=0 -czf "$XI/control.tar.gz" -C "$XI/control" .
	fakeroot -- tar --format=gnu --sort=name --mtime=@0 --numeric-owner --owner=0 --group=0 -czf "$XI/data.tar.gz" -C "$XR" .
	fakeroot -- tar --format=gnu --mtime=@0 --numeric-owner --owner=0 --group=0 -czf "$OUT/${XPKG}_${XVER}_${arch}.ipk" -C "$XI" ./debian-binary ./data.tar.gz ./control.tar.gz
	echo "   $OUT/${XPKG}_${XVER}_${arch}.ipk"
	if [ -n "$APK" ] && [ -x "$APK" ]; then
		unshare -r -- "$APK" mkpkg \
			--info "name:$XPKG" --info "version:$XVER-r$XREL" --info "arch:$arch" \
			--info "description:$XDESC" --info "license:MPL-2.0" --info "url:https://github.com/XTLS/Xray-core" \
			--info "maintainer:@dudulrealnofek" --info "origin:$XPKG" \
			--info "depends:$PKG" \
			--files "$XR" --output "$OUT/${XPKG}_${XVER}_${arch}.apk"
		echo "   $OUT/${XPKG}_${XVER}_${arch}.apk"
	fi
}

for ARCH in $ARCHS; do
	stage "$ARCH"
	read -r goarch sub hev <<<"$(target "$ARCH")"
	ROOT="$WORK/root-$goarch-$sub"
	SIZE=$(du -sb "$ROOT" | cut -f1)
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
	[ "$WITH_XRAY" = 1 ] && xray_pkg "$ARCH"
done

(cd "$OUT" && sha256sum ${PKG}_${VERSION}_* $( [ "$WITH_XRAY" = 1 ] && echo "${XPKG}_${XVER}_*" ) > sha256sums.txt)
echo "== done"
cat "$OUT/sha256sums.txt"
