from __future__ import annotations

import json
from typing import Annotated, Any

import requests
import typer

from api import ApiError
from github import (
    diff_team_members,
    github_session,
    invite_github_user,
    list_github_team_invitations,
    list_github_team_members,
    remove_github_user,
    skipped_existing_members,
    skipped_pending_invitations,
)
from keycloak import list_keycloak_users


def cli(
    keycloak_base_url: Annotated[str, typer.Option(help="Base URL of the Keycloak server.")],
    realm: Annotated[str, typer.Option(help="Keycloak realm to query.")],
    client_id: Annotated[str, typer.Option(help="Keycloak confidential client ID.")],
    client_secret: Annotated[str, typer.Option(help="Keycloak client secret or env:NAME.")],
    auth_realm: Annotated[str | None, typer.Option(help="Realm used for Keycloak token auth. Defaults to --realm.")] = None,
    linked_provider: Annotated[str | None, typer.Option(help="Federated identity provider alias to require, such as github.")] = None,
    group: Annotated[str | None, typer.Option(help="Group name or full group path to require.")] = None,
    realm_role: Annotated[str | None, typer.Option(help="Effective realm role name to require.")] = None,
    page_size: Annotated[int, typer.Option(min=1, max=100, help="API page size.")] = 100,
    github_org: Annotated[str | None, typer.Option(help="GitHub organization name.")] = None,
    github_team_slug: Annotated[str | None, typer.Option(help="GitHub team slug.")] = None,
    github_token: Annotated[str | None, typer.Option(help="GitHub token or env:NAME.")] = None,
    github_base_url: Annotated[str, typer.Option(help="GitHub API base URL.")] = "https://api.github.com",
    role: Annotated[str, typer.Option(help="GitHub team role for invited users: member or maintainer.")] = "member",
    invite_missing: Annotated[bool, typer.Option(help="Invite Keycloak users missing from the GitHub team.")] = False,
    remove_extra: Annotated[bool, typer.Option(help="Remove GitHub team members missing from Keycloak query results.")] = False,
    dry_run: Annotated[bool, typer.Option("--dry-run/--no-dry-run", help="Preview GitHub changes without mutating. Defaults to dry-run.")] = True,
) -> None:
    """Sync Keycloak-linked users to a GitHub org team."""

    if role not in {"member", "maintainer"}:
        raise typer.BadParameter("role must be 'member' or 'maintainer'")

    sync_enabled = invite_missing or remove_extra
    if sync_enabled:
        missing = [
            name
            for name, value in {
                "--github-org": github_org,
                "--github-team-slug": github_team_slug,
                "--github-token": github_token,
            }.items()
            if not value
        ]
        if missing:
            raise typer.BadParameter(f"GitHub sync requires {', '.join(missing)}")
        if not linked_provider:
            raise typer.BadParameter("GitHub sync requires --linked-provider so federatedUsername can be used as the GitHub username")

    try:
        keycloak_users = list_keycloak_users(
            keycloak_base_url=keycloak_base_url,
            realm=realm,
            auth_realm=auth_realm or realm,
            client_id=client_id,
            client_secret=client_secret,
            linked_provider=linked_provider,
            group=group,
            realm_role=realm_role,
            page_size=page_size,
        )

        if not sync_enabled:
            typer.echo(json.dumps(keycloak_users, indent=2, sort_keys=True))
            return

        assert github_org is not None
        assert github_team_slug is not None
        assert github_token is not None

        github = github_session(github_token)
        github_members = list_github_team_members(github, github_base_url, github_org, github_team_slug, page_size)
        github_invitations = list_github_team_invitations(github, github_base_url, github_org, github_team_slug, page_size)
        keycloak_github_users = sorted(
            user["federatedUsername"] for user in keycloak_users if user.get("federatedUsername")
        )
        to_invite, to_remove = diff_team_members(keycloak_github_users, github_members, github_invitations)
        existing_members = skipped_existing_members(keycloak_github_users, github_members)
        pending_invitations = skipped_pending_invitations(keycloak_github_users, github_invitations)

        invite_results = sync_invitations(
            enabled=invite_missing,
            dry_run=dry_run,
            usernames=to_invite,
            github=github,
            github_base_url=github_base_url,
            github_org=github_org,
            github_team_slug=github_team_slug,
            role=role,
        )
        remove_results = sync_removals(
            enabled=remove_extra,
            dry_run=dry_run,
            usernames=to_remove,
            github=github,
            github_base_url=github_base_url,
            github_org=github_org,
            github_team_slug=github_team_slug,
        )

        output = {
            "dryRun": dry_run,
            "keycloakUsers": keycloak_users,
            "keycloakGithubUsers": keycloak_github_users,
            "githubTeamMembers": github_members,
            "githubTeamInvitations": github_invitations,
            "skippedExistingMembers": existing_members,
            "skippedPendingInvitations": pending_invitations,
            "toInvite": to_invite if invite_missing else [],
            "toRemove": to_remove if remove_extra else [],
            "inviteResults": invite_results,
            "removeResults": remove_results,
        }
        typer.echo(json.dumps(output, indent=2, sort_keys=True))
    except (ApiError, RuntimeError, requests.RequestException, ValueError) as exc:
        typer.echo(f"Error: {exc}", err=True)
        raise typer.Exit(1) from exc


def sync_invitations(
    *,
    enabled: bool,
    dry_run: bool,
    usernames: list[str],
    github: requests.Session,
    github_base_url: str,
    github_org: str,
    github_team_slug: str,
    role: str,
) -> list[dict[str, Any]]:
    if not enabled:
        return []
    if dry_run:
        return [{"githubUsername": username, "status": "dry-run"} for username in usernames]
    return [
        invite_github_user(github, github_base_url, github_org, github_team_slug, username, role)
        for username in usernames
    ]


def sync_removals(
    *,
    enabled: bool,
    dry_run: bool,
    usernames: list[str],
    github: requests.Session,
    github_base_url: str,
    github_org: str,
    github_team_slug: str,
) -> list[dict[str, Any]]:
    if not enabled:
        return []
    if dry_run:
        return [{"githubUsername": username, "status": "dry-run"} for username in usernames]
    return [remove_github_user(github, github_base_url, github_org, github_team_slug, username) for username in usernames]


def main() -> None:
    typer.run(cli)


if __name__ == "__main__":
    main()
