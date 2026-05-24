package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dagger/github-org-member-sync/internal/dagger"
)

type KeycloakLinkedUser struct {
	UserID            string
	Username          string
	Email             string
	Enabled           bool
	FederatedUserID   string
	FederatedUsername string
}

type KeycloakLinkedUsers struct {
	Users []KeycloakLinkedUser
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

type keycloakGroup struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type keycloakRole struct {
	Name string `json:"name"`
}

// List Keycloak users in a realm with optional filters.
func (m *GithubOrgMemberSync) ListKeycloakUsers(
	ctx context.Context,
	keycloakBaseURL string,
	realm string,
	authRealm *string,
	linkedProvider *string,
	group *string,
	realmRole *string,
	clientId string,
	clientSecret *dagger.Secret,
	pageSize *int,
) (*KeycloakLinkedUsers, error) {
	if keycloakBaseURL == "" {
		return nil, fmt.Errorf("keycloakBaseURL is required")
	}
	if realm == "" {
		return nil, fmt.Errorf("realm is required")
	}
	if clientId == "" {
		return nil, fmt.Errorf("clientId is required")
	}
	if clientSecret == nil {
		return nil, fmt.Errorf("clientSecret is required")
	}
	authRealmValue := realm
	if authRealm != nil && *authRealm != "" {
		authRealmValue = *authRealm
	}
	perPage := 100
	if pageSize != nil && *pageSize > 0 {
		perPage = *pageSize
	}

	secretValue, err := clientSecret.Plaintext(ctx)
	if err != nil {
		return nil, fmt.Errorf("read client secret: %w", err)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	token, err := getAccessToken(ctx, httpClient, keycloakBaseURL, authRealmValue, clientId, secretValue)
	if err != nil {
		return nil, err
	}

	var results []KeycloakLinkedUser
	first := 0
	for {
		users, err := getUsersPage(ctx, httpClient, keycloakBaseURL, realm, token, first, perPage)
		if err != nil {
			return nil, err
		}

		for _, user := range users {
			identity, matches, err := userMatchesKeycloakFilters(ctx, httpClient, keycloakBaseURL, realm, token, user.ID, linkedProvider, group, realmRole)
			if err != nil {
				return nil, err
			}
			if !matches {
				continue
			}

			linkedUser := KeycloakLinkedUser{
				UserID:   user.ID,
				Username: user.Username,
				Email:    user.Email,
				Enabled:  user.Enabled,
			}
			if identity != nil {
				linkedUser.FederatedUserID = identity.UserID
				linkedUser.FederatedUsername = identity.UserName
			}

			results = append(results, linkedUser)
		}

		if len(users) < perPage {
			break
		}
		first += len(users)
	}

	return &KeycloakLinkedUsers{Users: results}, nil
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

func userMatchesKeycloakFilters(
	ctx context.Context,
	httpClient *http.Client,
	keycloakBaseURL string,
	realm string,
	accessToken string,
	userID string,
	linkedProvider *string,
	group *string,
	realmRole *string,
) (*keycloakFederatedIdentity, bool, error) {
	var matchedIdentity *keycloakFederatedIdentity

	if linkedProvider != nil && *linkedProvider != "" {
		identity, ok, err := getMatchingFederatedIdentity(ctx, httpClient, keycloakBaseURL, realm, userID, accessToken, *linkedProvider)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		matchedIdentity = identity
	}

	if group != nil && *group != "" {
		ok, err := userHasGroup(ctx, httpClient, keycloakBaseURL, realm, userID, accessToken, *group)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
	}

	if realmRole != nil && *realmRole != "" {
		ok, err := userHasRealmRole(ctx, httpClient, keycloakBaseURL, realm, userID, accessToken, *realmRole)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
	}

	return matchedIdentity, true, nil
}

func getMatchingFederatedIdentity(ctx context.Context, httpClient *http.Client, keycloakBaseURL string, realm string, userID string, accessToken string, provider string) (*keycloakFederatedIdentity, bool, error) {
	identities, err := getFederatedIdentities(ctx, httpClient, keycloakBaseURL, realm, userID, accessToken)
	if err != nil {
		return nil, false, err
	}

	for _, identity := range identities {
		if identity.IdentityProvider == provider {
			matchedIdentity := identity
			return &matchedIdentity, true, nil
		}
	}

	return nil, false, nil
}

func userHasGroup(ctx context.Context, httpClient *http.Client, keycloakBaseURL string, realm string, userID string, accessToken string, group string) (bool, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		buildURL(keycloakBaseURL, fmt.Sprintf("admin/realms/%s/users/%s/groups", url.PathEscape(realm), url.PathEscape(userID)), nil),
		nil,
	)
	if err != nil {
		return false, fmt.Errorf("build user groups request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	var groups []keycloakGroup
	if err := doJSON(req, httpClient, &groups); err != nil {
		return false, fmt.Errorf("list groups for user %q in realm %q: %w", userID, realm, err)
	}

	for _, userGroup := range groups {
		if userGroup.Name == group || userGroup.Path == group {
			return true, nil
		}
	}

	return false, nil
}

func userHasRealmRole(ctx context.Context, httpClient *http.Client, keycloakBaseURL string, realm string, userID string, accessToken string, role string) (bool, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		buildURL(keycloakBaseURL, fmt.Sprintf("admin/realms/%s/users/%s/role-mappings/realm/composite", url.PathEscape(realm), url.PathEscape(userID)), nil),
		nil,
	)
	if err != nil {
		return false, fmt.Errorf("build user realm roles request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	var roles []keycloakRole
	if err := doJSON(req, httpClient, &roles); err != nil {
		return false, fmt.Errorf("list realm roles for user %q in realm %q: %w", userID, realm, err)
	}

	for _, userRole := range roles {
		if userRole.Name == role {
			return true, nil
		}
	}

	return false, nil
}
