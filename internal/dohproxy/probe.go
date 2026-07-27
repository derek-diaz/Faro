package dohproxy

import (
	"context"
	"encoding/binary"
	"errors"
	"time"
)

// ProbeAddress tests the actual encrypted path associated with a provider IP.
func ProbeAddress(ctx context.Context, address string) (time.Duration, error) {
	endpoint, ok := EndpointForAddress(address)
	if !ok {
		return 0, errors.New("encrypted DNS is not available for this resolver")
	}
	client, err := newEndpointClient(endpoint)
	if err != nil {
		return 0, err
	}
	query := probeQuery(uint16(time.Now().UnixNano()))
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	started := time.Now()
	_, err = client.exchange(probeCtx, query)
	return time.Since(started), err
}

func probeQuery(id uint16) []byte {
	message := make([]byte, 12)
	binary.BigEndian.PutUint16(message[0:2], id)
	binary.BigEndian.PutUint16(message[2:4], 0x0100)
	binary.BigEndian.PutUint16(message[4:6], 1)
	for _, label := range []string{"example", "com"} {
		message = append(message, byte(len(label)))
		message = append(message, label...)
	}
	message = append(message, 0, 0, 1, 0, 1)
	return message
}
