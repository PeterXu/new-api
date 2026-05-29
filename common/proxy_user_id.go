package common

import (
	"net/url"
	"strconv"
)

// InjectUserIdInProxy transforms a proxy URL to embed a user ID in the username field.
//
//	Input:  socks5://user:pass@host:port  +  userId=42
//	Output: socks5://user@42:pass@host:port
//
// The proxy server splits the username on the first "@" to recover (original_user, user_id).
// The "@" separator is safe because user IDs are numeric and cannot contain "@".
//
// This is a no-op when injectUserId is false, proxyURL is empty, or userId is 0.
func InjectUserIdInProxy(proxyURL string, injectUserId bool, userId int) string {
	if !injectUserId || proxyURL == "" || userId == 0 {
		return proxyURL
	}

	parsedURL, err := url.Parse(proxyURL)
	if err != nil || parsedURL.User == nil {
		return proxyURL
	}

	username := parsedURL.User.Username()
	password, hasPassword := parsedURL.User.Password()

	newUsername := username + "@" + strconv.Itoa(userId)

	if hasPassword {
		parsedURL.User = url.UserPassword(newUsername, password)
	} else {
		parsedURL.User = url.User(newUsername)
	}

	return parsedURL.String()
}
