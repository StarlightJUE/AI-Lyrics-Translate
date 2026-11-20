#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "Usage: $0 <group> <out_dir>" >&2
  exit 1
fi

GROUP="$1"
OUT_DIR="$2"

mkdir -p "$OUT_DIR"
ABS_OUT="$(cd "$OUT_DIR" && pwd)"
ROOT_DIR="$(pwd)"

EXTRA_FILES=("config.example.yaml" "LICENSE" "README.en.md" "README.md")

case "$GROUP" in
  main)
    TARGETS=(
      "windows-amd64:windows:amd64:"
      "windows-arm64:windows:arm64:"
      "linux-amd64:linux:amd64:"
      "linux-arm64:linux:arm64:"
      "linux-riscv64:linux:riscv64:"
      "linux-loong64:linux:loong64:"
      "android-arm64:android:arm64:"
      "darwin-amd64:darwin:amd64:"
      "darwin-arm64:darwin:arm64:"
    )
    ;;
  sqlite-linux-win)
    TARGETS=(
      "windows-386:windows:386:"
      "linux-386:linux:386:"
      "linux-armv7:linux:arm:7"
      "linux-armv6:linux:arm:6"
      "linux-armv5:linux:arm:5"
      "linux-ppc64le:linux:ppc64le:"
      "linux-s390x:linux:s390x:"
    )
    ;;
  sqlite-bsd)
    TARGETS=(
      "freebsd-amd64:freebsd:amd64:"
      "freebsd-arm64:freebsd:arm64:"
      "freebsd-arm:freebsd:arm:"
      "freebsd-386:freebsd:386:"
      "openbsd-amd64:openbsd:amd64:"
      "openbsd-arm64:openbsd:arm64:"
      "netbsd-amd64:netbsd:amd64:"
    )
    ;;
  json-linux-unix)
    TARGETS=(
      "aix-ppc64:aix:ppc64:"
      "dragonfly-amd64:dragonfly:amd64:"
      "illumos-amd64:illumos:amd64:"
      "solaris-amd64:solaris:amd64:"
      "linux-mips:linux:mips:"
      "linux-mipsle:linux:mipsle:"
      "linux-mips64:linux:mips64:"
      "linux-mips64le:linux:mips64le:"
      "linux-ppc64:linux:ppc64:"
    )
    ;;
  json-bsd)
    TARGETS=(
      "netbsd-386:netbsd:386:"
      "netbsd-arm:netbsd:arm:"
      "netbsd-arm64:netbsd:arm64:"
      "openbsd-386:openbsd:386:"
      "openbsd-arm:openbsd:arm:"
      "openbsd-ppc64:openbsd:ppc64:"
      "openbsd-riscv64:openbsd:riscv64:"
    )
    ;;
  json-plan9-wasm)
    TARGETS=(
      "plan9-386:plan9:386:"
      "plan9-amd64:plan9:amd64:"
      "plan9-arm:plan9:arm:"
      "js-wasm:js:wasm:"
      "wasip1-wasm:wasip1:wasm:"
    )
    ;;
  *)
    echo "Unknown build group: $GROUP" >&2
    exit 1
    ;;
esac

WORK_TMP="$(mktemp -d)"
trap 'rm -rf "$WORK_TMP"' EXIT

total="${#TARGETS[@]}"
idx=0

for spec in "${TARGETS[@]}"; do
  idx=$((idx + 1))
  IFS=':' read -r suffix goos goarch goarm <<<"$spec"

  if [ "$goos" = "windows" ]; then
    bin_name="ai-lyrics-translate.exe"
    ext="zip"
  elif [ "$goarch" = "wasm" ]; then
    bin_name="ai-lyrics-translate.wasm"
    ext="tar.gz"
  else
    bin_name="ai-lyrics-translate"
    ext="tar.gz"
  fi

  stage="$WORK_TMP/$suffix"
  mkdir -p "$stage"

  echo "[$idx/$total] Building $suffix ($goos/$goarch${goarm:+ GOARM=$goarm})..."
  if [ -n "$goarm" ]; then
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" \
      go build -trimpath -ldflags="-s -w" -o "$stage/$bin_name" .
  else
    env -u GOARM CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build -trimpath -ldflags="-s -w" -o "$stage/$bin_name" .
  fi

  if [ "$goos" = "windows" ] || [ "$goarch" = "wasm" ]; then
    chmod 0644 "$stage/$bin_name"
  else
    chmod 0755 "$stage/$bin_name"
  fi

  for extra in "${EXTRA_FILES[@]}"; do
    cp "$ROOT_DIR/$extra" "$stage/$extra"
    chmod 0644 "$stage/$extra"
  done

  archive_path="$ABS_OUT/ai-lyrics-translate-${suffix}.${ext}"
  rm -f "$archive_path"

  if [ "$ext" = "zip" ]; then
    (cd "$stage" && zip -q -9 "$archive_path" "$bin_name" "${EXTRA_FILES[@]}")
    unzip -tq "$archive_path" >/dev/null
    entry_count="$(unzip -Z1 "$archive_path" | wc -l | tr -d ' ')"
  else
    tar -czf "$archive_path" -C "$stage" "$bin_name" "${EXTRA_FILES[@]}"
    entry_count="$(tar -tzf "$archive_path" | wc -l | tr -d ' ')"
  fi

  if [ "$entry_count" -ne 5 ]; then
    echo "Unexpected archive entry count ($entry_count) in $archive_path" >&2
    exit 1
  fi

  size_bytes="$(wc -c < "$archive_path" | tr -d ' ')"
  echo "OK  $(basename "$archive_path") ($size_bytes bytes, $entry_count entries)"
  rm -rf "$stage"
done

echo "Group '$GROUP' completed: $total archives created in $ABS_OUT"
