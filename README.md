# aqua-registry-updater

Renovate alternative only for [aqua-registry](https://github.com/aquaproj/aqua-registry). Overcome Renovate's scalability issue :rocket:

![image](https://github.com/aquaproj/aqua-registry-updater/assets/13323303/104e8553-8203-41f6-b42e-c44db0ce5be3)

## Overview

aqua-registry-updater is CLI to update [pkgs](https://github.com/aquaproj/aqua-registry/tree/main/pkgs).
aqua-registry-updater creates pull requests per package to update packages.

e.g. https://github.com/aquaproj/aqua-registry/pull/12531

By running aqua-registry-updater periodically by GitHub Actions, we can update packages continuously.

## Why not Renovate?

[#12221](https://github.com/aquaproj/aqua-registry/issues/12221)

aqua-registry has over 1,100 dependencies.
The number of dependencies is so many that Whitesource Renovate doesn't work well.
We tuned Renovate Configuration, but we couldn't solve the issue completely.

Continous update is crucial for aqua-registry because aqua-registry is tested by CI.
If dependencies aren't updated, we can't find aqua-registry is broken.

So we decided to develop aqua-registry-updater instead of Renovate only for aqua-registry.

:warning: aqua-registry-updater is only for aqua-registry.

## GitHub Access Token

- `GITHUB_TOKEN`
  - `pull-requests:write`
  - `contents:write`
- `AQUA_REGISTRY_UPDATER_CONTAINER_REGISTRY_TOKEN`
  - classic personal access token
  - `write:packages`
  - `read:org`

https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry

> GitHub Packages only supports authentication using a personal access token (classic).

## Requirements

- [GitHub CLI](https://cli.github.com/)
- [int128/ghcp](https://github.com/int128/ghcp)

## Usage

- [GitHub Actions Workflow](https://github.com/aquaproj/aqua-registry/blob/main/.github/workflows/update.yaml)
- [Configuration](https://github.com/aquaproj/aqua-registry/blob/main/aqua-registry-updater.yaml)
- [Example pull request](https://github.com/aquaproj/aqua-registry/pull/12531)

## LICENSE

[MIT](LICENSE)
