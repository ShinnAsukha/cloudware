# Cloudware Release Candidates

No approved Cloudware application release exists yet. All application releases must use a tag `cw-app-X.Y.Z` with the exact current-source hashes, tested source code, explicit approval and verified provenance.

A candidate contains `manifest.json` and files under `payload/apps/web/`, `payload/apps/api/`, or `payload/AI/`.

Database migrations, scripts, configuration secrets, node_modules and Docker volumes are forbidden in v0.1. The release workflow fails closed if attestations are not supported for the private repository. Do not publish sample releases.
