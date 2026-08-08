package unifi

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	requestTimeout           = 12 * time.Second
	maxResponse              = 8 << 20
	pageSize                 = 200
	networkIntegrationPrefix = "/proxy/network/integration/v1"
	integrationPrefix        = "/integration/v1"
)

var integrationPrefixes = [...]string{networkIntegrationPrefix, integrationPrefix}

type Site struct {
	ID                string `json:"id"`
	InternalReference string `json:"internalReference"`
	Name              string `json:"name"`
}

type Client struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	Name           string `json:"name"`
	ConnectedAt    string `json:"connectedAt"`
	IPAddress      string `json:"ipAddress"`
	MACAddress     string `json:"macAddress"`
	UplinkDeviceID string `json:"uplinkDeviceId"`
}

type Certificate struct {
	FingerprintSHA256 string `json:"fingerprint_sha256"`
	Subject           string `json:"subject"`
	Issuer            string `json:"issuer"`
	ExpiresAt         string `json:"expires_at"`
}

type apiClient struct {
	baseURL     string
	apiKey      string
	fingerprint string
	httpClient  *http.Client
}

type page[T any] struct {
	Offset     int `json:"offset"`
	Limit      int `json:"limit"`
	Count      int `json:"count"`
	TotalCount int `json:"totalCount"`
	Data       []T `json:"data"`
}

type responseError struct {
	StatusCode int
	Message    string
}

func (responseError *responseError) Error() string {
	if responseError.Message == "" {
		return fmt.Sprintf("UniFi returned HTTP %d", responseError.StatusCode)
	}
	return fmt.Sprintf("UniFi returned HTTP %d: %s", responseError.StatusCode, responseError.Message)
}

func newAPIClient(baseURL, apiKey, fingerprint string) (*apiClient, error) {
	normalized, err := normalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("enter a UniFi API key")
	}
	fingerprint = normalizeFingerprint(fingerprint)
	if err := validateFingerprint(fingerprint); err != nil {
		return nil, err
	}
	return &apiClient{
		baseURL:     normalized,
		apiKey:      apiKey,
		fingerprint: fingerprint,
		httpClient: &http.Client{
			Transport: newTransport(fingerprint),
			Timeout:   requestTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func validateFingerprint(fingerprint string) error {
	if fingerprint == "" {
		return nil
	}
	if len(fingerprint) != sha256.Size*2 {
		return errors.New("the trusted UniFi certificate fingerprint is invalid")
	}
	if _, err := hex.DecodeString(fingerprint); err != nil {
		return errors.New("the trusted UniFi certificate fingerprint is invalid")
	}
	return nil
}

func newTransport(fingerprint string) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialLocalNetwork
	transport.ResponseHeaderTimeout = 8 * time.Second
	transport.TLSClientConfig = tlsConfig(fingerprint)
	return transport
}

func tlsConfig(fingerprint string) *tls.Config {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if fingerprint == "" {
		return config
	}
	config.InsecureSkipVerify = true // Verification is replaced with an exact certificate pin below.
	config.VerifyConnection = func(state tls.ConnectionState) error {
		return verifyPinnedCertificate(state, fingerprint)
	}
	return config
}

func verifyPinnedCertificate(state tls.ConnectionState, fingerprint string) error {
	if len(state.PeerCertificates) == 0 {
		return errors.New("UniFi did not present a TLS certificate")
	}
	peerCertificate := state.PeerCertificates[0]
	now := time.Now()
	if now.Before(peerCertificate.NotBefore) || now.After(peerCertificate.NotAfter) {
		return errors.New("the trusted UniFi certificate is expired or not valid yet")
	}
	actual := certificateFingerprint(peerCertificate)
	if actual != fingerprint {
		return fmt.Errorf("UniFi certificate changed (expected %s, received %s)", displayFingerprint(fingerprint), displayFingerprint(actual))
	}
	return nil
}

func normalizeBaseURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("enter the local address of your UniFi console")
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "", errors.New("enter a valid UniFi console address")
	}
	if parsed.Scheme != "https" {
		return "", errors.New("UniFi integrations require an HTTPS console address")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("the UniFi console address cannot contain credentials, a query, or a fragment")
	}
	path := strings.TrimSuffix(parsed.EscapedPath(), "/")
	for _, suffix := range []string{networkIntegrationPrefix, integrationPrefix, "/proxy/network", "/network"} {
		if trimmed, found := strings.CutSuffix(path, suffix); found {
			path = trimmed
			break
		}
	}
	if path != "" {
		return "", errors.New("enter the console address without an application path")
	}
	parsed.Path, parsed.RawPath = "", ""
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func (client *apiClient) listSites(ctx context.Context) ([]Site, error) {
	var lastErr error
	for _, prefix := range integrationPrefixes {
		var result page[Site]
		err := client.get(ctx, prefix+"/sites?offset=0&limit=200", &result)
		if err == nil {
			return result.Data, nil
		}
		lastErr = err
		var apiErr *responseError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
			return nil, err
		}
	}
	return nil, lastErr
}

func (client *apiClient) listClients(ctx context.Context, siteID string) ([]Client, error) {
	siteID = strings.TrimSpace(siteID)
	if siteID == "" {
		return nil, errors.New("select a UniFi site")
	}
	var lastErr error
	for _, prefix := range integrationPrefixes {
		items, err := client.listClientsAt(ctx, prefix, siteID)
		if err == nil {
			return items, nil
		}
		lastErr = err
		var apiErr *responseError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
			return nil, err
		}
	}
	return nil, lastErr
}

func (client *apiClient) listClientsAt(ctx context.Context, prefix, siteID string) ([]Client, error) {
	var items []Client
	for offset := 0; ; offset += pageSize {
		var result page[Client]
		path := prefix + "/sites/" + url.PathEscape(siteID) + "/clients?offset=" + strconv.Itoa(offset) + "&limit=" + strconv.Itoa(pageSize)
		if err := client.get(ctx, path, &result); err != nil {
			return nil, err
		}
		items = append(items, result.Data...)
		if len(result.Data) < pageSize || result.TotalCount > 0 && offset+len(result.Data) >= result.TotalCount {
			return items, nil
		}
	}
}

func (client *apiClient) get(ctx context.Context, path string, destination any) (err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-API-Key", client.apiKey)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("connect to UniFi: %w", err)
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponse))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var payload struct {
			Message    string `json:"message"`
			StatusName string `json:"statusName"`
		}
		_ = json.Unmarshal(body, &payload)
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = strings.TrimSpace(payload.StatusName)
		}
		return &responseError{StatusCode: response.StatusCode, Message: message}
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return errors.New("UniFi returned an invalid response")
	}
	return nil
}

func certificateForAddress(ctx context.Context, baseURL string) (certificate *Certificate, err error) {
	normalized, err := normalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return nil, err
	}
	host := parsed.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}
	rawConnection, err := dialLocalNetwork(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("inspect UniFi certificate: %w", err)
	}
	connection := tls.Client(rawConnection, &tls.Config{
		InsecureSkipVerify: true, // Inspection only; no HTTP request or credential is sent.
		MinVersion:         tls.VersionTLS12,
		ServerName:         parsed.Hostname(),
	})
	defer func() {
		if closeErr := rawConnection.Close(); closeErr != nil {
			certificate = nil
			err = errors.Join(err, fmt.Errorf("close UniFi certificate connection: %w", closeErr))
		}
	}()
	if err := connection.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("inspect UniFi certificate: %w", err)
	}
	state := connection.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, errors.New("UniFi did not present a TLS certificate")
	}
	peerCertificate := state.PeerCertificates[0]
	return &Certificate{
		FingerprintSHA256: displayFingerprint(certificateFingerprint(peerCertificate)),
		Subject:           peerCertificate.Subject.String(),
		Issuer:            peerCertificate.Issuer.String(),
		ExpiresAt:         peerCertificate.NotAfter.UTC().Format(time.RFC3339),
	}, nil
}

func dialLocalNetwork(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, candidate := range resolved {
		if !isLocalAddress(candidate.IP) {
			continue
		}
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("the UniFi console address does not resolve to a private or local network address")
}

func isLocalAddress(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return true
	}
	_, carrierGradeNAT, _ := net.ParseCIDR("100.64.0.0/10")
	return carrierGradeNAT.Contains(ip)
}

func isCertificateValidationError(err error) bool {
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var certificateInvalid x509.CertificateInvalidError
	return errors.As(err, &unknownAuthority) || errors.As(err, &hostnameError) || errors.As(err, &certificateInvalid) ||
		strings.Contains(strings.ToLower(err.Error()), "certificate signed by unknown authority")
}

func certificateFingerprint(certificate *x509.Certificate) string {
	sum := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(sum[:])
}

func normalizeFingerprint(value string) string {
	return strings.ToLower(strings.NewReplacer(":", "", " ", "").Replace(strings.TrimSpace(value)))
}

func displayFingerprint(value string) string {
	value = strings.ToUpper(normalizeFingerprint(value))
	parts := make([]string, 0, len(value)/2)
	for len(value) >= 2 {
		parts = append(parts, value[:2])
		value = value[2:]
	}
	return strings.Join(parts, ":")
}
