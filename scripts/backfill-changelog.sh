#!/usr/bin/env bash
# Докладывает <id>.changelog.json к релизам <id>-v<версия>, у которых его нет. Берётся wasm/<id>/changelog.json из репозитория
# без записей новее версии релиза (scripts/wasm.mjs changelog). Нет файла или записи для версии релиза — релиз пропускается.
set -uo pipefail
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
re='^([a-z0-9][a-z0-9_-]{0,31})-v([0-9]+\.[0-9]+\.[0-9]+)$'
for tag in $(gh release list --limit 200 --json tagName --jq '.[].tagName'); do
  [[ $tag =~ $re ]] || continue
  id=${BASH_REMATCH[1]}
  version=${BASH_REMATCH[2]}
  [[ -f "wasm/$id/changelog.json" ]] || continue
  assets=$(gh release view "$tag" --json assets --jq '[.assets[].name] | join(" ")')
  [[ " $assets " == *" $id.changelog.json "* ]] && continue
  f="$tmp/$id.changelog.json"
  if node scripts/wasm.mjs changelog "$id" "$version" > "$f" 2>/dev/null \
    && jq -e --arg v "$version" 'any(.[]; .version == $v)' "$f" >/dev/null \
    && gh release upload "$tag" "$f#$id.changelog.json"; then
    echo "$tag: добавлен $id.changelog.json"
  else
    echo "::warning::$tag: $id.changelog.json не добавлен"
  fi
done
exit 0
