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

type GithubOrgMemberSync struct{}

type KeycloakLinkedUser struct {
	UserID            string
	Username          string
	Email             string
	Enabled           bool
	FederatedUserID   string
	FederatedUsername string
}

type GithubTeamInviteResult struct {
	KeycloakUserID   string
	KeycloakUsername string
	GithubUsername   string
	Status           string
	State            string
	Role             string
	Message          string
}

type keycloakTokenResponse struct {
	AccessToken string `json:"access_token"`
}

type keycloakUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Enabled  bool   `json:"enabled"`
}

type keycloakFederatedIdentity struct {
	IdentityProvider string `json:"identityProvider"`
	UserID           string `json:"userId"`
	UserName         string `json:"userName"`
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

// List Keycloak users in a realm with a linked identity provider.
func (m *GithubOrgMemberSync) ListKeycloakUsersByLinkedProvider(
	ctx context.Context,
	keycloakBaseURL string,
	realm string,
	authRealm string,
	idProvider string,
	clientId string,
	clientSecret *dagger.Secret,
	pageSize int,
) ([]KeycloakLinkedUser, error) {
	if keycloakBaseURL == "" {
		return nil, fmt.Errorf("keycloakBaseURL is required")
	}
	if realm == "" {
		return nil, fmt.Errorf("realm is required")
	}
	if idProvider == "" {
		return nil, fmt.Errorf("idProvider is required")
	}
	if clientId == "" {
		return nil, fmt.Errorf("clientId is required")
	}
	if clientSecret == nil {
		return nil, fmt.Errorf("clientSecret is required")
	}
	if authRealm == "" {
		authRealm = realm
	}
	if pageSize <= 0 {
		pageSize = 100
	}

	secretValue, err := clientSecret.Plaintext(ctx)
	if err != nil {
		return nil, fmt.Errorf("read client secret: %w", err)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	token, err := getAccessToken(ctx, httpClient, keycloakBaseURL, authRealm, clientId, secretValue)
	if err != nil {
		return nil, err
	}

	var results []KeycloakLinkedUser
	first := 0
	for {
		users, err := getUsersPage(ctx, httpClient, keycloakBaseURL, realm, token, first, pageSize)
		if err != nil {
			return nil, err
		}

		for _, user := range users {
			identities, err := getFederatedIdentities(ctx, httpClient, keycloakBaseURL, realm, user.ID, token)
			if err != nil {
				return nil, err
			}

			for _, identity := range identities {
				if identity.IdentityProvider != idProvider {
					continue
				}

				results = append(results, KeycloakLinkedUser{
					UserID:            user.ID,
					Username:          user.Username,
					Email:             user.Email,
					Enabled:           user.Enabled,
					FederatedUserID:   identity.UserID,
					FederatedUsername: identity.UserName,
				})
				break
			}
		}

		if len(users) < pageSize {
			break
		}
		first += len(users)
	}

	return results, nil
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

func getAccessToken(ctx context.Context, httpClient *http.Client, keycloakBaseURL string, authRealm string, clientId string, clientSecret string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", clientId)
	form.Set("client_secret", clientSecret)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		buildURL(keycloakBaseURL, fmt.Sprintf("realms/%s/protocol/openid-connect/token", url.PathEscape(authRealm)), nil),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var tokenResponse keycloakTokenResponse
	if err := doJSON(req, httpClient, &tokenResponse); err != nil {
		return "", fmt.Errorf("fetch access token: %w", err)
	}
	if tokenResponse.AccessToken == "" {
		return "", fmt.Errorf("fetch access token: empty access_token in response")
	}

	return tokenResponse.AccessToken, nil
}

func getUsersPage(ctx context.Context, httpClient *http.Client, keycloakBaseURL string, realm string, accessToken string, first int, pageSize int) ([]keycloakUser, error) {
	query := url.Values{}
	query.Set("first", strconv.Itoa(first))
	query.Set("max", strconv.Itoa(pageSize))

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		buildURL(keycloakBaseURL, fmt.Sprintf("admin/realms/%s/users", url.PathEscape(realm)), query),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("build users request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	var users []keycloakUser
	if err := doJSON(req, httpClient, &users); err != nil {
		return nil, fmt.Errorf("list users for realm %q: %w", realm, err)
	}

	return users, nil
}

func getFederatedIdentities(ctx context.Context, httpClient *http.Client, keycloakBaseURL string, realm string, userID string, accessToken string) ([]keycloakFederatedIdentity, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		buildURL(keycloakBaseURL, fmt.Sprintf("admin/realms/%s/users/%s/federated-identity", url.PathEscape(realm), url.PathEscape(userID)), nil),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("build federated identity request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	var identities []keycloakFederatedIdentity
	if err := doJSON(req, httpClient, &identities); err != nil {
		return nil, fmt.Errorf("list federated identities for user %q in realm %q: %w", userID, realm, err)
	}

	return identities, nil
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

func doJSON(req *http.Request, httpClient *http.Client, out any) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", req.Method, req.URL.String(), err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %s %s response: %w", req.Method, req.URL.String(), err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s returned %s: %s", req.Method, req.URL.String(), resp.Status, strings.TrimSpace(string(body)))
	}
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s %s response: %w", req.Method, req.URL.String(), err)
	}

	return nil
}

func buildURL(base string, path string, query url.Values) string {
	trimmedBase := strings.TrimRight(base, "/")
	trimmedPath := strings.TrimLeft(path, "/")
	fullURL := trimmedBase + "/" + trimmedPath
	if len(query) == 0 {
		return fullURL
	}
	return fullURL + "?" + query.Encode()
}
