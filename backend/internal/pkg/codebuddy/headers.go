package codebuddy

const (
	CLIVersion          = "2.149.0"
	WorkBuddyVersion    = "5.5.2"
	WorkBuddyCLIVersion = "2.137.1"
	CLIUserAgent        = "CLI/" + CLIVersion + " CodeBuddy/" + CLIVersion
)

func IdentityHeaders(profile string) map[string]string {
	if ProfileProduct(profile) == ProductCLI {
		return map[string]string{
			"User-Agent":    CLIUserAgent,
			"X-IDE-Type":    "CLI",
			"X-IDE-Name":    "CLI",
			"X-IDE-Version": CLIVersion,
		}
	}
	name := "WorkBuddy"
	if ProfileRegion(profile) == SiteIntl {
		name = "WorkBuddy AI"
	}
	return map[string]string{
		"User-Agent":    "WorkBuddy/" + WorkBuddyVersion + " " + name + "/" + WorkBuddyVersion + " CLI/" + WorkBuddyCLIVersion,
		"X-IDE-Type":    "WorkBuddy",
		"X-IDE-Name":    "WorkBuddy",
		"X-IDE-Version": WorkBuddyVersion,
	}
}

func SDKHeaders() map[string]string {
	return map[string]string{
		"x-stainless-arch":            "x64",
		"x-stainless-lang":            "js",
		"x-stainless-os":              "Linux",
		"x-stainless-package-version": "6.25.0",
		"x-stainless-retry-count":     "0",
		"x-stainless-runtime":         "node",
		"x-stainless-runtime-version": "v24.21.0",
		"X-Agent-Intent":              "craft",
		"X-Agent-Purpose":             "conversation",
		"X-Agent-Type":                "main",
		"X-Private-Data":              "false",
		"X-CodeBuddy-Request":         "1",
	}
}

func CredentialHeaders(profile, accessToken, domain, uid, enterpriseID string) map[string]string {
	headers := map[string]string{
		"Content-Type":     "application/json",
		"Accept":           "application/json",
		"Authorization":    "Bearer " + accessToken,
		"X-User-Id":        uid,
		"X-Enterprise-Id":  enterpriseID,
		"X-Tenant-Id":      enterpriseID,
		"X-Domain":         domain,
		"X-Product":        "SaaS",
		"X-Requested-With": "XMLHttpRequest",
	}
	for key, value := range IdentityHeaders(profile) {
		headers[key] = value
	}
	for key, value := range SDKHeaders() {
		headers[key] = value
	}
	return headers
}

func ApplyHeaders(dst map[string][]string, headers map[string]string) {
	for key, value := range headers {
		dst[key] = []string{value}
	}
}
