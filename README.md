# github-org-member-sync
Sync FOSS for All members from Keycloak to GitHub Organization

## Tasks

### List Keycloak users

Lists users in a target realm. Optional filters can limit results to users with a linked social login provider, group association, or assigned realm role.

Required inputs:
- `keycloakBaseURL`
- `realm`
- `clientId`
- `clientSecret`

Optional inputs:
- `authRealm`: defaults to `realm` when omitted
- `linkedProvider`: filters users by identity provider alias, such as `github`
- `group`: filters users by group name or full group path, such as `contributors` or `/contributors`
- `realmRole`: filters users by effective realm role name
- `pageSize`: defaults to `100` when omitted or set to `0`

Linked provider example:

```bash
dagger call list-keycloak-users \
  --keycloak-base-url https://sso.example.com \
  --realm fossforall \
  --auth-realm platform-admin \
  --linked-provider github \
  --client-id ci-admin \
  --client-secret env:KEYCLOAK_CLIENT_SECRET \
  users
```

Group example:

```bash
dagger call list-keycloak-users \
  --keycloak-base-url https://sso.example.com \
  --realm fossforall \
  --group /contributors \
  --client-id ci-admin \
  --client-secret env:KEYCLOAK_CLIENT_SECRET \
  users
```

Realm role example:

```bash
dagger call list-keycloak-users \
  --keycloak-base-url https://sso.example.com \
  --realm fossforall \
  --realm-role community-member \
  --client-id ci-admin \
  --client-secret env:KEYCLOAK_CLIENT_SECRET \
  users
```

Each user result includes the Keycloak user ID, username, email, enabled status, federated user ID, and federated username.

Federated fields are populated when `linkedProvider` is used and the user matches that provider.

The confidential client used for authentication must be allowed to obtain a token from `authRealm` and must have permission to read users in the target `realm`.

The result is chainable in Dagger shell through the returned `KeycloakLinkedUsers` object.

### Invite linked users to a GitHub team

Invites Keycloak-linked users to a GitHub organization team using their federated username as the GitHub login. Existing active team members and users with pending team invitations are skipped.

Required inputs:
- `usersJson`
- `githubOrg`
- `githubTeamSlug`
- `githubToken`

Optional inputs:
- `role`: defaults to `member`; must be `member` or `maintainer`
- `githubBaseURL`: defaults to `https://api.github.com`
- `pageSize`: defaults to `100` and is capped at `100`
- `dryRun`: defaults to `false`

Dry-run example:

```bash
dagger call invite-keycloak-users-to-github-org-team \
  --users-json '[{"userId":"keycloak-user-id","username":"keycloak-user","email":"user@example.com","enabled":true,"federatedUserID":"12345","federatedUsername":"github-login"}]' \
  --github-org fossforall \
  --github-team-slug contributors \
  --github-token env:GITHUB_TOKEN \
  --dry-run true
```

Live invitation example:

```bash
dagger call invite-keycloak-users-to-github-org-team \
  --users-json '[{"userId":"keycloak-user-id","username":"keycloak-user","email":"user@example.com","enabled":true,"federatedUserID":"12345","federatedUsername":"github-login"}]' \
  --github-org fossforall \
  --github-team-slug contributors \
  --github-token env:GITHUB_TOKEN \
  --role member
```

Each result includes the Keycloak user ID, Keycloak username, GitHub username, status, membership state, role, and message.

`githubTeamSlug` is the team slug, not the display name. The GitHub token must be able to read the team membership and manage organization/team membership.

### Chain in Dagger shell

Use the listing result directly as the invite input in Dagger shell:

```bash
dagger shell
```

```shell
list-keycloak-users \
  --keycloak-base-url https://sso.example.com \
  --realm fossforall \
  --auth-realm platform-admin \
  --linked-provider github \
  --client-id ci-admin \
  --client-secret env:KEYCLOAK_CLIENT_SECRET \
  | invite-to-github-org-team \
    --github-org fossforall \
    --github-team-slug contributors \
    --github-token env:GITHUB_TOKEN \
    --dry-run true
```

You can omit `--auth-realm` when the confidential client lives in the same realm being queried.

The same chain can be run without entering Dagger shell:

```bash
dagger call list-keycloak-users \
  --keycloak-base-url https://sso.example.com \
  --realm fossforall \
  --auth-realm platform-admin \
  --linked-provider github \
  --client-id ci-admin \
  --client-secret env:KEYCLOAK_CLIENT_SECRET \
  invite-to-github-org-team \
    --github-org fossforall \
    --github-team-slug contributors \
    --github-token env:GITHUB_TOKEN \
    --dry-run true
```
