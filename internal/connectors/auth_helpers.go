package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"api-monitor/internal/domain"
)

type cachedSession struct {
	headers   map[string]string
	expiresAt time.Time
}

var (
	sessionMu    sync.RWMutex
	sessionCache = make(map[string]cachedSession)
)

func getCachedSession(key string) (map[string]string, bool) {
	if key == "" {
		return nil, false
	}
	sessionMu.RLock()
	defer sessionMu.RUnlock()
	sess, ok := sessionCache[key]
	if !ok || time.Now().After(sess.expiresAt) {
		return nil, false
	}
	copyHeaders := make(map[string]string, len(sess.headers))
	for k, v := range sess.headers {
		copyHeaders[k] = v
	}
	return copyHeaders, true
}

func setCachedSession(key string, headers map[string]string, ttl time.Duration) {
	if key == "" || len(headers) == 0 {
		return
	}
	sessionMu.Lock()
	defer sessionMu.Unlock()
	copyHeaders := make(map[string]string, len(headers))
	for k, v := range headers {
		copyHeaders[k] = v
	}
	sessionCache[key] = cachedSession{
		headers:   copyHeaders,
		expiresAt: time.Now().Add(ttl),
	}
}

func invalidateCachedSession(key string) {
	if key == "" {
		return
	}
	sessionMu.Lock()
	defer sessionMu.Unlock()
	delete(sessionCache, key)
}

func newAPIUserHeaders(ctx context.Context, client *http.Client, instance domain.Instance) (map[string]string, json.RawMessage, error) {
	if instance.Credential == nil {
		return nil, nil, errMissingCredential()
	}

	username := firstNonEmpty(instance.Credential.Username, stringFromJSON(instance.Credential.JSON, "username", "email"))
	password := firstNonEmpty(instance.Credential.Password, stringFromJSON(instance.Credential.JSON, "password"))
	token := firstNonEmpty(
		instance.Credential.Value,
		stringFromJSON(instance.Credential.JSON, "access_token", "accessToken", "auth_token", "authToken", "token"),
	)
	userID := firstNonEmpty(
		stringFromJSON(instance.Credential.JSON, "user_id", "userId", "new_api_user", "newApiUser", "id"),
	)

	// 1. If username and password are provided, check session cache first
	if username != "" && password != "" {
		cacheKey := baseURL(instance, "") + ":newapi:" + username
		if cached, ok := getCachedSession(cacheKey); ok {
			return cached, nil, nil
		}

		body, _ := json.Marshal(map[string]string{"username": username, "password": password})
		raw, _, resHeaders, err := requestJSONWithHeaders(ctx, client, http.MethodPost, joinURL(baseURL(instance, ""), "/api/user/login"), map[string]string{}, body)
		if err == nil {
			var respObj map[string]any
			_ = json.Unmarshal(raw, &respObj)
			if success, ok := respObj["success"].(bool); ok && !success {
				errMsg := stringFromJSON(respObj, "message")
				if errMsg == "" {
					errMsg = "login failed"
				}
				if strings.Contains(strings.ToLower(errMsg), "turnstile") || strings.Contains(errMsg, "人机") || strings.Contains(errMsg, "验证") {
					err = fmt.Errorf("该站点已开启人机验证 (%s)，无法直接使用账号密码登录，请改用下方的【访问令牌 Access Token】与【用户 ID】", errMsg)
				} else {
					err = errors.New(errMsg)
				}
			} else {
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
					setCachedSession(cacheKey, authHeaders, 4*time.Hour)
					return authHeaders, raw, nil
				}
				err = errors.New("login succeeded but no session token or cookie returned")
			}
		}
		// If password login failed and we have no fallback token, return the login error
		if token == "" {
			return nil, raw, err
		}
	}

	// 2. Standalone Token / Cookie mode (e.g. bypassing captcha or token login)
	if token != "" {
		authHeaders := map[string]string{}
		if strings.HasPrefix(strings.ToLower(token), "bearer ") {
			authHeaders["Authorization"] = token
		} else if strings.Contains(token, "session=") || strings.Contains(token, "auth_token=") || strings.Contains(token, ";") {
			authHeaders["Cookie"] = token
		} else {
			authHeaders["Authorization"] = "Bearer " + token
			authHeaders["Cookie"] = "auth_token=" + token
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
	if token := firstNonEmpty(stringFromJSON(instance.Credential.JSON, "access_token", "accessToken", "auth_token", "authToken"), instance.Credential.Value); token != "" {
		root := baseURL(instance, "")
		headers := map[string]string{
			"Accept":          "application/json, text/plain, */*",
			"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
			"Origin":          root,
			"Referer":         strings.TrimRight(root, "/") + "/",
			"X-Requested-With": "XMLHttpRequest",
		}
		if strings.HasPrefix(strings.ToLower(token), "bearer ") {
			headers["Authorization"] = token
		} else {
			headers["Authorization"] = "Bearer " + token
			headers["Cookie"] = "auth_token=" + token
		}
		return headers, nil, nil
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
