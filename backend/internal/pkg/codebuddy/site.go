package codebuddy

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

const (
	SiteCN   = "cn"
	SiteIntl = "intl"

	ProductCLI       = "cli"
	ProductWorkBuddy = "workbuddy"

	ProfileCNCLI    = "cn-cli"
	ProfileCNWork   = "cn-work"
	ProfileIntlCLI  = "intl-cli"
	ProfileIntlWork = "intl-work"

	DomesticEndpoint      = "https://copilot.tencent.com"
	InternationalEndpoint = "https://www.codebuddy.ai"
	WorkBuddyCNEndpoint   = "https://www.workbuddy.cn"
	WorkBuddyIntlEndpoint = "https://www.workbuddy.ai"

	AuthHostCN   = "https://www.codebuddy.cn"
	AuthHostIntl = "https://www.workbuddy.ai"

	RefreshPath      = "/v2/plugin/auth/token/refresh"
	ChatPath         = "/v2/chat/completions"
	AuthStatePath    = "/v2/plugin/auth/state?platform=workbuddy"
	AuthTokenPath    = "/v2/plugin/auth/token"
	LoginAccountPath = "/v2/plugin/login/account"
	ConfigPath       = "/v3/config"

	DefaultSystemMessage = "You are a helpful assistant."
	DefaultUserAgent     = "codebuddy2api"
)

var ProfileEndpoints = map[string]string{
	ProfileCNCLI:    DomesticEndpoint,
	ProfileCNWork:   WorkBuddyCNEndpoint,
	ProfileIntlCLI:  InternationalEndpoint,
	ProfileIntlWork: WorkBuddyIntlEndpoint,
}

var DomainProfiles = map[string]string{
	"www.codebuddy.cn":    ProfileCNCLI,
	"www.workbuddy.cn":    ProfileCNWork,
	"copilot.tencent.com": ProfileCNCLI,
	"www.codebuddy.ai":    ProfileIntlCLI,
	"www.workbuddy.ai":    ProfileIntlWork,
}

var AllowedOrigins = map[string]struct{}{
	"https://www.workbuddy.cn":    {},
	"https://www.codebuddy.cn":    {},
	"https://copilot.tencent.com": {},
	"https://www.workbuddy.ai":    {},
	"https://www.codebuddy.ai":    {},
}

var SiteHosts = map[string]string{
	SiteCN:   AuthHostCN,
	SiteIntl: AuthHostIntl,
}

var BillingHosts = map[string]string{
	ProfileCNCLI:    AuthHostCN,
	ProfileCNWork:   WorkBuddyCNEndpoint,
	ProfileIntlCLI:  InternationalEndpoint,
	ProfileIntlWork: WorkBuddyIntlEndpoint,
}

func ProfileRegion(profile string) string {
	if _, ok := ProfileEndpoints[profile]; !ok {
		return ""
	}
	if strings.HasPrefix(profile, "intl") {
		return SiteIntl
	}
	return SiteCN
}

func ProfileProduct(profile string) string {
	if strings.HasSuffix(profile, "-work") {
		return ProductWorkBuddy
	}
	return ProductCLI
}

func NormalizeHost(value string) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return "", fmt.Errorf("empty host")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid host")
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme != "https" || host == "" || parsed.User != nil {
		return "", fmt.Errorf("unsupported host")
	}
	if _, ok := DomainProfiles[host]; !ok {
		return "", fmt.Errorf("unsupported host %s", host)
	}
	return host, nil
}

func TokenIssuerHost(accessToken string) string {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload := parts[1]
	if rem := len(payload) % 4; rem != 0 {
		payload += strings.Repeat("=", 4-rem)
	}
	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		decoded, err = base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}
	var claims struct {
		ISS string `json:"iss"`
	}
	if json.Unmarshal(decoded, &claims) != nil {
		return ""
	}
	host, err := NormalizeHost(claims.ISS)
	if err != nil {
		return ""
	}
	return host
}

func ProfileForAuth(domain, accessToken string) (string, error) {
	var domainHost, issuerHost string
	if strings.TrimSpace(domain) != "" {
		host, err := NormalizeHost(domain)
		if err != nil {
			return "", err
		}
		domainHost = host
	}
	if issuer := TokenIssuerHost(accessToken); issuer != "" {
		issuerHost = issuer
	}
	branded := make([]string, 0, 2)
	for _, host := range []string{domainHost, issuerHost} {
		if host != "" && host != "copilot.tencent.com" {
			branded = append(branded, host)
		}
	}
	profiles := map[string]struct{}{}
	for _, host := range branded {
		profiles[DomainProfiles[host]] = struct{}{}
	}
	if len(profiles) > 1 {
		return "", fmt.Errorf("conflicting credential products")
	}
	for profile := range profiles {
		return profile, nil
	}
	if domainHost != "" {
		return DomainProfiles[domainHost], nil
	}
	if issuerHost != "" {
		return DomainProfiles[issuerHost], nil
	}
	return ProfileCNCLI, nil
}

func EndpointForProfile(profile string) string {
	if endpoint, ok := ProfileEndpoints[profile]; ok {
		return endpoint
	}
	return DomesticEndpoint
}

func ChatCompletionsURL(profile string) string {
	return EndpointForProfile(profile) + ChatPath
}

func RefreshURL(profile string) string {
	return EndpointForProfile(profile) + RefreshPath
}

func AuthHostForSite(site string) (string, error) {
	host, ok := SiteHosts[strings.ToLower(strings.TrimSpace(site))]
	if !ok {
		return "", fmt.Errorf("unknown site %q (supported: cn, intl)", site)
	}
	return host, nil
}

func OriginAllowed(domain, accessToken string) bool {
	if host, err := NormalizeHost(domain); err == nil {
		if _, ok := AllowedOrigins["https://"+host]; ok {
			return true
		}
	}
	if host := TokenIssuerHost(accessToken); host != "" {
		if _, ok := AllowedOrigins["https://"+host]; ok {
			return true
		}
	}
	return false
}
