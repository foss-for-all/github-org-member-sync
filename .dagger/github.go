package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dagger/github-org-member-sync/internal/dagger"
)

type GithubTeamInviteResult struct {
	KeycloakUserID   string
	KeycloakUsername string
	GithubUsername   string
	Status           string
	State            string
	Role             string
	Message          string
}

type githubUser struct {
	Login string `json:"login"`
}

type githubInvitation struct {
	Login *string `json:"login"`
}

type githubTeamMembership struct {
	Role  string `json:"role"`
	State string `json:"state"`
}

// Invite these linked users to a GitHub organization team.
func (users *KeycloakLinkedUsers) InviteToGithubOrgTeam(
	ctx context.Context,
	githubOrg string,
	githubTeamSlug string,
	githubToken *dagger.Secret,
	role *string,
	githubBaseURL *string,
	pageSize *int,
	dryRun *bool,
) ([]GithubTeamInviteResult, error) {
	if users == nil {
		return nil, fmt.Errorf("users is required")
	}

	return inviteKeycloakUsersToGithubOrgTeam(ctx, users.Users, githubOrg, githubTeamSlug, githubToken, role, githubBaseURL, pageSize, dryRun)
}

// Invite linked Keycloak users to a GitHub organization team.
func (m *GithubOrgMemberSync) InviteKeycloakUsersToGithubOrgTeam(
	ctx context.Context,
	usersJson string,
	githubOrg string,
	githubTeamSlug string,
	githubToken *dagger.Secret,
	role *string,
	githubBaseURL *string,
	pageSize *int,
	dryRun *bool,
) ([]GithubTeamInviteResult, error) {
	if usersJson == "" {
		return nil, fmt.Errorf("usersJson is required")
	}

	var users []KeycloakLinkedUser
	if err := json.Unmarshal([]byte(usersJson), &users); err != nil {
		return nil, fmt.Errorf("parse usersJson: %w", err)
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("usersJson must contain at least one user")
	}

	return inviteKeycloakUsersToGithubOrgTeam(ctx, users, githubOrg, githubTeamSlug, githubToken, role, githubBaseURL, pageSize, dryRun)
}

func inviteKeycloakUsersToGithubOrgTeam(
	ctx context.Context,
	users []KeycloakLinkedUser,
	githubOrg string,
	githubTeamSlug string,
	githubToken *dagger.Secret,
	role *string,
	githubBaseURL *string,
	pageSize *int,
	dryRun *bool,
) ([]GithubTeamInviteResult, error) {
	if len(users) == 0 {
		return nil, fmt.Errorf("users is required")
	}
	if githubOrg == "" {
		return nil, fmt.Errorf("githubOrg is required")
	}
	if githubTeamSlug == "" {
		return nil, fmt.Errorf("githubTeamSlug is required")
	}
	if githubToken == nil {
		return nil, fmt.Errorf("githubToken is required")
	}
	membershipRole := "member"
	if role != nil && *role != "" {
		membershipRole = *role
	}
	if membershipRole != "member" && membershipRole != "maintainer" {
		return nil, fmt.Errorf("role must be member or maintainer")
	}
	apiBaseURL := "https://api.github.com"
	if githubBaseURL != nil && *githubBaseURL != "" {
		apiBaseURL = *githubBaseURL
	}
	perPage := 100
	if pageSize != nil && *pageSize > 0 {
		perPage = *pageSize
	}
	if perPage > 100 {
		perPage = 100
	}
	shouldDryRun := false
	if dryRun != nil {
		shouldDryRun = *dryRun
	}

	token, err := githubToken.Plaintext(ctx)
	if err != nil {
		return nil, fmt.Errorf("read GitHub token: %w", err)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	members, err := listGithubTeamMembers(ctx, httpClient, apiBaseURL, token, githubOrg, githubTeamSlug, perPage)
	if err != nil {
		return nil, err
	}
	invitations, err := listGithubTeamInvitations(ctx, httpClient, apiBaseURL, token, githubOrg, githubTeamSlug, perPage)
	if err != nil {
		return nil, err
	}

	results := make([]GithubTeamInviteResult, 0, len(users))
	for _, user := range users {
		result := GithubTeamInviteResult{
			KeycloakUserID:   user.UserID,
			KeycloakUsername: user.Username,
			GithubUsername:   user.FederatedUsername,
			Role:             membershipRole,
		}

		githubUsername := strings.TrimSpace(user.FederatedUsername)
		if githubUsername == "" {
			result.Status = "skipped_missing_github_username"
			result.Message = "Keycloak linked user has no federated username"
			results = append(results, result)
			continue
		}

		lookupUsername := strings.ToLower(githubUsername)
		if members[lookupUsername] {
			result.Status = "already_member"
			result.Message = "GitHub user is already an active team member"
			results = append(results, result)
			continue
		}
		if invitations[lookupUsername] {
			result.Status = "already_invited"
			result.State = "pending"
			result.Message = "GitHub user already has a pending team invitation"
			results = append(results, result)
			continue
		}
		if shouldDryRun {
			result.Status = "dry_run"
			result.Message = "Would add or update GitHub team membership"
			results = append(results, result)
			continue
		}

		membership, err := putGithubTeamMembership(ctx, httpClient, apiBaseURL, token, githubOrg, githubTeamSlug, githubUsername, membershipRole)
		if err != nil {
			result.Status = "failed"
			result.Message = err.Error()
			results = append(results, result)
			continue
		}

		result.Role = membership.Role
		result.State = membership.State
		if membership.State == "pending" {
			result.Status = "invited"
			result.Message = "GitHub team invitation is pending"
		} else {
			result.Status = "updated"
			result.Message = "GitHub team membership was added or updated"
		}
		results = append(results, result)
	}

	return results, nil
}

func listGithubTeamMembers(ctx context.Context, httpClient *http.Client, githubBaseURL string, token string, org string, teamSlug string, pageSize int) (map[string]bool, error) {
	members := map[string]bool{}
	page := 1
	for {
		query := url.Values{}
		query.Set("role", "all")
		query.Set("per_page", strconv.Itoa(pageSize))
		query.Set("page", strconv.Itoa(page))

		req, err := newGithubRequest(ctx, http.MethodGet, githubBaseURL, token, fmt.Sprintf("orgs/%s/teams/%s/members", url.PathEscape(org), url.PathEscape(teamSlug)), query, nil)
		if err != nil {
			return nil, fmt.Errorf("build GitHub team members request: %w", err)
		}

		var pageMembers []githubUser
		if err := doJSON(req, httpClient, &pageMembers); err != nil {
			return nil, fmt.Errorf("list GitHub team members for %s/%s: %w", org, teamSlug, err)
		}
		for _, member := range pageMembers {
			if member.Login != "" {
				members[strings.ToLower(member.Login)] = true
			}
		}

		if len(pageMembers) < pageSize {
			break
		}
		page++
	}

	return members, nil
}

func listGithubTeamInvitations(ctx context.Context, httpClient *http.Client, githubBaseURL string, token string, org string, teamSlug string, pageSize int) (map[string]bool, error) {
	invitations := map[string]bool{}
	page := 1
	for {
		query := url.Values{}
		query.Set("per_page", strconv.Itoa(pageSize))
		query.Set("page", strconv.Itoa(page))

		req, err := newGithubRequest(ctx, http.MethodGet, githubBaseURL, token, fmt.Sprintf("orgs/%s/teams/%s/invitations", url.PathEscape(org), url.PathEscape(teamSlug)), query, nil)
		if err != nil {
			return nil, fmt.Errorf("build GitHub team invitations request: %w", err)
		}

		var pageInvitations []githubInvitation
		if err := doJSON(req, httpClient, &pageInvitations); err != nil {
			return nil, fmt.Errorf("list GitHub team invitations for %s/%s: %w", org, teamSlug, err)
		}
		for _, invitation := range pageInvitations {
			if invitation.Login != nil && *invitation.Login != "" {
				invitations[strings.ToLower(*invitation.Login)] = true
			}
		}

		if len(pageInvitations) < pageSize {
			break
		}
		page++
	}

	return invitations, nil
}

func putGithubTeamMembership(ctx context.Context, httpClient *http.Client, githubBaseURL string, token string, org string, teamSlug string, username string, role string) (githubTeamMembership, error) {
	body, err := json.Marshal(map[string]string{"role": role})
	if err != nil {
		return githubTeamMembership{}, fmt.Errorf("encode GitHub team membership request: %w", err)
	}

	req, err := newGithubRequest(ctx, http.MethodPut, githubBaseURL, token, fmt.Sprintf("orgs/%s/teams/%s/memberships/%s", url.PathEscape(org), url.PathEscape(teamSlug), url.PathEscape(username)), nil, bytes.NewReader(body))
	if err != nil {
		return githubTeamMembership{}, fmt.Errorf("build GitHub team membership request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	var membership githubTeamMembership
	if err := doJSON(req, httpClient, &membership); err != nil {
		return githubTeamMembership{}, fmt.Errorf("add or update GitHub team membership for %q: %w", username, err)
	}

	return membership, nil
}

func newGithubRequest(ctx context.Context, method string, githubBaseURL string, token string, path string, query url.Values, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, buildURL(githubBaseURL, path, query), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	return req, nil
}
