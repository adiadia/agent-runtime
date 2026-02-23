# Contributing

## Development

1. Create a branch from `main`.
2. Make focused changes with tests.
3. Run local validation before opening a PR:

```bash
make validate
```

## Releasing

1. Update `CHANGELOG.md`:
- Move relevant entries from `Unreleased` into a new `## [vX.Y.Z] - YYYY-MM-DD` section.
- Keep entries user-facing and grouped under `Added`, `Changed`, `Fixed`, etc.
2. Run full validation:

```bash
make validate
```

3. Create and push a release tag:

```bash
git tag vX.Y.Z
git push origin vX.Y.Z
```

4. GitHub Actions will automatically:
- build and publish API + worker Docker images to GHCR,
- create a GitHub Release using the matching `CHANGELOG.md` section.
