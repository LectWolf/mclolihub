package service

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
)

func codeBuddyChatCompletionsURL(account *Account) (string, error) {
	creds, err := codebuddy.ParseCredentials(account.Credentials)
	if err != nil {
		return "", fmt.Errorf("invalid codebuddy credentials: %w", err)
	}
	return codebuddy.ChatCompletionsURL(creds.Profile), nil
}

func applyCodeBuddyUpstreamHeaders(header http.Header, account *Account, accessToken string) {
	if account == nil || header == nil {
		return
	}
	creds, err := codebuddy.ParseCredentials(account.Credentials)
	if err != nil {
		return
	}
	if strings.TrimSpace(accessToken) != "" {
		creds.AccessToken = accessToken
	}
	for key, value := range codebuddy.CredentialHeaders(creds.Profile, creds.AccessToken, creds.Domain, creds.UID, creds.EnterpriseID) {
		header.Set(key, value)
	}
}

func prepareCodeBuddyChatBody(body []byte) ([]byte, error) {
	return codebuddy.EnsureLeadingSystemMessage(body)
}
