package main

import (
	"fmt"

	"dagger/github-org-member-sync/internal/dagger"
)

type UserspaceTailscale struct {
	Service *dagger.Service
}

const userspaceTailscaleProxy = "socks5://tailscale:1055"

// Create a userspace Tailscale service that can be chained into other functions.
func (m *GithubOrgMemberSync) UserspaceTailscale(
	authKey *dagger.Secret,
	hostname *string,
) (*UserspaceTailscale, error) {
	if authKey == nil {
		return nil, fmt.Errorf("authKey is required")
	}

	hostnameValue := "github-org-member-sync"
	if hostname != nil && *hostname != "" {
		hostnameValue = *hostname
	}

	service := dag.Container().
		From("tailscale/tailscale:stable").
		WithSecretVariable("TS_AUTHKEY", authKey).
		WithEnvVariable("TS_HOSTNAME", hostnameValue).
		WithExposedPort(1055).
		AsService(dagger.ContainerAsServiceOpts{
			Args: []string{"sh", "-ec", userspaceTailscaleServiceCommand},
		})

	return &UserspaceTailscale{Service: service}, nil
}

// Attach this userspace Tailscale service to a container through proxy variables.
func (t *UserspaceTailscale) WithContainer(container *dagger.Container) (*dagger.Container, error) {
	if t == nil || t.Service == nil {
		return nil, fmt.Errorf("tailscale service is required")
	}
	if container == nil {
		return nil, fmt.Errorf("container is required")
	}

	return container.
		WithServiceBinding("tailscale", t.Service).
		WithEnvVariable("ALL_PROXY", userspaceTailscaleProxy).
		WithEnvVariable("HTTP_PROXY", userspaceTailscaleProxy).
		WithEnvVariable("HTTPS_PROXY", userspaceTailscaleProxy).
		WithEnvVariable("all_proxy", userspaceTailscaleProxy).
		WithEnvVariable("http_proxy", userspaceTailscaleProxy).
		WithEnvVariable("https_proxy", userspaceTailscaleProxy), nil
}

const userspaceTailscaleServiceCommand = `
tailscaled --tun=userspace-networking --socks5-server=0.0.0.0:1055 --state=mem: &

i=0
until tailscale up --auth-key="$TS_AUTHKEY" --hostname="$TS_HOSTNAME" --accept-routes=true; do
	i=$((i + 1))
	if [ "$i" -ge 50 ]; then
		exit 1
	fi
	sleep 0.2
done

tailscale status
wait
`
