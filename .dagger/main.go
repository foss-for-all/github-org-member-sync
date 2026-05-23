package main

import (
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
