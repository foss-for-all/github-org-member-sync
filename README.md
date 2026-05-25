# member-sync

Sync Keycloak-linked users to a GitHub organization team.

The CLI lists users from a Keycloak realm by default. GitHub team changes only run when `--invite-missing` or `--remove-extra` is passed, and GitHub mutations are dry-run by default.

## Install

With uv:

```bash
uv sync
```

Or with pip:

```bash
python -m pip install -e .
```

## Default Behavior

Without GitHub sync flags, the command only queries Keycloak and prints matching users as JSON.

```bash
member-sync \
  --keycloak-base-url https://sso.example.com \
  --realm fossforall \
  --client-id ci-admin \
  --client-secret env:KEYCLOAK_CLIENT_SECRET
```

## Keycloak Options

Required:

- `--keycloak-base-url`: Keycloak server base URL.
- `--realm`: Realm to query.
- `--client-id`: Confidential client ID.
- `--client-secret`: Client secret. Use `env:NAME` to read from an environment variable.

Optional:

- `--auth-realm`: Realm used for token authentication. Defaults to `--realm`.
- `--linked-provider`: Federated identity provider alias to require, such as `github`.
- `--group`: Group name or full group path to require, such as `contributors` or `/contributors`.
- `--realm-role`: Effective realm role name to require.
- `--page-size`: API page size. Defaults to `100`.

The Keycloak client must be allowed to obtain a token and read users, federated identities, groups, and effective realm roles in the target realm.

## List Keycloak Users

List users linked to GitHub:

```bash
member-sync \
  --keycloak-base-url https://sso.example.com \
  --realm fossforall \
  --auth-realm platform-admin \
  --client-id ci-admin \
  --client-secret env:KEYCLOAK_CLIENT_SECRET \
  --linked-provider github
```

List linked users filtered by group and realm role:

```bash
member-sync \
  --keycloak-base-url https://sso.example.com \
  --realm fossforall \
  --client-id ci-admin \
  --client-secret env:KEYCLOAK_CLIENT_SECRET \
  --linked-provider github \
  --group /contributors \
  --realm-role community-member
```

Keycloak-only output is a JSON array:

```json
[
  {
    "email": "user@example.com",
    "enabled": true,
    "federatedUserID": "12345",
    "federatedUsername": "github-login",
    "userId": "keycloak-user-id",
    "username": "keycloak-user"
  }
]
```

## GitHub Sync Options

Required when `--invite-missing` or `--remove-extra` is used:

- `--github-org`: GitHub organization name.
- `--github-team-slug`: GitHub team slug, not display name.
- `--github-token`: GitHub token. Use `env:NAME` to read from an environment variable.
- `--linked-provider`: Required because GitHub usernames come from Keycloak `federatedUsername` for the matched provider.

Optional:

- `--github-base-url`: GitHub API base URL. Defaults to `https://api.github.com`.
- `--role`: Team role for invited users. Must be `member` or `maintainer`. Defaults to `member`.
- `--invite-missing`: Invite/add Keycloak-linked users missing from the GitHub team.
- `--remove-extra`: Remove GitHub team members missing from the Keycloak query result.
- `--dry-run / --no-dry-run`: Preview or apply GitHub changes. Defaults to `--dry-run`.

The GitHub token must be able to read team members and invitations, and manage organization/team membership for live changes.

## Dry-Run Sync

Preview missing invitations:

```bash
member-sync \
  --keycloak-base-url https://sso.example.com \
  --realm fossforall \
  --client-id github-org-agent \
  --client-secret env:KEYCLOAK_CLIENT_SECRET \
  --linked-provider github \
  --github-org fossforall \
  --github-team-slug full-members \
  --github-token env:GITHUB_TOKEN \
  --invite-missing
```

Preview removals:

```bash
member-sync \
  --keycloak-base-url https://sso.example.com \
  --realm fossforall \
  --client-id ci-admin \
  --client-secret env:KEYCLOAK_CLIENT_SECRET \
  --linked-provider github \
  --group /contributors \
  --github-org fossforall \
  --github-team-slug contributors \
  --github-token env:GITHUB_TOKEN \
  --remove-extra
```

Preview a full sync:

```bash
member-sync \
  --keycloak-base-url https://sso.example.com \
  --realm fossforall \
  --client-id ci-admin \
  --client-secret env:KEYCLOAK_CLIENT_SECRET \
  --linked-provider github \
  --group /contributors \
  --realm-role community-member \
  --github-org fossforall \
  --github-team-slug contributors \
  --github-token env:GITHUB_TOKEN \
  --invite-missing \
  --remove-extra
```

## Live Sync

Live GitHub changes require `--no-dry-run`.

```bash
member-sync \
  --keycloak-base-url https://sso.example.com \
  --realm fossforall \
  --client-id ci-admin \
  --client-secret env:KEYCLOAK_CLIENT_SECRET \
  --linked-provider github \
  --group /contributors \
  --realm-role community-member \
  --github-org fossforall \
  --github-team-slug contributors \
  --github-token env:GITHUB_TOKEN \
  --invite-missing \
  --remove-extra \
  --no-dry-run
```

## Sync Output

Sync mode prints a JSON object:

```json
{
  "dryRun": true,
  "keycloakGithubUsers": ["existing-login", "new-login", "pending-login"],
  "githubTeamInvitations": ["pending-login"],
  "githubTeamMembers": ["existing-login"],
  "inviteResults": [
    {
      "githubUsername": "new-login",
      "status": "dry-run"
    }
  ],
  "keycloakUsers": [],
  "removeResults": [],
  "skippedExistingMembers": ["existing-login"],
  "skippedPendingInvitations": ["pending-login"],
  "toInvite": ["new-login"],
  "toRemove": []
}
```

The diagnostic fields help explain why `toInvite` is empty:

- `keycloakGithubUsers`: GitHub usernames derived from matched Keycloak federated identities.
- `skippedExistingMembers`: Keycloak-derived usernames already active in the GitHub team.
- `skippedPendingInvitations`: Keycloak-derived usernames already pending invitation to the GitHub team.

## API Behavior

Keycloak:

- Authenticates with client credentials at `/realms/{auth_realm}/protocol/openid-connect/token`.
- Lists users from `/admin/realms/{realm}/users`.
- Applies linked provider, group, and realm role filters client-side.

GitHub:

- Lists active team members from `/orgs/{org}/teams/{team_slug}/members`.
- Lists pending team invitations from `/orgs/{org}/teams/{team_slug}/invitations`.
- Adds missing users with `PUT /orgs/{org}/teams/{team_slug}/memberships/{username}`.
- Removes extra users with `DELETE /orgs/{org}/teams/{team_slug}/memberships/{username}`.
