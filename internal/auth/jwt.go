package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

func ExtractAccountIDFromTokens(idToken string, accessToken string) string {
	if accountID := extractAccountID(idToken); accountID != "" {
		return accountID
	}
	return extractAccountID(accessToken)
}

func extractAccountID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]string
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	for _, key := range []string{"https://api.openai.com/auth/account_id", "account_id", "accountId", "org_id", "organization_id"} {
		if claims[key] != "" {
			return claims[key]
		}
	}
	return ""
}
