#!/usr/bin/env bash
# Releases the version in version.properties: pushes main, waits for the
# Quality checks run on that commit, and pushes the tag vX.Y.Z only when it
# passed. The tag starts the Release workflow, which runs the same checks again
# before it publishes.
#
#   1. set version= in version.properties and commit it ("chore: release X.Y.Z")
#   2. scripts/release.sh            # --no-watch: don't wait for the Release workflow
#
# When the checks fail, fix them, commit, and run it again: the tag is not
# pushed until a commit passes.
set -euo pipefail

watch_release=1
[[ "${1:-}" == "--no-watch" ]] && watch_release=0

cd "$(git rev-parse --show-toplevel)"
die() { echo "release: $*" >&2; exit 1; }

version="$(git show HEAD:version.properties | awk -F= '$1 == "version" { print $2; exit }')"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "version.properties at HEAD has no X.Y.Z version: '$version'"
tag="v$version"

[[ "$(git branch --show-current)" == main ]] || die "not on main"
git diff --quiet HEAD -- version.properties || die "version.properties has uncommitted changes"
git fetch --quiet origin main --tags
git merge-base --is-ancestor origin/main HEAD || die "main is behind origin/main; pull first"
git rev-parse -q --verify "refs/tags/$tag" >/dev/null && die "$tag already exists; bump version.properties"
sha="$(git rev-parse HEAD)"
git push origin main

echo "release: waiting for Quality checks on ${sha:0:7}"
run=""
for _ in $(seq 60); do
    run="$(gh run list --workflow quality.yml --branch main --commit "$sha" --event push --limit 1 --json databaseId --jq '.[0].databaseId // empty')"
    [[ -n "$run" ]] && break
    sleep 5
done
[[ -n "$run" ]] || die "no Quality checks run started for ${sha:0:7}"
if ! gh run watch "$run" --exit-status --interval 30 >/dev/null; then
    gh run view "$run" --log-failed | grep -E ' FAILED$|##\[error\]' | head -20 >&2 || true
    die "Quality checks failed: https://github.com/$(gh repo view --json nameWithOwner --jq .nameWithOwner)/actions/runs/$run ($tag not pushed)"
fi

git tag "$tag" "$sha"
git push origin "$tag"
echo "release: pushed $tag"

((watch_release)) || exit 0
release_run=""
for _ in $(seq 60); do
    release_run="$(gh run list --workflow macos-release.yml --commit "$sha" --event push --limit 1 --json databaseId --jq '.[0].databaseId // empty')"
    [[ -n "$release_run" ]] && break
    sleep 5
done
[[ -n "$release_run" ]] || die "no Release run started for $tag"
echo "release: waiting for the Release workflow (about 20 minutes)"
gh run watch "$release_run" --exit-status --interval 60 >/dev/null || die "Release workflow failed: gh run view $release_run --log-failed"
gh release view "$tag" --json url --jq .url
