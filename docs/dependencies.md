# Dependency Maintenance

Dependency updates are intentionally automated only where the source of truth is
a supported manifest.

## Dependabot coverage

`.github/dependabot.yml` monitors weekly:

- Go modules in both `/` and `/test/integration`;
- GitHub Actions references under `.github/workflows`;
- Dockerfile image references;
- Docker Compose image references.

The two Go modules use cross-directory grouping by dependency name. A shared
dependency such as pgx can therefore be updated in one pull request instead of
drifting between the runtime and integration-test modules.

GitHub Actions remain pinned by commit SHA. Dependabot supports SHA-pinned
repository actions and updates the adjacent version comment when the reference
is associated with a release tag.

## Deliberately manual pins

Dependabot cannot safely infer every version encoded inside arbitrary CI shell
variables. Review these pins explicitly during template maintenance:

- `SQLC_VERSION` and its archive SHA-256 in `.github/workflows/ci.yml`;
- the Atlas Community image reference stored in the workflow environment;
- PostgreSQL image digests embedded directly in workflow service definitions
  or integration-test constants.

When changing a binary/archive version, update its checksum in the same commit.
When changing an image tag, resolve and commit the intended immutable digest.

## Update policy

Dependency pull requests must pass the same quality, database, and integration
gates as normal changes. Major upgrades should be reviewed for behavior and
migration impact rather than merged solely because automation opened the pull
request.
