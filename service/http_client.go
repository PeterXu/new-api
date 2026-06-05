package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"golang.org/x/net/proxy"
)

// NOTE:
//  There are two proxy urls: a). channel proxy url; b). actual proxy url
//  In general, these two proxy urls are the same.
//  However, they are different when channel setting's injectUserId is true.

var (
	httpClient              *http.Client
	ssrfProtectedHTTPClient *http.Client
	proxyClientLock         sync.RWMutex
	proxyClients            = make(map[string]*http.Client)
)

func checkRedirect(req *http.Request, via []*http.Request) error {
	urlStr := req.URL.String()
	if err := validateURLWithCurrentFetchSetting(urlStr, true); err != nil {
		return fmt.Errorf("redirect to %s blocked: %v", urlStr, err)
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	return nil
}

func checkProtectedFetchRedirect(req *http.Request, via []*http.Request) error {
	urlStr := req.URL.String()
	if err := ValidateSSRFProtectedFetchURL(urlStr); err != nil {
		return fmt.Errorf("redirect to %s blocked: %v", urlStr, err)
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	return nil
}

func validateURLWithCurrentFetchSetting(urlStr string, applyDomainIPFilter bool) error {
	fetchSetting := system_setting.GetFetchSetting()
	return common.ValidateURLWithFetchSetting(urlStr, fetchSetting.EnableSSRFProtection, fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, applyDomainIPFilter && fetchSetting.ApplyIPFilterForDomain)
}

func ValidateSSRFProtectedFetchURL(urlStr string) error {
	return validateURLWithCurrentFetchSetting(urlStr, true)
}

func InitHttpClient() {
	transport := &http.Transport{
		MaxIdleConns:        common.RelayMaxIdleConns,
		MaxIdleConnsPerHost: common.RelayMaxIdleConnsPerHost,
		IdleConnTimeout:     time.Duration(common.RelayIdleConnTimeout) * time.Second,
		ForceAttemptHTTP2:   true,
		Proxy:               http.ProxyFromEnvironment, // Support HTTP_PROXY, HTTPS_PROXY, NO_PROXY env vars
	}
	if common.TLSInsecureSkipVerify {
		transport.TLSClientConfig = common.InsecureTLSConfig
	}

	if common.RelayTimeout == 0 {
		httpClient = &http.Client{
			Transport:     transport,
			CheckRedirect: checkRedirect,
		}
	} else {
		httpClient = &http.Client{
			Transport:     transport,
			Timeout:       time.Duration(common.RelayTimeout) * time.Second,
			CheckRedirect: checkRedirect,
		}
	}
	ssrfProtectedHTTPClient = newProtectedFetchHTTPClient()
}

// GetHttpClient returns the general outbound client used by relay/provider
// integrations. Do not attach the SSRF-protected dialer here: provider base URLs
// are root/operator-managed deployment targets, not arbitrary user-controlled
// input, and may legitimately point at private networks, private-link endpoints,
// self-hosted services, or local proxies. Code paths that fetch arbitrary
// user-controlled URLs must use GetSSRFProtectedHTTPClient or
// ValidateSSRFProtectedFetchURL instead.
func GetHttpClient() *http.Client {
	return httpClient
}

// GetSSRFProtectedHTTPClient 返回带拨号时 SSRF 校验的客户端。
// ssrfProtectedHTTPClient 由 InitHttpClient 在启动时初始化，运行期只读。
func GetSSRFProtectedHTTPClient() *http.Client {
	if fetchSetting := system_setting.GetFetchSetting(); fetchSetting != nil && !fetchSetting.EnableSSRFProtection {
		return GetHttpClient()
	}
	return ssrfProtectedHTTPClient
}

// GetHttpClientWithProxy returns the default client or a proxy-enabled one when proxyURL is provided.
func GetHttpClientWithProxy(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		return GetHttpClient(), nil
	}
	return NewProxyHttpClient(proxyURL)
}

// ResetProxyClientCache 清空代理客户端缓存，确保下次使用时重新初始化
func ResetProxyClientCache() {
	proxyClientLock.Lock()
	defer proxyClientLock.Unlock()
	for _, client := range proxyClients {
		if transport, ok := client.Transport.(*http.Transport); ok && transport != nil {
			transport.CloseIdleConnections()
		}
	}
	proxyClients = make(map[string]*http.Client)
}

// NewProxyHttpClient 创建支持代理的 HTTP 客户端
func NewProxyHttpClient(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		if client := GetHttpClient(); client != nil {
			return client, nil
		}
		return http.DefaultClient, nil
	}

	proxyClientLock.RLock()
	if client, ok := proxyClients[proxyURL]; ok {
		proxyClientLock.RUnlock()
		return client, nil
	}
	proxyClientLock.RUnlock()

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}

	switch parsedURL.Scheme {
	case "http", "https":
		transport := &http.Transport{
			MaxIdleConns:        common.RelayMaxIdleConns,
			MaxIdleConnsPerHost: common.RelayMaxIdleConnsPerHost,
			IdleConnTimeout:     time.Duration(common.RelayIdleConnTimeout) * time.Second,
			ForceAttemptHTTP2:   true,
			Proxy:               http.ProxyURL(parsedURL),
		}
		if common.TLSInsecureSkipVerify {
			transport.TLSClientConfig = common.InsecureTLSConfig
		}
		client := &http.Client{
			Transport:     transport,
			CheckRedirect: checkRedirect,
		}
		client.Timeout = time.Duration(common.RelayTimeout) * time.Second
		proxyClientLock.Lock()
		if existing, ok := proxyClients[proxyURL]; ok {
			proxyClientLock.Unlock()
			return existing, nil
		}
		proxyClients[proxyURL] = client
		proxyClientLock.Unlock()
		return client, nil

	case "socks5", "socks5h":
		// 获取认证信息
		var auth *proxy.Auth
		if parsedURL.User != nil {
			auth = &proxy.Auth{
				User:     parsedURL.User.Username(),
				Password: "",
			}
			if password, ok := parsedURL.User.Password(); ok {
				auth.Password = password
			}
		}

		// 创建 SOCKS5 代理拨号器
		// proxy.SOCKS5 使用 tcp 参数，所有 TCP 连接包括 DNS 查询都将通过代理进行。行为与 socks5h 相同
		dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, proxy.Direct)
		if err != nil {
			return nil, err
		}

		transport := &http.Transport{
			MaxIdleConns:        common.RelayMaxIdleConns,
			MaxIdleConnsPerHost: common.RelayMaxIdleConnsPerHost,
			IdleConnTimeout:     time.Duration(common.RelayIdleConnTimeout) * time.Second,
			ForceAttemptHTTP2:   true,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
		}
		if common.TLSInsecureSkipVerify {
			transport.TLSClientConfig = common.InsecureTLSConfig
		}

		client := &http.Client{Transport: transport, CheckRedirect: checkRedirect}
		client.Timeout = time.Duration(common.RelayTimeout) * time.Second
		proxyClientLock.Lock()
		if existing, ok := proxyClients[proxyURL]; ok {
			proxyClientLock.Unlock()
			return existing, nil
		}
		proxyClients[proxyURL] = client
		proxyClientLock.Unlock()
		return client, nil

	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s, must be http, https, socks5 or socks5h", parsedURL.Scheme)
	}
}

// Removes proxy clients for a channel if no other channel uses them
func CleanupChannelProxy(channelId int) {
	channel, err := model.CacheGetChannel(channelId)
	if err != nil {
		return
	}

	channelProxyURL, injectUserId := GetChannelProxyConfig(channel)
	cleanupProxy(channelProxyURL, injectUserId, map[int]bool{channelId: true})
}

// Cleans up proxy clients for a specific proxy config.
// Used when the proxy config is already known (e.g., after capturing old config before an update).
func CleanupChannelProxyConfig(proxyURL string, injectUserId bool, excludeChannelId int) {
	cleanupProxy(proxyURL, injectUserId, map[int]bool{excludeChannelId: true})
}

// Removes proxy clients for a set of channels that are about to be deleted or disabled.
// Must be called BEFORE the channels are deleted/disabled so that channel data is still available from cache.
// After this, the caller should delete/disable the channels and call model.InitChannelCache().
func CleanupChannelProxyBatch(channelIds []int) {
	type proxyKey struct {
		url          string
		injectUserId bool
	}
	seen := make(map[proxyKey]bool)
	excludeSet := make(map[int]bool, len(channelIds))
	for _, id := range channelIds {
		excludeSet[id] = true
	}

	for _, id := range channelIds {
		channel, err := model.CacheGetChannel(id)
		if err != nil {
			continue
		}
		proxyURL, injectUserId := GetChannelProxyConfig(channel)
		if proxyURL == "" {
			continue
		}
		key := proxyKey{proxyURL, injectUserId}
		if seen[key] {
			continue
		}
		seen[key] = true
		cleanupProxy(proxyURL, injectUserId, excludeSet)
	}
}

// Removes proxy clients for the given config if no enabled channel uses them.
// excludeSet contains channel IDs that should be excluded from the "still in use" check.
func cleanupProxy(proxyURL string, injectUserId bool, excludeSet map[int]bool) {
	if proxyURL == "" {
		return
	}
	if !isProxyURLUsedByEnabledChannels(proxyURL, injectUserId, excludeSet) {
		if injectUserId {
			removeInjectedProxyClients(proxyURL)
		} else {
			removeProxyClient(proxyURL)
		}
	}
}

// Removes a specific proxy client by channel proxy url from cache
func removeProxyClient(proxyURL string) {
	if proxyURL == "" {
		return
	}
	var transport *http.Transport
	proxyClientLock.Lock()
	if client, ok := proxyClients[proxyURL]; ok {
		if t, ok := client.Transport.(*http.Transport); ok && t != nil {
			transport = t
		}
		delete(proxyClients, proxyURL)
	}
	proxyClientLock.Unlock()
	if transport != nil {
		transport.CloseIdleConnections()
	}
}

// Removes all proxy clients matching channel proxy url with InjectUserId is true.
// Matching assumes injected usernames follow the pattern "{baseUsername}@{userId}".
// This will produce false positives if a base proxy username itself contains "@" (e.g., "admin@corp"
// would match as an injected variant of "admin"). This is acceptable because proxy usernames
// containing "@" are uncommon and the worst case is an unnecessary removal + lazy recreation.
func removeInjectedProxyClients(proxyURL string) {
	if proxyURL == "" {
		return
	}

	// Parse base URL to extract pattern component
	parsedURL, err := url.Parse(proxyURL)
	if err != nil || parsedURL.User == nil {
		// No injection possible, remove directly
		removeProxyClient(proxyURL)
		return
	}

	host := parsedURL.Host
	username := parsedURL.User.Username()
	password, _ := parsedURL.User.Password()

	proxyClientLock.Lock()
	var transports []*http.Transport
	for key, client := range proxyClients {
		keyParsed, err := url.Parse(key)
		if err != nil || keyParsed.User == nil {
			continue
		}
		keyUsername := keyParsed.User.Username()
		keyPassword, _ := keyParsed.User.Password()

		// Check if this key's username starts with username@
		// e.g., username="user", injected username="user@42"
		if keyParsed.Host == host && keyPassword == password {
			if strings.HasPrefix(keyUsername, username+"@") {
				if t, ok := client.Transport.(*http.Transport); ok && t != nil {
					transports = append(transports, t)
				}
				delete(proxyClients, key)
			}
		}
	}
	proxyClientLock.Unlock()
	for _, t := range transports {
		t.CloseIdleConnections()
	}
}

// Checks if any enabled channel uses this channel proxy URL
// For injected URLs (injectUserId=true), also requires InjectUserIdInProxy to match
// excludeSet contains channel IDs to exclude from the check (e.g., channels being deleted)
func isProxyURLUsedByEnabledChannels(proxyURL string, injectUserId bool, excludeSet map[int]bool) bool {
	if proxyURL == "" {
		return false
	}

	channels, err := model.GetAllEnabledChannels()
	if err != nil {
		return true // conservatively assume proxy is still in use
	}
	for _, channel := range channels {
		if excludeSet[channel.Id] {
			continue
		}
		channelProxyURL, channelInjectUserId := GetChannelProxyConfig(channel)
		if channelProxyURL == proxyURL && channelInjectUserId == injectUserId {
			return true
		}
	}
	return false
}

// Extracts channel proxy url and injectUserId setting from channel
func GetChannelProxyConfig(channel *model.Channel) (proxyURL string, injectUserId bool) {
	if channel.Setting == nil || *channel.Setting == "" {
		return "", false
	}
	var settings dto.ChannelSettings
	if err := common.Unmarshal([]byte(*channel.Setting), &settings); err != nil {
		return "", false
	}
	return settings.Proxy, settings.InjectUserIdInProxy
}
