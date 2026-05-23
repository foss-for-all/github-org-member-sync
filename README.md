# github-org-member-sync
Sync FOSS for All members from Keycloak to GitHub Organization

## Tasks

### List Keycloak users by linked provider

Lists users in a target realm that have a linked social login for a specific identity provider alias such as `github`.

Required inputs:
- `keycloakBaseURL`
- `realm`
- `idProvider`
- `clientId`
- `clientSecret`

Optional inputs:
- `authRealm`: defaults to `realm` when omitted
- `pageSize`: defaults to `100` when omitted or set to `0`

Example:

```bash
dagger call list-keycloak-users-by-linked-provider \
  --keycloak-base-url https://sso.example.com \
  --realm fossforall \
  --auth-realm platform-admin \
  --id-provider github \
  --client-i-d ci-admin \
  --client-secret env:KEYCLOAK_CLIENT_SECRET
```

Each result includes the Keycloak user ID, username, email, enabled status, federated user ID, and federated username.

The confidential client used for authentication must be allowed to obtain a token from `authRealm` and must have permission to read users in the target `realm`.
