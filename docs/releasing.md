# Publish a Faro release

[All guides](README.md) · Before this: [Run the development checks](development.md)

This guide is for maintainers with repository and Docker Hub publishing access. A pushed release tag triggers image publication.

## 1. Prepare the release

1. Choose an unused semantic version for the release.
2. Update the version in [internal/version/version.go](../internal/version/version.go).
3. Keep the version in [frontend/package.json](../frontend/package.json) and its lockfile consistent.
4. Review image tags in [templates/faro.xml](../templates/faro.xml) if the template pins a release.
5. Update affected guides and screenshots to match the release.
6. Run the relevant tests and frontend build in [Development](development.md). Check the [publishing workflow](../.github/workflows/docker-publish.yml) for the exact CI checks and tool versions it uses.
7. Commit and review the release changes through the project's normal workflow.

## 2. Configure publishing access

In GitHub, configure an environment named **Faro CI**:

| Name | Type | Value |
| --- | --- | --- |
| `DOCKERHUB_TOKEN` | Secret | A token that can push the Faro image. |
| `DOCKERHUB_USERNAME` | Optional variable | Docker Hub login account; defaults to `tabierto`. |
| `DOCKERHUB_NAMESPACE` | Optional variable | Image namespace; defaults to `tabierto`. |

Make sure the `faro` repository exists in the selected Docker Hub namespace. Keep tokens in GitHub secrets, not in committed files.

## 3. Tag the reviewed commit

From the exact commit you want to publish, create and push its version tag. The following is the command shape; replace `vX.Y.Z` with the actual release, not a literal placeholder:

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
```

The workflow runs for tags matching `v*.*.*`. Confirm the tag and commit before pushing; publication follows automatically.

## 4. Verify publication

1. Open the **Publish Docker images** workflow run in GitHub Actions.
2. Check that verification and publication both succeed.
3. Confirm Docker Hub has the full-version tag, minor-version tag, and `latest` for `linux/amd64` and `linux/arm64`.
4. Pull the published full-version image into an isolated installation.
5. Complete startup, test DNS, and check the displayed version.
6. Publish the corresponding GitHub release notes if you want the new release available to Faro's release-notification check. The Docker workflow does not create those notes for you.

**You are done when:** the intended image is available for both architectures and the published release passes the installation checks.
