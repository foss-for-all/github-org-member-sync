from __future__ import annotations

from typing import Any

import requests

from api import normalize_base_url, paginated_get, request_json, resolve_secret


def github_session(github_token: str) -> requests.Session:
    session = requests.Session()
    session.headers.update(
        {
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {resolve_secret(github_token)}",
            "X-GitHub-Api-Version": "2022-11-28",
        }
    )
    return session


def list_github_team_members(
    session: requests.Session,
    github_base_url: str,
    org: str,
    team_slug: str,
    page_size: int,
) -> list[str]:
    url = f"{normalize_base_url(github_base_url)}/orgs/{org}/teams/{team_slug}/members"
    members = paginated_get(session, url, page_size=page_size, params={"role": "all"})
    return sorted(member["login"] for member in members if member.get("login"))


def list_github_team_invitations(
    session: requests.Session,
    github_base_url: str,
    org: str,
    team_slug: str,
    page_size: int,
) -> list[str]:
    url = f"{normalize_base_url(github_base_url)}/orgs/{org}/teams/{team_slug}/invitations"
    invitations = paginated_get(session, url, page_size=page_size)
    logins: list[str] = []
    for invitation in invitations:
        invitee = invitation.get("invitee") or {}
        login = invitation.get("login") or invitee.get("login")
        if login:
            logins.append(login)
    return sorted(logins)


def invite_github_user(
    session: requests.Session,
    github_base_url: str,
    org: str,
    team_slug: str,
    username: str,
    role: str,
) -> dict[str, Any]:
    url = f"{normalize_base_url(github_base_url)}/orgs/{org}/teams/{team_slug}/memberships/{username}"
    data = request_json(session, "PUT", url, expected_status=(200, 201), json={"role": role})
    return {"githubUsername": username, "status": "invited", "response": data}


def remove_github_user(
    session: requests.Session,
    github_base_url: str,
    org: str,
    team_slug: str,
    username: str,
) -> dict[str, Any]:
    url = f"{normalize_base_url(github_base_url)}/orgs/{org}/teams/{team_slug}/memberships/{username}"
    request_json(session, "DELETE", url, expected_status=(204,))
    return {"githubUsername": username, "status": "removed"}


def diff_team_members(
    keycloak_github_users: list[str],
    github_members: list[str],
    github_invitations: list[str],
) -> tuple[list[str], list[str]]:
    existing_or_pending = {username.lower() for username in github_members + github_invitations}
    keycloak_github_users_set = {username.lower() for username in keycloak_github_users}
    to_invite = [username for username in keycloak_github_users if username.lower() not in existing_or_pending]
    to_remove = [username for username in github_members if username.lower() not in keycloak_github_users_set]
    return to_invite, to_remove


def skipped_existing_members(keycloak_github_users: list[str], github_members: list[str]) -> list[str]:
    github_members_set = {username.lower() for username in github_members}
    return [username for username in keycloak_github_users if username.lower() in github_members_set]


def skipped_pending_invitations(keycloak_github_users: list[str], github_invitations: list[str]) -> list[str]:
    github_invitations_set = {username.lower() for username in github_invitations}
    return [username for username in keycloak_github_users if username.lower() in github_invitations_set]
