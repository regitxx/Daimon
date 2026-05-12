# Publishing the Daimon SDKs

This file is the playbook for cutting a release of `daimon` (PyPI) and
`@daimon/sdk` (npm). It's deliberately concrete: every step is a
verbatim command. The Go daemon + CLI are not published as a package
right now — users build from source via `make build`.

The two SDKs are released together in lock-step on shared semver
(`0.1.0`, `0.1.1`, ...). Don't drift them; the live cross-language smoke
tests rely on wire-shape parity.

## Prerequisites — one-time setup

1. **PyPI account + token.** Create at <https://pypi.org/account/register/>,
   then [generate a project-scoped API token](https://pypi.org/manage/account/token/)
   for `daimon`. Store as the password under user `__token__` in
   `~/.pypirc` or pass via `TWINE_USERNAME=__token__ TWINE_PASSWORD=pypi-...`.
2. **npm account + organization.** Create at <https://www.npmjs.com/signup>,
   then `npm login`. The package is scoped (`@daimon/sdk`) so you also
   need the `daimon` organization on npm, with you as an owner. Two-factor
   on the org is recommended; `npm publish` will prompt for the OTP.
3. **Local tools.**
   ```sh
   python -m pip install --upgrade build twine
   (cd sdk/typescript && npm install)
   ```

## Pre-release checklist

Run from a clean checkout of `main`:

1. **CI green on the release commit.** The
   [`ci.yml`](./.github/workflows/ci.yml) workflow runs the full matrix
   (Go + Python 3.10–3.13 + Node 18/20/22) on every push to `main`. The
   release commit must be green before you cut a tag.
2. **Version is bumped.** Both manifests must move in lock-step:
   - [`sdk/python/pyproject.toml`](./sdk/python/pyproject.toml) — `version = "0.1.0"`
   - [`sdk/typescript/package.json`](./sdk/typescript/package.json) — `"version": "0.1.0"`
   PyPI uses PEP 440 (`0.1.0.dev0` → `0.1.0`); npm uses semver
   (`0.1.0-dev.0` → `0.1.0`). Don't try to align the pre-release
   suffixes byte-for-byte — they're constrained by each registry's
   parser. Just align the public version (the leading `0.1.0`).
3. **CHANGELOGs are accurate.** Move entries from `[Unreleased]` to a
   new dated section in each SDK's `CHANGELOG.md`. Drop one-liners
   into the README's status line if the public-facing surface changed.
4. **Local smoke.** From the repo root:
   ```sh
   (cd sdk/python && rm -rf dist && python -m build && python -m twine check dist/*)
   (cd sdk/typescript && npm run build && npm pack --dry-run)
   ```
   Twine check must report `PASSED` on both wheel and sdist. `npm pack
   --dry-run` must list `LICENSE`, `CHANGELOG.md`, and `dist/` contents.
   `prepublishOnly` in `package.json` reruns typecheck + test + build
   on actual publish, so a stale dist will be caught — but verifying
   here saves a round-trip with the registry on real publish.
5. **Cross-language streaming smoke.** Optional but recommended for
   `0.x.0` minors: run [`examples/streaming/`](./examples/streaming) end-to-end
   against a real daimon and Ollama. If anything regresses in the
   wire shape, both example scripts will fail in the same way and the
   audit chain verify will mismatch.

## Cut the release

1. **Tag the release commit.** From `main`, on the commit that passed CI:
   ```sh
   git tag -a v0.1.0 -m "Daimon v0.1.0: daemon + CLI + Python/TS SDKs + 4 streaming providers"
   git push origin v0.1.0
   ```
   GitHub auto-renders the tag as a release; you can optionally promote
   it to a full release on the web UI with CHANGELOG copy.

2. **Publish to PyPI:**
   ```sh
   cd sdk/python
   rm -rf dist
   python -m build
   python -m twine check dist/*
   python -m twine upload dist/*           # prompts for token if not in ~/.pypirc
   ```
   Verify at <https://pypi.org/project/daimon/>.

3. **Publish to npm:**
   ```sh
   cd sdk/typescript
   npm publish                              # respects publishConfig.access: public
   ```
   `prepublishOnly` runs `npm run typecheck && npm run test && npm run
   build` first, so this can't ship a stale dist. Verify at
   <https://www.npmjs.com/package/@daimon/sdk>.

## Post-release smoke

From a clean shell (not the repo root), to catch packaging issues that
only show up after the registry has the artifact:

```sh
# Python
python -m venv /tmp/daimon-pubsmoke && source /tmp/daimon-pubsmoke/bin/activate
pip install daimon
python -c "from daimon import Client, StreamHandle; print('imports OK')"

# TypeScript
mkdir /tmp/daimon-npm-smoke && cd /tmp/daimon-npm-smoke
npm init -y >/dev/null
npm install @daimon/sdk
node -e 'import("@daimon/sdk").then(m => console.log("imports OK; VERSION=", m.VERSION))'
```

If a running daimon is available, the smoke scripts in
[`examples/streaming/`](./examples/streaming) work against the
published packages with minor import path swaps (drop the `dist/`
relative import and use `@daimon/sdk` directly).

## Iterating between releases

For dev / RC builds, bump the pre-release suffix and re-publish:

- Python: `0.1.1.dev0` → `0.1.1.dev1` → `0.1.1.rc0` → `0.1.1`
- TypeScript: `0.1.1-dev.0` → `0.1.1-dev.1` → `0.1.1-rc.0` → `0.1.1`

`npm publish --tag dev` keeps a dev version off the `latest` channel
so `npm install @daimon/sdk` doesn't pick it up. PyPI doesn't have a
direct equivalent; `pip install daimon` always resolves to the
highest stable version unless you pass `--pre`.

## If you need to unpublish

PyPI allows file deletion but **not** re-upload of the same version
number — once `0.1.0` is yanked, you must move to `0.1.1`. npm allows
unpublish within 72 hours of publish, after which you must deprecate
instead. Both registries treat unpublish as user-hostile; prefer
shipping `0.1.1` with a fix over yanking `0.1.0`.

Yank PyPI: there's no `twine` subcommand for this — yanks are an
operator action through the web UI at
<https://pypi.org/manage/project/daimon/releases/>, under the broken
release's "Options" menu → "Yank". Yanked versions stay installable
by pinned-version requests (`pip install daimon==0.1.0`) but stop
appearing as default resolutions. Document the reason in the yank
form so it surfaces in the resolver error message users see.

Unpublish npm (within 72h):

```sh
npm unpublish @daimon/sdk@0.1.0
```

Deprecate npm (any time, preferred for older releases):

```sh
npm deprecate @daimon/sdk@0.1.0 "Use 0.1.1+; 0.1.0 had <issue>"
```
