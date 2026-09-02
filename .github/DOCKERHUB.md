# Docker Hub publishing

The GitHub Actions workflow publishes a multi-architecture image to Docker Hub
whenever `main` is pushed, a `v*` Git tag is pushed, or the workflow is run
manually.

Before its first run, configure these values in **Settings → Secrets and
variables → Actions**:

| Type | Name | Value |
| --- | --- |
| Variable | `DOCKERHUB_USERNAME` | Your Docker Hub username (lowercase) |
| Secret | `DOCKERHUB_TOKEN` | A Docker Hub personal access token with read and write permissions |

For compatibility, `DOCKERHUB_USERNAME` may also be stored as a repository
secret. It must be configured in this repository, not only in another
repository or an organization without access granted to this repository.

The image is published as `<DOCKERHUB_USERNAME>/litepan`. A push to `main`
creates the `latest`, `main`, and commit-SHA tags. Pushing a tag such as
`v0.5.2` additionally creates the `v0.5.2` tag.

Deploy the published image with, for example:

```yaml
services:
  litepan:
    image: your-dockerhub-username/litepan:latest
```
