#!/usr/bin/env bash
# Докладывает <id>.json к релизам <id>-v<версия>, у которых его нет. Файл строится из опубликованного .wasm (scripts/wasm.mjs describe):
# sha256 файла должен совпасть с опубликованным <id>.wasm.sha256, id и версия в oc_meta — с тегом. Не совпало — релиз пропускается с предупреждением.
set -uo pipefail
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
re='^([a-z0-9][a-z0-9_-]{0,31})-v([0-9]+\.[0-9]+\.[0-9]+)$'
for tag in $(gh release list --limit 200 --json tagName --jq '.[].tagName'); do
  [[ $tag =~ $re ]] || continue
  id=${BASH_REMATCH[1]}
  version=${BASH_REMATCH[2]}
  assets=$(gh release view "$tag" --json assets --jq '[.assets[].name] | join(" ")')
  [[ " $assets " == *" $id.json "* ]] && continue
  if [[ " $assets " != *" $id.wasm "* || " $assets " != *" $id.wasm.sha256 "* ]]; then
    echo "::warning::$tag: нет $id.wasm или $id.wasm.sha256"
    continue
  fi
  d="$tmp/$tag"
  mkdir -p "$d"
  if gh release download "$tag" -p "$id.wasm" -p "$id.wasm.sha256" -D "$d" \
    && node scripts/wasm.mjs describe "$id" "$version" "$d/$id.wasm" "$d/$id.wasm.sha256" > "$d/$id.json" \
    && gh release upload "$tag" "$d/$id.json"; then
    echo "$tag: добавлен $id.json"
  else
    echo "::warning::$tag: $id.json не добавлен"
  fi
done
exit 0
