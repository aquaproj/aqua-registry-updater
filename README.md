# aqua-registry-updater

[![DeepWiki](https://img.shields.io/badge/DeepWiki-aquaproj%2Faqua--registry--updater-blue.svg?logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACwAAAAyCAYAAAAnWDnqAAAAAXNSR0IArs4c6QAAA05JREFUaEPtmUtyEzEQhtWTQyQLHNak2AB7ZnyXZMEjXMGeK/AIi+QuHrMnbChYY7MIh8g01fJoopFb0uhhEqqcbWTp06/uv1saEDv4O3n3dV60RfP947Mm9/SQc0ICFQgzfc4CYZoTPAswgSJCCUJUnAAoRHOAUOcATwbmVLWdGoH//PB8mnKqScAhsD0kYP3j/Yt5LPQe2KvcXmGvRHcDnpxfL2zOYJ1mFwrryWTz0advv1Ut4CJgf5uhDuDj5eUcAUoahrdY/56ebRWeraTjMt/00Sh3UDtjgHtQNHwcRGOC98BJEAEymycmYcWwOprTgcB6VZ5JK5TAJ+fXGLBm3FDAmn6oPPjR4rKCAoJCal2eAiQp2x0vxTPB3ALO2CRkwmDy5WohzBDwSEFKRwPbknEggCPB/imwrycgxX2NzoMCHhPkDwqYMr9tRcP5qNrMZHkVnOjRMWwLCcr8ohBVb1OMjxLwGCvjTikrsBOiA6fNyCrm8V1rP93iVPpwaE+gO0SsWmPiXB+jikdf6SizrT5qKasx5j8ABbHpFTx+vFXp9EnYQmLx02h1QTTrl6eDqxLnGjporxl3NL3agEvXdT0WmEost648sQOYAeJS9Q7bfUVoMGnjo4AZdUMQku50McDcMWcBPvr0SzbTAFDfvJqwLzgxwATnCgnp4wDl6Aa+Ax283gghmj+vj7feE2KBBRMW3FzOpLOADl0Isb5587h/U4gGvkt5v60Z1VLG8BhYjbzRwyQZemwAd6cCR5/XFWLYZRIMpX39AR0tjaGGiGzLVyhse5C9RKC6ai42ppWPKiBagOvaYk8lO7DajerabOZP46Lby5wKjw1HCRx7p9sVMOWGzb/vA1hwiWc6jm3MvQDTogQkiqIhJV0nBQBTU+3okKCFDy9WwferkHjtxib7t3xIUQtHxnIwtx4mpg26/HfwVNVDb4oI9RHmx5WGelRVlrtiw43zboCLaxv46AZeB3IlTkwouebTr1y2NjSpHz68WNFjHvupy3q8TFn3Hos2IAk4Ju5dCo8B3wP7VPr/FGaKiG+T+v+TQqIrOqMTL1VdWV1DdmcbO8KXBz6esmYWYKPwDL5b5FA1a0hwapHiom0r/cKaoqr+27/XcrS5UwSMbQAAAABJRU5ErkJggg==)](https://deepwiki.com/aquaproj/aqua-registry-updater)

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

Continuous update is crucial for aqua-registry because aqua-registry is tested by CI.
If dependencies aren't updated, we can't find aqua-registry is broken.

So we decided to develop aqua-registry-updater instead of Renovate only for aqua-registry.

:warning: aqua-registry-updater is only for aqua-registry.

## GitHub Access Token

- `GITHUB_TOKEN`
  - `pull-requests:write`
  - `contents:write`
- `AQUA_REGISTRY_UPDATER_CONTAINER_REGISTRY_TOKEN`
  - [GitHub Actions Token](https://docs.github.com/en/packages/managing-github-packages-using-github-actions-workflows/publishing-and-installing-a-package-with-github-actions#upgrading-a-workflow-that-accesses-a-registry-using-a-personal-access-token)
  - `packages:write`

## Requirements

- [GitHub CLI](https://cli.github.com/)
- [int128/ghcp](https://github.com/int128/ghcp)

## Usage

- [GitHub Actions Workflow](https://github.com/aquaproj/aqua-registry/blob/main/.github/workflows/update.yaml)
- [Configuration](https://github.com/aquaproj/aqua-registry/blob/main/aqua-registry-updater.yaml)
- [Example pull request](https://github.com/aquaproj/aqua-registry/pull/12531)

## LICENSE

[MIT](LICENSE)
