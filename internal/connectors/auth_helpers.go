package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"api-monitor/internal/domain"
)

func newAPIUserHeaders(ctx context.Context, client *http.Client, instance domain.Instance) (map[string]string, json.RawMessage, error) {
	if instance.Credential == nil {
		return nil, nil, errMissingCredential()
	}

	username := firstNonEmpty(instance.Credential.Username, stringFromJSON(instance.Credential.JSON, "username", "email"))
	password := firstNonEmpty(instance.Credential.Password, stringFromJSON(instance.Credential.JSON, "password"))
	token := firstNonEmpty(
		instance.Credential.Value,
		stringFromJSON(instance.Credential.JSON, "access_token", "accessToken", "token"),
	)
	userID := firstNonEmpty(
		stringFromJSON(instance.Credential.JSON, "user_id", "userId", "new_api_user", "newApiUser", "id"),
	)

	// 1. If username and password are provided, always attempt standard login first.
	// This automatically negotiates the session cookies, token, and the correct numeric user ID.
	if username != "" && password != "" {
		body, _ := json.Marshal(map[string]string{"username": username, "password": password})
		raw, _, resHeaders, err := requestJSONWithHeaders(ctx, client, http.MethodPost, joinURL(baseURL(instance, ""), "/api/user/login"), map[string]string{}, body)
		if err == nil {
			data := objectFromAny(unwrapData(raw))
			user := objectFromAny(data["user"])
			loginToken := firstNonEmpty(
				stringFromJSON(data, "token", "access_token", "accessToken"),
				stringFromJSON(user, "token", "access_token", "accessToken"),
			)
			loginUserID := firstNonEmpty(
				stringFromJSON(user, "id", "user_id", "userId"),
				stringFromJSON(data, "user_id", "userId", "id"),
			)
			cookies := collectCookies(resHeaders)
			authHeaders := map[string]string{}
			if loginToken != "" {
				authHeaders["Authorization"] = "Bearer " + loginToken
			}
			if loginUserID != "" {
				authHeaders["New-Api-User"] = loginUserID
			}
			if len(cookies) > 0 {
				authHeaders["Cookie"] = strings.Join(cookies, "; ")
			}
			if authHeaders["Authorization"] != "" || authHeaders["Cookie"] != "" {
				return authHeaders, raw, nil
			}
		}
		// If password login failed and we have no fallback token, return the login error
		if token == "" {
			return nil, raw, err
		}
	}

	// 2. Standalone Token mode (e.g. bypassing captcha or token login)
	if token != "" {
		authHeaders := map[string]string{
			"Authorization": "Bearer " + token,
		}
		if userID == "" && username != "" {
			userID = username
		}
		if userID != "" {
			authHeaders["New-Api-User"] = userID
		}
		return authHeaders, nil, nil
	}

	return nil, nil, errors.New("missing new-api username or password")
}

func sub2APIUserHeaders(ctx context.Context, client *http.Client, instance domain.Instance) (map[string]string, json.RawMessage, error) {
	headers := bearerHeaders(instance)
	if headers["Authorization"] != "" {
		return headers, nil, nil
	}
	if instance.Credential == nil {
		return nil, nil, errMissingCredential()
	}
	if token := firstNonEmpty(stringFromJSON(instance.Credential.JSON, "access_token", "accessToken"), instance.Credential.Value); token != "" {
		return map[string]string{"Authorization": "Bearer " + token}, nil, nil
	}
	email := firstNonEmpty(instance.Credential.Username, stringFromJSON(instance.Credential.JSON, "email", "username"))
	password := firstNonEmpty(instance.Credential.Password, stringFromJSON(instance.Credential.JSON, "password"))
	if email == "" || password == "" {
		return nil, nil, errors.New("missing sub2Api email or password")
	}
	payload := map[string]string{"email": email, "password": password}
	if token := stringFromJSON(instance.Credential.JSON, "turnstile_token", "turnstileToken"); token != "" {
		payload["turnstile_token"] = token
	}
	body, _ := json.Marshal(payload)
	raw, _, err := requestJSON(ctx, client, http.MethodPost, joinURL(baseURL(instance, ""), "/api/v1/auth/login"), map[string]string{}, body)
	if err != nil {
		return nil, raw, err
	}
	data := objectFromAny(unwrapData(raw))
	token := firstNonEmpty(stringFromJSON(data, "access_token", "accessToken", "token"), stringFromJSON(objectFromAny(data["token"]), "access_token"))
	if token == "" {
		return nil, raw, errors.New("sub2Api login response did not include access_token")
	}
	return map[string]string{"Authorization": "Bearer " + token}, raw, nil
}

func collectCookies(headers http.Header) []string {
	values := headers.Values("Set-Cookie")
	out := make([]string, 0, len(values))
	for _, value := range values {
		if idx := strings.Index(value, ";"); idx >= 0 {
			value = value[:idx]
		}
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func requestFirstJSON(ctx context.Context, client *http.Client, method, root string, paths []string, headers map[string]string, body []byte) (json.RawMessage, string, error) {
	var lastRaw json.RawMessage
	var lastErr error
	for _, path := range paths {
		raw, _, err := requestJSON(ctx, client, method, joinURL(root, path), headers, body)
		if err == nil {
			return raw, path, nil
		}
		lastRaw = raw
		lastErr = err
	}
	return lastRaw, "", lastErr
}
