#!/bin/sh
# Builds geet for Android as build/android/<abi>/libgeet.so, the name and
# place an app's jniLibs need: Android 10+ only runs executables shipped
# as native libraries (the app's own data directory is mounted noexec).
#
# GOOS=android links against bionic through the NDK's clang, so DNS goes
# through Android's resolver; a plain linux/arm64 build would look for an
# /etc/resolv.conf that Android doesn't have.
#
# Usage: scripts/build-android.sh [version]
# Needs ANDROID_NDK_HOME, or an NDK under $ANDROID_HOME/ndk (newest wins).
set -eu

version=${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
api=31 # the app's minSdk

ndk=${ANDROID_NDK_HOME:-}
if [ -z "$ndk" ]; then
	sdk=${ANDROID_HOME:-$HOME/Android/Sdk}
	ndk=$(ls -d "$sdk"/ndk/* 2>/dev/null | sort -V | tail -n 1)
fi
if [ -z "$ndk" ] || [ ! -d "$ndk" ]; then
	echo "no Android NDK found: set ANDROID_NDK_HOME" >&2
	exit 1
fi
case $(uname -s) in
Darwin) host=darwin-x86_64 ;; # Apple silicon runs it through Rosetta
*) host=linux-x86_64 ;;
esac
bin=$ndk/toolchains/llvm/prebuilt/$host/bin

build() { # abi goarch clang-target [goarm]
	out=build/android/$1
	mkdir -p "$out"
	echo "building $1"
	CGO_ENABLED=1 GOOS=android GOARCH=$2 GOARM=${4:-} CC="$bin/$3$api-clang" \
		go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$out/libgeet.so" ./cmd/geet
}

build arm64-v8a arm64 aarch64-linux-android
build armeabi-v7a arm armv7a-linux-androideabi 7
build x86_64 amd64 x86_64-linux-android
