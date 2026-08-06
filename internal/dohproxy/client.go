package dohproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	dnsMessageContentType = "application/dns-message"
	contentTypeHeader     = "Content-Type"
	maxDNSMessageBytes    = 65535
)

type endpointClient struct {
	endpoint Endpoint
	client   *http.Client
}

func newEndpointClient(endpoint Endpoint) (*endpointClient, error) {
	if err := validateEndpoint(endpoint); err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(endpoint.URL)
	host := parsed.Hostname()
	transport := &http.Transport{
		Proxy:             nil,
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: host,
		},
		DialContext: bootstrapDialer(host, endpoint.BootstrapIPs),
	}
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if request.URL.Scheme != "https" || !strings.EqualFold(request.URL.Hostname(), host) {
				return errors.New("encrypted DNS endpoint redirected to a different host")
			}
			if len(via) >= 3 {
				return errors.New("too many encrypted DNS redirects")
			}
			return nil
		},
	}
	return &endpointClient{endpoint: cloneEndpoint(endpoint), client: client}, nil
}

func bootstrapDialer(endpointHost string, addresses []string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, requestedAddress string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(requestedAddress)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(strings.TrimSuffix(host, "."), strings.TrimSuffix(endpointHost, ".")) || port != "443" {
			return nil, fmt.Errorf("encrypted DNS attempted an unexpected connection to %s", requestedAddress)
		}
		var lastErr error
		dialer := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
		for _, address := range addresses {
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(address, port))
			if dialErr == nil {
				return connection, nil
			}
			lastErr = dialErr
		}
		if lastErr == nil {
			lastErr = errors.New("no bootstrap address is configured")
		}
		return nil, lastErr
	}
}

func (c *endpointClient) exchange(ctx context.Context, query []byte) ([]byte, error) {
	return exchangeWithClient(ctx, c.endpoint.URL, query, c.client)
}

func exchangeWithClient(ctx context.Context, endpointURL string, query []byte, client *http.Client) (message []byte, err error) {
	if err := validateQuery(query); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(query))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", dnsMessageContentType)
	request.Header.Set(contentTypeHeader, dnsMessageContentType)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			message = nil
			err = errors.Join(err, closeErr)
		}
	}()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("encrypted DNS returned HTTP %d", response.StatusCode)
	}
	contentType, _, parseErr := mime.ParseMediaType(response.Header.Get(contentTypeHeader))
	if parseErr != nil || !strings.EqualFold(contentType, dnsMessageContentType) {
		return nil, fmt.Errorf("encrypted DNS returned unexpected content type %q", response.Header.Get(contentTypeHeader))
	}
	message, err = io.ReadAll(io.LimitReader(response.Body, maxDNSMessageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(message) > maxDNSMessageBytes {
		return nil, errors.New("encrypted DNS response is too large")
	}
	if err := validateResponse(query, message); err != nil {
		return nil, err
	}
	return message, nil
}

func validateQuery(message []byte) error {
	if len(message) < 12 || len(message) > maxDNSMessageBytes {
		return errors.New("invalid DNS query")
	}
	if message[2]&0x80 != 0 {
		return errors.New("DNS query has the response bit set")
	}
	return nil
}

func validateResponse(query, response []byte) error {
	if len(response) < 12 {
		return errors.New("encrypted DNS returned a short response")
	}
	if binary.BigEndian.Uint16(response[:2]) != binary.BigEndian.Uint16(query[:2]) {
		return errors.New("encrypted DNS returned a mismatched query ID")
	}
	if response[2]&0x80 == 0 {
		return errors.New("encrypted DNS returned a message without the response bit")
	}
	return nil
}

func serverFailure(query []byte) []byte {
	if len(query) < 12 {
		return nil
	}
	response := append([]byte(nil), query...)
	response[2] |= 0x80
	response[3] = (response[3] & 0xf0) | 0x02
	response[3] |= 0x80
	binary.BigEndian.PutUint16(response[6:8], 0)
	binary.BigEndian.PutUint16(response[8:10], 0)
	return response
}

func dnsResponseCode(message []byte) byte {
	if len(message) < 4 {
		return 2
	}
	return message[3] & 0x0f
}
